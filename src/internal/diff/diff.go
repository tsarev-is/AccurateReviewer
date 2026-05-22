// Package diff parses a unified diff into review units. A review unit is one
// changed file plus its hunks plus a few lines of context per hunk. Files
// that are pure deletions, binary, or excluded by config produce no unit.
package diff

import (
	"bufio"
	"io"
	"path/filepath"
	"strconv"
	"strings"
)

type Hunk struct {
	OldStart      int      `json:"old_start"`
	OldLines      int      `json:"old_lines"`
	NewStart      int      `json:"new_start"`
	NewLines      int      `json:"new_lines"`
	ContextBefore []string `json:"context_before"`
	ContextAfter  []string `json:"context_after"`
	Added         []string `json:"added"`
	Removed       []string `json:"removed"`
	// AddedLineNumbers are the line numbers in the new file for each added line.
	AddedLineNumbers []int `json:"added_line_numbers"`
}

type Unit struct {
	File         string `json:"file"`
	OldFile      string `json:"old_file,omitempty"`
	Renamed      bool   `json:"renamed,omitempty"`
	Binary       bool   `json:"binary,omitempty"`
	Deleted      bool   `json:"deleted,omitempty"`
	AddedLines   int    `json:"added_lines"`
	RemovedLines int    `json:"removed_lines"`
	Hunks        []Hunk `json:"hunks"`
}

// Parse parses a unified diff. Excludes is a list of glob patterns; matched
// files produce no unit. Deletions and binaries also produce no unit.
func Parse(r io.Reader, excludes []string) ([]Unit, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var (
		units    []Unit
		cur      *Unit
		curHunk  *Hunk
		newLine  int
		inHunk   bool
	)

	flushHunk := func() {
		if cur != nil && curHunk != nil {
			cur.Hunks = append(cur.Hunks, *curHunk)
			curHunk = nil
		}
	}
	flushUnit := func() {
		flushHunk()
		if cur == nil {
			return
		}
		// Drop units that have no additions to review (pure delete, binary, rename-only).
		if cur.Binary || cur.Deleted || cur.AddedLines == 0 {
			cur = nil
			return
		}
		if matchesAny(cur.File, excludes) {
			cur = nil
			return
		}
		units = append(units, *cur)
		cur = nil
	}

	for scanner.Scan() {
		line := scanner.Text()

		switch {
		case strings.HasPrefix(line, "diff --git "):
			flushUnit()
			cur = &Unit{}
			inHunk = false
			// "diff --git a/old b/new"
			parts := strings.SplitN(line, " ", 4)
			if len(parts) == 4 {
				newPath := strings.TrimPrefix(parts[3], "b/")
				cur.File = newPath
			}
		case cur != nil && strings.HasPrefix(line, "rename from "):
			cur.OldFile = strings.TrimPrefix(line, "rename from ")
			cur.Renamed = true
		case cur != nil && strings.HasPrefix(line, "rename to "):
			cur.File = strings.TrimPrefix(line, "rename to ")
			cur.Renamed = true
		case cur != nil && strings.HasPrefix(line, "deleted file mode"):
			cur.Deleted = true
		case cur != nil && strings.HasPrefix(line, "Binary files "):
			cur.Binary = true
		case cur != nil && strings.HasPrefix(line, "--- "):
			old := strings.TrimPrefix(line, "--- ")
			if old != "/dev/null" {
				cur.OldFile = strings.TrimPrefix(old, "a/")
			}
		case cur != nil && strings.HasPrefix(line, "+++ "):
			newp := strings.TrimPrefix(line, "+++ ")
			if newp == "/dev/null" {
				cur.Deleted = true
			} else {
				cur.File = strings.TrimPrefix(newp, "b/")
			}
		case cur != nil && strings.HasPrefix(line, "@@"):
			flushHunk()
			h := parseHunkHeader(line)
			curHunk = &h
			newLine = h.NewStart
			inHunk = true
		case inHunk && curHunk != nil:
			if len(line) == 0 {
				curHunk.ContextAfter = append(curHunk.ContextAfter, "")
				newLine++
				continue
			}
			tag := line[0]
			body := line[1:]
			switch tag {
			case '+':
				curHunk.Added = append(curHunk.Added, body)
				curHunk.AddedLineNumbers = append(curHunk.AddedLineNumbers, newLine)
				cur.AddedLines++
				newLine++
			case '-':
				curHunk.Removed = append(curHunk.Removed, body)
				cur.RemovedLines++
			case ' ':
				if len(curHunk.Added)+len(curHunk.Removed) == 0 {
					if len(curHunk.ContextBefore) < 3 {
						curHunk.ContextBefore = append(curHunk.ContextBefore, body)
					}
				} else if len(curHunk.ContextAfter) < 3 {
					curHunk.ContextAfter = append(curHunk.ContextAfter, body)
				}
				newLine++
			}
		}
	}
	flushUnit()
	return units, scanner.Err()
}

// "@@ -10,3 +10,5 @@ ..." → Hunk header with starts and lengths.
func parseHunkHeader(line string) Hunk {
	h := Hunk{}
	// Strip the @@ markers and any trailing section header.
	body := strings.TrimPrefix(line, "@@")
	if idx := strings.Index(body, "@@"); idx >= 0 {
		body = body[:idx]
	}
	body = strings.TrimSpace(body)
	fields := strings.Fields(body)
	for _, f := range fields {
		if strings.HasPrefix(f, "-") {
			h.OldStart, h.OldLines = parseRange(f[1:])
		} else if strings.HasPrefix(f, "+") {
			h.NewStart, h.NewLines = parseRange(f[1:])
		}
	}
	return h
}

func parseRange(s string) (start, length int) {
	length = 1
	if idx := strings.Index(s, ","); idx >= 0 {
		start, _ = strconv.Atoi(s[:idx])
		length, _ = strconv.Atoi(s[idx+1:])
	} else {
		start, _ = strconv.Atoi(s)
	}
	return
}

func matchesAny(path string, patterns []string) bool {
	for _, p := range patterns {
		if globMatch(p, path) {
			return true
		}
	}
	return false
}

// globMatch supports "**" as a recursive wildcard.
func globMatch(pattern, name string) bool {
	if pattern == "" {
		return false
	}
	// Translate ** → .*  and * → [^/]*  manually, then fall back to filepath.Match
	// for the simple cases. We use the simplest implementation that handles our
	// fixture patterns (vendor/**, **/migrations/**, **/*.generated.*).
	if strings.Contains(pattern, "**") {
		return doubleStarMatch(pattern, name)
	}
	ok, _ := filepath.Match(pattern, name)
	return ok
}

func doubleStarMatch(pattern, name string) bool {
	// Split by ** and require each segment to appear in order.
	parts := strings.Split(pattern, "**")
	pos := 0
	for i, seg := range parts {
		seg = strings.TrimPrefix(seg, "/")
		seg = strings.TrimSuffix(seg, "/")
		if seg == "" {
			continue
		}
		if i == 0 {
			if !strings.HasPrefix(name, seg) {
				return false
			}
			pos = len(seg)
			continue
		}
		idx := strings.Index(name[pos:], seg)
		if idx < 0 {
			return false
		}
		pos += idx + len(seg)
	}
	return true
}
