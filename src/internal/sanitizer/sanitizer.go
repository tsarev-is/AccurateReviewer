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
}

type Options struct {
	NeutraliseEnabled bool
}

func Default() Options { return Options{NeutraliseEnabled: true} }

// Sanitize wraps the snippet in boundary delimiters and replaces matches of
// the injection patterns with a [neutralised:<rule>] marker, preserving line
// numbers so report locations stay valid.
func Sanitize(snippet string, opts Options) string {
	body := snippet
	if opts.NeutraliseEnabled {
		for _, rl := range rules {
			body = rl.pattern.ReplaceAllString(body, "[neutralised:"+rl.name+"]")
		}
	}
	var b strings.Builder
	b.WriteString(StartDelimiter)
	b.WriteByte('\n')
	b.WriteString(body)
	if !strings.HasSuffix(body, "\n") {
		b.WriteByte('\n')
	}
	b.WriteString(EndDelimiter)
	b.WriteByte('\n')
	return b.String()
}
