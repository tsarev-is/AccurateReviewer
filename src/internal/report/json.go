package report

import (
	"encoding/json"
	"io"

	"github.com/scaratec/accurate-reviewer/internal/worker"
)

// JSONReport is the machine-readable shape produced by `review --output
// X.json`. The same struct is consumed by `accurate-reviewer post-comments`
// so the two commands stay in lockstep without anyone redefining the
// schema. Adding a field is safe; renaming or removing one is a breaking
// change and needs the `schema_version` bump below.
type JSONReport struct {
	SchemaVersion    int              `json:"schema_version"`
	BlockingSeverity string           `json:"blocking_severity"`
	Reviewed         []string         `json:"reviewed"`
	Findings         []worker.Finding `json:"findings"`
}

const JSONSchemaVersion = 1

// JSON writes the structured report. Blocking is whatever severity the
// .review.yml chose; downstream tools can decide their own thresholds, but
// recording the threshold the review actually used here means a JSON
// report can be replayed deterministically.
func JSON(out io.Writer, findings []worker.Finding, blocking string, reviewedFiles []string) error {
	rep := JSONReport{
		SchemaVersion:    JSONSchemaVersion,
		BlockingSeverity: blocking,
		Reviewed:         reviewedFiles,
		Findings:         findings,
	}
	if rep.Reviewed == nil {
		rep.Reviewed = []string{}
	}
	if rep.Findings == nil {
		rep.Findings = []worker.Finding{}
	}
	enc := json.NewEncoder(out)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}
