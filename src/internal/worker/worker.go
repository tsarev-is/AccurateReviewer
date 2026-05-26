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
	// Occurrences carries the secondary locations of a grouped finding —
	// "the same problem class also at these (file, line)s". The primary
	// location stays in File/Line so single-location findings serialise
	// identically to the pre-grouping schema (the field is omitempty).
	// Master fills this in after dedupe + suppressions; workers themselves
	// always return one location per finding.
	Occurrences []Location `json:"occurrences,omitempty"`
	// Fix is an optional, validated, ready-to-apply patch the model offered
	// alongside the finding. The worker drops any fix that targets lines
	// outside the unit's added range or a different file before it reaches
	// the report — only fixes the operator can safely `git apply` survive.
	Fix *Fix `json:"fix,omitempty"`
}

// Location is a (file, line) pair used by Finding.Occurrences. Kept tiny
// on purpose — anything richer should be a full Finding promoted to the
// primary slot, not a richer Location.
type Location struct {
	File string `json:"file"`
	Line int    `json:"line"`
}

// Fix is a model-suggested, mechanically-applicable patch. We deliberately
// model it as a small set of (file, line-range, new-text) replacements —
// not raw unified diff — for two reasons: (1) LLMs reliably emit text
// blocks for given line numbers but reliably mangle "@@ -X,Y +X,Y @@"
// headers; (2) we can validate the replacement against the diff (only
// added lines are eligible) BEFORE the patch ever reaches `git apply`,
// avoiding "applied but it touched legacy code" surprises. The apply-fixes
// subcommand synthesises the unified diff at apply time.
type Fix struct {
	Description  string        `json:"description,omitempty"`
	Replacements []Replacement `json:"replacements"`
}

// Replacement specifies one contiguous span of lines to rewrite. Line
// numbers are 1-based and inclusive. NewText is the FULL replacement
// content for that span; the apply-fixes synthesiser does NOT do
// per-line diffing inside the span — it replaces the whole region.
// NewText may contain any number of lines (zero means "delete the span").
type Replacement struct {
	File      string `json:"file"`
	StartLine int    `json:"start_line"`
	EndLine   int    `json:"end_line"`
	NewText   string `json:"new_text"`
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
	Security     = Worker{Name: "security", Prompt: securityPrompt}
	Logic        = Worker{Name: "logic", Prompt: logicPrompt}
	Architecture = Worker{Name: "architecture", Prompt: architecturePrompt}
)

const findingSchemaPrompt = `Respond with ONLY a JSON array of findings — no
prose, no markdown fences. Each element MUST be an object with these keys:
  - "file":     string  (path of the file the issue is in)
  - "line":     integer (1-based line number)
  - "severity": one of "critical" | "high" | "medium" | "low" | "info"
  - "title":    short imperative summary (≤ 80 chars)
  - "why":      one or two sentences explaining the impact
  - "cwe":      CWE id like "CWE-89", or omit if not applicable
  - "fix":      optional — when, AND ONLY when, you can write a minimal,
                mechanically-applicable replacement that resolves the
                issue without changing unrelated behaviour. Schema:
                  {
                    "description": "<one-line explanation, optional>",
                    "replacements": [
                      {
                        "file":       "<must match the file under review>",
                        "start_line": <inclusive, 1-based line in the file>,
                        "end_line":   <inclusive, 1-based; equal to start_line for single-line replacements>,
                        "new_text":   "<the full new content for the span; multiple lines separated by \n>"
                      }
                    ]
                  }
                Replacements MUST target lines that are part of the
                ADDED side of the diff under review. Do not propose
                edits to context lines or other files; such fixes will
                be dropped before they reach the report. Omit "fix"
                entirely if you cannot produce a confident, minimal
                replacement.
If you find nothing, return an empty array [].`

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

const architecturePrompt = `You are an architectural reviewer. Compare the
code between the delimiters against the project context above and report
violations of the project's established conventions: wrong layer/module
for the responsibility shown, dependencies pointing the wrong way (e.g. a
domain package importing a transport package), patterns or frameworks the
project does not use being introduced ad-hoc, public API surface that
diverges from how the rest of the codebase exposes things. Only report
issues you can justify from the project context — if the snapshot does
not show a relevant convention, do NOT speculate. Ignore style and
formatting.

` + findingSchemaPrompt

// languageHints adds a short, language-specific paragraph to the worker
// prompt before the JSON-schema instructions. Hints are static strings
// baked into the binary — they are NOT attacker-controlled and therefore
// do not need to flow through the sanitizer. Outer key = worker name,
// inner key = analyzer.Snapshot.Language.Primary value (lowercase, the
// extension-derived names from analyzer.extLang).
//
// Languages without an entry simply skip the hint; this keeps the prompt
// stable for languages the analyzer recognises but for which we have no
// specialised guidance yet.
var languageHints = map[string]map[string]string{
	"security": {
		"go": "Go-specific focus: data races on shared state without sync primitives, " +
			"SQL built via string concatenation instead of database/sql parameterised " +
			"Query/Exec, html/template vs text/template confusion enabling XSS, " +
			"unbounded io.ReadAll on untrusted input, exec.Command with user-supplied " +
			"arguments, weak randomness from math/rand for security tokens, missing " +
			"context cancellation enabling resource exhaustion.",
		"python": "Python-specific focus: pickle/marshal/yaml.load on untrusted data " +
			"(insecure deserialisation), eval/exec/compile on user input, subprocess " +
			"with shell=True and interpolated arguments, SQL via string interpolation " +
			"instead of parameterised cursor.execute, requests/urllib calls with " +
			"verify=False, Flask/Django responses rendering unescaped user input, " +
			"weak hashlib choices (md5/sha1) for security purposes.",
		"javascript": "JavaScript/Node-specific focus: prototype pollution via " +
			"Object.assign/merge of untrusted input, eval/Function on user input, " +
			"child_process.exec with interpolated arguments, SQL via template " +
			"literals instead of parameterised queries, missing output escaping " +
			"in templates enabling XSS, insecure JWT verification (alg:none), " +
			"using Math.random for security-sensitive tokens.",
		"typescript": "TypeScript-specific focus: same vectors as JavaScript " +
			"(prototype pollution, eval, child_process, XSS, weak JWT), plus " +
			"unsafe `as any` / `as unknown as T` casts that bypass type guarantees " +
			"on untrusted boundaries.",
		"rust": "Rust-specific focus: unsafe blocks crossing trust boundaries, " +
			"manual lifetime/ownership tricks that may UAF, command::new with " +
			"shell-interpolated arguments, deserialisation via serde on untrusted " +
			"input without size limits, panics on untrusted input enabling DoS.",
		"java": "Java-specific focus: ObjectInputStream / XMLDecoder / SnakeYAML " +
			"deserialisation of untrusted data, JDBC SQL built by concatenation " +
			"instead of PreparedStatement, Runtime.exec with interpolated " +
			"arguments, XXE via unconfigured DocumentBuilderFactory/SAXParser, " +
			"weak MessageDigest choices for security purposes.",
		"csharp": "C#/.NET-specific focus: SQL built via string concatenation or " +
			"interpolation instead of SqlParameter / parameterised DbCommand, " +
			"insecure deserialisation via BinaryFormatter / NetDataContractSerializer / " +
			"SoapFormatter / LosFormatter on untrusted input, Process.Start with " +
			"shell-interpolated arguments, weak randomness from System.Random for " +
			"security tokens (use RandomNumberGenerator), XXE via XmlReader / " +
			"XmlDocument with DtdProcessing=Parse or XmlResolver left non-null, " +
			"Razor @Html.Raw of untrusted input enabling XSS, weak hashes " +
			"(MD5 / SHA1) used for security purposes, [AllowAnonymous] applied to " +
			"endpoints that handle authenticated data.",
	},
	"logic": {
		"go": "Go-specific focus: ignored errors from os/io/db calls, nil-pointer " +
			"panics on map/struct fields after a returned err, goroutine leaks from " +
			"forgotten cancel/close, defer in loops, range-loop variable capture in " +
			"closures spawned as goroutines (still a footgun on older toolchains), " +
			"forgotten io.Closer or sql.Rows.Close, type assertions without the " +
			"comma-ok form.",
		"python": "Python-specific focus: mutable default arguments, late-binding " +
			"closures inside loops, missing await on coroutines, except: that " +
			"swallows KeyboardInterrupt/SystemExit, off-by-one in slice/range, " +
			"forgotten context manager on file/db handles, integer division vs true " +
			"division mismatches.",
		"javascript": "JavaScript/Node-specific focus: missing await on promises, " +
			"unhandled rejections, == vs === confusion on falsy values, this-binding " +
			"loss in callbacks, forEach with async callback ignoring sequencing, " +
			"missing error handlers on streams/emitters.",
		"typescript": "TypeScript-specific focus: same JS pitfalls plus implicit " +
			"`any` masking real bugs, narrowing that is invalidated by a later " +
			"await/yield, non-null assertions (`x!`) on values that can be undefined.",
		"rust": "Rust-specific focus: unwrap/expect on Result/Option that can " +
			"realistically be Err/None, integer overflow in debug-only checked " +
			"paths, .await points inside critical sections holding a Mutex guard, " +
			"forgotten ? on fallible calls.",
		"java": "Java-specific focus: NullPointerException on chained calls, " +
			"resource leaks (missing try-with-resources), == vs .equals on " +
			"boxed types, ConcurrentModificationException in iteration-then-mutate " +
			"patterns, swallowed InterruptedException without re-asserting the flag.",
		"csharp": "C#/.NET-specific focus: async void outside event handlers, " +
			".Result / .Wait() on Tasks producing sync-context deadlocks in " +
			"ASP.NET classic / UI threads, missing await on Task-returning calls " +
			"(fire-and-forget swallowing exceptions), missing using / Dispose on " +
			"IDisposable resources (DbConnection, FileStream, HttpResponseMessage), " +
			"multiple enumeration of IEnumerable that hides a re-executed query, " +
			"== on boxed value types instead of .Equals, missing ConfigureAwait(false) " +
			"in library code, null-conditional (?.) chains that mask a NullReferenceException " +
			"one level deeper.",
	},
}

// promptFor renders the worker's base prompt with an optional
// language-specific hint paragraph inserted before the JSON-schema
// instructions. When `language` is empty or has no hint registered for
// this worker, the prompt is identical to `w.Prompt` — so the absence of
// a snapshot leaves prompts unchanged from the pre-v1.0 shape.
func (w Worker) promptFor(language string) string {
	if language == "" {
		return w.Prompt
	}
	hints, ok := languageHints[w.Name]
	if !ok {
		return w.Prompt
	}
	hint, ok := hints[language]
	if !ok {
		return w.Prompt
	}
	// Splice the hint right before findingSchemaPrompt. Both halves live in
	// w.Prompt as one constant, so we split on a stable marker line. The
	// marker is the literal first line of findingSchemaPrompt — if that
	// changes, the split silently falls through and the hint is appended
	// at the end, which is still better than no hint at all.
	const marker = "Respond with ONLY a JSON array of findings"
	idx := strings.Index(w.Prompt, marker)
	if idx < 0 {
		return w.Prompt + "\n\nLanguage-specific guidance:\n" + hint + "\n"
	}
	return w.Prompt[:idx] + "Language-specific guidance:\n" + hint + "\n\n" + w.Prompt[idx:]
}

// Run executes one worker against one review unit, returning structured findings.
//
// taskContext is optional: when empty the rendered prompt omits the
// "Task context" section entirely (so a reviewer with no linked ticket
// sees the same prompt shape as before this feature existed).
//
// language is also optional: when non-empty and a hint is registered for
// (w.Name, language), a short language-specific guidance paragraph is
// spliced into the base prompt before the JSON-schema section. The
// master decides whether to pass a language at all (it considers the
// project snapshot and the checks.language_specific_prompts toggle), so
// the worker stays oblivious to that policy.
func (w Worker) Run(ctx context.Context, provider llm.Provider, model string, unit diff.Unit, projectContext, taskContext, language string) Result {
	body := renderUnit(unit)
	opts := sanitizer.Default()
	wrappedCode := sanitizer.Sanitize(body, opts)
	// projectContext originates from filesystem data the analyzer ingested
	// (package names, READMEs, manifest fields). Treat it as untrusted and
	// wrap it through the sanitizer so an attacker cannot prompt-inject
	// the worker by planting instructions in any of those sources.
	wrappedProject := sanitizer.SanitizeProject(projectContext, opts)

	var taskSection string
	if strings.TrimSpace(taskContext) != "" {
		taskSection = "Task context:\n" + sanitizer.SanitizeTask(taskContext, opts) + "\n"
	}
	basePrompt := w.promptFor(language)
	prompt := fmt.Sprintf("%s\n\n%sProject context:\n%s\nCode under review:\n%s\n", basePrompt, taskSection, wrappedProject, wrappedCode)

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
		findings[i].Fix = validateFix(findings[i].Fix, unit)
	}
	return Result{Worker: w.Name, Findings: findings, UsedTokens: resp.UsedTokens}
}

// validateFix drops a model-supplied fix that we cannot safely apply.
// "Safely" means:
//
//   - every Replacement targets the SAME file as the unit under review
//     (the model has no licence to rewrite unrelated files from inside
//     one unit's prompt);
//   - every line in every Replacement's [start_line, end_line] range
//     is part of the unit's ADDED set (context lines and unchanged
//     legacy code stay off-limits — same protection model as
//     `noqa-review` only working on added lines);
//   - the range is well-formed (start_line ≤ end_line, both ≥ 1);
//   - the replacement set is non-empty.
//
// Any violation drops the WHOLE fix (returning nil), not the individual
// bad replacement. A half-applied fix is harder to reason about than a
// missing one, and the finding itself stays — operator just doesn't get
// the auto-patch.
func validateFix(f *Fix, unit diff.Unit) *Fix {
	if f == nil || len(f.Replacements) == 0 {
		return nil
	}
	added := map[int]bool{}
	for _, h := range unit.Hunks {
		for _, ln := range h.AddedLineNumbers {
			added[ln] = true
		}
	}
	if len(added) == 0 {
		return nil
	}
	for _, r := range f.Replacements {
		if r.File != unit.File {
			return nil
		}
		if r.StartLine < 1 || r.EndLine < r.StartLine {
			return nil
		}
		for ln := r.StartLine; ln <= r.EndLine; ln++ {
			if !added[ln] {
				return nil
			}
		}
	}
	return f
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
