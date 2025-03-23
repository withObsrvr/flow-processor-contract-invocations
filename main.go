package main

import (
	"context"
	"encoding/json"
	"errors"
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

// ErrorType categorizes the type of error for better handling
type ErrorType string

const (
	ErrorTypeConfig      ErrorType = "config"
	ErrorTypeProcessing  ErrorType = "processing"
	ErrorTypeParsing     ErrorType = "parsing"
	ErrorTypeConsumer    ErrorType = "consumer"
	ErrorTypeUnsupported ErrorType = "unsupported"
)

// ErrorSeverity indicates how serious an error is
type ErrorSeverity string

const (
	ErrorSeverityFatal   ErrorSeverity = "fatal"
	ErrorSeverityError   ErrorSeverity = "error"
	ErrorSeverityWarning ErrorSeverity = "warning"
)

// ProcessorError implements a rich error type for better error handling
type ProcessorError struct {
	Err             error                  // Original error
	Type            ErrorType              // Category of error
	Severity        ErrorSeverity          // How serious the error is
	TransactionHash string                 // Transaction context
	LedgerSequence  uint32                 // Ledger context
	ContractID      string                 // Contract context
	Context         map[string]interface{} // Additional metadata
}

// NewProcessorError creates a new processor error with the given parameters
func NewProcessorError(err error, errType ErrorType, severity ErrorSeverity) *ProcessorError {
	return &ProcessorError{
		Err:      err,
		Type:     errType,
		Severity: severity,
		Context:  make(map[string]interface{}),
	}
}

// Error implements the error interface
func (e *ProcessorError) Error() string {
	parts := []string{fmt.Sprintf("[%s:%s] %v", e.Type, e.Severity, e.Err)}

	if e.LedgerSequence > 0 {
		parts = append(parts, fmt.Sprintf("ledger=%d", e.LedgerSequence))
	}

	if e.TransactionHash != "" {
		parts = append(parts, fmt.Sprintf("tx=%s", e.TransactionHash))
	}

	if e.ContractID != "" {
		parts = append(parts, fmt.Sprintf("contract=%s", e.ContractID))
	}

	for k, v := range e.Context {
		parts = append(parts, fmt.Sprintf("%s=%v", k, v))
	}

	return strings.Join(parts, " ")
}

// WithLedger adds ledger sequence context to the error
func (e *ProcessorError) WithLedger(sequence uint32) *ProcessorError {
	e.LedgerSequence = sequence
	return e
}

// WithTransaction adds transaction hash context to the error
func (e *ProcessorError) WithTransaction(hash string) *ProcessorError {
	e.TransactionHash = hash
	return e
}

// WithContract adds contract ID context to the error
func (e *ProcessorError) WithContract(id string) *ProcessorError {
	e.ContractID = id
	return e
}

// WithContext adds additional context to the error
func (e *ProcessorError) WithContext(key string, value interface{}) *ProcessorError {
	e.Context[key] = value
	return e
}

// ContractInvocation represents a contract invocation event
type ContractInvocation struct {
	// Transaction context
	Timestamp       time.Time `json:"timestamp"`
	LedgerSequence  uint32    `json:"ledger_sequence"`
	TransactionHash string    `json:"transaction_hash"`
	TransactionID   int64     `json:"transaction_id"`

	// Invocation context
	ContractID      string `json:"contract_id"`
	InvokingAccount string `json:"invoking_account"`
	FunctionName    string `json:"function_name"`
	Successful      bool   `json:"successful"`

	// Invocation details
	Arguments        []json.RawMessage `json:"arguments,omitempty"`
	DiagnosticEvents []DiagnosticEvent `json:"diagnostic_events,omitempty"`
	ContractCalls    []ContractCall    `json:"contract_calls,omitempty"`
	StateChanges     []StateChange     `json:"state_changes,omitempty"`
	TtlExtensions    []TtlExtension    `json:"ttl_extensions,omitempty"`
	TemporaryData    []TemporaryData   `json:"temporary_data,omitempty"`

	// Searchable tags for filtering
	Tags map[string]string `json:"tags,omitempty"`
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

// ProcessorStats contains operational metrics for the processor
type ProcessorStats struct {
	ProcessedLedgers  uint32    `json:"processed_ledgers"`
	InvocationsFound  uint64    `json:"invocations_found"`
	SuccessfulInvokes uint64    `json:"successful_invokes"`
	FailedInvokes     uint64    `json:"failed_invokes"`
	LastLedger        uint32    `json:"last_ledger"`
	LastProcessedTime time.Time `json:"last_processed_time"`
	StartTime         time.Time `json:"start_time"`
}

// ContractInvocationProcessor implements the pluginapi.Processor and pluginapi.Consumer interfaces
type ContractInvocationProcessor struct {
	consumers         []pluginapi.Consumer
	networkPassphrase string
	mu                sync.RWMutex
	stats             ProcessorStats
}

// New creates a new instance of the plugin
func New() pluginapi.Plugin {
	return &ContractInvocationProcessor{
		stats: ProcessorStats{
			StartTime: time.Now(),
		},
	}
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
		return NewProcessorError(
			errors.New("missing network_passphrase in configuration"),
			ErrorTypeConfig,
			ErrorSeverityFatal,
		)
	}

	// Check for API version compatibility
	if apiVersion, ok := config["flow_api_version"].(string); ok {
		log.Printf("Flow API Version: %s", apiVersion)
		// Could implement version compatibility check here
	}

	p.networkPassphrase = networkPassphrase
	log.Printf("Initialized ContractInvocationProcessor with network: %s", networkPassphrase)
	return nil
}

// RegisterConsumer implements the ConsumerRegistry interface
func (p *ContractInvocationProcessor) RegisterConsumer(consumer pluginapi.Consumer) {
	p.mu.Lock()
	defer p.mu.Unlock()

	log.Printf("Registering consumer with ContractInvocationProcessor: %s", consumer.Name())
	p.consumers = append(p.consumers, consumer)
}

// Process handles incoming messages (implements pluginapi.Processor)
func (p *ContractInvocationProcessor) Process(ctx context.Context, msg pluginapi.Message) error {
	// Check for context cancellation first
	if err := ctx.Err(); err != nil {
		return NewProcessorError(
			fmt.Errorf("context canceled before processing: %w", err),
			ErrorTypeProcessing,
			ErrorSeverityWarning,
		)
	}

	// Type assertion with improved error handling
	ledgerCloseMeta, ok := msg.Payload.(xdr.LedgerCloseMeta)
	if !ok {
		return NewProcessorError(
			fmt.Errorf("expected xdr.LedgerCloseMeta payload, got %T", msg.Payload),
			ErrorTypeUnsupported,
			ErrorSeverityError,
		)
	}

	sequence := ledgerCloseMeta.LedgerSequence()
	log.Printf("Processing ledger %d for contract invocations", sequence)

	txReader, err := ingest.NewLedgerTransactionReaderFromLedgerCloseMeta(p.networkPassphrase, ledgerCloseMeta)
	if err != nil {
		return NewProcessorError(
			fmt.Errorf("error creating transaction reader: %w", err),
			ErrorTypeProcessing,
			ErrorSeverityError,
		).WithLedger(sequence)
	}
	defer txReader.Close()

	// Process each transaction
	for {
		// Use a timeout context for each transaction
		_, cancel := context.WithTimeout(ctx, 10*time.Second)
		tx, err := txReader.Read()
		cancel() // Always cancel to prevent context leak

		if err == io.EOF {
			break
		}

		if err != nil {
			// Continue processing despite transaction read errors
			procErr := NewProcessorError(
				fmt.Errorf("error reading transaction: %w", err),
				ErrorTypeProcessing,
				ErrorSeverityWarning,
			).WithLedger(sequence)
			log.Printf("Warning: %s", procErr.Error())
			continue
		}

		txHash := tx.Result.TransactionHash.HexString()

		// Check each operation for contract invocations
		for opIndex, op := range tx.Envelope.Operations() {
			if op.Body.Type == xdr.OperationTypeInvokeHostFunction {
				// Use a timeout context for each invocation processing
				invCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
				invocation, err := p.processContractInvocation(invCtx, tx, opIndex, op, ledgerCloseMeta)
				cancel() // Always cancel to prevent context leak

				if err != nil {
					var procErr *ProcessorError
					if !errors.As(err, &procErr) {
						// Wrap if not already a ProcessorError
						procErr = NewProcessorError(
							err,
							ErrorTypeProcessing,
							ErrorSeverityWarning,
						)
					}

					procErr.WithLedger(sequence).WithTransaction(txHash)
					log.Printf("Error processing contract invocation: %s", procErr.Error())

					p.mu.Lock()
					p.stats.FailedInvokes++
					p.mu.Unlock()
					continue
				}

				if invocation != nil {
					log.Printf("Found contract invocation for contract ID: %s, function: %s",
						invocation.ContractID, invocation.FunctionName)

					p.mu.Lock()
					p.stats.InvocationsFound++
					if invocation.Successful {
						p.stats.SuccessfulInvokes++
					}
					p.mu.Unlock()

					// Forward with a timeout context
					fwdCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
					err := p.forwardToConsumers(fwdCtx, invocation)
					cancel() // Always cancel to prevent context leak

					if err != nil {
						var procErr *ProcessorError
						if !errors.As(err, &procErr) {
							// Wrap if not already a ProcessorError
							procErr = NewProcessorError(
								err,
								ErrorTypeConsumer,
								ErrorSeverityWarning,
							)
						}

						procErr.WithLedger(sequence).
							WithTransaction(txHash).
							WithContract(invocation.ContractID)

						log.Printf("Error forwarding invocation: %s", procErr.Error())
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

// forwardToConsumers sends the invocation to all registered consumers
func (p *ContractInvocationProcessor) forwardToConsumers(ctx context.Context, invocation *ContractInvocation) error {
	// Check for context cancellation
	if err := ctx.Err(); err != nil {
		return NewProcessorError(
			fmt.Errorf("context canceled before forwarding: %w", err),
			ErrorTypeConsumer,
			ErrorSeverityWarning,
		)
	}

	// Serialize the invocation to JSON
	jsonBytes, err := json.Marshal(invocation)
	if err != nil {
		return NewProcessorError(
			fmt.Errorf("error marshaling invocation: %w", err),
			ErrorTypeProcessing,
			ErrorSeverityWarning,
		)
	}

	// Create message for consumers
	msg := pluginapi.Message{
		Payload:   jsonBytes,
		Timestamp: time.Now(),
		Metadata: map[string]interface{}{
			"ledger_sequence":  invocation.LedgerSequence,
			"contract_id":      invocation.ContractID,
			"function_name":    invocation.FunctionName,
			"transaction_hash": invocation.TransactionHash,
		},
	}

	// Make a copy of consumers to avoid race conditions
	p.mu.RLock()
	consumers := make([]pluginapi.Consumer, len(p.consumers))
	copy(consumers, p.consumers)
	p.mu.RUnlock()

	// Send to each consumer
	for _, consumer := range consumers {
		// Check context before each consumer to allow early exit
		if err := ctx.Err(); err != nil {
			return NewProcessorError(
				fmt.Errorf("context canceled during forwarding: %w", err),
				ErrorTypeConsumer,
				ErrorSeverityWarning,
			)
		}

		// Process with a timeout for each consumer
		consumerCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
		err := consumer.Process(consumerCtx, msg)
		cancel() // Always cancel to prevent context leak

		if err != nil {
			return NewProcessorError(
				fmt.Errorf("error in consumer %s: %w", consumer.Name(), err),
				ErrorTypeConsumer,
				ErrorSeverityWarning,
			).WithContext("consumer", consumer.Name())
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

// GetStatus returns operational metrics for the processor
func (p *ContractInvocationProcessor) GetStatus() map[string]interface{} {
	p.mu.RLock()
	defer p.mu.RUnlock()

	return map[string]interface{}{
		"stats":     p.stats,
		"uptime":    time.Since(p.stats.StartTime).String(),
		"consumers": len(p.consumers),
	}
}

// processContractInvocation extracts and processes a contract invocation
func (p *ContractInvocationProcessor) processContractInvocation(
	ctx context.Context,
	tx ingest.LedgerTransaction,
	opIndex int,
	op xdr.Operation,
	meta xdr.LedgerCloseMeta,
) (*ContractInvocation, error) {
	// Check for context cancellation
	if err := ctx.Err(); err != nil {
		return nil, NewProcessorError(
			fmt.Errorf("context canceled during invocation processing: %w", err),
			ErrorTypeProcessing,
			ErrorSeverityWarning,
		).WithContext("operation_index", opIndex)
	}

	// Use the correct method to access the invoke host function operation
	invokeHostFunction := op.Body.InvokeHostFunctionOp

	// Get the invoking account
	var invokingAccount xdr.AccountId
	if op.SourceAccount != nil {
		invokingAccount = op.SourceAccount.ToAccountId()
	} else {
		invokingAccount = tx.Envelope.SourceAccount().ToAccountId()
	}

	invokingAccountAddress, err := invokingAccount.GetAddress()
	if err != nil {
		return nil, NewProcessorError(
			fmt.Errorf("error getting invoking account address: %w", err),
			ErrorTypeParsing,
			ErrorSeverityWarning,
		).WithContext("operation_index", opIndex)
	}

	// Get contract ID if available
	var contractID string
	var functionName string
	var arguments []json.RawMessage

	if function := invokeHostFunction.HostFunction; function.Type == xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
		contractIDBytes := function.MustInvokeContract().ContractAddress.ContractId
		contractID, err = strkey.Encode(strkey.VersionByteContract, contractIDBytes[:])
		if err != nil {
			return nil, NewProcessorError(
				fmt.Errorf("error encoding contract ID: %w", err),
				ErrorTypeParsing,
				ErrorSeverityWarning,
			).WithContext("operation_index", opIndex)
		}

		// Extract function name from the function parameters
		invokeContract := function.MustInvokeContract()

		// The function name is typically the first argument in Soroban contract calls
		if len(invokeContract.Args) > 0 {
			// Try to get the function name from the first argument if it's a symbol
			if sym, ok := invokeContract.Args[0].GetSym(); ok {
				functionName = string(sym)
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

		// Process arguments
		for i := startIdx; i < len(invokeContract.Args); i++ {
			if arg, err := json.Marshal(invokeContract.Args[i]); err == nil {
				arguments = append(arguments, arg)
			}
		}
	}

	// Check if the operation was successful
	successful := tx.Result.Successful()

	// Create the invocation event
	invocation := &ContractInvocation{
		Timestamp:        time.Now(), // Use current time, ideally would use ledger close time
		LedgerSequence:   meta.LedgerSequence(),
		TransactionHash:  tx.Result.TransactionHash.HexString(),
		TransactionID:    int64(tx.Index),
		ContractID:       contractID,
		InvokingAccount:  invokingAccountAddress,
		FunctionName:     functionName,
		Arguments:        arguments,
		Successful:       successful,
		DiagnosticEvents: p.extractDiagnosticEvents(tx, opIndex),
		ContractCalls:    p.extractContractCalls(tx, opIndex),
		StateChanges:     p.extractStateChanges(tx, opIndex),
		TtlExtensions:    p.extractTtlExtensions(tx, opIndex),
		TemporaryData:    p.extractTemporaryData(tx, opIndex, arguments),
		Tags:             make(map[string]string),
	}

	// Add searchable tags for filtering
	invocation.Tags["contract_id"] = contractID
	invocation.Tags["function_name"] = functionName
	invocation.Tags["successful"] = fmt.Sprintf("%t", successful)
	invocation.Tags["invoking_account"] = invokingAccountAddress

	return invocation, nil
}

// GetSchemaDefinition implements the SchemaProvider interface
func (p *ContractInvocationProcessor) GetSchemaDefinition() string {
	return `
type ContractInvocation {
	timestamp: String!
	ledgerSequence: Int!
	transactionHash: String!
	transactionId: Int!
	contractId: String!
	invokingAccount: String!
	functionName: String!
	successful: Boolean!
	arguments: [JSON]
	diagnosticEvents: [DiagnosticEvent]
	contractCalls: [ContractCall]
	stateChanges: [StateChange]
	ttlExtensions: [TtlExtension]
	temporaryData: [TemporaryData]
	tags: JSON
}

type DiagnosticEvent {
	contractId: String!
	topics: [String]!
	data: JSON
}

type ContractCall {
	fromContract: String!
	toContract: String!
	function: String!
	successful: Boolean!
}

type StateChange {
	contractId: String!
	key: String!
	oldValue: JSON
	newValue: JSON
	operation: String!
}

type TtlExtension {
	contractId: String!
	oldTtl: Int!
	newTtl: Int!
}

type TemporaryData {
	type: String!
	key: String!
	value: JSON
	expiresAt: String
	ledgerEntry: JSON
	contractId: String
	relatedArgs: [Int]
	argValues: [JSON]
}

scalar JSON
`
}

// GetQueryDefinitions implements the SchemaProvider interface
func (p *ContractInvocationProcessor) GetQueryDefinitions() string {
	return `
	getContractInvocationByHash(hash: String!): ContractInvocation
	getContractInvocationsByContract(contractId: String!, limit: Int, offset: Int): [ContractInvocation]
	getContractInvocationsByFunction(contractId: String!, functionName: String!, limit: Int, offset: Int): [ContractInvocation]
	getContractInvocationsByAccount(account: String!, limit: Int, offset: Int): [ContractInvocation]
`
}
