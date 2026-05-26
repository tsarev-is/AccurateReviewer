package cli

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// bitbucketPoster implements platformPoster for Bitbucket Cloud via the
// `bb` CLI (https://github.com/ktrysmt/go-bitbucket-cli or a compatible
// fork). Bitbucket lacks a single de-facto official CLI on par with `gh`
// or `glab` — the binary name `bb` is the most common community choice,
// and `.review.yml` lets operators override it if their tool of choice
// resolves to something else.
//
// Inline comments anchor to (path, line) directly; Bitbucket does not
// require base/head SHA position data the way GitLab does, so the call
// is simpler.
type bitbucketPoster struct {
	bin string
}

func newBitbucketPoster(binOverride string) *bitbucketPoster {
	if binOverride == "" {
		binOverride = "bb"
	}
	return &bitbucketPoster{bin: binOverride}
}

func (b *bitbucketPoster) Name() string { return "bitbucket" }

func (b *bitbucketPoster) Preflight() (string, error) {
	resolved, err := exec.LookPath(b.bin)
	if err != nil {
		return "", fmt.Errorf("'%s' CLI not found on PATH — install a Bitbucket CLI (e.g. https://github.com/ktrysmt/go-bitbucket-cli) before running post-comments --platform bitbucket", b.bin)
	}
	return resolved, nil
}

func (b *bitbucketPoster) HeadSHA(prID int) (string, error) {
	out, err := exec.Command(b.bin, "pr", "view", strconv.Itoa(prID), "--output", "json", "-F", "source.commit.hash").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	sha := strings.Trim(strings.TrimSpace(string(out)), `"`)
	if sha == "" {
		return "", fmt.Errorf("empty SHA from bb")
	}
	return sha, nil
}

func (b *bitbucketPoster) RepoSlug(override string) string {
	if override != "" {
		return override
	}
	out, err := exec.Command(b.bin, "repo", "view", "--output", "json", "-F", "full_name").CombinedOutput()
	if err != nil {
		return "OWNER/REPO"
	}
	return strings.Trim(strings.TrimSpace(string(out)), `"`)
}

func (b *bitbucketPoster) PostInline(prID int, commitSHA, repoSlug, file string, line int, body string) error {
	args := []string{
		"pr", "comment",
		strconv.Itoa(prID),
		"--repo", repoSlug,
		"--file", file,
		"--line", strconv.Itoa(line),
		"--message", body,
	}
	out, err := exec.Command(b.bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
