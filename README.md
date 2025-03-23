# Flow Plugin: Contract Invocations Processor

This Flow processor plugin extracts and processes contract invocations from the Stellar/Soroban blockchain. It identifies contract invocation operations, extracts metadata, and forwards the processed invocations to registered consumers.

## Features

- Extracts contract invocations from Soroban blockchain data
- Processes contract function calls with arguments
- Tracks diagnostic events, contract-to-contract calls, state changes, and TTL extensions
- Implements rich error handling with detailed context
- Provides operational metrics and health status
- Implements GraphQL schema for data API integration
- Uses thread-safe operations for concurrent processing

## Configuration

```json
{
  "network_passphrase": "Public Global Stellar Network ; September 2015",
  "flow_api_version": "1.0.0"
}
```

| Parameter | Required | Type | Default | Description |
|-----------|----------|------|---------|-------------|
| network_passphrase | Yes | string | - | The network passphrase for the Stellar network to process |
| flow_api_version | No | string | - | The Flow API version for compatibility checks |

## Input & Output Schema

### Input

The plugin expects input messages with:
- Payload type: `xdr.LedgerCloseMeta` from the Stellar XDR types
- Expected format: Ledger close metadata containing transactions with contract invocations

### Output

The plugin produces messages with:
- Payload type: JSON-serialized `ContractInvocation` struct
- Metadata: Contains ledger sequence, contract ID, function name, transaction hash
- Format: Structured contract invocation data with function arguments and execution context

Example:
```json
{
  "timestamp": "2023-05-16T09:28:15Z",
  "ledger_sequence": 123456,
  "transaction_hash": "abcdef1234567890",
  "transaction_id": 42,
  "contract_id": "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4",
  "invoking_account": "GDNHSWTGFOZQDEHQFQH23KIZJXSUDNF76QR674WXCXIE4HW5RUD5FYN5",
  "function_name": "increment",
  "successful": true,
  "arguments": [
    {"type":"uint32","value":"5"}
  ],
  "tags": {
    "contract_id": "CAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAAABSC4",
    "function_name": "increment",
    "successful": "true",
    "invoking_account": "GDNHSWTGFOZQDEHQFQH23KIZJXSUDNF76QR674WXCXIE4HW5RUD5FYN5"
  }
}
```

## GraphQL Schema

The plugin contributes the following GraphQL schema:

### Types

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

type DiagnosticEvent {
    contractId: String!
    topics: [String]!
    data: JSON
}

type ContractCall {
    fromContract: String!
    toContract: String!
    function: String!
    successful: Boolean!
}

type StateChange {
    contractId: String!
    key: String!
    oldValue: JSON
    newValue: JSON
    operation: String!
}

type TtlExtension {
    contractId: String!
    oldTtl: Int!
    newTtl: Int!
}

type TemporaryData {
    type: String!
    key: String!
    value: JSON
    expiresAt: String
    ledgerEntry: JSON
    contractId: String
    relatedArgs: [Int]
    argValues: [JSON]
}

scalar JSON
```

### Queries

```graphql
getContractInvocationByHash(hash: String!): ContractInvocation
getContractInvocationsByContract(contractId: String!, limit: Int, offset: Int): [ContractInvocation]
getContractInvocationsByFunction(contractId: String!, functionName: String!, limit: Int, offset: Int): [ContractInvocation]
getContractInvocationsByAccount(account: String!, limit: Int, offset: Int): [ContractInvocation]
```

## Metrics & Monitoring

The plugin exposes these operational metrics:

| Metric | Type | Description |
|--------|------|-------------|
| processed_ledgers | Counter | Total number of ledgers processed |
| invocations_found | Counter | Total number of contract invocations found |
| successful_invokes | Counter | Total number of successful invocations |
| failed_invokes | Counter | Total number of failed invocations |
| last_ledger | Gauge | Sequence number of the last processed ledger |
| last_processed_time | Timestamp | When the last ledger was processed |
| uptime | Duration | How long the processor has been running |

## Development

### Prerequisites

- Go 1.23.4 or compatible
- Nix (optional, for reproducible builds)

### Building

```bash
# With Go
go build -buildmode=plugin -o flow-processor-contract-invocations.so .

# With Nix
nix build
```

The plugin will be built as a shared object file that can be loaded by the Flow runtime.

## License

This project is licensed under the terms specified in the repository.