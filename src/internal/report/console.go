// Package report renders the master's output. Console is the only MVP format.
package report

import (
	"fmt"
	"io"
	"strings"

	"github.com/scaratec/accurate-reviewer/internal/worker"
)

var severityRank = map[string]int{
	"critical": 4, "high": 3, "medium": 2, "low": 1, "info": 0,
}

// Console writes a grouped, colourless plain-text report. Returns true iff
// any finding meets the blocking severity. The `reviewedFiles` line lets
// downstream consumers see which files were inspected even when the
// findings list is empty.
func Console(out io.Writer, findings []worker.Finding, blocking string, reviewedFiles []string) bool {
	if len(reviewedFiles) > 0 {
		fmt.Fprintf(out, "Reviewed: %s\n", strings.Join(reviewedFiles, ", "))
	}
	blocked := false
	threshold := severityRank[blocking]
	for _, f := range findings {
		if severityRank[f.Severity] >= threshold {
			blocked = true
		}
		fmt.Fprintf(out, "  [%s] %s:%d %s\n", f.Severity, f.File, f.Line, f.Title)
		if f.CWE != "" {
			fmt.Fprintf(out, "      cwe=%s\n", f.CWE)
		}
		fmt.Fprintf(out, "      why: %s\n", f.Why)
	}
	fmt.Fprintf(out, "%d findings\n", len(findings))
	return blocked
}
