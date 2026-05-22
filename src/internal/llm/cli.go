package llm

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"time"
)

// CLIProvider talks to a local LLM CLI (claude, codex, or a test fake) by
// spawning it as a subprocess. The prompt is written to the child's stdin
// and the response is read from stdout. Tokens used are not reported by the
// upstream CLIs, so the provider estimates them as len(prompt)+len(response)
// in 4-byte chunks unless the child prints a `__USED_TOKENS=<n>` marker on
// the last line of stdout (test fakes use this to drive the budget tests).
type CLIProvider struct {
	Name_     string
	Bin       string
	Args      []string
	ModelFlag string
	Timeout   time.Duration
	PassEnv   []string
}

func (p *CLIProvider) Name() string { return p.Name_ }

const usedTokensMarker = "__USED_TOKENS="

func (p *CLIProvider) Complete(ctx context.Context, req Request) (*Response, error) {
	if p.Bin == "" {
		return nil, fmt.Errorf("llm: cli.bin is empty for provider %q", p.Name_)
	}

	timeout := p.Timeout
	if timeout == 0 {
		timeout = 30 * time.Second
	}
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	args := append([]string{}, p.Args...)
	if p.ModelFlag != "" && req.Model != "" {
		args = append(args, p.ModelFlag, req.Model)
	}

	cmd := exec.CommandContext(cctx, p.Bin, args...)
	cmd.Stdin = strings.NewReader(req.Prompt)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	// Hand the spawned CLI a minimal environment: only the variables we
	// were told to pass through plus a small set of metadata vars that the
	// BDD fake uses to script per-role / per-worker responses. Real
	// `claude`/`codex` ignore the metadata vars.
	cmd.Env = buildEnv(p.PassEnv, req)

	err := cmd.Run()
	if err != nil {
		// Prefer the child's stderr verbatim — both real CLIs and the BDD
		// fake produce a useful message there. Falling back to the exec
		// error is just a safety net for the "process never started" case.
		msg := strings.TrimSpace(stderr.String())
		if msg == "" {
			msg = fmt.Sprintf("%s: %v", p.Bin, err)
		}
		return nil, fmt.Errorf("%s", msg)
	}

	text, used := parseOutput(stdout.String())
	if used == 0 {
		used = estimateTokens(req.Prompt) + estimateTokens(text)
	}
	return &Response{Text: text, UsedTokens: used}, nil
}

func buildEnv(passThrough []string, req Request) []string {
	out := []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + os.Getenv("HOME"),
		"ACCURATE_REVIEWER_ROLE=" + string(req.Role),
		"ACCURATE_REVIEWER_WORKER=" + req.Worker,
		"ACCURATE_REVIEWER_MODEL=" + req.Model,
	}
	for _, name := range passThrough {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	// A handful of env vars BDD scenarios use to point the fake CLI at its
	// per-scenario state files. Real CLIs ignore them.
	for _, name := range []string{
		"ACCURATE_REVIEWER_MOCK_SCRIPT",
		"ACCURATE_REVIEWER_MOCK_PROMPT_LOG",
	} {
		if v, ok := os.LookupEnv(name); ok {
			out = append(out, name+"="+v)
		}
	}
	return out
}

// parseOutput strips an optional trailing __USED_TOKENS=N line and returns
// the response text plus the parsed token count. The marker is a test
// convention; real CLIs never emit it.
func parseOutput(s string) (string, int) {
	s = strings.TrimRight(s, "\n")
	idx := strings.LastIndex(s, "\n"+usedTokensMarker)
	if idx < 0 {
		if strings.HasPrefix(s, usedTokensMarker) {
			n, _ := strconv.Atoi(strings.TrimPrefix(s, usedTokensMarker))
			return "", n
		}
		return s, 0
	}
	marker := s[idx+1:]
	n, _ := strconv.Atoi(strings.TrimPrefix(marker, usedTokensMarker))
	return s[:idx], n
}

func estimateTokens(s string) int {
	if s == "" {
		return 0
	}
	return (len(s) + 3) / 4
}
