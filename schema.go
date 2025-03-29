package main

// GetSchemaDefinition implements the SchemaProvider interface
func (p *ContractInvocationProcessor) GetSchemaDefinition() string {
	return `
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
`
}

// GetQueryDefinitions implements the SchemaProvider interface
func (p *ContractInvocationProcessor) GetQueryDefinitions() string {
	return `
	getContractInvocationByHash(hash: String!): ContractInvocation
	getContractInvocationsByContract(contractId: String!, limit: Int, offset: Int): [ContractInvocation]
	getContractInvocationsByFunction(contractId: String!, functionName: String!, limit: Int, offset: Int): [ContractInvocation]
	getContractInvocationsByAccount(account: String!, limit: Int, offset: Int): [ContractInvocation]
`
}
