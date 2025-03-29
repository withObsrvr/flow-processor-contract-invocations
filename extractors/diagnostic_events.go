package extractors

import (
	"encoding/json"
	"log"

	"github.com/stellar/go/ingest"
	"github.com/stellar/go/strkey"
)

// DiagnosticEvent represents a diagnostic event emitted during contract execution
type DiagnosticEvent struct {
	ContractID string          `json:"contract_id"`
	Topics     []string        `json:"topics"`
	Data       json.RawMessage `json:"data"`
}

// ExtractDiagnosticEvents extracts diagnostic events from transaction meta
func ExtractDiagnosticEvents(tx ingest.LedgerTransaction, opIndex int) []DiagnosticEvent {
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
