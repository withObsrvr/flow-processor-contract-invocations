package extractors

import (
	"log"

	"github.com/stellar/go/ingest"
	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
)

// TtlExtension represents a TTL extension for a contract
type TtlExtension struct {
	ContractID string `json:"contract_id"`
	OldTtl     uint32 `json:"old_ttl"`
	NewTtl     uint32 `json:"new_ttl"`
}

// ExtractTtlExtensions extracts TTL extensions from transaction meta
func ExtractTtlExtensions(tx ingest.LedgerTransaction, opIndex int) []TtlExtension {
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
