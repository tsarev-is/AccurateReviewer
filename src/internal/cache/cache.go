// Package cache stores per-(unit, worker) finding sets so repeated reviews
// of an unchanged hunk skip the LLM round-trip entirely. The cache key is a
// content hash so any change to the file path, hunk content, worker prompt,
// project snapshot summary, or tool version invalidates automatically — no
// TTL is needed and stale entries never silently match a different question.
//
// Note on suppressions: noqa-review markers live in the hunk content, so
// adding/removing a marker changes the cache key and forces re-evaluation.
// Findings returned from the cache are passed through the same
// applySuppressions pass as fresh ones, so a cached entry on a line that
// now carries a marker is still correctly silenced.
package cache

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/scaratec/accurate-reviewer/internal/diff"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

const dirName = ".review-cache/findings"

// Key derives the cache key for one (unit, worker) pair. The version string
// must change whenever any input that could affect the model's answer
// changes — at minimum the tool's own version and the worker's prompt text.
// Callers pass `worker.Prompt` directly so a prompt edit invalidates every
// stored entry without anyone having to remember to bump a counter.
//
// `projectContext` is the same snapshot summary the master feeds into every
// worker prompt; including it in the key means a change to
// `.review-cache/project.json` (new framework detected, language re-detected)
// invalidates stored findings instead of silently replaying a stale answer
// derived from the old context.
//
// `model` is the LLM model the worker was invoked with. Including it in
// the key prevents budget-fallback runs (cheaper model) from polluting
// the cache for full-quality runs, and vice versa — without this, a
// review that engaged the fallback would seed lower-quality findings
// that a subsequent budget-healthy run would silently replay.
func Key(u diff.Unit, w worker.Worker, toolVersion, projectContext, model string) string {
	h := sha256.New()
	fmt.Fprintf(h, "v3\n%s\n%s\n%s\n", toolVersion, w.Name, model)
	h.Write([]byte(w.Prompt))
	h.Write([]byte{0})
	h.Write([]byte(projectContext))
	h.Write([]byte{0})
	h.Write([]byte(u.File))
	h.Write([]byte{0})
	// Hunks are already in source order — hash their content + line numbers
	// so a "same code at different lines" change still invalidates.
	for _, hunk := range u.Hunks {
		fmt.Fprintf(h, "@@%d,%d+%d,%d\n", hunk.OldStart, hunk.OldLines, hunk.NewStart, hunk.NewLines)
		for i, ln := range hunk.Added {
			n := hunk.NewStart
			if i < len(hunk.AddedLineNumbers) {
				n = hunk.AddedLineNumbers[i]
			}
			fmt.Fprintf(h, "+%d:%s\n", n, ln)
		}
		for _, ln := range hunk.Removed {
			fmt.Fprintf(h, "-%s\n", ln)
		}
		// Context is part of the prompt the model sees, so it must be in
		// the key — otherwise a context-only neighbour change would
		// silently serve a stale answer.
		for _, ln := range hunk.ContextBefore {
			fmt.Fprintf(h, " B:%s\n", ln)
		}
		for _, ln := range hunk.ContextAfter {
			fmt.Fprintf(h, " A:%s\n", ln)
		}
	}
	return hex.EncodeToString(h.Sum(nil))
}

type entry struct {
	Key        string           `json:"key"`
	Worker     string           `json:"worker"`
	File       string           `json:"file"`
	Findings   []worker.Finding `json:"findings"`
	UsedTokens int              `json:"used_tokens"`
}

// Load returns (findings, tokens, true) if a cache entry exists for key.
// The tokens value is the model's reported usage at the time of the original
// call; replaying it lets the master keep its running budget consistent
// across cache hits and misses.
func Load(root, key string) ([]worker.Finding, int, bool) {
	path := filepath.Join(root, dirName, key+".json")
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, 0, false
	}
	var e entry
	if err := json.Unmarshal(data, &e); err != nil {
		// Corrupt entry — treat as a miss and let the next save overwrite.
		return nil, 0, false
	}
	return e.Findings, e.UsedTokens, true
}

// Save persists one (unit, worker) result. Save failures are reported but
// must not block the review — the cache is a performance affordance, not a
// correctness boundary.
func Save(root, key string, w worker.Worker, u diff.Unit, findings []worker.Finding, usedTokens int) error {
	dir := filepath.Join(root, dirName)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return fmt.Errorf("cache mkdir: %w", err)
	}
	// Sort findings deterministically so the on-disk file is stable and
	// diff-friendly when the .review-cache/ ends up in version control or
	// is moved between CI runs.
	sorted := append([]worker.Finding(nil), findings...)
	sort.SliceStable(sorted, func(i, j int) bool {
		if sorted[i].Line != sorted[j].Line {
			return sorted[i].Line < sorted[j].Line
		}
		return sorted[i].Title < sorted[j].Title
	})
	e := entry{Key: key, Worker: w.Name, File: u.File, Findings: sorted, UsedTokens: usedTokens}
	data, err := json.MarshalIndent(e, "", "  ")
	if err != nil {
		return fmt.Errorf("cache marshal: %w", err)
	}
	tmp := filepath.Join(dir, key+".json.tmp")
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return fmt.Errorf("cache write: %w", err)
	}
	final := filepath.Join(dir, key+".json")
	if err := os.Rename(tmp, final); err != nil {
		return fmt.Errorf("cache rename: %w", err)
	}
	return nil
}
