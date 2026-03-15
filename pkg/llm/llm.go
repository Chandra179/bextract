package llm

import "context"

// Client is the vendor-agnostic LLM interface used by Phase C.
type Client interface {
	// Complete sends a prompt and returns the model's text response.
	Complete(ctx context.Context, prompt string) (string, error)
}
