package main

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"strconv"
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

		// Define the minimum supported version
		minSupportedVersion := "1.0.0"

		// Simple semver comparison
		if !isCompatibleVersion(apiVersion, minSupportedVersion) {
			return NewProcessorError(
				fmt.Errorf("flow_api_version %s is not compatible with minimum supported version %s",
					apiVersion, minSupportedVersion),
				ErrorTypeConfig,
				ErrorSeverityFatal,
			)
		}

		log.Printf("API version %s is compatible with minimum version %s", apiVersion, minSupportedVersion)
	}

	p.networkPassphrase = networkPassphrase
	log.Printf("Initialized ContractInvocationProcessor with network: %s", networkPassphrase)
	return nil
}

// isCompatibleVersion checks if the current version is compatible with the minimum required version
// This is a simplified version compare - in production, use a proper semver library
func isCompatibleVersion(current, minimum string) bool {
	// Parse versions - this is a simple implementation
	// In production code, use a proper semver library
	currentParts := strings.Split(current, ".")
	minimumParts := strings.Split(minimum, ".")

	// Ensure we have at least 3 parts (major.minor.patch)
	for len(currentParts) < 3 {
		currentParts = append(currentParts, "0")
	}
	for len(minimumParts) < 3 {
		minimumParts = append(minimumParts, "0")
	}

	// Compare major version
	currentMajor, _ := strconv.Atoi(currentParts[0])
	minimumMajor, _ := strconv.Atoi(minimumParts[0])

	if currentMajor > minimumMajor {
		return true
	}
	if currentMajor < minimumMajor {
		return false
	}

	// Compare minor version
	currentMinor, _ := strconv.Atoi(currentParts[1])
	minimumMinor, _ := strconv.Atoi(minimumParts[1])

	if currentMinor > minimumMinor {
		return true
	}
	if currentMinor < minimumMinor {
		return false
	}

	// Compare patch version
	currentPatch, _ := strconv.Atoi(currentParts[2])
	minimumPatch, _ := strconv.Atoi(minimumParts[2])

	return currentPatch >= minimumPatch
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
			).WithContext("consumer", consumer.Name())
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
		if sorobanMeta != nil && sorobanMeta.Events != nil {
			for _, event := range sorobanMeta.Events {
				// Look for contract-to-contract calls in the events
				if event.Type == xdr.ContractEventTypeContract && event.Body.V0 != nil {
					topics := event.Body.V0.Topics

					// Skip if there aren't enough topics for a contract call (need at least 3)
					// First topic is usually the function name, second and third can be contract addresses
					if len(topics) < 3 {
						continue
					}

					// Get function name from first topic (if it's a symbol)
					var functionName string
					if sym, ok := topics[0].GetSym(); ok {
						functionName = string(sym)
					} else {
						continue // Skip if first topic is not a symbol
					}

					// Check if this is a contract call related function
					if !isContractCallFunction(functionName) {
						continue
					}

					// Extract "from" contract address (caller) from second topic
					var fromContractID string
					// Handle second topic as potential address
					if address, err := extractAddressFromScVal(topics[1]); err == nil && address != "" {
						fromContractID = address
					} else {
						continue // Skip if we can't get a valid from address
					}

					// Extract "to" contract address (callee) from third topic
					var toContractID string
					// Handle third topic as potential address
					if address, err := extractAddressFromScVal(topics[2]); err == nil && address != "" {
						toContractID = address
					} else {
						continue // Skip if we can't get a valid to address
					}

					// If both contract IDs are valid and different, record the call
					if fromContractID != "" && toContractID != "" && fromContractID != toContractID {
						// Build arguments from the remaining topics
						var args []string
						for i := 3; i < len(topics); i++ {
							argBytes, err := json.Marshal(topics[i])
							if err != nil {
								log.Printf("Error marshaling argument topic: %v", err)
								continue
							}
							args = append(args, string(argBytes))
						}

						// Add data if present
						if event.Body.V0.Data != (xdr.ScVal{}) {
							dataBytes, err := json.Marshal(event.Body.V0.Data)
							if err == nil {
								args = append(args, string(dataBytes))
							}
						}

						// Create and add the contract call
						contractCall := ContractCall{
							FromContract: fromContractID,
							ToContract:   toContractID,
							Function:     functionName,
							Successful:   true, // Default to true, to be updated based on diagnostics
						}

						calls = append(calls, contractCall)
						log.Printf("Found contract call from %s to %s, function: %s", fromContractID, toContractID, functionName)
					}
				}
			}
		}

		// Process diagnostic events if available
		if sorobanMeta != nil && sorobanMeta.DiagnosticEvents != nil {
			// Just log that we found diagnostic events, no need to process each one individually yet
			log.Printf("Found %d diagnostic events for potential contract calls", len(sorobanMeta.DiagnosticEvents))
		}
	}

	return calls
}

// extractAddressFromScVal attempts to extract a contract or account address from an ScVal
func extractAddressFromScVal(val xdr.ScVal) (string, error) {
	// Check for Address type
	if val.Type == xdr.ScValTypeScvAddress {
		address, ok := val.GetAddress()
		if !ok {
			return "", fmt.Errorf("failed to get address from ScVal")
		}

		switch address.Type {
		case xdr.ScAddressTypeScAddressTypeContract:
			if address.ContractId != nil {
				return strkey.Encode(strkey.VersionByteContract, address.ContractId[:])
			}
		case xdr.ScAddressTypeScAddressTypeAccount:
			if address.AccountId != nil {
				if addr, err := address.AccountId.GetAddress(); err == nil {
					return addr, nil
				}
			}
		}
	}

	// Check for Bytes type (which might contain an address)
	if val.Type == xdr.ScValTypeScvBytes {
		if bytes, ok := val.GetBytes(); ok && len(bytes) == 32 {
			// Might be a contract ID in bytes form
			return strkey.Encode(strkey.VersionByteContract, bytes)
		}
	}

	return "", fmt.Errorf("value does not contain a valid address")
}

// isContractCallFunction checks if a function name indicates a contract-to-contract call
func isContractCallFunction(functionName string) bool {
	// Common function names that might indicate contract calls
	contractCallFunctions := map[string]bool{
		"invoke":          true,
		"call":            true,
		"invoke_contract": true,
		"cross_contract":  true,
		"xchain":          true,
		"transfer":        true,
		"send":            true,
		"execute":         true,
	}

	// Check if the function name is in our list
	if contractCallFunctions[functionName] {
		return true
	}

	// Check for common prefixes or suffixes
	if strings.HasPrefix(functionName, "call_") ||
		strings.HasPrefix(functionName, "invoke_") ||
		strings.HasSuffix(functionName, "_call") ||
		strings.HasSuffix(functionName, "_invoke") {
		return true
	}

	return false
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
		// Track contract data changes in Operations/Changes
		for _, op := range tx.UnsafeMeta.V3.Operations {
			if op.Changes != nil {
				for _, change := range op.Changes {
					// Process created contract data (state create)
					if created, ok := change.GetCreated(); ok {
						if created.Data.Type == xdr.LedgerEntryTypeContractData {
							contractData := created.Data.ContractData

							// Process the contract data creation
							if contractData.Contract.Type == xdr.ScAddressTypeScAddressTypeContract && contractData.Contract.ContractId != nil {
								// Get contract ID
								contractID, err := strkey.Encode(strkey.VersionByteContract, contractData.Contract.ContractId[:])
								if err != nil {
									log.Printf("Error encoding contract ID: %v", err)
									continue
								}

								// Get key and new value
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

								// Create state change
								stateChange := StateChange{
									ContractID: contractID,
									Key:        string(keyBytes),
									NewValue:   valueBytes,
									Operation:  "create",
								}

								changes = append(changes, stateChange)
								log.Printf("Found state CREATION for contract %s, key: %s", contractID, string(keyBytes))
							}
						}
					}

					// Process updated contract data (state update)
					if updated, ok := change.GetUpdated(); ok {
						if updated.Data.Type == xdr.LedgerEntryTypeContractData {
							contractData := updated.Data.ContractData

							// Process the contract data update
							if contractData.Contract.Type == xdr.ScAddressTypeScAddressTypeContract && contractData.Contract.ContractId != nil {
								// Get contract ID
								contractID, err := strkey.Encode(strkey.VersionByteContract, contractData.Contract.ContractId[:])
								if err != nil {
									log.Printf("Error encoding contract ID: %v", err)
									continue
								}

								// Get key and new value
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

								// Create state change
								stateChange := StateChange{
									ContractID: contractID,
									Key:        string(keyBytes),
									NewValue:   valueBytes,
									Operation:  "update",
								}

								// Try to find the previous value if we can
								// Note: In a complete implementation, we would track the old value by matching
								// this contract and key in a previous entry in TxChangesBefore

								changes = append(changes, stateChange)
								log.Printf("Found state UPDATE for contract %s, key: %s", contractID, string(keyBytes))
							}
						}
					}

					// Process removed contract data (state delete)
					if removed, ok := change.GetRemoved(); ok {
						if removed.Type == xdr.LedgerEntryTypeContractData {
							contractData := removed.ContractData

							// Process the contract data removal
							if contractData.Contract.Type == xdr.ScAddressTypeScAddressTypeContract && contractData.Contract.ContractId != nil {
								// Get contract ID
								contractID, err := strkey.Encode(strkey.VersionByteContract, contractData.Contract.ContractId[:])
								if err != nil {
									log.Printf("Error encoding contract ID: %v", err)
									continue
								}

								// Get key
								keyBytes, err := json.Marshal(contractData.Key)
								if err != nil {
									log.Printf("Error marshaling key: %v", err)
									continue
								}

								// Create state change
								stateChange := StateChange{
									ContractID: contractID,
									Key:        string(keyBytes),
									Operation:  "delete",
								}

								// Try to find the old value if we can
								// Note: In a complete implementation, we would track the old value by matching
								// this contract and key in a previous entry in TxChangesBefore

								changes = append(changes, stateChange)
								log.Printf("Found state DELETION for contract %s, key: %s", contractID, string(keyBytes))
							}
						}
					}
				}
			}
		}

		// Also check the Soroban-specific events for state changes
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil && sorobanMeta.Events != nil {
			for _, event := range sorobanMeta.Events {
				// Some contract events also indicate state changes through their topics and data
				// This is more specialized and depends on the contract's conventions
				if event.Type == xdr.ContractEventTypeContract && event.Body.V0 != nil {
					topics := event.Body.V0.Topics

					// Skip if there are no topics
					if len(topics) < 1 {
						continue
					}

					// Get event type from first topic
					var eventType string
					if sym, ok := topics[0].GetSym(); ok {
						eventType = string(sym)
					} else {
						continue // Skip if first topic is not a symbol
					}

					// Look for events that indicate state changes like "set_value", "update", etc.
					// These are contract-specific and would need to be customized
					if eventType == "set" || eventType == "update" || eventType == "store" ||
						eventType == "delete" || eventType == "remove" {
						// These events likely indicate state changes
						// Log for future implementation
						log.Printf("Found potential state change event: %s", eventType)
					}
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
		// Check for TTL extensions in ledger entry changes
		for _, op := range tx.UnsafeMeta.V3.Operations {
			if op.Changes != nil {
				for _, change := range op.Changes {
					// Process updated contract data for TTL extensions
					if updated, ok := change.GetUpdated(); ok {
						if updated.Data.Type == xdr.LedgerEntryTypeContractData {
							contractData := updated.Data.ContractData

							// If this is a ContractData entry, check for TTL changes by comparing
							// the LastModifiedLedgerSeq with the previous value
							if contractData.Contract.Type == xdr.ScAddressTypeScAddressTypeContract &&
								contractData.Contract.ContractId != nil {

								// Get contract ID
								contractID, err := strkey.Encode(strkey.VersionByteContract, contractData.Contract.ContractId[:])
								if err != nil {
									log.Printf("Error encoding contract ID: %v", err)
									continue
								}

								// Check if the update includes TTL changes
								// In a real implementation, we would compare with the previous entry
								// For simplicity, we'll create TTL extension records when we detect
								// updated contract data entries with a high LastModifiedLedgerSeq
								newTtl := uint32(updated.LastModifiedLedgerSeq)
								oldTtl := uint32(0) // Placeholder, in a real impl we'd find the old value

								// Only record when new TTL > old TTL
								if newTtl > oldTtl {
									ttlExtension := TtlExtension{
										ContractID: contractID,
										OldTtl:     oldTtl,
										NewTtl:     newTtl,
									}
									extensions = append(extensions, ttlExtension)
									log.Printf("Found TTL extension for contract %s, new TTL: %d", contractID, newTtl)
								}
							}
						}
					}
				}
			}
		}

		// Check for explicit TTL extensions in the Soroban meta
		// In Soroban, TTL extensions might be stored in specific fields
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil {
			// For future TTL extension implementation:
			// In future Soroban versions, TTL extensions might be available in:
			// 1. SorobanTransactionMeta.FootprintChanges
			// 2. SorobanTransactionMeta.ResourceChanges
			// 3. SorobanTransactionMeta.ExtendFootprintTTL

			// For now, log the presence of diagnostic events which might contain TTL info
			if sorobanMeta.DiagnosticEvents != nil && len(sorobanMeta.DiagnosticEvents) > 0 {
				log.Printf("Found diagnostic events that might contain TTL extension information")
			}
		}
	}

	return extensions
}

// extractArguments extracts arguments from the contract invocation
func (p *ContractInvocationProcessor) extractArguments(tx ingest.LedgerTransaction, opIndex int) []json.RawMessage {
	var arguments []json.RawMessage

	// Check if we have the right operation type (InvokeHostFunction)
	if tx.Envelope.Type != xdr.EnvelopeTypeEnvelopeTypeTx {
		return arguments
	}

	// Get the operations
	ops := tx.Envelope.Operations()
	if opIndex >= len(ops) {
		log.Printf("Operation index %d out of bounds (max: %d)", opIndex, len(ops)-1)
		return arguments
	}

	// Get the specific operation
	op := ops[opIndex]

	// Check if it's an InvokeHostFunction operation
	if op.Body.Type != xdr.OperationTypeInvokeHostFunction {
		return arguments
	}

	// Get the host function
	invokeHostFunction := op.Body.InvokeHostFunctionOp
	function := invokeHostFunction.HostFunction

	// Check if it's a contract invocation
	if function.Type != xdr.HostFunctionTypeHostFunctionTypeInvokeContract {
		return arguments
	}

	// Extract the contract invocation details
	invokeContract := function.MustInvokeContract()

	// Skip the first argument if it's the function name (symbol)
	startIdx := 0
	if len(invokeContract.Args) > 0 {
		if _, ok := invokeContract.Args[0].GetSym(); ok {
			startIdx = 1
		}
	}

	// Process and extract arguments with dual representation
	for i := startIdx; i < len(invokeContract.Args); i++ {
		// Use the dual representation for better argument parsing
		dualRep := serializeScVal(invokeContract.Args[i])

		// Marshal the dual representation to JSON
		dualRepJson, jsonErr := json.Marshal(dualRep)
		if jsonErr == nil {
			arguments = append(arguments, dualRepJson)
			log.Printf("Extracted argument %d of type %s", i, dualRep.Type)
		} else {
			log.Printf("Failed to marshal dual representation for argument %d: %v", i, jsonErr)
			// Fallback to regular marshaling if dual representation fails
			if arg, err2 := json.Marshal(invokeContract.Args[i]); err2 == nil {
				arguments = append(arguments, arg)
				log.Printf("Extracted argument %d (fallback)", i)
			} else {
				log.Printf("Failed to marshal argument %d: %v", i, err2)
			}
		}
	}

	// Also check Soroban meta for additional context
	if tx.UnsafeMeta.V == 3 {
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil && sorobanMeta.Events != nil {
			// Look for events that might contain additional argument info
			for _, event := range sorobanMeta.Events {
				if event.Type == xdr.ContractEventTypeContract && event.Body.V0 != nil {
					// Check if this is an event for the operation's contract
					// Skip the first topic (event name) and look for argument-related topics
					if len(event.Body.V0.Topics) > 1 {
						// Log the topics for debugging
						for i, topic := range event.Body.V0.Topics {
							if i == 0 {
								// First topic is usually the event name
								if sym, ok := topic.GetSym(); ok {
									eventName := string(sym)
									log.Printf("Found event: %s", eventName)
								}
								continue
							}

							// For other topics, check if they might be related to arguments
							// For now, just log them
							log.Printf("Event topic %d: %v", i, topic)
						}

						// Check if event has data that's not empty (all Zero values)
						emptyScVal := xdr.ScVal{}
						if event.Body.V0.Data != emptyScVal {
							log.Printf("Event has data field, might contain additional argument info")
						}
					}
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
						// Process the modified entry
						if modified.Data.Type == xdr.LedgerEntryTypeContractData {
							// Log the detection of a modified entry
							log.Printf("Found modified contract data entry")
						}
					}
				}
			}
		}

		// Also check the Soroban-specific metadata
		sorobanMeta := tx.UnsafeMeta.V3.SorobanMeta
		if sorobanMeta != nil {
			// Check for any temporary data in the events
			if sorobanMeta.Events != nil {
				for _, event := range sorobanMeta.Events {
					if event.Type == xdr.ContractEventTypeContract && event.Body.V0 != nil {
						// Check for events that might indicate temporary data creation
						if len(event.Body.V0.Topics) > 0 {
							if sym, ok := event.Body.V0.Topics[0].GetSym(); ok {
								eventName := string(sym)
								// Look for events related to temporary data
								if strings.Contains(strings.ToLower(eventName), "temp") ||
									strings.Contains(strings.ToLower(eventName), "cache") ||
									strings.Contains(strings.ToLower(eventName), "ephemeral") {
									log.Printf("Found event potentially related to temporary data: %s", eventName)
								}
							}
						}
					}
				}
			}
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
				argBytes, err := json.Marshal(invokeContract.Args[0])
				if err != nil {
					log.Printf("Error marshaling first argument: %v", err)
				} else {
					log.Printf("First argument value: %s", string(argBytes))
				}

				// Try to extract function name from the function itself if available
				if invokeContract.FunctionName != "" {
					functionName = string(invokeContract.FunctionName)
				}
			}
		}

		// Extract arguments using the dedicated method
		arguments = p.extractArguments(tx, opIndex)
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
	invocation.Tags["ledger"] = fmt.Sprintf("%d", meta.LedgerSequence())

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

// Add these structures after the existing ContractInvocation struct

// DualValue represents both raw and decoded versions of a value
type DualValue struct {
	Raw     string          `json:"raw"`     // Raw encoded data
	Decoded json.RawMessage `json:"decoded"` // Decoded human-readable data
}

// ScValDualRepresentation stores both raw and decoded versions of a Stellar ScVal
type ScValDualRepresentation struct {
	Type      string          `json:"type"`
	RawValue  string          `json:"raw_value"`
	Value     json.RawMessage `json:"value"`
	TypeInt   int32           `json:"type_int,omitempty"`
	ScValType string          `json:"scval_type,omitempty"`
}

// serializeScVal converts a Stellar ScVal to its dual representation (raw and decoded)
func serializeScVal(scVal xdr.ScVal) *ScValDualRepresentation {
	result := &ScValDualRepresentation{
		TypeInt:   int32(scVal.Type),
		ScValType: scVal.Type.String(),
	}

	// Set the type name based on the ScVal type
	if scValTypeName, ok := scVal.ArmForSwitch(int32(scVal.Type)); ok {
		result.Type = scValTypeName
	} else {
		result.Type = fmt.Sprintf("unknown_type_%d", scVal.Type)
	}

	// Get the raw value
	if raw, err := scVal.MarshalBinary(); err == nil {
		result.RawValue = base64.StdEncoding.EncodeToString(raw)
	}

	// Get the decoded value based on type
	var decodedValue interface{}

	switch scVal.Type {
	case xdr.ScValTypeScvBool:
		if val, ok := scVal.GetB(); ok {
			decodedValue = val
		}
	case xdr.ScValTypeScvVoid:
		decodedValue = nil
	case xdr.ScValTypeScvU32:
		if val, ok := scVal.GetU32(); ok {
			decodedValue = val
		}
	case xdr.ScValTypeScvI32:
		if val, ok := scVal.GetI32(); ok {
			decodedValue = val
		}
	case xdr.ScValTypeScvU64:
		if val, ok := scVal.GetU64(); ok {
			decodedValue = val
		}
	case xdr.ScValTypeScvI64:
		if val, ok := scVal.GetI64(); ok {
			decodedValue = val
		}
	case xdr.ScValTypeScvTimepoint:
		if val, ok := scVal.GetTimepoint(); ok {
			decodedValue = val
		}
	case xdr.ScValTypeScvDuration:
		if val, ok := scVal.GetDuration(); ok {
			decodedValue = val
		}
	case xdr.ScValTypeScvU128:
		if val, ok := scVal.GetU128(); ok {
			// Convert u128 to readable format
			decodedValue = map[string]interface{}{
				"hi": val.Hi,
				"lo": val.Lo,
			}
		}
	case xdr.ScValTypeScvI128:
		if val, ok := scVal.GetI128(); ok {
			// Convert i128 to readable format
			decodedValue = map[string]interface{}{
				"hi": val.Hi,
				"lo": val.Lo,
			}
		}
	case xdr.ScValTypeScvBytes:
		if val, ok := scVal.GetBytes(); ok {
			// Base64 encode bytes
			decodedValue = base64.StdEncoding.EncodeToString(val)
		}
	case xdr.ScValTypeScvString:
		if val, ok := scVal.GetStr(); ok {
			decodedValue = string(val)
		}
	case xdr.ScValTypeScvSymbol:
		if val, ok := scVal.GetSym(); ok {
			decodedValue = string(val)
		}
	case xdr.ScValTypeScvVec:
		if val, ok := scVal.GetVec(); ok && val != nil {
			// Process array of values
			elements := make([]interface{}, 0, len(*val))
			for _, elem := range *val {
				elements = append(elements, serializeScVal(elem))
			}
			decodedValue = elements
		}
	case xdr.ScValTypeScvMap:
		if val, ok := scVal.GetMap(); ok && val != nil {
			// Process map of key-value pairs
			mapResult := make(map[string]interface{})
			for _, entry := range *val {
				keyScVal := serializeScVal(entry.Key)
				valueScVal := serializeScVal(entry.Val)

				// Use the key's string representation as map key if possible
				keyBytes, _ := json.Marshal(keyScVal)
				mapResult[string(keyBytes)] = valueScVal
			}
			decodedValue = mapResult
		}
	case xdr.ScValTypeScvAddress:
		if val, ok := scVal.GetAddress(); ok {
			// Convert address to string format
			switch val.Type {
			case xdr.ScAddressTypeScAddressTypeAccount:
				if val.AccountId != nil {
					if address, err := val.AccountId.GetAddress(); err == nil {
						decodedValue = map[string]interface{}{
							"type":    "account",
							"address": address,
						}
					}
				}
			case xdr.ScAddressTypeScAddressTypeContract:
				if val.ContractId != nil {
					contractID, err := strkey.Encode(strkey.VersionByteContract, val.ContractId[:])
					if err == nil {
						decodedValue = map[string]interface{}{
							"type":        "contract",
							"contract_id": contractID,
						}
					}
				}
			}
		}
	default:
		// For other types, use string representation
		decodedValue = scVal.String()
	}

	// Marshal the decoded value to JSON
	if decodedValue != nil {
		valueBytes, err := json.Marshal(decodedValue)
		if err == nil {
			result.Value = valueBytes
		}
	}

	return result
}
