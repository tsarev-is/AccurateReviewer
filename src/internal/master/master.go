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
}

type Master struct {
	Cfg      *config.Config
	Provider llm.Provider
	Snapshot *analyzer.Snapshot
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
	workerNames := make([]string, 0, len(workers))
	for _, w := range workers {
		workerNames = append(workerNames, w.Name)
	}
	m.logf("starting: %d unit(s), workers=[%s]", len(units), strings.Join(workerNames, ","))

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

	for idx, u := range units {
		m.logf("unit %d/%d: %s", idx+1, len(units), u.File)
		var wg sync.WaitGroup
		for _, w := range workers {
			if report.BudgetExceeded {
				break
			}
			// Cache lookup happens before spawning the goroutine so a hit
			// short-circuits both the worker AND the LLM subprocess. The
			// hit counts toward the budget (using the originally-recorded
			// token cost) so a heavily-cached run still respects max_tokens.
			if cacheOn {
				key := cache.Key(u, w, m.ToolVersion, projectCtx)
				if cached, tokens, ok := cache.Load(cacheRoot, key); ok {
					m.logf("  -> %s on %s: cache hit (%d finding(s), %d token(s))", w.Name, u.File, len(cached), tokens)
					mu.Lock()
					calledSet[w.Name] = true
					report.Findings = append(report.Findings, cached...)
					report.UsedTokens += tokens
					if m.Cfg.Budget.MaxTokens > 0 && report.UsedTokens > m.Cfg.Budget.MaxTokens {
						report.BudgetExceeded = true
					}
					mu.Unlock()
					continue
				}
			}
			wg.Add(1)
			go func(w worker.Worker, u diff.Unit) {
				defer wg.Done()
				m.logf("  -> %s on %s", w.Name, u.File)
				start := time.Now()
				res := w.Run(ctx, m.Provider, m.Cfg.LLM.Worker.Model, u, projectContext, m.TaskContext)
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
					// silent zero-finding result.
					if cacheOn {
						key := cache.Key(u, w, m.ToolVersion, projectCtx)
						if err := cache.Save(cacheRoot, key, w, u, res.Findings, res.UsedTokens); err != nil {
							m.logf("  ?? cache save for %s on %s: %v", w.Name, u.File, err)
						}
					}
				}
				report.Findings = append(report.Findings, res.Findings...)
				report.UsedTokens += res.UsedTokens
				if m.Cfg.Budget.MaxTokens > 0 && report.UsedTokens > m.Cfg.Budget.MaxTokens {
					report.BudgetExceeded = true
				}
			}(w, u)
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
	return report, nil
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
	return out
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
