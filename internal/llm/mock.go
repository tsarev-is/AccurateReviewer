package llm

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"time"
)

// MockProvider talks to an HTTP mock server pointed at by ACCURATE_REVIEWER_MOCK_URL.
// The BDD harness owns the mock server's lifecycle and scripts its responses
// for each scenario via setup endpoints. Keeping the contract over HTTP — not
// in-process — lets the same test infrastructure exercise the binary as a
// black box (same way burn-your-code's MI1 calls the Go binary via subprocess).
type MockProvider struct {
	URL    string
	Client *http.Client
}

func NewMockProvider() *MockProvider {
	url := os.Getenv("ACCURATE_REVIEWER_MOCK_URL")
	if url == "" {
		url = "http://127.0.0.1:8765"
	}
	return &MockProvider{
		URL:    url,
		Client: &http.Client{Timeout: 30 * time.Second},
	}
}

func (m *MockProvider) Name() string { return "mock" }

type mockReq struct {
	Role   string `json:"role"`
	Worker string `json:"worker"`
	Model  string `json:"model"`
	Prompt string `json:"prompt"`
}

type mockResp struct {
	Text       string `json:"text"`
	UsedTokens int    `json:"used_tokens"`
	Error      string `json:"error"`
	DelayMs    int    `json:"delay_ms"`
}

func (m *MockProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	body, _ := json.Marshal(mockReq{
		Role: string(req.Role), Worker: req.Worker, Model: req.Model, Prompt: req.Prompt,
	})
	httpReq, err := http.NewRequestWithContext(ctx, "POST", m.URL+"/complete", bytesReader(body))
	if err != nil {
		return nil, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	resp, err := m.Client.Do(httpReq)
	if err != nil {
		return nil, fmt.Errorf("mock provider call: %w", err)
	}
	defer resp.Body.Close()
	var out mockResp
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return nil, err
	}
	if out.DelayMs > 0 {
		select {
		case <-time.After(time.Duration(out.DelayMs) * time.Millisecond):
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	if out.Error != "" {
		return nil, errors.New(out.Error)
	}
	return &Response{Text: out.Text, UsedTokens: out.UsedTokens}, nil
}
