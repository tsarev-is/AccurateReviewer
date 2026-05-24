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

// Suppressions returns a map from added-line number to the reason text
// supplied after an inline `noqa-review:` marker on that same line. The
// recognised comment prefixes cover the comment syntax of every language we
// realistically target — `//`, `#`, `--`, and `/*` — without language
// detection: matching is purely textual on the added line. Findings whose
// `(file, line)` matches an entry here are dropped by the master.
//
// Only added lines are scanned; pre-existing legacy markers in unchanged
// context lines must not silence findings on the diff (that would let an
// attacker mute a security finding by planting a marker nearby in an
// untouched line).
func (u Unit) Suppressions() map[int]string {
	out := map[int]string{}
	for _, h := range u.Hunks {
		for i, line := range h.Added {
			reason, ok := extractSuppression(line)
			if !ok {
				continue
			}
			ln := h.NewStart
			if i < len(h.AddedLineNumbers) {
				ln = h.AddedLineNumbers[i]
			}
			out[ln] = reason
		}
	}
	return out
}

// extractSuppression looks for a `noqa-review:` marker that sits inside a
// real trailing comment on the line and returns the trimmed reason. We
// scan left-to-right tracking string-literal state so a `//`, `#`, `--`,
// or `/*` that appears inside a quoted string cannot satisfy the
// comment-opener requirement. Without this, an attacker who controls any
// string literal on the flagged line could plant a fake comment opener
// and mute a security finding — see the BDD scenario
// "noqa-review inside a string literal is NOT honored".
func extractSuppression(line string) (string, bool) {
	const marker = "noqa-review:"
	commentStart, blockComment := findCommentStart(line)
	if commentStart < 0 {
		return "", false
	}
	// The marker must live inside the comment region, not before it.
	rest := line[commentStart:]
	idx := strings.Index(rest, marker)
	if idx < 0 {
		return "", false
	}
	reason := rest[idx+len(marker):]
	if blockComment {
		// /* … */ — drop the closer plus any whitespace on either side
		// of it. The double TrimSpace handles "reason */ ", "reason*/",
		// and "reason */" alike.
		reason = strings.TrimSpace(reason)
		reason = strings.TrimSuffix(reason, "*/")
		reason = strings.TrimSpace(reason)
	}
	return strings.TrimSpace(reason), true
}

// findCommentStart returns the byte offset of the first comment opener on
// the line that is NOT inside a string literal, plus a flag indicating
// whether the opener is the block-comment `/*` (so the caller can strip
// the matching `*/`). Recognised string delimiters: `"`, `'`, and “ ` “.
// Backslash escapes inside `"` and `'` are honored; backticks are raw and
// have no escape. The scanner is a deliberately small subset — it doesn't
// need to fully parse any one language, only to avoid mistaking
// in-string punctuation for code structure.
func findCommentStart(line string) (int, bool) {
	const (
		stateCode = iota
		stateDouble
		stateSingle
		stateBacktick
	)
	state := stateCode
	i := 0
	for i < len(line) {
		c := line[i]
		switch state {
		case stateCode:
			switch {
			case c == '"':
				state = stateDouble
				i++
			case c == '\'':
				state = stateSingle
				i++
			case c == '`':
				state = stateBacktick
				i++
			case c == '/' && i+1 < len(line) && line[i+1] == '/':
				return i, false
			case c == '/' && i+1 < len(line) && line[i+1] == '*':
				return i, true
			case c == '#':
				return i, false
			case c == '-' && i+1 < len(line) && line[i+1] == '-':
				return i, false
			default:
				i++
			}
		case stateDouble, stateSingle:
			if c == '\\' && i+1 < len(line) {
				i += 2
				continue
			}
			if (state == stateDouble && c == '"') || (state == stateSingle && c == '\'') {
				state = stateCode
			}
			i++
		case stateBacktick:
			if c == '`' {
				state = stateCode
			}
			i++
		}
	}
	return -1, false
}

// Parse parses a unified diff. Excludes is a list of glob patterns; matched
// files produce no unit. Deletions and binaries also produce no unit.
func Parse(r io.Reader, excludes []string) ([]Unit, error) {
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 4*1024*1024)

	var (
		units   []Unit
		cur     *Unit
		curHunk *Hunk
		newLine int
		inHunk  bool
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
