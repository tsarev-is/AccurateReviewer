// Package cves runs a deterministic dependency-vulnerability scan as a
// pre-flight step before the LLM-driven review. Like the secrets scanner
// (internal/secrets), it never sends anything to an LLM — false negatives
// on a known CVE are too expensive to risk.
//
// The actual scanning is delegated to a user-installed `osv-scanner`
// binary (https://github.com/google/osv-scanner). This is consistent with
// the project's rule of shelling out to existing CLIs rather than
// embedding HTTP clients in the binary — osv-scanner already handles
// network access, manifest parsing for every supported ecosystem, and
// rate limits to the OSV database.
//
// If osv-scanner is not on PATH, Scan returns ([], nil) when
// Options.Required is false (the default in pre-flight context — let a
// user without the tool installed continue with the LLM review) and an
// error when Options.Required is true (the standalone `scan-cves`
// subcommand sets this so a missing dep is loud, not silent).
package cves

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	"github.com/scaratec/accurate-reviewer/internal/severity"
)

const DefaultBin = "osv-scanner"

// Vuln is one CVE/advisory hit on one dependency. ID is typically a
// GHSA-* identifier (osv-scanner's primary key); CVE is the
// cross-referenced CVE id when present in osv's aliases list.
type Vuln struct {
	File     string `json:"file"`
	Package  string `json:"package"`
	Version  string `json:"version"`
	ID       string `json:"id"`
	CVE      string `json:"cve,omitempty"`
	Severity string `json:"severity"`
	Summary  string `json:"summary"`
	FixedIn  string `json:"fixed_in,omitempty"`
}

type Options struct {
	Bin            string
	TimeoutSeconds int
	// MinSeverity drops anything below the given level (info|low|medium|
	// high|critical). Empty string keeps everything.
	MinSeverity string
	// Required: when true, a missing osv-scanner is an error rather than
	// a silent "no findings". The pre-flight uses false; the explicit
	// `scan-cves` subcommand uses true.
	Required bool
}

// Scan runs osv-scanner on `root` and returns the parsed advisories.
// osv-scanner exits 0 when nothing is found and 1 when vulnerabilities
// are present — both are treated as success here. Any other exit code is
// surfaced verbatim from the child's stderr.
func Scan(ctx context.Context, root string, opts Options) ([]Vuln, error) {
	bin := opts.Bin
	if bin == "" {
		bin = DefaultBin
	}
	resolved, err := exec.LookPath(bin)
	if err != nil {
		if opts.Required {
			return nil, fmt.Errorf("%q CLI not found on PATH — install https://github.com/google/osv-scanner before running cve scans", bin)
		}
		// Optional pre-flight: missing tool is not an error.
		return nil, nil
	}

	timeout := time.Duration(opts.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 60 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(cctx, resolved, "--format", "json", "--recursive", root)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		// osv-scanner returns 1 when vulnerabilities are reported. Detect
		// that case by exit code, not by message text, so a child that
		// happens to print "vuln" on stderr in some future version does
		// not get misclassified.
		exitErr, ok := err.(*exec.ExitError)
		if !ok || exitErr.ExitCode() > 1 {
			msg := strings.TrimSpace(stderr.String())
			if msg == "" {
				msg = err.Error()
			}
			return nil, fmt.Errorf("osv-scanner: %s", msg)
		}
	}

	vulns, err := parseOSVOutput(stdout.Bytes())
	if err != nil {
		return nil, fmt.Errorf("parse osv-scanner output: %w", err)
	}
	if opts.MinSeverity != "" {
		vulns = filterBySeverity(vulns, opts.MinSeverity)
	}
	return vulns, nil
}

// osvOutput mirrors the relevant subset of osv-scanner's --format=json
// schema. We do not declare every field — only what we read — so a
// future osv-scanner release that adds fields keeps parsing cleanly.
type osvOutput struct {
	Results []struct {
		Source struct {
			Path string `json:"path"`
			Type string `json:"type"`
		} `json:"source"`
		Packages []struct {
			Package struct {
				Name      string `json:"name"`
				Version   string `json:"version"`
				Ecosystem string `json:"ecosystem"`
			} `json:"package"`
			Vulnerabilities []struct {
				ID       string   `json:"id"`
				Summary  string   `json:"summary"`
				Aliases  []string `json:"aliases"`
				Affected []struct {
					Ranges []struct {
						Type   string `json:"type"`
						Events []struct {
							Introduced string `json:"introduced,omitempty"`
							Fixed      string `json:"fixed,omitempty"`
						} `json:"events"`
					} `json:"ranges"`
				} `json:"affected"`
				DatabaseSpecific struct {
					Severity string `json:"severity"`
				} `json:"database_specific"`
				Severity []struct {
					Type  string `json:"type"`
					Score string `json:"score"`
				} `json:"severity"`
			} `json:"vulnerabilities"`
		} `json:"packages"`
	} `json:"results"`
}

func parseOSVOutput(raw []byte) ([]Vuln, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	var out osvOutput
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, err
	}
	var vulns []Vuln
	for _, r := range out.Results {
		for _, pkg := range r.Packages {
			for _, v := range pkg.Vulnerabilities {
				vuln := Vuln{
					File:     r.Source.Path,
					Package:  pkg.Package.Name,
					Version:  pkg.Package.Version,
					ID:       v.ID,
					Summary:  v.Summary,
					Severity: normaliseSeverity(v.DatabaseSpecific.Severity),
					CVE:      pickCVE(v.Aliases),
					FixedIn:  pickFixedVersion(v.Affected),
				}
				vulns = append(vulns, vuln)
			}
		}
	}
	return vulns, nil
}

// pickCVE returns the first CVE-* alias if any; osv advisories may also
// list GHSA-* / OSV-* aliases that we ignore here (they would be the
// same advisory's id under a different naming scheme).
func pickCVE(aliases []string) string {
	for _, a := range aliases {
		if strings.HasPrefix(a, "CVE-") {
			return a
		}
	}
	return ""
}

// pickFixedVersion returns the first non-empty `fixed` event in the
// affected ranges. osv-scanner lists ranges per ecosystem; we surface a
// single fix version because the comment template can only carry one,
// and an exact version is more actionable than the bare advisory.
func pickFixedVersion(affected []struct {
	Ranges []struct {
		Type   string `json:"type"`
		Events []struct {
			Introduced string `json:"introduced,omitempty"`
			Fixed      string `json:"fixed,omitempty"`
		} `json:"events"`
	} `json:"ranges"`
}) string {
	for _, a := range affected {
		for _, r := range a.Ranges {
			for _, e := range r.Events {
				if e.Fixed != "" {
					return e.Fixed
				}
			}
		}
	}
	return ""
}

// normaliseSeverity maps osv-scanner's "LOW"/"MEDIUM"/"HIGH"/"CRITICAL"
// (and the occasional empty string for advisories without a categorical
// severity) onto the closed enum the rest of the pipeline understands.
// Anything unknown becomes "medium" — high enough to surface, low enough
// to stay below the default `blocking: critical` so an unclassified
// advisory does not silently fail every PR.
func normaliseSeverity(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "critical":
		return "critical"
	case "high":
		return "high"
	case "moderate", "medium":
		return "medium"
	case "low":
		return "low"
	case "negligible", "info", "informational":
		return "info"
	case "":
		return "medium"
	default:
		return "medium"
	}
}

func filterBySeverity(in []Vuln, min string) []Vuln {
	out := in[:0]
	for _, v := range in {
		if severity.AtLeast(v.Severity, min) {
			out = append(out, v)
		}
	}
	return out
}
