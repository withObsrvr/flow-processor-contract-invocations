package main

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/stellar/go/protocols/horizon"
	"github.com/stellar/go/support/log"
	"github.com/stellar/go/xdr"
)

// ProcessorName is the unique name for this processor
const ProcessorName = "contract-invocations"

// MinSupportedFlowApiVersion is the minimum Flow API version this processor supports
const MinSupportedFlowApiVersion = "1.0.0"

// Event represents an event from the Flow system
type Event struct {
	ID    string          `json:"id"`
	Topic string          `json:"topic"`
	Data  json.RawMessage `json:"data"`
}

// ContractInvocationProcessor is the main processor implementation
type ContractInvocationProcessor struct {
	config        map[string]interface{}
	stellarClient StellarClientInterface
	logger        *log.Entry
}

// StellarClientInterface defines the interface for interacting with Stellar
type StellarClientInterface interface {
	// Add methods as needed
}

// Initialize sets up the processor with the provided configuration
func (p *ContractInvocationProcessor) Initialize(config map[string]interface{}, logger *log.Entry) error {
	p.config = config
	p.logger = logger

	// Check for required configuration
	if networkPassphrase, ok := config["network_passphrase"]; !ok || networkPassphrase == "" {
		return fmt.Errorf("network_passphrase is required in the configuration")
	}

	// Check API version compatibility
	if flowApiVersion, ok := config["flow_api_version"]; ok {
		p.logger.Infof("Flow API Version: %s", flowApiVersion)
		if !isCompatibleVersion(flowApiVersion.(string), MinSupportedFlowApiVersion) {
			err := fmt.Errorf("incompatible Flow API version: %s (minimum supported: %s)",
				flowApiVersion, MinSupportedFlowApiVersion)
			p.logger.Error(err)
			return err
		}
		p.logger.Infof("Flow API version %s is compatible with processor", flowApiVersion)
	}

	// Initialize Stellar client if needed
	// p.stellarClient = NewStellarClient(config)

	return nil
}

// GetName returns the unique processor name
func (p *ContractInvocationProcessor) GetName() string {
	return ProcessorName
}

// GetDataTopics returns the topics this processor handles
func (p *ContractInvocationProcessor) GetDataTopics() []string {
	return []string{"tx.success", "tx.failure"}
}

// ProcessEvent processes incoming transaction events
func (p *ContractInvocationProcessor) ProcessEvent(ctx context.Context, event *Event) ([]map[string]interface{}, error) {
	p.logger.WithFields(log.F{"event_id": event.ID, "event_topic": event.Topic}).Debug("Processing event")

	var txMeta xdr.TransactionMeta
	var txEnvelope xdr.TransactionEnvelope
	var txResult xdr.TransactionResult
	var ledgerSequence uint32
	var timestamp time.Time
	var txHash string
	var txSuccessful bool

	// Process by event topic
	switch event.Topic {
	case "tx.success":
		var txSuccess horizon.Transaction
		if err := json.Unmarshal(event.Data, &txSuccess); err != nil {
			return nil, fmt.Errorf("error unmarshalling transaction success event: %s", err)
		}

		// Extract required information from transaction
		if err := xdr.SafeUnmarshalBase64(txSuccess.EnvelopeXdr, &txEnvelope); err != nil {
			return nil, fmt.Errorf("error unmarshalling transaction envelope XDR: %s", err)
		}

		if err := xdr.SafeUnmarshalBase64(txSuccess.ResultXdr, &txResult); err != nil {
			return nil, fmt.Errorf("error unmarshalling transaction result XDR: %s", err)
		}

		if err := xdr.SafeUnmarshalBase64(txSuccess.ResultMetaXdr, &txMeta); err != nil {
			return nil, fmt.Errorf("error unmarshalling transaction meta XDR: %s", err)
		}

		ledgerSequence = uint32(txSuccess.Ledger)
		timestamp = txSuccess.LedgerCloseTime
		txHash = txSuccess.Hash
		txSuccessful = true

	case "tx.failure":
		var txFailure horizon.Transaction
		if err := json.Unmarshal(event.Data, &txFailure); err != nil {
			return nil, fmt.Errorf("error unmarshalling transaction failure event: %s", err)
		}

		// Extract required information from failed transaction
		if err := xdr.SafeUnmarshalBase64(txFailure.EnvelopeXdr, &txEnvelope); err != nil {
			return nil, fmt.Errorf("error unmarshalling transaction envelope XDR: %s", err)
		}

		if err := xdr.SafeUnmarshalBase64(txFailure.ResultXdr, &txResult); err != nil {
			return nil, fmt.Errorf("error unmarshalling transaction result XDR: %s", err)
		}

		if err := xdr.SafeUnmarshalBase64(txFailure.ResultMetaXdr, &txMeta); err != nil {
			return nil, fmt.Errorf("error unmarshalling transaction meta XDR: %s", err)
		}

		ledgerSequence = uint32(txFailure.Ledger)
		timestamp = txFailure.LedgerCloseTime
		txHash = txFailure.Hash
		txSuccessful = false

	default:
		return nil, fmt.Errorf("unsupported event topic: %s", event.Topic)
	}

	// Get transaction details
	transactionId := generateTransactionID(ledgerSequence)

	// Process each operation in the transaction to find contract invocations
	var results []map[string]interface{}
	if p.hasContractInvocations(txEnvelope, txMeta) {
		invocations, err := p.processContractInvocations(
			ctx,
			txEnvelope,
			txMeta,
			txResult,
			txHash,
			ledgerSequence,
			transactionId,
			timestamp,
			txSuccessful)
		if err != nil {
			return nil, err
		}
		results = append(results, invocations...)
	}

	return results, nil
}

// generateTransactionID creates a unique ID based on ledger sequence
func generateTransactionID(ledgerSequence uint32) int64 {
	// A simple implementation - in production, you'd want something more robust
	return int64(ledgerSequence) * 100000
}

// SorobanAddressToString converts a Soroban contract address to a string
func SorobanAddressToString(address xdr.ScAddress) (string, error) {
	switch address.Type {
	case xdr.ScAddressTypeScAddressTypeContract:
		if address.ContractId == nil {
			return "", fmt.Errorf("contract address has nil contract ID")
		}

		// In a real implementation, we would use strkey.Encode
		// For simplicity, just return a string representation of the bytes
		bytes := address.ContractId[:]
		return fmt.Sprintf("C_%x", bytes), nil

	case xdr.ScAddressTypeScAddressTypeAccount:
		if address.AccountId == nil {
			return "", fmt.Errorf("account address has nil account ID")
		}

		return address.AccountId.Address()

	default:
		return "", fmt.Errorf("unknown address type: %d", address.Type)
	}
}

// hasContractInvocations checks if the transaction includes contract invocations
func (p *ContractInvocationProcessor) hasContractInvocations(txEnvelope xdr.TransactionEnvelope, txMeta xdr.TransactionMeta) bool {
	switch txEnvelope.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTx:
		for _, op := range txEnvelope.V1.Tx.Operations {
			if op.Body.Type == xdr.OperationTypeInvokeHostFunction {
				return true
			}
		}
	case xdr.EnvelopeTypeEnvelopeTypeTxV0:
		for _, op := range txEnvelope.V0.Tx.Operations {
			if op.Body.Type == xdr.OperationTypeInvokeHostFunction {
				return true
			}
		}
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		innerOps := txEnvelope.FeeBump.Tx.InnerTx.V1.Tx.Operations
		for _, op := range innerOps {
			if op.Body.Type == xdr.OperationTypeInvokeHostFunction {
				return true
			}
		}
	}
	return false
}

// processContractInvocations processes a transaction containing contract invocations
func (p *ContractInvocationProcessor) processContractInvocations(
	ctx context.Context,
	txEnvelope xdr.TransactionEnvelope,
	txMeta xdr.TransactionMeta,
	txResult xdr.TransactionResult,
	txHash string,
	ledgerSequence uint32,
	transactionId int64,
	timestamp time.Time,
	txSuccessful bool) ([]map[string]interface{}, error) {

	p.logger.WithFields(log.F{"tx_hash": txHash}).Debug("Processing contract invocation transaction")

	var results []map[string]interface{}
	var operations []xdr.Operation
	var sourceAccount string

	// Extract operations and source account based on envelope type
	switch txEnvelope.Type {
	case xdr.EnvelopeTypeEnvelopeTypeTx:
		operations = txEnvelope.V1.Tx.Operations
		sourceAccount = txEnvelope.V1.Tx.SourceAccount.Address()
	case xdr.EnvelopeTypeEnvelopeTypeTxV0:
		operations = txEnvelope.V0.Tx.Operations
		// Need to handle the ed25519 address properly
		ed25519 := txEnvelope.V0.Tx.SourceAccountEd25519
		var accountID xdr.AccountId
		if err := accountID.SetAddress(ed25519.String()); err != nil {
			return nil, fmt.Errorf("error setting account ID from ed25519: %w", err)
		}
		sourceAccount = accountID.Address()
	case xdr.EnvelopeTypeEnvelopeTypeTxFeeBump:
		operations = txEnvelope.FeeBump.Tx.InnerTx.V1.Tx.Operations
		sourceAccount = txEnvelope.FeeBump.Tx.InnerTx.V1.Tx.SourceAccount.Address()
	default:
		return nil, fmt.Errorf("unsupported envelope type: %d", txEnvelope.Type)
	}

	// Process each operation
	for opIndex, op := range operations {
		if op.Body.Type != xdr.OperationTypeInvokeHostFunction {
			continue
		}

		// Get the operation source account or use transaction source account
		opSourceAccount := sourceAccount
		if op.SourceAccount != nil {
			opSourceAccount = op.SourceAccount.Address()
		}

		// Process the invoke host function operation
		invokeHostFn := op.Body.InvokeHostFunctionOp
		if invokeHostFn.HostFunction.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
			p.logger.WithFields(log.F{
				"function_type": invokeHostFn.HostFunction.Type.String(),
			}).Debug("Skipping non-invoke contract host function")
			continue
		}

		// Get contract ID and function
		invokeArgs := invokeHostFn.HostFunction.InvokeContract
		if len(invokeArgs.Args) < 1 {
			p.logger.Debug("Skipping invoke contract with no arguments")
			continue
		}

		// Extract contract ID
		contractId, err := SorobanAddressToString(invokeArgs.ContractAddress)
		if err != nil {
			p.logger.WithError(err).Warn("Error converting contract address to string")
			continue
		}

		// Extract function name
		fnName, err := extractFunctionName(invokeArgs.FunctionName)
		if err != nil {
			p.logger.WithError(err).Warn("Error extracting function name")
			continue
		}

		// Determine operation success
		opSuccess := false
		if txSuccessful && txResult.Result.Results != nil {
			if len(*txResult.Result.Results) > opIndex {
				result := (*txResult.Result.Results)[opIndex]
				if result.Tr.Type == xdr.OperationTypeInvokeHostFunction &&
					result.Tr.InvokeHostFunctionResult.Code == xdr.InvokeHostFunctionResultCodeInvokeHostFunctionSuccess {
					opSuccess = true
				}
			}
		}

		// Extract various data from the transaction
		contractCalls := extractContractCalls(txMeta, p.logger)
		stateChanges := extractStateChanges(txMeta, contractId, p.logger)
		ttlExtensions := extractTtlExtensions(txMeta, p.logger)
		arguments := extractArguments(invokeArgs.Args, p.logger)
		temporaryData := extractTemporaryData(txMeta, invokeArgs.Args, p.logger)
		diagnosticEvents := extractDiagnosticEvents(txMeta, p.logger)

		// Create the result object
		result := map[string]interface{}{
			"timestamp":        timestamp.Format(time.RFC3339),
			"ledgerSequence":   ledgerSequence,
			"transactionHash":  txHash,
			"transactionId":    transactionId,
			"contractId":       contractId,
			"invokingAccount":  opSourceAccount,
			"functionName":     fnName,
			"successful":       opSuccess,
			"arguments":        arguments,
			"contractCalls":    contractCalls,
			"stateChanges":     stateChanges,
			"ttlExtensions":    ttlExtensions,
			"temporaryData":    temporaryData,
			"diagnosticEvents": diagnosticEvents,
			"tags":             generateTags(contractId, fnName, opSourceAccount),
		}

		results = append(results, result)
	}

	return results, nil
}

// generateTags creates tags for the contract invocation
func generateTags(contractId, functionName, invokingAccount string) map[string]interface{} {
	return map[string]interface{}{
		"contract_id":      contractId,
		"function_name":    functionName,
		"invoking_account": invokingAccount,
	}
}

// extractFunctionName extracts the function name from the XDR ScSymbol
func extractFunctionName(functionNameXdr xdr.ScSymbol) (string, error) {
	fnName := string(functionNameXdr)
	if fnName == "" {
		return "", fmt.Errorf("empty function name")
	}
	return fnName, nil
}

// isCompatibleVersion checks if the current version is compatible with the minimum required version
func isCompatibleVersion(currentVersion, minVersion string) bool {
	// Ensure we have three parts (major.minor.patch) for both versions
	currentParts := strings.Split(currentVersion, ".")
	minParts := strings.Split(minVersion, ".")

	// Pad with zeros if parts are missing
	for len(currentParts) < 3 {
		currentParts = append(currentParts, "0")
	}
	for len(minParts) < 3 {
		minParts = append(minParts, "0")
	}

	// Compare major version
	if currentParts[0] != minParts[0] {
		return currentParts[0] > minParts[0]
	}

	// If major versions are equal, compare minor version
	if currentParts[1] != minParts[1] {
		return currentParts[1] > minParts[1]
	}

	// If minor versions are equal, compare patch version
	return currentParts[2] >= minParts[2]
}

// Declare the external extractor functions that will be implemented in the extractors package
func extractContractCalls(txMeta xdr.TransactionMeta, logger *log.Entry) []ContractCall {
	// This will be implemented in extractors/contract_calls.go
	return []ContractCall{}
}

func extractStateChanges(txMeta xdr.TransactionMeta, contractId string, logger *log.Entry) []StateChange {
	// This will be implemented in extractors/state_changes.go
	return []StateChange{}
}

func extractTtlExtensions(txMeta xdr.TransactionMeta, logger *log.Entry) []TtlExtension {
	// This will be implemented in extractors/ttl_extensions.go
	return []TtlExtension{}
}

func extractArguments(args []xdr.ScVal, logger *log.Entry) []json.RawMessage {
	// This will be implemented in extractors/arguments.go
	return []json.RawMessage{}
}

func extractTemporaryData(txMeta xdr.TransactionMeta, args []xdr.ScVal, logger *log.Entry) []TemporaryData {
	// This will be implemented in extractors/temporary_data.go
	return []TemporaryData{}
}

func extractDiagnosticEvents(txMeta xdr.TransactionMeta, logger *log.Entry) []DiagnosticEvent {
	// This will be implemented in extractors/diagnostic_events.go
	return []DiagnosticEvent{}
}
