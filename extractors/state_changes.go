package extractors

import (
	"encoding/json"
	"log"

	"github.com/stellar/go/ingest"
	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
)

// StateChange represents a contract state change
type StateChange struct {
	ContractID string          `json:"contract_id"`
	Key        string          `json:"key"`
	OldValue   json.RawMessage `json:"old_value,omitempty"`
	NewValue   json.RawMessage `json:"new_value,omitempty"`
	Operation  string          `json:"operation"` // "create", "update", "delete"
}

// ExtractStateChanges extracts state changes from transaction meta
func ExtractStateChanges(tx ingest.LedgerTransaction, opIndex int) []StateChange {
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
