package main

import (
	"fmt"
	"strings"
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
	Component       string                 // Component that generated the error
	Details         string                 // Additional details about the error
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
	parts := []string{fmt.Sprintf("[%s:%s]", e.Type, e.Severity)}

	if e.Component != "" {
		parts[0] = fmt.Sprintf("[%s:%s:%s]", e.Component, e.Type, e.Severity)
	}

	parts[0] = parts[0] + fmt.Sprintf(" %v", e.Err)

	if e.Details != "" {
		parts = append(parts, fmt.Sprintf("details=%s", e.Details))
	}

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

// Unwrap returns the underlying error
func (e *ProcessorError) Unwrap() error {
	return e.Err
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

// WithDetails adds detailed information to the error
func (e *ProcessorError) WithDetails(details string) *ProcessorError {
	e.Details = details
	return e
}

// WithComponent sets the component that generated the error
func (e *ProcessorError) WithComponent(component string) *ProcessorError {
	e.Component = component
	return e
}

// WithContext adds additional context to the error
func (e *ProcessorError) WithContext(key string, value interface{}) *ProcessorError {
	e.Context[key] = value
	return e
}

// Custom error types for the processor
var (
	ErrNoContractAddress       = fmt.Errorf("no contract address found")
	ErrInvalidContractAddress  = fmt.Errorf("invalid contract address")
	ErrNoFunctionName          = fmt.Errorf("no function name found")
	ErrMissingConfiguration    = fmt.Errorf("required configuration is missing")
	ErrIncompatibleVersion     = fmt.Errorf("incompatible API version")
	ErrUnsupportedEnvelopeType = fmt.Errorf("unsupported transaction envelope type")
	ErrNoContractInvocations   = fmt.Errorf("no contract invocations found in transaction")
	ErrUnsupportedEventTopic   = fmt.Errorf("unsupported event topic")
	ErrXDRDecoding             = fmt.Errorf("error decoding XDR data")
	ErrInvalidScVal            = fmt.Errorf("invalid ScVal format")
	ErrInvalidArgument         = fmt.Errorf("invalid argument")
	ErrNoMetadata              = fmt.Errorf("no metadata found")
	ErrInvalidMetadata         = fmt.Errorf("invalid metadata format")
)

// Helper functions for creating specific error types

// ContractCallError creates an error for contract call extraction issues
func ContractCallError(err error, details string, tx string) *ProcessorError {
	return NewProcessorError(err, ErrorTypeProcessing, ErrorSeverityError).
		WithComponent("ContractCalls").
		WithDetails(details).
		WithTransaction(tx)
}

// StateChangeError creates an error for state change extraction issues
func StateChangeError(err error, details string, tx string) *ProcessorError {
	return NewProcessorError(err, ErrorTypeProcessing, ErrorSeverityError).
		WithComponent("StateChanges").
		WithDetails(details).
		WithTransaction(tx)
}

// ArgumentError creates an error for argument extraction issues
func ArgumentError(err error, details string) *ProcessorError {
	return NewProcessorError(err, ErrorTypeParsing, ErrorSeverityError).
		WithComponent("Arguments").
		WithDetails(details)
}

// TemporaryDataError creates an error for temporary data extraction issues
func TemporaryDataError(err error, details string, tx string) *ProcessorError {
	return NewProcessorError(err, ErrorTypeProcessing, ErrorSeverityError).
		WithComponent("TemporaryData").
		WithDetails(details).
		WithTransaction(tx)
}
