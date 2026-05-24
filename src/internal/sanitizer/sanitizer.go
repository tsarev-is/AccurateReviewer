// Package sanitizer wraps every code chunk in a clearly labelled boundary
// before it is sent to an LLM, and neutralises well-known injection patterns.
// The wrapping is unconditional; only the neutralisation passes can be
// disabled via config. Defence in depth: the LLM must always see the chunk
// as "data inside delimiters", never as part of its own instructions.
package sanitizer

import (
	"regexp"
	"strings"
)

const (
	StartDelimiter = "===CODE-UNDER-REVIEW==="
	EndDelimiter   = "===END-CODE-UNDER-REVIEW==="

	// Any untrusted text that gets interpolated into a worker prompt next
	// to the code under review (e.g. analyzer-produced project context)
	// must also be wrapped, otherwise an attacker who controls that text
	// — via a crafted package name, README, or framework manifest the
	// analyzer ingests — can prompt-inject the LLM. CWE-74.
	StartProjectDelimiter = "===PROJECT-CONTEXT==="
	EndProjectDelimiter   = "===END-PROJECT-CONTEXT==="

	// Task descriptions fetched from a tracker (Jira/GitHub) or a local
	// file are arbitrary user-controlled text and get the same wrap +
	// neutralise treatment as the rest of the untrusted inputs.
	StartTaskDelimiter = "===TASK-CONTEXT==="
	EndTaskDelimiter   = "===END-TASK-CONTEXT==="
)

type rule struct {
	name    string
	pattern *regexp.Regexp
}

// Each pattern is deliberately narrow. We want the sanitizer to flag genuine
// injection attempts and leave legitimate code (including documentation that
// happens to use the same English words in a non-imperative context) alone.
var rules = []rule{
	{"ignore-instructions", regexp.MustCompile(`(?i)ignore\s+(all\s+)?previous\s+instructions`)},
	{"role-override", regexp.MustCompile(`(?i)you\s+are\s+now\s+a\s+\w+\s+assistant`)},
	{"system-impersonation", regexp.MustCompile(`(?im)^\s*(?:#|//|/\*)?\s*SYSTEM\s*:`)},
	{"tool-call-forgery", regexp.MustCompile(`</?tool_use>`)},
	{"end-delimiter-fake", regexp.MustCompile(regexp.QuoteMeta(EndDelimiter))},
	{"end-project-delimiter-fake", regexp.MustCompile(regexp.QuoteMeta(EndProjectDelimiter))},
	{"end-task-delimiter-fake", regexp.MustCompile(regexp.QuoteMeta(EndTaskDelimiter))},
}

type Options struct {
	NeutraliseEnabled bool
}

func Default() Options { return Options{NeutraliseEnabled: true} }

// Sanitize wraps the snippet in CODE-UNDER-REVIEW boundary delimiters and
// replaces matches of the injection patterns with a [neutralised:<rule>]
// marker, preserving line numbers so report locations stay valid.
func Sanitize(snippet string, opts Options) string {
	return wrap(snippet, StartDelimiter, EndDelimiter, opts)
}

// SanitizeProject wraps untrusted project context (language, frameworks,
// manifest paths the analyzer produced from filesystem data) in a separate
// PROJECT-CONTEXT boundary so the LLM treats it as data rather than as part
// of its own instructions. Same neutralisation rules apply.
func SanitizeProject(snippet string, opts Options) string {
	return wrap(snippet, StartProjectDelimiter, EndProjectDelimiter, opts)
}

// SanitizeTask wraps an externally-sourced task description (from a Jira
// or GitHub issue, or a local file) in a TASK-CONTEXT boundary. The body
// is fully under attacker control whenever the issue tracker is open to
// outside contributors, so the same neutralisation passes apply.
func SanitizeTask(snippet string, opts Options) string {
	return wrap(snippet, StartTaskDelimiter, EndTaskDelimiter, opts)
}

func wrap(snippet, startDelim, endDelim string, opts Options) string {
	body := snippet
	if opts.NeutraliseEnabled {
		for _, rl := range rules {
			body = rl.pattern.ReplaceAllString(body, "[neutralised:"+rl.name+"]")
		}
	}
	var b strings.Builder
	b.WriteString(startDelim)
	b.WriteByte('\n')
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(endDelim)
	b.WriteByte('\n')
	return b.String()
}
