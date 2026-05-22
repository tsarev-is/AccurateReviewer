package llm

import "context"

// AnthropicProvider is a stub for the MVP. The Master/Worker code paths must
// be able to *select* it from config and pass the same Request shape, but the
// MVP test suite never makes a live network call.
type AnthropicProvider struct {
	APIKey string
	Model  string
}

func (p *AnthropicProvider) Name() string { return "anthropic" }

func (p *AnthropicProvider) Complete(_ context.Context, _ Request) (*Response, error) {
	return nil, ErrNotConfigured
}
