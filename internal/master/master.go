// Package master coordinates worker agents. It decides which workers to run
// per review unit, runs them in parallel, deduplicates findings across
// workers, and enforces the token budget.
package master

import (
	"context"
	"errors"
	"sort"
	"strings"
	"sync"

	"github.com/scaratec/accurate-reviewer/internal/analyzer"
	"github.com/scaratec/accurate-reviewer/internal/config"
	"github.com/scaratec/accurate-reviewer/internal/diff"
	"github.com/scaratec/accurate-reviewer/internal/llm"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

type Report struct {
	Findings        []worker.Finding
	UsedTokens      int
	WorkersCalled   []string
	WorkerErrors    []error
	BudgetExceeded  bool
}

type Master struct {
	Cfg      *config.Config
	Provider llm.Provider
	Snapshot *analyzer.Snapshot
}

// Review runs every enabled worker against every unit. Findings produced by
// multiple workers on the same (file, line, normalised-title) collapse into
// one, keeping the highest severity.
func (m *Master) Review(ctx context.Context, units []diff.Unit) (*Report, error) {
	workers := m.enabledWorkers()
	if len(workers) == 0 {
		return &Report{}, nil
	}

	projectContext := m.projectContext()

	var (
		mu         sync.Mutex
		report     = &Report{}
		calledSet  = map[string]bool{}
	)

	for _, u := range units {
		var wg sync.WaitGroup
		for _, w := range workers {
			if report.BudgetExceeded {
				break
			}
			wg.Add(1)
			go func(w worker.Worker, u diff.Unit) {
				defer wg.Done()
				res := w.Run(ctx, m.Provider, m.Cfg.LLM.Worker.Model, u, projectContext)
				mu.Lock()
				defer mu.Unlock()
				calledSet[res.Worker] = true
				if res.Err != nil {
					report.WorkerErrors = append(report.WorkerErrors, errors.New("worker "+res.Worker+" failed: "+res.Err.Error()))
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
			break
		}
	}
	for k := range calledSet {
		report.WorkersCalled = append(report.WorkersCalled, k)
	}
	sort.Strings(report.WorkersCalled)
	report.Findings = dedupe(report.Findings)
	return report, nil
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
	severityRank := map[string]int{
		"critical": 4, "high": 3, "medium": 2, "low": 1, "info": 0,
	}
	type key struct{ file, title string; line int }
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
		if severityRank[f.Severity] > severityRank[cur.Severity] {
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
