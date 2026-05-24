// Package report renders the master's output. Console is the only MVP format.
package report

import (
	"fmt"
	"io"
	"strings"
	"unicode"

	"github.com/scaratec/accurate-reviewer/internal/severity"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

// Console writes a grouped, colourless plain-text report. Returns true iff
// any finding meets the blocking severity. The `reviewedFiles` line lets
// downstream consumers see which files were inspected even when the
// findings list is empty.
func Console(out io.Writer, findings []worker.Finding, blocking string, reviewedFiles []string) bool {
	if len(reviewedFiles) > 0 {
		fmt.Fprintf(out, "Reviewed: %s\n", strings.Join(reviewedFiles, ", "))
	}
	blocked := false
	for _, f := range findings {
		if severity.AtLeast(f.Severity, blocking) {
			blocked = true
		}
		fmt.Fprintf(out, "  [%s] %s:%d %s\n", f.Severity, sanitiseForTerminal(f.File), f.Line, sanitiseForTerminal(f.Title))
		if f.CWE != "" {
			fmt.Fprintf(out, "      cwe=%s\n", sanitiseForTerminal(f.CWE))
		}
		fmt.Fprintf(out, "      why: %s\n", sanitiseForTerminal(f.Why))
	}
	fmt.Fprintf(out, "%d findings\n", len(findings))
	return blocked
}

// sanitiseForTerminal scrubs LLM-supplied strings of control characters
// before they reach the user's terminal. A prompt-injected response can
// otherwise embed ANSI/OSC escape sequences (cursor moves, SGR resets,
// even OSC-8 hyperlinks that some terminals action automatically). We
// keep printable runes, tabs, and intra-string spaces; everything else
// — including \r, the ESC byte (0x1B), DEL (0x7F), and the C1 range
// (U+0080–U+009F) — becomes a literal "?" so the output stays readable
// without losing finding context. CWE-116.
func sanitiseForTerminal(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\t':
			b.WriteRune(r)
		case r < 0x20, r == 0x7F, r >= 0x80 && r <= 0x9F:
			b.WriteByte('?')
		case !unicode.IsPrint(r):
			b.WriteByte('?')
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}
