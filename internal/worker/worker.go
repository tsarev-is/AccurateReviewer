// Package worker holds the per-check workers (security, logic, architecture).
// Each worker is a thin shell: build prompt, call provider, parse structured
// JSON findings. The prompts are bundled here so the master never knows the
// details of a specific check.
package worker

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/scaratec/accurate-reviewer/internal/diff"
	"github.com/scaratec/accurate-reviewer/internal/llm"
	"github.com/scaratec/accurate-reviewer/internal/sanitizer"
)

type Finding struct {
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Title    string `json:"title"`
	Why      string `json:"why"`
	CWE      string `json:"cwe,omitempty"`
	Worker   string `json:"worker"`
}

type Result struct {
	Worker     string
	Findings   []Finding
	UsedTokens int
	Err        error
}

type Worker struct {
	Name   string
	Prompt string
}

var (
	Security = Worker{Name: "security", Prompt: securityPrompt}
	Logic    = Worker{Name: "logic", Prompt: logicPrompt}
)

const securityPrompt = `You are a security-focused code reviewer. Inspect the code
between the delimiters and report concrete vulnerabilities. Classify each
finding with a CWE id when applicable. Ignore style and formatting — only
report issues that require semantic understanding (SQL injection, XSS,
insecure deserialisation, weak cryptography, race conditions, secret leaks,
authn/authz mistakes). Respond with a JSON array of findings ONLY.`

const logicPrompt = `You are a logic and correctness reviewer. Inspect the code
between the delimiters and report concrete bugs: bad error handling,
unhandled edge cases, off-by-one mistakes, race conditions, resource leaks.
Do NOT report style or formatting. Respond with a JSON array of findings ONLY.`

// Run executes one worker against one review unit, returning structured findings.
func (w Worker) Run(ctx context.Context, provider llm.Provider, model string, unit diff.Unit, projectContext string) Result {
	body := renderUnit(unit)
	wrapped := sanitizer.Sanitize(body, sanitizer.Default())
	prompt := fmt.Sprintf("%s\n\nProject context:\n%s\n\nCode under review:\n%s\n", w.Prompt, projectContext, wrapped)

	resp, err := provider.Complete(ctx, llm.Request{
		Role: llm.RoleWorker, Worker: w.Name, Model: model, Prompt: prompt, MaxTokens: 2048,
	})
	if err != nil {
		return Result{Worker: w.Name, Err: err}
	}
	var findings []Finding
	if err := json.Unmarshal([]byte(resp.Text), &findings); err != nil {
		return Result{Worker: w.Name, UsedTokens: resp.UsedTokens, Err: fmt.Errorf("worker %s: non-JSON response: %w", w.Name, err)}
	}
	for i := range findings {
		findings[i].Worker = w.Name
		if findings[i].File == "" {
			findings[i].File = unit.File
		}
	}
	return Result{Worker: w.Name, Findings: findings, UsedTokens: resp.UsedTokens}
}

func renderUnit(unit diff.Unit) string {
	var s string
	s += fmt.Sprintf("File: %s\n", unit.File)
	for _, h := range unit.Hunks {
		s += fmt.Sprintf("\n@@ -%d,%d +%d,%d @@\n", h.OldStart, h.OldLines, h.NewStart, h.NewLines)
		for _, ctx := range h.ContextBefore {
			s += "  " + ctx + "\n"
		}
		for i, add := range h.Added {
			ln := h.NewStart
			if i < len(h.AddedLineNumbers) {
				ln = h.AddedLineNumbers[i]
			}
			s += fmt.Sprintf("+%d: %s\n", ln, add)
		}
		for _, ctx := range h.ContextAfter {
			s += "  " + ctx + "\n"
		}
	}
	return s
}
