// Synthesises review units from a directory walk. Used by `review --full`,
// which has no real diff: every source file is treated as "all-added" so the
// same master/worker pipeline can run unchanged.
package diff

import (
	"bytes"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// sourceExt is the closed set of file extensions the full-mode walk
// considers. We restrict by extension instead of "everything that isn't
// binary" because the full mode is the path where an audit could
// otherwise drag generated artefacts, lock files, fixtures, or stray
// dotfiles into the LLM prompt and burn the user's token budget. Editing
// this list is the canonical way to add a new language.
var sourceExt = map[string]bool{
	".go":    true,
	".py":    true,
	".js":    true,
	".jsx":   true,
	".ts":    true,
	".tsx":   true,
	".rs":    true,
	".java":  true,
	".rb":    true,
	".php":   true,
	".c":     true,
	".cc":    true,
	".cpp":   true,
	".h":     true,
	".hpp":   true,
	".cs":    true,
	".kt":    true,
	".swift": true,
}

// WalkAsUnits walks `root` and returns one Unit per source file. Files
// matching `excludes` (using the same glob syntax as diff.Parse), files
// inside ".git" or ".review-cache", binaries, dotfiles, files whose
// extension is not in the source whitelist, and files larger than
// maxBytes are skipped. The returned units have a single hunk whose
// Added lines are the entire file content with line numbers starting at
// 1 — indistinguishable to the worker from a "new file" entry in a real
// diff.
func WalkAsUnits(root string, excludes []string, maxBytes int64) ([]Unit, error) {
	if maxBytes <= 0 {
		// 256 KiB is well above any hand-written source file. A larger
		// file is almost certainly generated or vendored and would only
		// burn LLM tokens for no reviewer value.
		maxBytes = 256 * 1024
	}
	var units []Unit
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, rerr := filepath.Rel(root, path)
		if rerr != nil {
			return nil
		}
		rel = filepath.ToSlash(rel)
		if d.IsDir() {
			// Hard-coded skips: directories that universally do NOT
			// contain reviewable source. `bin/` is intentionally NOT in
			// this list — it's a Makefile-output convention for this
			// project but a real source directory for many consumers, so
			// users who need it skipped add it to .review.yml `exclude`.
			base := d.Name()
			if base == ".git" || base == ".review-cache" || base == "node_modules" || base == "vendor" {
				if rel != "." {
					return filepath.SkipDir
				}
			}
			// Skip any directory whose name starts with a dot — these
			// hold IDE state, caches, or test scaffolding (e.g. the BDD
			// `.ar-*` scratch files live at the workdir root, but the
			// rule extends naturally to `.idea`, `.venv`, `.pytest_cache`).
			if strings.HasPrefix(base, ".") && rel != "." {
				return filepath.SkipDir
			}
			return nil
		}
		if rel == "." || rel == "" {
			return nil
		}
		base := filepath.Base(rel)
		if strings.HasPrefix(base, ".") {
			return nil
		}
		ext := strings.ToLower(filepath.Ext(rel))
		if !sourceExt[ext] {
			return nil
		}
		if matchesAny(rel, excludes) {
			return nil
		}
		info, ierr := d.Info()
		if ierr != nil {
			return nil
		}
		if info.Size() > maxBytes {
			return nil
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		if isBinary(data) {
			return nil
		}
		u := makeFullUnit(rel, data)
		if u.AddedLines == 0 {
			return nil
		}
		units = append(units, u)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.SliceStable(units, func(i, j int) bool { return units[i].File < units[j].File })
	return units, nil
}

func makeFullUnit(rel string, data []byte) Unit {
	// Strip a trailing newline so the synthesised hunk does not carry an
	// empty final added line (which would skew line counts).
	content := strings.TrimRight(string(data), "\n")
	if content == "" {
		return Unit{File: rel}
	}
	lines := strings.Split(content, "\n")
	h := Hunk{
		OldStart: 0,
		OldLines: 0,
		NewStart: 1,
		NewLines: len(lines),
		Added:    lines,
	}
	h.AddedLineNumbers = make([]int, len(lines))
	for i := range lines {
		h.AddedLineNumbers[i] = i + 1
	}
	return Unit{
		File:       rel,
		AddedLines: len(lines),
		Hunks:      []Hunk{h},
	}
}

// isBinary uses the classic "NUL byte in the first 8 KiB" heuristic. It is
// not perfect — some valid UTF-16 text trips it — but it is good enough to
// keep PNGs and compiled artefacts out of the LLM prompt without an
// external dependency on libmagic.
func isBinary(data []byte) bool {
	n := len(data)
	if n > 8192 {
		n = 8192
	}
	return bytes.IndexByte(data[:n], 0) >= 0
}
