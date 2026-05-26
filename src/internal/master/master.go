// Package master coordinates worker agents. It decides which workers to run
// per review unit, runs them in parallel, deduplicates findings across
// workers, and enforces the token budget.
package master

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/scaratec/accurate-reviewer/internal/analyzer"
	"github.com/scaratec/accurate-reviewer/internal/cache"
	"github.com/scaratec/accurate-reviewer/internal/config"
	"github.com/scaratec/accurate-reviewer/internal/diff"
	"github.com/scaratec/accurate-reviewer/internal/llm"
	"github.com/scaratec/accurate-reviewer/internal/severity"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

type Report struct {
	Findings       []worker.Finding
	UsedTokens     int
	WorkersCalled  []string
	WorkerErrors   []error
	BudgetExceeded bool
	// FallbackEngaged is true once the master has switched workers from
	// LLM.Worker.Model to LLM.Fallback.Model during this run. It stays true
	// for the remainder of the report — the switch is sticky, the master
	// never returns to the expensive model mid-run.
	FallbackEngaged bool
}

type Master struct {
	Cfg       *config.Config
	Providers *ProviderSet
	Snapshot  *analyzer.Snapshot
	// TaskContext, if non-empty, is rendered into each worker prompt as a
	// "Task context" block (already sanitized in the caller's helper). It
	// gives workers the intent of the change so they can flag mismatches
	// between what the diff does and what the task asked for.
	TaskContext string
	// Progress, if non-nil, receives one-line human-readable status updates
	// as units and workers start/finish. Intended for stderr so the user can
	// watch the review unfold; stdout is reserved for the structured report.
	Progress io.Writer
	// ToolVersion participates in the cache key so an upgrade invalidates
	// every stored finding (different binary, possibly different prompts or
	// parsing). The caller passes cli.Version here; tests pass a stable
	// string so cache hits are reproducible.
	ToolVersion string
	// CacheRoot is the directory that contains .review-cache/. Empty means
	// "current working directory" — same convention the analyzer uses.
	CacheRoot string
}

func (m *Master) logf(format string, args ...any) {
	if m.Progress == nil {
		return
	}
	fmt.Fprintf(m.Progress, "[review] "+format+"\n", args...)
}

// Review runs every enabled worker against every unit. Findings produced by
// multiple workers on the same (file, line, normalised-title) collapse into
// one, keeping the highest severity.
func (m *Master) Review(ctx context.Context, units []diff.Unit) (*Report, error) {
	workers := m.enabledWorkers()
	if len(workers) == 0 {
		m.logf("no workers enabled — skipping")
		return &Report{}, nil
	}

	projectContext := m.projectContext()
	workerLanguage := m.workerLanguage()
	workerNames := make([]string, 0, len(workers))
	for _, w := range workers {
		workerNames = append(workerNames, w.Name)
	}
	m.logf("starting: %d unit(s), workers=[%s]", len(units), strings.Join(workerNames, ","))
	if workerLanguage != "" {
		m.logf("language-specific prompts enabled for: %s", workerLanguage)
	}

	var (
		mu        sync.Mutex
		report    = &Report{}
		calledSet = map[string]bool{}
	)

	cacheRoot := m.CacheRoot
	if cacheRoot == "" {
		cacheRoot = "."
	}
	cacheOn := m.Cfg.Cache.IsEnabled()
	// Render the project context once so every cache key sees the same
	// string. Doing it inside the loop would still be correct but would
	// re-walk Snapshot fields per (unit × worker).
	projectCtx := m.projectContext()

	// fallbackEngaged is sticky once true: the budget threshold check
	// flips it under mu between worker spawns so concurrent goroutines
	// from the same unit see a consistent value at spawn time. Per-worker
	// provider overrides are intentionally abandoned once it flips —
	// every subsequent call resolves through ProviderSet.Fallback for
	// uniform cheap-path behaviour.
	fallbackEngaged := false
	hasFallback := m.Providers != nil && m.Providers.Fallback != nil

	for idx, u := range units {
		m.logf("unit %d/%d: %s", idx+1, len(units), u.File)
		var wg sync.WaitGroup
		for _, w := range workers {
			if report.BudgetExceeded {
				break
			}
			mu.Lock()
			engaged := fallbackEngaged
			mu.Unlock()
			providerForCall, modelForCall := m.Providers.For(w.Name, engaged)
			// Cache key incorporates the provider's name so two providers
			// running the same worker on the same model — say a local
			// fake versus claude in CI — keep their findings in separate
			// slots. Otherwise a fake's "no findings" result would
			// silently replay against a real claude run.
			cacheModelKey := providerForCall.Name() + ":" + modelForCall
			// Cache lookup happens before spawning the goroutine so a hit
			// short-circuits both the worker AND the LLM subprocess. The
			// hit counts toward the budget (using the originally-recorded
			// token cost) so a heavily-cached run still respects max_tokens.
			if cacheOn {
				key := cache.Key(u, w, m.ToolVersion, projectCtx, cacheModelKey)
				if cached, tokens, ok := cache.Load(cacheRoot, key); ok {
					m.logf("  -> %s on %s: cache hit (%d finding(s), %d token(s))", w.Name, u.File, len(cached), tokens)
					mu.Lock()
					calledSet[w.Name] = true
					report.Findings = append(report.Findings, cached...)
					report.UsedTokens += tokens
					m.applyBudgetPolicy(report, &fallbackEngaged, hasFallback)
					mu.Unlock()
					continue
				}
			}
			wg.Add(1)
			go func(w worker.Worker, u diff.Unit, provider llm.Provider, model, cacheKeyModel string) {
				defer wg.Done()
				m.logf("  -> %s on %s (provider=%s model=%s)", w.Name, u.File, provider.Name(), model)
				start := time.Now()
				res := w.Run(ctx, provider, model, u, projectContext, m.TaskContext, workerLanguage)
				elapsed := time.Since(start).Round(time.Millisecond)
				mu.Lock()
				defer mu.Unlock()
				calledSet[res.Worker] = true
				if res.Err != nil {
					m.logf("  !! %s on %s failed in %s: %v", w.Name, u.File, elapsed, res.Err)
					report.WorkerErrors = append(report.WorkerErrors, errors.New("worker "+res.Worker+" failed: "+res.Err.Error()))
				} else {
					m.logf("  <- %s on %s done in %s: %d finding(s), %d token(s)", w.Name, u.File, elapsed, len(res.Findings), res.UsedTokens)
					// Only successful runs go to the cache — a saved
					// error would otherwise be replayed forever as a
					// silent zero-finding result. Key includes the
					// provider+model so fallback-quality results never
					// replay against a budget-healthy run.
					if cacheOn {
						key := cache.Key(u, w, m.ToolVersion, projectCtx, cacheKeyModel)
						if err := cache.Save(cacheRoot, key, w, u, res.Findings, res.UsedTokens); err != nil {
							m.logf("  ?? cache save for %s on %s: %v", w.Name, u.File, err)
						}
					}
				}
				report.Findings = append(report.Findings, res.Findings...)
				report.UsedTokens += res.UsedTokens
				m.applyBudgetPolicy(report, &fallbackEngaged, hasFallback)
			}(w, u, providerForCall, modelForCall, cacheModelKey)
		}
		wg.Wait()
		if report.BudgetExceeded {
			m.logf("budget exceeded after %d token(s) — stopping further units", report.UsedTokens)
			break
		}
	}
	m.logf("done: %d raw finding(s), %d token(s) used", len(report.Findings), report.UsedTokens)
	for k := range calledSet {
		report.WorkersCalled = append(report.WorkersCalled, k)
	}
	sort.Strings(report.WorkersCalled)
	report.Findings = dedupe(report.Findings)
	report.Findings = m.applySuppressions(units, report.Findings)
	report.Findings = groupOccurrences(report.Findings)
	return report, nil
}

// groupOccurrences clusters findings of the same problem class that the
// worker has surfaced at multiple locations (different lines in one file
// or the same problem in another file). It runs AFTER dedupe and
// suppressions so we group only on what survives:
//
//   - Key: (worker, normalised-title, normalised-cwe). Cross-worker
//     grouping is intentionally out of scope — two workers seeing the
//     "same" title are still distinct epistemic claims, and merging
//     them would be hard to justify in the report.
//   - The cluster's primary location is the highest-severity finding
//     (ties: first seen). Every other location is recorded on the
//     primary's Occurrences. The primary's Why/Title/CWE come from the
//     promoted finding.
//
// Findings without a CWE group only with other no-CWE findings of the
// same (worker, title); this is conservative — we'd rather emit two
// groups than incorrectly merge two distinct issues that happen to
// share a noun.
func groupOccurrences(in []worker.Finding) []worker.Finding {
	if len(in) < 2 {
		return in
	}
	type gk struct {
		worker, title, cwe string
	}
	bucket := map[gk]int{} // gk -> index into out
	out := make([]worker.Finding, 0, len(in))
	for _, f := range in {
		k := gk{
			worker: f.Worker,
			title:  normalise(f.Title),
			cwe:    strings.ToLower(strings.TrimSpace(f.CWE)),
		}
		idx, exists := bucket[k]
		if !exists {
			bucket[k] = len(out)
			out = append(out, f)
			continue
		}
		cur := out[idx]
		incoming := worker.Location{File: f.File, Line: f.Line}
		// Same primary location is a no-op — dedupe should have collapsed
		// it earlier, but defend against a future change leaving a true
		// duplicate alive.
		if incoming.File == cur.File && incoming.Line == cur.Line {
			continue
		}
		// Same as occurrence we already recorded? Skip.
		alreadyRecorded := false
		for _, occ := range cur.Occurrences {
			if occ == incoming {
				alreadyRecorded = true
				break
			}
		}
		if alreadyRecorded {
			continue
		}
		if severity.Rank(f.Severity) > severity.Rank(cur.Severity) {
			// Promote f to primary; demote the previous primary to an
			// occurrence so its location stays visible in the report.
			demoted := worker.Location{File: cur.File, Line: cur.Line}
			f.Occurrences = append([]worker.Location{demoted}, cur.Occurrences...)
			out[idx] = f
		} else {
			cur.Occurrences = append(cur.Occurrences, incoming)
			out[idx] = cur
		}
	}
	return out
}

// applySuppressions drops findings that target a line carrying an inline
// `noqa-review:` marker on the added side of the diff. Each silenced
// finding is reported to the progress log together with the developer's
// stated reason, so the trail is visible even when the report itself shows
// "0 findings". A marker with no matching finding is a no-op and does not
// log anything — the BDD scenario asserts this explicitly.
func (m *Master) applySuppressions(units []diff.Unit, findings []worker.Finding) []worker.Finding {
	if len(findings) == 0 {
		return findings
	}
	type key struct {
		file string
		line int
	}
	supp := map[key]string{}
	for _, u := range units {
		for ln, reason := range u.Suppressions() {
			supp[key{file: u.File, line: ln}] = reason
		}
	}
	if len(supp) == 0 {
		return findings
	}
	kept := findings[:0]
	suppressed := 0
	for _, f := range findings {
		if reason, ok := supp[key{file: f.File, line: f.Line}]; ok {
			suppressed++
			m.logf("suppressed 1 finding at %s:%d (noqa-review: %s)", f.File, f.Line, reason)
			continue
		}
		kept = append(kept, f)
	}
	if suppressed > 0 {
		m.logf("suppressed %d finding(s) via noqa-review", suppressed)
	}
	return kept
}

func (m *Master) enabledWorkers() []worker.Worker {
	var out []worker.Worker
	if m.Cfg.Checks.Security {
		out = append(out, worker.Security)
	}
	if m.Cfg.Checks.Logic {
		out = append(out, worker.Logic)
	}
	// Architecture is opt-in and depends on a project snapshot existing.
	// Without the snapshot the worker has no conventions to compare against,
	// so silently skip it rather than emit speculative findings.
	if m.Cfg.Checks.Architecture && m.Snapshot != nil {
		out = append(out, worker.Architecture)
	}
	return out
}

// applyBudgetPolicy is called after every UsedTokens update (cache hit or
// fresh worker result), with mu already held by the caller. It enforces
// two thresholds:
//
//   - At report.UsedTokens > FallbackAt * MaxTokens (and a fallback
//     (provider, model) was configured, and we are not already on it):
//     flip fallbackEngaged. Subsequent worker spawns resolve through
//     ProviderSet.For(_, true) and pick up the fallback path.
//   - At report.UsedTokens > MaxTokens with no fallback configured, OR
//     while already on the fallback: set BudgetExceeded so the dispatch
//     loops break out.
//
// Callers MUST hold mu — this function mutates *fallbackEngaged and
// report fields that the dispatch loop also reads.
func (m *Master) applyBudgetPolicy(report *Report, fallbackEngaged *bool, hasFallback bool) {
	max := m.Cfg.Budget.MaxTokens
	if max <= 0 {
		return
	}
	used := report.UsedTokens
	if hasFallback && !*fallbackEngaged {
		threshold := int(float64(max) * m.Cfg.Budget.FallbackAt)
		if used > threshold {
			m.logf("budget threshold reached (used=%d, limit=%d, fallback_at=%.2f) — switching to fallback provider/model",
				used, max, m.Cfg.Budget.FallbackAt)
			*fallbackEngaged = true
			report.FallbackEngaged = true
			// Allow the run to continue under the cheaper path. Hard
			// stop is reserved for the case where even the fallback
			// blows past MaxTokens — handled below on a subsequent
			// invocation.
			return
		}
	}
	if used > max {
		// Either no fallback configured, or we are already running on
		// the fallback and still exceeded the limit. Either way the
		// only remaining safety is to stop dispatching new workers.
		report.BudgetExceeded = true
	}
}

// workerLanguage decides whether to pass a language hint to workers. It
// returns "" when there is no snapshot (workers have nothing project-
// specific to specialise for) or when the operator has explicitly
// disabled the toggle in .review.yml. Otherwise it returns the primary
// language from the snapshot — which may itself be "unknown" for
// projects the analyzer could not classify, in which case the worker's
// hint map has no entry and the prompt remains unchanged.
func (m *Master) workerLanguage() string {
	if m.Snapshot == nil {
		return ""
	}
	if !m.Cfg.Checks.LanguagePromptsEnabled() {
		return ""
	}
	primary := m.Snapshot.Language.Primary
	if primary == "" || primary == "unknown" {
		return ""
	}
	return primary
}

func (m *Master) projectContext() string {
	if m.Snapshot == nil {
		return "(no project snapshot available)"
	}
	var b strings.Builder
	b.WriteString("primary language: " + m.Snapshot.Language.Primary)
	if len(m.Snapshot.Frameworks) > 0 {
		b.WriteString("\nframeworks: ")
		for i, f := range m.Snapshot.Frameworks {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(f.Name)
		}
	}
	return b.String()
}

// dedupe collapses findings by (file, line, normalised-title), keeping the
// highest-severity representative and recording every worker that flagged it.
func dedupe(in []worker.Finding) []worker.Finding {
	type key struct {
		file, title string
		line        int
	}
	bucket := map[key]worker.Finding{}
	order := []key{}
	for _, f := range in {
		k := key{file: f.File, line: f.Line, title: normalise(f.Title)}
		cur, ok := bucket[k]
		if !ok {
			bucket[k] = f
			order = append(order, k)
			continue
		}
		if severity.Rank(f.Severity) > severity.Rank(cur.Severity) {
			f.Worker = cur.Worker + "+" + f.Worker
			bucket[k] = f
		} else {
			cur.Worker = cur.Worker + "+" + f.Worker
			bucket[k] = cur
		}
	}
	out := make([]worker.Finding, 0, len(order))
	for _, k := range order {
		out = append(out, bucket[k])
	}
	return out
}

func normalise(s string) string {
	return strings.ToLower(strings.TrimSpace(s))
}
