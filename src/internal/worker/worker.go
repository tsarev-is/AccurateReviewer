// Package worker holds the per-check workers (security, logic, architecture).
// Each worker is a thin shell: build prompt, call provider, parse structured
// JSON findings. The prompts are bundled here so the master never knows the
// details of a specific check.
package worker

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

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

const findingSchemaPrompt = `Respond with ONLY a JSON array of findings — no
prose, no markdown fences. Each element MUST be an object with exactly these
keys:
  - "file":     string  (path of the file the issue is in)
  - "line":     integer (1-based line number)
  - "severity": one of "critical" | "high" | "medium" | "low" | "info"
  - "title":    short imperative summary (≤ 80 chars)
  - "why":      one or two sentences explaining the impact
  - "cwe":      CWE id like "CWE-89", or omit if not applicable
If you find nothing, return an empty array []. Do NOT include any other keys
(no "description", no "recommendation", no "fix" — fold those into "why").`

const securityPrompt = `You are a security-focused code reviewer. Inspect the
code between the delimiters and report concrete vulnerabilities. Classify
each finding with a CWE id when applicable. Ignore style and formatting —
only report issues that require semantic understanding (SQL injection, XSS,
insecure deserialisation, weak cryptography, race conditions, secret leaks,
authn/authz mistakes).

` + findingSchemaPrompt

const logicPrompt = `You are a logic and correctness reviewer. Inspect the
code between the delimiters and report concrete bugs: bad error handling,
unhandled edge cases, off-by-one mistakes, race conditions, resource leaks.
Do NOT report style or formatting.

` + findingSchemaPrompt

// Run executes one worker against one review unit, returning structured findings.
func (w Worker) Run(ctx context.Context, provider llm.Provider, model string, unit diff.Unit, projectContext string) Result {
	body := renderUnit(unit)
	opts := sanitizer.Default()
	wrappedCode := sanitizer.Sanitize(body, opts)
	// projectContext originates from filesystem data the analyzer ingested
	// (package names, READMEs, manifest fields). Treat it as untrusted and
	// wrap it through the sanitizer so an attacker cannot prompt-inject
	// the worker by planting instructions in any of those sources.
	wrappedProject := sanitizer.SanitizeProject(projectContext, opts)
	prompt := fmt.Sprintf("%s\n\nProject context:\n%s\nCode under review:\n%s\n", w.Prompt, wrappedProject, wrappedCode)

	resp, err := provider.Complete(ctx, llm.Request{
		Role: llm.RoleWorker, Worker: w.Name, Model: model, Prompt: prompt, MaxTokens: 2048,
	})
	if err != nil {
		return Result{Worker: w.Name, Err: err}
	}
	findings, perr := parseFindings(resp.Text)
	if perr != nil {
		return Result{Worker: w.Name, UsedTokens: resp.UsedTokens, Err: fmt.Errorf("worker %s: non-JSON response: %w", w.Name, perr)}
	}
	for i := range findings {
		findings[i].Worker = w.Name
		if findings[i].File == "" {
			findings[i].File = unit.File
		}
		findings[i].Severity = normaliseSeverity(findings[i].Severity)
	}
	return Result{Worker: w.Name, Findings: findings, UsedTokens: resp.UsedTokens}
}

// validSeverity is the closed enum the rest of the pipeline understands. An
// LLM-supplied severity outside this set would otherwise hash to rank 0 in
// dedupe() and in the blocking-threshold check, silently letting a genuine
// finding bypass the gate. Anything unrecognised becomes "low" — never
// "info" (which is below the default `report_minimum: low` and would hide
// the finding entirely) and never the maximum ("critical") since we cannot
// trust the model to escalate honestly. CWE-20.
var validSeverity = map[string]bool{
	"critical": true, "high": true, "medium": true, "low": true, "info": true,
}

func normaliseSeverity(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if validSeverity[s] {
		return s
	}
	return "low"
}

// parseFindings extracts a JSON array (or single object) of findings from a
// model response that may be wrapped in markdown code fences, prefixed with
// prose, or trailed with explanations. Real CLIs (claude especially) ignore
// "respond with JSON ONLY" instructions often enough that the worker has to
// be lenient.
//
// Strategy: build a small set of candidate substrings, try parsing each as
// both an array and a single object, return the first success. Candidates
// are derived in this order so the "cleanest" interpretation wins ties:
//
//  1. the trimmed raw response;
//  2. the same response with one layer of ```…``` fence removed;
//  3. for each of the above, every balanced [...] and every balanced {...}
//     found by scanning with string-literal awareness — not just the first
//     bracket, because prose like "[WARNING]" or "Found [3] issues" would
//     otherwise hijack extraction.
//
// An empty response means "no findings" — no error.
func parseFindings(raw string) ([]Finding, error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return nil, nil
	}
	bases := []string{s}
	if stripped := stripCodeFence(s); stripped != s && stripped != "" {
		bases = append(bases, stripped)
	}

	candidates := make([]string, 0, len(bases)*4)
	seen := map[string]bool{}
	add := func(c string) {
		c = strings.TrimSpace(c)
		if c == "" || seen[c] {
			return
		}
		seen[c] = true
		candidates = append(candidates, c)
	}
	for _, b := range bases {
		add(b)
		for _, arr := range balancedRuns(b, '[', ']') {
			add(arr)
		}
		for _, obj := range balancedRuns(b, '{', '}') {
			add(obj)
		}
	}

	var lastErr error
	for _, c := range candidates {
		// Try array first — that's what the prompt asks for.
		var findings []Finding
		if err := json.Unmarshal([]byte(c), &findings); err == nil {
			return findings, nil
		} else {
			lastErr = err
		}
		// Fall back to a single object the model may have returned for a
		// one-finding response.
		var single Finding
		if err := json.Unmarshal([]byte(c), &single); err == nil {
			return []Finding{single}, nil
		} else {
			lastErr = err
		}
	}
	if lastErr == nil {
		lastErr = fmt.Errorf("no JSON array or object found in response")
	}
	return nil, lastErr
}

// stripCodeFence removes one layer of ```…``` from a string. Models love
// wrapping JSON in these, sometimes tagged (```json). The closing fence is
// required to sit on its own line (after a newline) so triple-backticks
// that appear inside a JSON string value cannot prematurely terminate the
// extraction.
func stripCodeFence(s string) string {
	if !strings.HasPrefix(s, "```") {
		return s
	}
	nl := strings.IndexByte(s, '\n')
	if nl < 0 {
		return s
	}
	body := s[nl+1:]
	// Closing fence is "\n```\n" or "\n```" at the very end. Use Index
	// (not LastIndex) so the first proper closing fence wins — any later
	// triple-backticks belong to a different region.
	if end := strings.Index(body, "\n```"); end >= 0 {
		body = body[:end]
	} else if strings.HasSuffix(body, "```") && !strings.Contains(body[:len(body)-3], "```") {
		// No newline before the closing fence but no interior fences
		// either — safe to chop the suffix.
		body = body[:len(body)-3]
	}
	return strings.TrimSpace(body)
}

// balancedRuns returns every balanced span between matching `open` / `close`
// characters in `s`, skipping bytes inside double-quoted JSON string
// literals so brackets in titles or "why" text don't confuse the scanner.
// Spans are returned in start-order; nested spans are skipped (the outer
// one is what callers actually want).
func balancedRuns(s string, open, close byte) []string {
	var out []string
	i := 0
	for i < len(s) {
		c := s[i]
		if c != open {
			i++
			continue
		}
		end := matchBalanced(s, i, open, close)
		if end < 0 {
			return out
		}
		out = append(out, s[i:end+1])
		i = end + 1 // skip past the matched span — do not descend into it
	}
	return out
}

// matchBalanced scans forward from `start` (which must be at the `open`
// byte) and returns the index of the matching `close` byte, or -1. JSON
// string literals are skipped as opaque, and backslash escapes inside them
// are honoured so an embedded \" doesn't end the string early.
func matchBalanced(s string, start int, open, close byte) int {
	depth := 0
	inStr := false
	escape := false
	for i := start; i < len(s); i++ {
		c := s[i]
		if escape {
			escape = false
			continue
		}
		if inStr {
			switch c {
			case '\\':
				escape = true
			case '"':
				inStr = false
			}
			continue
		}
		switch c {
		case '"':
			inStr = true
		case open:
			depth++
		case close:
			depth--
			if depth == 0 {
				return i
			}
		}
	}
	return -1
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
