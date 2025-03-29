package main

import (
	"encoding/json"
	"time"
)

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

// ContractCall represents a contract-to-contract call
type ContractCall struct {
	FromContract     string            `json:"from_contract"`
	ToContract       string            `json:"to_contract"`
	FunctionName     string            `json:"function_name"`
	Arguments        []json.RawMessage `json:"arguments,omitempty"`
	ReturnValue      json.RawMessage   `json:"return_value,omitempty"`
	Successful       bool              `json:"successful"`
	DiagnosticEvents []DiagnosticEvent `json:"diagnostic_events,omitempty"`
}

// StateChange represents a change to contract state
type StateChange struct {
	ContractID    string          `json:"contract_id"`
	LedgerKey     string          `json:"ledger_key"`
	KeyType       string          `json:"key_type"`
	KeyName       string          `json:"key_name,omitempty"`
	BeforeValue   json.RawMessage `json:"before_value,omitempty"`
	AfterValue    json.RawMessage `json:"after_value,omitempty"`
	ChangeType    string          `json:"change_type"` // created, updated, deleted
	RawLedgerKey  json.RawMessage `json:"raw_ledger_key,omitempty"`
	RawBeforeData json.RawMessage `json:"raw_before_data,omitempty"`
	RawAfterData  json.RawMessage `json:"raw_after_data,omitempty"`
}

// TtlExtension represents a contract state TTL extension
type TtlExtension struct {
	ContractID     string          `json:"contract_id"`
	LedgerKey      string          `json:"ledger_key"`
	ExtendedToSlot uint32          `json:"extended_to_slot"`
	ExtendedToTime time.Time       `json:"extended_to_time,omitempty"`
	RawLedgerKey   json.RawMessage `json:"raw_ledger_key,omitempty"`
}

// TemporaryData represents temporary data generated during contract execution
type TemporaryData struct {
	Type        string            `json:"type"`
	Key         string            `json:"key"`
	Value       json.RawMessage   `json:"value,omitempty"`
	ExpiresAt   uint32            `json:"expires_at,omitempty"`
	LedgerEntry json.RawMessage   `json:"ledger_entry,omitempty"`
	ContractID  string            `json:"contract_id,omitempty"`
	RelatedArgs []int             `json:"related_args,omitempty"`
	ArgValues   []json.RawMessage `json:"arg_values,omitempty"`
}

// DiagnosticEvent represents a diagnostic event emitted during contract execution
type DiagnosticEvent struct {
	ContractID      string            `json:"contract_id"`
	Topics          []string          `json:"topics"`
	RawTopics       []json.RawMessage `json:"raw_topics,omitempty"`
	Data            json.RawMessage   `json:"data,omitempty"`
	EventIndex      int               `json:"event_index"`
	EventType       string            `json:"event_type"`
	IsError         bool              `json:"is_error"`
	IsContractEvent bool              `json:"is_contract_event"`
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

// DualValue represents both raw and decoded versions of a value
type DualValue struct {
	Raw     string          `json:"raw"`     // Raw encoded data
	Decoded json.RawMessage `json:"decoded"` // Decoded human-readable data
}

// ScValDualRepresentation represents both raw and decoded versions of ScVal
type ScValDualRepresentation struct {
	Raw     json.RawMessage `json:"raw"`
	Decoded interface{}     `json:"decoded,omitempty"`
	Type    string          `json:"type"`
}

// ScAddress represents a Stellar Soroban address
type ScAddress struct {
	Type    string          `json:"type"`
	Address string          `json:"address"`
	Raw     json.RawMessage `json:"raw,omitempty"`
}

// ScContractInstance represents a contract instance
type ScContractInstance struct {
	ContractID string          `json:"contract_id"`
	Executable json.RawMessage `json:"executable,omitempty"`
	Storage    json.RawMessage `json:"storage,omitempty"`
	Raw        json.RawMessage `json:"raw,omitempty"`
}

// ScHostFunction represents a host function call
type ScHostFunction struct {
	Type         string            `json:"type"`
	ContractID   string            `json:"contract_id,omitempty"`
	FunctionName string            `json:"function_name,omitempty"`
	Args         []json.RawMessage `json:"args,omitempty"`
	Raw          json.RawMessage   `json:"raw,omitempty"`
}

// XDROperations contains utility functions for working with XDR operations
type XDROperations struct{}

// NewXDROperations creates a new instance of XDROperations
func NewXDROperations() *XDROperations {
	return &XDROperations{}
}
