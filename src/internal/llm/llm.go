// Package llm is the provider-abstraction layer the master and workers call
// through. In MVP we ship one real provider (mock) and one stub (anthropic).
// The Anthropic stub returns ErrNotConfigured so the test suite can verify
// that real network calls are never made by accident.
package llm

import (
	"context"
	"errors"
)

type Role string

const (
	RoleMaster Role = "master"
	RoleWorker Role = "worker"
)

type Request struct {
	Role        Role
	Worker      string // "security", "logic", "" for master
	Model       string
	Prompt      string
	MaxTokens   int
}

type Response struct {
	Text       string
	UsedTokens int
}

type Provider interface {
	Name() string
	Complete(ctx context.Context, req Request) (*Response, error)
}

var ErrNotConfigured = errors.New("llm: provider not configured for live calls")
