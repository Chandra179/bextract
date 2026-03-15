package llm

// Compile-time check: anthropicClient must satisfy the Client interface.
var _ Client = (*anthropicClient)(nil)
