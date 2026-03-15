package llm

import (
	"context"
	"fmt"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
)

type anthropicClient struct {
	client    anthropic.Client
	model     string
	maxTokens int64
}

// NewAnthropicClient constructs an Anthropic-backed LLM Client.
func NewAnthropicClient(apiKey, model string, maxTokens int) Client {
	c := anthropic.NewClient(option.WithAPIKey(apiKey))
	return &anthropicClient{client: c, model: model, maxTokens: int64(maxTokens)}
}

func (a *anthropicClient) Complete(ctx context.Context, prompt string) (string, error) {
	msg, err := a.client.Messages.New(ctx, anthropic.MessageNewParams{
		Model:     anthropic.Model(a.model),
		MaxTokens: a.maxTokens,
		Messages: []anthropic.MessageParam{
			anthropic.NewUserMessage(anthropic.NewTextBlock(prompt)),
		},
	})
	if err != nil {
		return "", err
	}
	for _, block := range msg.Content {
		if block.Type == "text" {
			return block.Text, nil
		}
	}
	return "", fmt.Errorf("no text block in response")
}
