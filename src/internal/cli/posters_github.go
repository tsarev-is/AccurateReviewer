package cli

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
)

// githubPoster implements platformPoster for GitHub via the `gh` CLI.
// All requests go through `gh api` so auth, proxy, and base-URL handling
// stay where `gh` already manages them — the binary itself never opens
// an HTTP connection.
type githubPoster struct {
	bin string
}

func newGithubPoster(binOverride string) *githubPoster {
	if binOverride == "" {
		binOverride = "gh"
	}
	return &githubPoster{bin: binOverride}
}

func (g *githubPoster) Name() string { return "github" }

func (g *githubPoster) Preflight() (string, error) {
	resolved, err := exec.LookPath(g.bin)
	if err != nil {
		return "", fmt.Errorf("'%s' CLI not found on PATH — install https://cli.github.com/ before running post-comments", g.bin)
	}
	return resolved, nil
}

func (g *githubPoster) HeadSHA(pr int) (string, error) {
	out, err := exec.Command(g.bin, "pr", "view", strconv.Itoa(pr), "--json", "headRefOid", "-q", ".headRefOid").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	sha := strings.TrimSpace(string(out))
	if sha == "" {
		return "", fmt.Errorf("empty SHA from gh")
	}
	return sha, nil
}

// RepoSlug returns the user-provided slug or queries gh for the
// current repo's owner/name. Result is not cached — the helper runs at
// most once per `post-comments` invocation.
func (g *githubPoster) RepoSlug(override string) string {
	if override != "" {
		return override
	}
	out, err := exec.Command(g.bin, "repo", "view", "--json", "nameWithOwner", "-q", ".nameWithOwner").CombinedOutput()
	if err != nil {
		// Falling back to the literal "OWNER/REPO" placeholder is
		// intentional — the subsequent `gh api` call will fail with a
		// clear 404 message naming the bogus repo, which is more useful
		// than a generic "could not detect repo" error here.
		return "OWNER/REPO"
	}
	return strings.TrimSpace(string(out))
}

func (g *githubPoster) PostInline(pr int, commitSHA, repoSlug, file string, line int, body string) error {
	args := []string{
		"api",
		"-X", "POST",
		"repos/" + repoSlug + "/pulls/" + strconv.Itoa(pr) + "/comments",
		"-f", "body=" + body,
		"-f", "commit_id=" + commitSHA,
		"-f", "path=" + file,
		"-F", "line=" + strconv.Itoa(line),
		"-f", "side=RIGHT",
	}
	out, err := exec.Command(g.bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
