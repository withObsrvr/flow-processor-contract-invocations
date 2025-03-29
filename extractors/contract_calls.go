package extractors

import (
	"fmt"
	"log"
	"strings"

	"github.com/stellar/go/strkey"
	"github.com/stellar/go/support/log"
	"github.com/stellar/go/xdr"
	"github.com/stellar/stellar-etl/internal/transform"
)

// ContractCall represents a contract-to-contract call
type ContractCall struct {
	FromContract string `json:"fromContract"`
	ToContract   string `json:"toContract"`
	Function     string `json:"function"`
	Successful   bool   `json:"successful"`
}

// ExtractContractCalls extracts contract-to-contract calls from transaction metadata
func ExtractContractCalls(txMeta xdr.TransactionMeta, logger *log.Entry) []ContractCall {
	logger.Debug("Extracting contract calls")
	var contractCalls []ContractCall

	// Only proceed if we have V3 metadata with Soroban data
	if txMeta.V != 3 || txMeta.V3.SorobanMeta == nil {
		logger.Debug("No Soroban metadata found for contract calls extraction")
		return contractCalls
	}

	// Process events to find contract calls
	for _, event := range txMeta.V3.SorobanMeta.Events {
		// Check if the event represents a contract call
		if event.ContractEvent == nil || event.ContractEvent.Type != xdr.ContractEventTypeContract {
			continue
		}

		// Extract topics
		topics := event.ContractEvent.Body.V0.Topics
		if len(topics) < 3 {
			// We need at least: function name, from contract, to contract
			continue
		}

		// First topic should be the function name
		if topics[0].Type != xdr.ScValTypeScvSymbol {
			continue
		}
		functionName := string(topics[0].Sym)

		// Second topic should be the "from" contract address
		fromContract, err := extractContractAddress(topics[1])
		if err != nil {
			logger.WithError(err).Debug("Failed to extract 'from' contract address")
			continue
		}

		// Third topic should be the "to" contract address
		toContract, err := extractContractAddress(topics[2])
		if err != nil {
			logger.WithError(err).Debug("Failed to extract 'to' contract address")
			continue
		}

		// Only record if "from" and "to" are different to avoid self-calls
		if fromContract == toContract {
			logger.WithField("contract", fromContract).Debug("Skipping self-call")
			continue
		}

		// Create contract call record
		contractCall := ContractCall{
			FromContract: fromContract,
			ToContract:   toContract,
			Function:     functionName,
			Successful:   true, // Default to true, would need event data to determine failure
		}

		// If we have event data, try to extract success status
		if event.ContractEvent.Body.V0.Data.Type == xdr.ScValTypeScvMap {
			extractSuccessStatus(&contractCall, event.ContractEvent.Body.V0.Data)
		}

		// Process diagnostic events if available
		for _, diagEvent := range txMeta.V3.SorobanMeta.DiagnosticEvents {
			if diagEvent.Event.ContractEvent != nil &&
				diagEvent.Event.ContractEvent.Type == xdr.ContractEventTypeContract {
				// Could analyze diagnostic events for error indicators
			}
		}

		contractCalls = append(contractCalls, contractCall)
		logger.WithFields(log.F{
			"from":       contractCall.FromContract,
			"to":         contractCall.ToContract,
			"function":   contractCall.Function,
			"successful": contractCall.Successful,
		}).Debug("Found contract call")
	}

	return contractCalls
}

// extractContractAddress extracts a contract address from a ScVal
func extractContractAddress(val xdr.ScVal) (string, error) {
	if val.Type == xdr.ScValTypeScvAddress {
		return transform.SorobanAddressToString(val.Address)
	}
	return "", ErrInvalidContractAddress
}

// extractSuccessStatus attempts to extract success status from event data
func extractSuccessStatus(call *ContractCall, data xdr.ScVal) {
	// This would depend on the specific protocol used by contracts
	// For demonstration, assume a map with a "success" key
	if data.Type == xdr.ScValTypeScvMap {
		for _, entry := range data.Map {
			if entry.Key.Type == xdr.ScValTypeScvSymbol &&
				string(entry.Key.Sym) == "success" {
				// Check if the value is a boolean
				if entry.Val.Type == xdr.ScValTypeScvBool {
					call.Successful = entry.Val.B
					break
				}
			}
		}
	}
}

// ErrInvalidContractAddress is returned when a contract address is invalid
var ErrInvalidContractAddress = error(nil)

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

// ProcessInvocation processes a contract invocation and extracts contract calls
// This method is currently unused but will be used in future implementations
// when the XDR structure includes the necessary fields
func ProcessInvocation(
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
		ProcessInvocation(&subInvocation, contractID, calls)
	}
}
