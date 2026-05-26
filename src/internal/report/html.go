package report

import (
	"fmt"
	"html/template"
	"io"
	"sort"
	"strings"
	"time"

	"github.com/scaratec/accurate-reviewer/internal/severity"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

// HTML writes a self-contained report — one file, no external assets — so
// the user can open it in a browser straight from disk or behind the
// `accurate-reviewer serve` HTTP server. Findings are grouped per file
// and per severity within each file. Every model-supplied string is run
// through `html/template`'s context-aware escaper so a prompt-injected
// response cannot inject script tags or break out of the document
// structure (CWE-79). The `Reviewed:` list is truncated when long so an
// audit of a thousand files does not produce a mile-wide header.
func HTML(out io.Writer, findings []worker.Finding, blocking string, reviewedFiles []string) error {
	type fileGroup struct {
		File     string
		Findings []worker.Finding
	}
	byFile := map[string][]worker.Finding{}
	for _, f := range findings {
		byFile[f.File] = append(byFile[f.File], f)
	}
	var groups []fileGroup
	for _, file := range sortedKeys(byFile) {
		grp := byFile[file]
		sort.SliceStable(grp, func(i, j int) bool {
			ri, rj := severity.Rank(grp[i].Severity), severity.Rank(grp[j].Severity)
			if ri != rj {
				return ri > rj
			}
			return grp[i].Line < grp[j].Line
		})
		groups = append(groups, fileGroup{File: file, Findings: grp})
	}

	blockedCount := 0
	for _, f := range findings {
		if severity.AtLeast(f.Severity, blocking) {
			blockedCount++
		}
	}

	const reviewedHead = 10
	reviewedPreview := reviewedFiles
	reviewedRemaining := 0
	if len(reviewedFiles) > reviewedHead {
		reviewedPreview = reviewedFiles[:reviewedHead]
		reviewedRemaining = len(reviewedFiles) - reviewedHead
	}

	data := struct {
		GeneratedAt       string
		Blocking          string
		TotalFindings     int
		BlockedCount      int
		ReviewedPreview   []string
		ReviewedJoined    string
		ReviewedRemaining int
		ReviewedAll       string
		Groups            []fileGroup
		CSS               template.CSS
	}{
		GeneratedAt:       time.Now().UTC().Format(time.RFC3339),
		Blocking:          blocking,
		TotalFindings:     len(findings),
		BlockedCount:      blockedCount,
		ReviewedPreview:   reviewedPreview,
		ReviewedJoined:    strings.Join(reviewedPreview, ", "),
		ReviewedRemaining: reviewedRemaining,
		ReviewedAll:       strings.Join(reviewedFiles, ", "),
		Groups:            groups,
		CSS:               template.CSS(cssBody),
	}

	tmpl, err := template.New("report").Funcs(template.FuncMap{
		"upper": strings.ToUpper,
		"lower": strings.ToLower,
	}).Parse(htmlBody)
	if err != nil {
		return fmt.Errorf("parse template: %w", err)
	}
	return tmpl.Execute(out, data)
}

func sortedKeys(m map[string][]worker.Finding) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

const htmlBody = `<!DOCTYPE html>
<html lang="en"><head>
<meta charset="utf-8">
<title>AccurateReviewer report</title>
<style>{{.CSS}}</style>
</head><body>
<header><h1>AccurateReviewer report</h1>
<p class="meta">Generated {{.GeneratedAt}} &middot; {{.TotalFindings}} finding(s) &middot; {{.BlockedCount}} at or above <code>{{.Blocking}}</code></p>
{{- if .ReviewedPreview }}
{{- if gt .ReviewedRemaining 0 }}
<details class="reviewed"><summary>Reviewed: <code>{{.ReviewedJoined}}</code> &hellip; ({{.ReviewedRemaining}} more)</summary>
<p class="meta"><code>{{.ReviewedAll}}</code></p>
</details>
{{- else }}
<p class="meta">Reviewed: <code>{{.ReviewedJoined}}</code></p>
{{- end }}
{{- end }}
</header><main>
{{- if not .Groups }}
<p class="empty">No findings.</p>
{{- end }}
{{- range .Groups }}
<section class="file"><h2>{{.File}}</h2><ol class="findings">
{{- range .Findings }}
<li class="finding sev-{{lower .Severity}}"><div class="head"><span class="sev">{{upper .Severity}}</span> <span class="loc">line {{.Line}}</span> <span class="title">{{.Title}}</span>
{{- if .Fix }} <span class="fixbadge">fix available</span>{{end}}</div>
{{- if .CWE }}
<div class="cwe">{{.CWE}}</div>
{{- end }}
<div class="why">{{.Why}}</div>
{{- if .Occurrences }}
<div class="occurrences">also at:
{{- range $i, $o := .Occurrences }}{{if $i}},{{end}} <code>{{$o.File}}:{{$o.Line}}</code>{{end}}</div>
{{- end }}
{{- if .Fix }}
<details class="fix"><summary>Suggested fix{{if .Fix.Description}}: {{.Fix.Description}}{{end}}</summary>
{{- range .Fix.Replacements }}
<pre class="patch"><code>{{.File}}:{{.StartLine}}-{{.EndLine}}
{{.NewText}}</code></pre>
{{- end }}
</details>
{{- end }}
{{- if .Worker }}
<div class="worker">flagged by: {{.Worker}}</div>
{{- end }}
</li>
{{- end }}
</ol></section>
{{- end }}
</main></body></html>
`

const cssBody = `
body { font-family: -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif; background: #fafafa; color: #222; margin: 0; }
header { background: #1f2937; color: #fff; padding: 1.5rem 2rem; }
header h1 { margin: 0 0 .25rem; font-size: 1.4rem; }
header .meta { margin: .25rem 0; font-size: .9rem; opacity: .85; }
header code { background: rgba(255,255,255,.12); padding: 1px 5px; border-radius: 3px; }
header details.reviewed summary { cursor: pointer; font-size: .9rem; opacity: .85; }
main { max-width: 960px; margin: 1.5rem auto; padding: 0 1rem; }
.empty { color: #666; font-style: italic; }
section.file { background: #fff; border: 1px solid #e2e8f0; border-radius: 6px; margin-bottom: 1.5rem; padding: 1rem 1.25rem; }
section.file h2 { font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: 1rem; color: #1f2937; margin: 0 0 .5rem; word-break: break-all; }
ol.findings { list-style: none; padding: 0; margin: 0; }
li.finding { border-left: 4px solid #cbd5e1; padding: .5rem .75rem; margin: .5rem 0; background: #f8fafc; border-radius: 0 4px 4px 0; }
li.finding.sev-critical { border-color: #b91c1c; }
li.finding.sev-high     { border-color: #ea580c; }
li.finding.sev-medium   { border-color: #ca8a04; }
li.finding.sev-low      { border-color: #2563eb; }
li.finding.sev-info     { border-color: #64748b; }
.head { display: flex; gap: .5rem; align-items: baseline; flex-wrap: wrap; }
.sev { font-size: .7rem; font-weight: 700; padding: 2px 6px; border-radius: 3px; background: #1f2937; color: #fff; }
.sev-critical .sev { background: #b91c1c; }
.sev-high .sev     { background: #ea580c; }
.sev-medium .sev   { background: #ca8a04; }
.sev-low .sev      { background: #2563eb; }
.loc { color: #475569; font-family: ui-monospace, monospace; font-size: .85rem; }
.title { font-weight: 600; }
.cwe, .worker, .occurrences { font-size: .8rem; color: #64748b; margin-top: .25rem; }
.occurrences code { background: #e2e8f0; padding: 1px 4px; border-radius: 3px; }
.fixbadge { font-size: .65rem; font-weight: 700; padding: 1px 5px; border-radius: 3px; background: #16a34a; color: #fff; }
details.fix { margin-top: .4rem; font-size: .85rem; }
details.fix summary { cursor: pointer; color: #15803d; }
pre.patch { background: #0f172a; color: #e2e8f0; padding: .5rem .75rem; border-radius: 4px; overflow-x: auto; font-family: ui-monospace, SFMono-Regular, Consolas, monospace; font-size: .8rem; margin: .35rem 0; }
.why { margin-top: .35rem; line-height: 1.4; }
`
