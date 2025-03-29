# Flow Processor: Soroban Contract Invocations

This processor extracts and processes Soroban smart contract invocations from the Stellar blockchain, providing detailed information about contract interactions.

## Features

- **Contract Invocation Detection**: Identifies and extracts contract invocation operations
- **Contract-to-Contract Call Tracking**: Traces calls between different contracts
- **State Change Monitoring**: Records contract state changes with before and after values
- **TTL Extension Tracking**: Captures TTL extensions for contract data
- **Argument Extraction**: Parses and formats function arguments
- **Temporary Data Tracking**: Records temporary data created during contract execution
- **Diagnostic Event Logging**: Captures contract diagnostic events

## Installation

Build the processor as a plugin:

```bash
make build
```

This will create a `contract-invocations.so` file that can be loaded by the Flow engine.

## Configuration

In your Flow configuration, add the following:

```yaml
processors:
  - name: contract-invocations
    path: /path/to/contract-invocations.so
    config:
      network_passphrase: "Public Global Stellar Network ; September 2015"
      # Optional configuration parameters
      flow_api_version: "1.0.0"
```

## Output

The processor outputs data with the following schema:

```graphql
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
```

## Queries

The processor supports the following GraphQL queries:

```graphql
getContractInvocationByHash(hash: String!): ContractInvocation
getContractInvocationsByContract(contractId: String!, limit: Int, offset: Int): [ContractInvocation]
getContractInvocationsByFunction(contractId: String!, functionName: String!, limit: Int, offset: Int): [ContractInvocation]
getContractInvocationsByAccount(account: String!, limit: Int, offset: Int): [ContractInvocation]
```

## Development

### Requirements

- Go 1.19 or later
- Access to a Stellar Horizon instance

### Building

```bash
make build      # Build the plugin
make clean      # Clean build artifacts
make test       # Run tests
make lint       # Run linters
make dev        # Clean and rebuild
```

### Code Structure

- `processor.go`: Main processor implementation
- `schema.go`: GraphQL schema definitions
- `types.go`: Type definitions
- `errors.go`: Error handling
- `utility.go`: Utility functions
- `extractors/`: Directory containing extraction logic
  - `contract_calls.go`: Contract call extraction
  - `state_changes.go`: State change extraction
  - `ttl_extensions.go`: TTL extension extraction
  - `arguments.go`: Argument extraction
  - `temporary_data.go`: Temporary data extraction
  - `diagnostic_events.go`: Diagnostic event extraction

## License

Copyright © 2023 Obsrvr, Inc.

Licensed under the Apache License, Version 2.0