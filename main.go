package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"strings"
	"sync"
	"time"

	"github.com/stellar/go/ingest"
	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
	"github.com/withObsrvr/pluginapi"
)

// ContractInvocation represents a contract invocation event
type ContractInvocation struct {
	Timestamp        time.Time         `json:"timestamp"`
	LedgerSequence   uint32            `json:"ledger_sequence"`
	TransactionHash  string            `json:"transaction_hash"`
	ContractID       string            `json:"contract_id"`
	InvokingAccount  string            `json:"invoking_account"`
	FunctionName     string            `json:"function_name"`
	Arguments        []json.RawMessage `json:"arguments,omitempty"`
	Successful       bool              `json:"successful"`
	DiagnosticEvents []DiagnosticEvent `json:"diagnostic_events,omitempty"`
	ContractCalls    []ContractCall    `json:"contract_calls,omitempty"`
	StateChanges     []StateChange     `json:"state_changes,omitempty"`
	TtlExtensions    []TtlExtension    `json:"ttl_extensions,omitempty"`
	TemporaryData    []TemporaryData   `json:"temporary_data,omitempty"`
}

// DiagnosticEvent represents a diagnostic event emitted during contract execution
type DiagnosticEvent struct {
	ContractID string          `json:"contract_id"`
	Topics     []string        `json:"topics"`
	Data       json.RawMessage `json:"data"`
}

// ContractCall represents a contract-to-contract call
type ContractCall struct {
	FromContract string `json:"from_contract"`
	ToContract   string `json:"to_contract"`
	Function     string `json:"function"`
	Successful   bool   `json:"successful"`
}

// StateChange represents a contract state change
type StateChange struct {
	ContractID string          `json:"contract_id"`
	Key        string          `json:"key"`
	OldValue   json.RawMessage `json:"old_value,omitempty"`
	NewValue   json.RawMessage `json:"new_value,omitempty"`
	Operation  string          `json:"operation"` // "create", "update", "delete"
}

// TtlExtension represents a TTL extension for a contract
type TtlExtension struct {
	ContractID string `json:"contract_id"`
	OldTtl     uint32 `json:"old_ttl"`
	NewTtl     uint32 `json:"new_ttl"`
}

// TemporaryData represents temporary data created during contract execution
type TemporaryData struct {
	Type        string          `json:"type"`
	Key         string          `json:"key"`
	Value       json.RawMessage `json:"value"`
	ExpiresAt   uint64          `json:"expires_at,omitempty"`
	LedgerEntry json.RawMessage `json:"ledger_entry,omitempty"`
	ContractID  string          `json:"contract_id,omitempty"`
	// Add fields to track which arguments were used
	RelatedArgs []int             `json:"related_args,omitempty"` // Indices of arguments that relate to this data
	ArgValues   []json.RawMessage `json:"arg_values,omitempty"`   // Values of related arguments
}

// ContractInvocationProcessor implements the pluginapi.Processor and pluginapi.Consumer interfaces
type ContractInvocationProcessor struct {
	consumers         []pluginapi.Consumer
	networkPassphrase string
	mu                sync.RWMutex
	stats             struct {
		ProcessedLedgers  uint32
		InvocationsFound  uint64
		SuccessfulInvokes uint64
		FailedInvokes     uint64
		LastLedger        uint32
		LastProcessedTime time.Time
	}
}

// New creates a new instance of the plugin
func New() pluginapi.Plugin {
	return &ContractInvocationProcessor{}
}

// Name returns the name of the plugin
func (p *ContractInvocationProcessor) Name() string {
	return "flow/processor/contract-invocations"
}

// Version returns the version of the plugin
func (p *ContractInvocationProcessor) Version() string {
	return "1.0.0"
}

// Type returns the type of the plugin
func (p *ContractInvocationProcessor) Type() pluginapi.PluginType {
	return pluginapi.ProcessorPlugin
}

// Initialize sets up the processor with configuration
func (p *ContractInvocationProcessor) Initialize(config map[string]interface{}) error {
	networkPassphrase, ok := config["network_passphrase"].(string)
	if !ok {
		return fmt.Errorf("missing network_passphrase in configuration")
	}

	p.networkPassphrase = networkPassphrase
	log.Printf("Initialized ContractInvocationProcessor with network: %s", networkPassphrase)
	return nil
}

// RegisterConsumer implements the ConsumerRegistry interface
func (p *ContractInvocationProcessor) RegisterConsumer(consumer pluginapi.Consumer) {
	p.consumers = append(p.consumers, consumer)
	log.Printf("Registered consumer with ContractInvocationProcessor: %s", consumer.Name())
}

// Process handles incoming messages (implements pluginapi.Processor)
func (p *ContractInvocationProcessor) Process(ctx context.Context, msg pluginapi.Message) error {
	// Check if this is a ledger close meta
	ledgerCloseMeta, ok := msg.Payload.(xdr.LedgerCloseMeta)
	if !ok {
		return fmt.Errorf("expected xdr.LedgerCloseMeta payload, got %T", msg.Payload)
	}

	sequence := ledgerCloseMeta.LedgerSequence()
	log.Printf("Processing ledger %d for contract invocations", sequence)

	txReader, err := ingest.NewLedgerTransactionReaderFromLedgerCloseMeta(p.networkPassphrase, ledgerCloseMeta)
	if err != nil {
		return fmt.Errorf("error creating transaction reader: %w", err)
	}
	defer txReader.Close()

	// Process each transaction
	for {
		tx, err := txReader.Read()
		if err == io.EOF {
			break
		}
		if err != nil {
			return fmt.Errorf("error reading transaction: %w", err)
		}

		// Check each operation for contract invocations
		for opIndex, op := range tx.Envelope.Operations() {
			if op.Body.Type == xdr.OperationTypeInvokeHostFunction {
				invocation, err := p.processContractInvocation(tx, opIndex, op, ledgerCloseMeta)
				if err != nil {
					log.Printf("Error processing contract invocation: %v", err)
					continue
				}

				if invocation != nil {
					if err := p.forwardToConsumers(ctx, invocation); err != nil {
						log.Printf("Error forwarding invocation: %v", err)
					}
				}
			}
		}
	}

	p.mu.Lock()
	p.stats.ProcessedLedgers++
	p.stats.LastLedger = sequence
	p.stats.LastProcessedTime = time.Now()
	p.mu.Unlock()

	return nil
}

func (p *ContractInvocationProcessor) processContractInvocation(
	tx ingest.LedgerTransaction,
	opIndex int,
	op xdr.Operation,
	meta xdr.LedgerCloseMeta,
) (*ContractInvocation, error) {
	// Use the correct method to access the invoke host function operation
	invokeHostFunction := op.Body.InvokeHostFunctionOp

	// Get the invoking account
	var invokingAccount xdr.AccountId
	if op.SourceAccount != nil {
		invokingAccount = op.SourceAccount.ToAccountId()
	} else {
		invokingAccount = tx.Envelope.SourceAccount().ToAccountId()
	}

	// Get contract ID if available
	var contractID string
	var functionName string
	var arguments []json.RawMessage

	if function := invokeHostFunction.HostFunction; function.Type == xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
		contractIDBytes := function.MustInvokeContract().ContractAddress.ContractId
		var err error
		contractID, err = strkey.Encode(strkey.VersionByteContract, contractIDBytes[:])
		if err != nil {
			return nil, fmt.Errorf("error encoding contract ID: %w", err)
		}

		// Extract function name from the function parameters
		invokeContract := function.MustInvokeContract()

		// The function name is typically the first argument in Soroban contract calls
		if len(invokeContract.Args) > 0 {
			// Try to get the function name from the first argument if it's a symbol
			if sym, ok := invokeContract.Args[0].GetSym(); ok {
				functionName = string(sym)
				log.Printf("Found function name from first argument: %s", functionName)
			} else {
				// If the first argument isn't a symbol, try to inspect it more deeply
				// Log the type for debugging
				log.Printf("First argument type: %v", invokeContract.Args[0].Type)

				// Try to marshal the argument to see its structure
				argBytes, _ := json.Marshal(invokeContract.Args[0])
				log.Printf("First argument value: %s", string(argBytes))

				// Try to extract function name from the function itself if available
				if invokeContract.FunctionName != "" {
					functionName = string(invokeContract.FunctionName)
					log.Printf("Found function name from FunctionName field: %s", functionName)
				}
			}
		}

		// Extract function arguments
		// Skip the first argument if it's the function name (symbol)
		startIdx := 0
		if len(invokeContract.Args) > 0 {
			if _, ok := invokeContract.Args[0].GetSym(); ok {
				startIdx = 1
			}
		}

		// Extract remaining arguments
		for i := startIdx; i < len(invokeContract.Args); i++ {
			argBytes, err := json.Marshal(invokeContract.Args[i])
			if err != nil {
				log.Printf("Error marshaling argument %d: %v", i, err)
				continue
			}
			arguments = append(arguments, argBytes)
		}

		log.Printf("Extracted %d arguments for function %s", len(arguments), functionName)
	}

	// Determine if invocation was successful
	successful := false

	// First check the transaction result code
	if tx.Result.Result.Result.Code == xdr.TransactionResultCodeTxSuccess {
		// If the transaction was successful, check the operation result
		if tx.Result.Result.Result.Results != nil {
			if results := *tx.Result.Result.Result.Results; len(results) > opIndex {
				if result := results[opIndex]; result.Code == xdr.OperationResultCodeOpInner {
					if result.Tr != nil {
						if invokeResult, ok := result.Tr.GetInvokeHostFunctionResult(); ok {
							successful = invokeResult.Code == xdr.InvokeHostFunctionResultCodeInvokeHostFunctionSuccess
						}
					}
				}
			}
		}
	}

	// Add debug logging
	log.Printf("Transaction result code: %v", tx.Result.Result.Result.Code)

	// Also check for diagnostic events - their presence often indicates success
	if tx.UnsafeMeta.V == 3 {
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil && len(sorobanMeta.Events) > 0 {
			// If there are diagnostic events, it's likely the invocation was successful
			log.Printf("Found %d diagnostic events, which suggests successful execution", len(sorobanMeta.Events))
			// We'll still use the result code as the primary indicator, but this is useful for debugging
		}
	}

	log.Printf("Final success determination for %s: %v", functionName, successful)

	p.mu.Lock()
	p.stats.InvocationsFound++
	if successful {
		p.stats.SuccessfulInvokes++
	} else {
		p.stats.FailedInvokes++
	}
	p.mu.Unlock()

	// Create invocation record
	invocation := &ContractInvocation{
		Timestamp:       time.Unix(int64(meta.LedgerHeaderHistoryEntry().Header.ScpValue.CloseTime), 0),
		LedgerSequence:  meta.LedgerSequence(),
		TransactionHash: tx.Result.TransactionHash.HexString(),
		ContractID:      contractID,
		InvokingAccount: invokingAccount.Address(),
		Successful:      successful,
		FunctionName:    functionName,
		Arguments:       arguments,
	}

	// Extract diagnostic events
	invocation.DiagnosticEvents = p.extractDiagnosticEvents(tx, opIndex)

	// If function name is still empty, try to extract it from diagnostic events as a fallback
	if invocation.FunctionName == "" && len(invocation.DiagnosticEvents) > 0 {
		for _, event := range invocation.DiagnosticEvents {
			if len(event.Topics) > 0 {
				// Try to parse the first topic to see if it contains a function name
				var topicData map[string]interface{}
				if err := json.Unmarshal([]byte(event.Topics[0]), &topicData); err == nil {
					if symValue, ok := topicData["Sym"].(string); ok && symValue != "" {
						invocation.FunctionName = symValue
						log.Printf("Extracted function name from diagnostic event: %s", invocation.FunctionName)
						break
					}
				}
			}
		}
	}

	// Extract contract-to-contract calls
	invocation.ContractCalls = p.extractContractCalls(tx, opIndex)

	// Extract state changes
	invocation.StateChanges = p.extractStateChanges(tx, opIndex)

	// Extract TTL extensions
	invocation.TtlExtensions = p.extractTtlExtensions(tx, opIndex)

	// Extract temporary data
	invocation.TemporaryData = p.extractTemporaryData(tx, opIndex, arguments)

	return invocation, nil
}

func (p *ContractInvocationProcessor) forwardToConsumers(ctx context.Context, invocation *ContractInvocation) error {
	jsonBytes, err := json.Marshal(invocation)
	if err != nil {
		return fmt.Errorf("error marshaling invocation: %w", err)
	}

	log.Printf("Forwarding invocation with function name: %s", invocation.FunctionName)
	log.Printf("JSON payload: %s", string(jsonBytes))

	log.Printf("Forwarding event to %d consumers", len(p.consumers))
	for _, consumer := range p.consumers {
		log.Printf("Forwarding to consumer: %s", consumer.Name())
		if err := consumer.Process(ctx, pluginapi.Message{
			Payload:   jsonBytes,
			Timestamp: time.Now(),
			Metadata: map[string]interface{}{
				"ledger_sequence": invocation.LedgerSequence,
				"contract_id":     invocation.ContractID,
				"function_name":   invocation.FunctionName,
			},
		}); err != nil {
			return fmt.Errorf("error in consumer chain: %w", err)
		}
	}
	return nil
}

// Close implements the Consumer interface
func (p *ContractInvocationProcessor) Close() error {
	// Clean up any resources if needed
	return nil
}

// extractDiagnosticEvents extracts diagnostic events from transaction meta
func (p *ContractInvocationProcessor) extractDiagnosticEvents(tx ingest.LedgerTransaction, opIndex int) []DiagnosticEvent {
	var events []DiagnosticEvent

	// Check if we have diagnostic events in the transaction meta
	if tx.UnsafeMeta.V == 3 {
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil && sorobanMeta.Events != nil {
			for _, event := range sorobanMeta.Events {
				// Convert contract ID
				contractIDBytes := event.ContractId
				contractID, err := strkey.Encode(strkey.VersionByteContract, contractIDBytes[:])
				if err != nil {
					log.Printf("Error encoding contract ID for diagnostic event: %v", err)
					continue
				}

				// Convert topics to strings
				var topics []string
				for _, topic := range event.Body.V0.Topics {
					// For simplicity, we'll just convert to JSON
					topicJSON, err := json.Marshal(topic)
					if err != nil {
						log.Printf("Error marshaling topic: %v", err)
						continue
					}
					topics = append(topics, string(topicJSON))
				}

				// Convert data to JSON
				dataJSON, err := json.Marshal(event.Body.V0.Data)
				if err != nil {
					log.Printf("Error marshaling event data: %v", err)
					continue
				}

				events = append(events, DiagnosticEvent{
					ContractID: contractID,
					Topics:     topics,
					Data:       dataJSON,
				})
			}
		}
	}

	return events
}

// extractContractCalls extracts contract calls from transaction meta
func (p *ContractInvocationProcessor) extractContractCalls(tx ingest.LedgerTransaction, opIndex int) []ContractCall {
	var calls []ContractCall

	// Check if we have Soroban meta in the transaction
	if tx.UnsafeMeta.V == 3 {
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil {
			// Process diagnostic events which may contain contract calls
			if len(sorobanMeta.DiagnosticEvents) > 0 {
				// Log diagnostic events for debugging
				log.Printf("Found %d diagnostic events", len(sorobanMeta.DiagnosticEvents))

				// Future implementation will process these events
				// when the XDR structure is better understood
			}
		}
	}

	return calls
}

// processInvocation processes a contract invocation and extracts contract calls
// This method is currently unused but will be used in future implementations
// when the XDR structure includes the necessary fields
func (p *ContractInvocationProcessor) processInvocation(
	invocation *xdr.SorobanAuthorizedInvocation,
	fromContract string,
	calls *[]ContractCall,
) {
	if invocation == nil {
		return
	}

	// Get the contract ID for this invocation
	var contractID string
	if invocation.Function.Type == xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn {
		contractIDBytes := invocation.Function.ContractFn.ContractAddress.ContractId
		var err error
		contractID, err = strkey.Encode(strkey.VersionByteContract, contractIDBytes[:])
		if err != nil {
			log.Printf("Error encoding contract ID for invocation: %v", err)
			return
		}
	}

	// Get the function name
	var functionName string
	if invocation.Function.Type == xdr.SorobanAuthorizedFunctionTypeSorobanAuthorizedFunctionTypeContractFn {
		functionName = string(invocation.Function.ContractFn.FunctionName)
	}

	// If we have both a from and to contract, record the call
	if fromContract != "" && contractID != "" && fromContract != contractID {
		*calls = append(*calls, ContractCall{
			FromContract: fromContract,
			ToContract:   contractID,
			Function:     functionName,
			Successful:   true, // We don't have success info at this level
		})
	}

	// Process sub-invocations
	for _, subInvocation := range invocation.SubInvocations {
		p.processInvocation(&subInvocation, contractID, calls)
	}
}

// extractStateChanges extracts state changes from transaction meta
func (p *ContractInvocationProcessor) extractStateChanges(tx ingest.LedgerTransaction, opIndex int) []StateChange {
	var changes []StateChange

	// Check if we have Soroban meta in the transaction
	if tx.UnsafeMeta.V == 3 {
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil {
			// Based on the debug output, there's no direct state changes field
			// State changes would need to be extracted from other transaction data

			// Process events which may contain state change information
			if len(sorobanMeta.Events) > 0 {
				for _, _event := range sorobanMeta.Events {
					// Process events for state changes
					// Implementation depends on the structure of ContractEvent
					_ = _event // Placeholder until implementation is complete
				}
			}
		}
	}

	return changes
}

// extractTtlExtensions extracts TTL extensions from transaction meta
func (p *ContractInvocationProcessor) extractTtlExtensions(tx ingest.LedgerTransaction, opIndex int) []TtlExtension {
	var extensions []TtlExtension

	// Check if we have Soroban meta in the transaction
	if tx.UnsafeMeta.V == 3 {
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil {
			// Based on the debug output, SorobanTransactionMeta has:
			// - Events
			// - ReturnValue
			// - DiagnosticEvents
			// But no TTL extension information in this version

			// If TTL extensions are added in future versions, they would be processed here
		}
	}

	return extensions
}

// extractArguments extracts arguments from the contract invocation
func (p *ContractInvocationProcessor) extractArguments(tx ingest.LedgerTransaction, opIndex int) []json.RawMessage {
	var arguments []json.RawMessage

	// Check if we have Soroban meta in the transaction
	if tx.UnsafeMeta.V == 3 {
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil {
			// Based on the debug output, there's no direct arguments field
			// Arguments would need to be extracted from other transaction data

			// Process events which may contain argument information
			if len(sorobanMeta.Events) > 0 {
				for _, _event := range sorobanMeta.Events {
					// Process events for arguments
					// Implementation depends on the structure of ContractEvent
					_ = _event // Placeholder until implementation is complete
				}
			}
		}
	}

	return arguments
}

// extractTemporaryData extracts temporary data from transaction meta
func (p *ContractInvocationProcessor) extractTemporaryData(
	tx ingest.LedgerTransaction,
	opIndex int,
	functionArgs []json.RawMessage,
) []TemporaryData {
	var tempData []TemporaryData

	// Check if we have Soroban meta in the transaction
	if tx.UnsafeMeta.V == 3 {
		// First, log the entire meta structure to understand what's available
		metaBytes, _ := json.Marshal(tx.UnsafeMeta.V3)
		log.Printf("Transaction meta V3 structure: %s", string(metaBytes))

		// Look for ledger entry changes in the transaction metadata
		// These are typically in the operations results or in the transaction meta
		for _, op := range tx.UnsafeMeta.V3.Operations {
			if op.Changes != nil {
				for _, change := range op.Changes {
					// Check if this is a ledger entry creation or modification
					if created, ok := change.GetCreated(); ok {
						// Check if this is a contract data entry
						if created.Data.Type == xdr.LedgerEntryTypeContractData {
							contractData := created.Data.ContractData

							// Log the contract data for debugging
							contractDataBytes, _ := json.Marshal(contractData)
							log.Printf("Contract data: %s", string(contractDataBytes))

							// Check if this is temporary data (has TTL)
							if created.LastModifiedLedgerSeq > 0 { // This is a proxy for TTL
								// Extract contract ID
								// Check if the contract address is valid
								if contractData.Contract.Type == 0 {
									log.Printf("Contract address type is 0, which may indicate an invalid address, skipping")
									continue
								}

								var contractIDBytes []byte

								// Handle different contract address types
								switch contractData.Contract.Type {
								case xdr.ScAddressTypeScAddressTypeContract:
									if contractData.Contract.ContractId == nil {
										log.Printf("ContractId is nil, skipping")
										continue
									}
									contractIDBytes = contractData.Contract.ContractId[:]
								case xdr.ScAddressTypeScAddressTypeAccount:
									if contractData.Contract.AccountId == nil {
										log.Printf("AccountId is nil, skipping")
										continue
									}
									// Handle account ID differently if needed
									log.Printf("Contract address is an account ID, not a contract ID")
									continue
								default:
									log.Printf("Unknown contract address type: %d", contractData.Contract.Type)
									continue
								}

								contractID, err := strkey.Encode(strkey.VersionByteContract, contractIDBytes)
								if err != nil {
									log.Printf("Error encoding contract ID: %v", err)
									continue
								}

								// Extract key and value
								keyBytes, err := json.Marshal(contractData.Key)
								if err != nil {
									log.Printf("Error marshaling key: %v", err)
									continue
								}

								valueBytes, err := json.Marshal(contractData.Val)
								if err != nil {
									log.Printf("Error marshaling value: %v", err)
									continue
								}

								// Create temporary data entry
								tempDataEntry := TemporaryData{
									Type:        "ContractData",
									Key:         string(keyBytes),
									Value:       valueBytes,
									ExpiresAt:   uint64(created.LastModifiedLedgerSeq), // Use as proxy for TTL
									LedgerEntry: nil,
									ContractID:  contractID,
								}

								// Try to correlate with function arguments
								keyStr := string(keyBytes)
								for i, arg := range functionArgs {
									argStr := string(arg)
									if strings.Contains(keyStr, argStr) {
										tempDataEntry.RelatedArgs = append(tempDataEntry.RelatedArgs, i)
										tempDataEntry.ArgValues = append(tempDataEntry.ArgValues, arg)
									}
								}

								tempData = append(tempData, tempDataEntry)
								log.Printf("Found temporary contract data with key: %s", keyStr)
							}
						}
					}

					// Also check for modified entries that might be temporary data
					if modified, ok := change.GetUpdated(); ok {
						// Just log for now until implementation is complete
						log.Printf("Found modified entry, implementation pending")
						_ = modified // Use the variable to avoid unused error
					}
				}
			}
		}

		// Also check the Soroban-specific metadata
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil {
			// Log the Soroban meta structure
			sorobanMetaBytes, _ := json.Marshal(sorobanMeta)
			log.Printf("Soroban meta structure: %s", string(sorobanMetaBytes))

			// Check for any fields that might contain temporary data information
			// This depends on the specific structure of SorobanTransactionMeta

			// Example: If there's a ResourceChanges field
			// if resourceChanges := sorobanMeta.ResourceChanges; resourceChanges != nil {
			//     // Process resource changes for temporary data
			//     // ...
			// }

			// Example: If there's a FootprintChanges field
			// if footprintChanges := sorobanMeta.FootprintChanges; footprintChanges != nil {
			//     // Process footprint changes for temporary data
			//     // ...
			// }
		}
	}

	return tempData
}
