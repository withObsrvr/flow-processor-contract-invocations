package extractors

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"log"

	"github.com/stellar/go/ingest"
	"github.com/stellar/go/strkey"
	"github.com/stellar/go/xdr"
)

// ScValDualRepresentation stores both raw and decoded versions of a Stellar ScVal
type ScValDualRepresentation struct {
	Type      string          `json:"type"`
	RawValue  string          `json:"raw_value"`
	Value     json.RawMessage `json:"value"`
	TypeInt   int32           `json:"type_int,omitempty"`
	ScValType string          `json:"scval_type,omitempty"`
}

// ExtractArguments extracts arguments from the contract invocation
func ExtractArguments(tx ingest.LedgerTransaction, opIndex int) []json.RawMessage {
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
