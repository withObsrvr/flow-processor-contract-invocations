package extractors

import (
	"encoding/json"
	"log"
	"strings"

	"github.com/stellar/go/ingest"
	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
)

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

// ExtractTemporaryData extracts temporary data from transaction meta
func ExtractTemporaryData(
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
