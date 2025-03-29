package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"time"

	"github.com/stellar/go/support/log"
	"github.com/stellar/go/xdr"
)

// XDRToJSON converts XDR to JSON with proper formatting
func XDRToJSON(v interface{}) (json.RawMessage, error) {
	json, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("error marshaling to JSON: %w", err)
	}
	return json, nil
}

// FormatTime formats a time in RFC3339 format
func FormatTime(t time.Time) string {
	return t.Format(time.RFC3339)
}

// FormatUint64AsTime formats a uint64 Unix timestamp as RFC3339
func FormatUint64AsTime(unixTime uint64) string {
	t := time.Unix(int64(unixTime), 0)
	return FormatTime(t)
}

// GetSCValType returns a human-readable type name for an XDR ScVal
func GetSCValType(scVal xdr.ScVal) string {
	switch scVal.Type {
	case xdr.ScValTypeScvBool:
		return "Boolean"
	case xdr.ScValTypeScvVoid:
		return "Void"
	case xdr.ScValTypeScvError:
		return "Error"
	case xdr.ScValTypeScvU32:
		return "Uint32"
	case xdr.ScValTypeScvI32:
		return "Int32"
	case xdr.ScValTypeScvU64:
		return "Uint64"
	case xdr.ScValTypeScvI64:
		return "Int64"
	case xdr.ScValTypeScvTimepoint:
		return "Timepoint"
	case xdr.ScValTypeScvDuration:
		return "Duration"
	case xdr.ScValTypeScvU128:
		return "Uint128"
	case xdr.ScValTypeScvI128:
		return "Int128"
	case xdr.ScValTypeScvU256:
		return "Uint256"
	case xdr.ScValTypeScvI256:
		return "Int256"
	case xdr.ScValTypeScvBytes:
		return "Bytes"
	case xdr.ScValTypeScvString:
		return "String"
	case xdr.ScValTypeScvSymbol:
		return "Symbol"
	case xdr.ScValTypeScvVec:
		return "Vector"
	case xdr.ScValTypeScvMap:
		return "Map"
	case xdr.ScValTypeScvAddress:
		return "Address"
	case xdr.ScValTypeScvLedgerKeyContractInstance:
		return "LedgerKeyContractInstance"
	case xdr.ScValTypeScvLedgerKeyNonce:
		return "LedgerKeyNonce"
	case xdr.ScValTypeScvContractInstance:
		return "ContractInstance"
	default:
		return fmt.Sprintf("Unknown(%d)", scVal.Type)
	}
}

// ScValToString converts an ScVal to a string representation (best effort)
func ScValToString(scVal xdr.ScVal) string {
	switch scVal.Type {
	case xdr.ScValTypeScvBool:
		return fmt.Sprintf("%v", scVal.B)
	case xdr.ScValTypeScvVoid:
		return "void"
	case xdr.ScValTypeScvU32:
		return fmt.Sprintf("%d", scVal.U32)
	case xdr.ScValTypeScvI32:
		return fmt.Sprintf("%d", scVal.I32)
	case xdr.ScValTypeScvU64:
		if scVal.U64 != nil {
			return fmt.Sprintf("%d", *scVal.U64)
		}
		return "0"
	case xdr.ScValTypeScvI64:
		if scVal.I64 != nil {
			return fmt.Sprintf("%d", *scVal.I64)
		}
		return "0"
	case xdr.ScValTypeScvBytes:
		if scVal.Bytes != nil {
			return base64.StdEncoding.EncodeToString([]byte(*scVal.Bytes))
		}
		return ""
	case xdr.ScValTypeScvString:
		if scVal.Str != nil {
			return string(*scVal.Str)
		}
		return ""
	case xdr.ScValTypeScvSymbol:
		if scVal.Sym != nil {
			return string(*scVal.Sym)
		}
		return ""
	default:
		return fmt.Sprintf("[%s]", GetSCValType(scVal))
	}
}

// ParseScValToJSON attempts to parse an ScVal into a JSON-compatible value
func ParseScValToJSON(scVal xdr.ScVal, logger *log.Entry) (interface{}, error) {
	switch scVal.Type {
	case xdr.ScValTypeScvBool:
		return scVal.B, nil
	case xdr.ScValTypeScvVoid:
		return nil, nil
	case xdr.ScValTypeScvU32:
		return scVal.U32, nil
	case xdr.ScValTypeScvI32:
		return scVal.I32, nil
	case xdr.ScValTypeScvU64:
		if scVal.U64 != nil {
			return *scVal.U64, nil
		}
		return uint64(0), nil
	case xdr.ScValTypeScvI64:
		if scVal.I64 != nil {
			return *scVal.I64, nil
		}
		return int64(0), nil
	case xdr.ScValTypeScvTimepoint:
		if scVal.Timepoint != nil {
			// Convert TimePoint to uint64 if needed
			return FormatUint64AsTime(uint64(*scVal.Timepoint)), nil
		}
		return FormatUint64AsTime(0), nil
	case xdr.ScValTypeScvDuration:
		if scVal.Duration != nil {
			return *scVal.Duration, nil
		}
		return uint64(0), nil
	case xdr.ScValTypeScvBytes:
		if scVal.Bytes != nil {
			return base64.StdEncoding.EncodeToString([]byte(*scVal.Bytes)), nil
		}
		return "", nil
	case xdr.ScValTypeScvString:
		if scVal.Str != nil {
			return string(*scVal.Str), nil
		}
		return "", nil
	case xdr.ScValTypeScvSymbol:
		if scVal.Sym != nil {
			return string(*scVal.Sym), nil
		}
		return "", nil
	case xdr.ScValTypeScvVec:
		if scVal.Vec == nil {
			return []interface{}{}, nil
		}

		result := make([]interface{}, 0, len(*scVal.Vec))
		for i := 0; i < len(*scVal.Vec); i++ {
			item := (*scVal.Vec)[i]
			parsed, err := ParseScValToJSON(item, logger)
			if err != nil {
				logger.WithError(err).Debug("Error parsing vector item")
				continue
			}
			result = append(result, parsed)
		}
		return result, nil
	case xdr.ScValTypeScvMap:
		if scVal.Map == nil {
			return map[string]interface{}{}, nil
		}

		result := make(map[string]interface{})
		for i := 0; i < len(*scVal.Map); i++ {
			entry := (*scVal.Map)[i]
			// Try to use the key as a string if possible
			keyStr := ScValToString(entry.Key)

			parsed, err := ParseScValToJSON(entry.Val, logger)
			if err != nil {
				logger.WithError(err).Debug("Error parsing map value")
				continue
			}
			result[keyStr] = parsed
		}
		return result, nil
	case xdr.ScValTypeScvAddress:
		if scVal.Address != nil {
			address, err := scVal.Address.String()
			if err != nil {
				return nil, err
			}
			return address, nil
		}
		return "", nil
	default:
		// For complex types, just return the type name
		return fmt.Sprintf("[%s]", GetSCValType(scVal)), nil
	}
}

// IsEmptyValue checks if a value is empty
func IsEmptyValue(v reflect.Value) bool {
	switch v.Kind() {
	case reflect.Array, reflect.Map, reflect.Slice, reflect.String:
		return v.Len() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Interface, reflect.Ptr:
		return v.IsNil()
	}
	return false
}

// Truncate truncates a string to a specified length and adds ellipsis if truncated
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "..."
}

// SanitizeKey removes invalid characters from a key for use in JSON
func SanitizeKey(key string) string {
	// Replace any characters that might be problematic in JSON keys
	replacer := strings.NewReplacer(
		".", "_",
		"$", "_",
		"#", "_",
		"[", "_",
		"]", "_",
		"/", "_",
		"\\", "_",
		":", "_",
	)
	return replacer.Replace(key)
}
