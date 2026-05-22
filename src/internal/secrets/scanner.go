// Package secrets is the deterministic, no-LLM pre-flight check.
// Cost of a leaked token is catastrophic and LLMs have unacceptable
// false-negative rates for this class of problem, so it must never go
// through the model.
package secrets

import (
	"bufio"
	"io"
	"math"
	"regexp"
	"strings"
)

type Finding struct {
	Rule     string `json:"rule"`
	File     string `json:"file"`
	Line     int    `json:"line"`
	Severity string `json:"severity"`
	Match    string `json:"match"` // redacted in reports
}

type rule struct {
	name    string
	pattern *regexp.Regexp
}

var rules = []rule{
	{"aws-access-key", regexp.MustCompile(`(?:AKIA|ASIA)[A-Z0-9]{16}`)},
	{"github-fine-grain", regexp.MustCompile(`github_pat_[A-Za-z0-9_]{20,}`)},
	{"github-pat", regexp.MustCompile(`ghp_[A-Za-z0-9]{36,}`)},
	{"stripe-live-key", regexp.MustCompile(`sk_live_[A-Za-z0-9]{16,}`)},
	{"slack-bot-token", regexp.MustCompile(`xox[abp]-[A-Za-z0-9-]{20,}`)},
	{"google-api-key", regexp.MustCompile(`AIza[A-Za-z0-9_\-]{30,}`)},
	{"pem-private-key", regexp.MustCompile(`-----BEGIN (?:RSA |EC |DSA |OPENSSH |)PRIVATE KEY-----`)},
}

// sensitiveAssignment matches: const|var|let foo_token = "..." / foo_key = '...'
// — any identifier ending in token|key|secret|password|passwd|apikey assigned
// to a quoted string. We then run the entropy test on the captured string only.
var sensitiveAssignment = regexp.MustCompile(`(?i)\b([A-Za-z_][A-Za-z0-9_]*(?:token|key|secret|password|passwd|apikey))\b\s*[:=]\s*["']([^"']+)["']`)

// Scan returns findings for one source file. Path is recorded verbatim so the
// caller controls how it appears in the report.
func Scan(path string, r io.Reader, entropyThreshold float64) ([]Finding, error) {
	var out []Finding
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 64*1024), 1024*1024)
	lineNo := 0
	for scanner.Scan() {
		lineNo++
		line := scanner.Text()

		for _, rl := range rules {
			if m := rl.pattern.FindString(line); m != "" {
				out = append(out, Finding{
					Rule: rl.name, File: path, Line: lineNo,
					Severity: "critical", Match: redact(m),
				})
			}
		}
		// Generic entropy rule — only on assignments to suspicious names,
		// so we don't fire on random base64-looking literals in test data.
		for _, m := range sensitiveAssignment.FindAllStringSubmatch(line, -1) {
			value := m[2]
			if shannonEntropy(value) >= entropyThreshold && len(value) >= 16 {
				out = append(out, Finding{
					Rule: "generic-entropy", File: path, Line: lineNo,
					Severity: "critical", Match: redact(value),
				})
			}
		}
	}
	return out, scanner.Err()
}

func redact(s string) string {
	if len(s) <= 6 {
		return "***"
	}
	return s[:3] + strings.Repeat("*", len(s)-6) + s[len(s)-3:]
}

func shannonEntropy(s string) float64 {
	if s == "" {
		return 0
	}
	var freq [256]int
	for i := 0; i < len(s); i++ {
		freq[s[i]]++
	}
	n := float64(len(s))
	h := 0.0
	for _, c := range freq {
		if c == 0 {
			continue
		}
		p := float64(c) / n
		h -= p * math.Log2(p)
	}
	return h
}
