package cli

import (
	"fmt"
	"net/url"
	"os/exec"
	"strconv"
	"strings"
)

// gitlabPoster implements platformPoster for GitLab via the `glab` CLI
// (https://gitlab.com/gitlab-org/cli). Inline notes on MRs need
// position data (base/start/head SHA, new path, new line) — we POST
// directly through `glab api` rather than relying on a higher-level
// subcommand because glab's `mr note` only supports thread-level
// comments, not file-line-anchored ones.
type gitlabPoster struct {
	bin string
}

func newGitlabPoster(binOverride string) *gitlabPoster {
	if binOverride == "" {
		binOverride = "glab"
	}
	return &gitlabPoster{bin: binOverride}
}

func (g *gitlabPoster) Name() string { return "gitlab" }

func (g *gitlabPoster) Preflight() (string, error) {
	resolved, err := exec.LookPath(g.bin)
	if err != nil {
		return "", fmt.Errorf("'%s' CLI not found on PATH — install https://gitlab.com/gitlab-org/cli before running post-comments --platform gitlab", g.bin)
	}
	return resolved, nil
}

// HeadSHA returns the MR head SHA via `glab mr view <id> --output json`.
// The JSON field is `diff_refs.head_sha`; we use jq-style -O to keep the
// call shape consistent with how `gh pr view` is invoked for GitHub.
func (g *gitlabPoster) HeadSHA(mrID int) (string, error) {
	out, err := exec.Command(g.bin, "mr", "view", strconv.Itoa(mrID), "--output", "json", "-F", "diff_refs.head_sha").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	sha := strings.TrimSpace(string(out))
	sha = strings.Trim(sha, `"`) // glab's -F returns the raw JSON value, may carry quotes
	if sha == "" {
		return "", fmt.Errorf("empty SHA from glab")
	}
	return sha, nil
}

func (g *gitlabPoster) RepoSlug(override string) string {
	if override != "" {
		return override
	}
	out, err := exec.Command(g.bin, "repo", "view", "--output", "json", "-F", "path_with_namespace").CombinedOutput()
	if err != nil {
		return "OWNER/REPO"
	}
	return strings.Trim(strings.TrimSpace(string(out)), `"`)
}

// PostInline calls the GitLab Merge Request discussions endpoint with
// the position fields required for an inline note. GitLab requires
// owner/repo to be URL-encoded as a single :id path segment, so we
// percent-encode the slash.
func (g *gitlabPoster) PostInline(mrID int, commitSHA, repoSlug, file string, line int, body string) error {
	projectID := url.PathEscape(repoSlug) // "owner/repo" -> "owner%2Frepo"
	endpoint := "projects/" + projectID + "/merge_requests/" + strconv.Itoa(mrID) + "/discussions"
	args := []string{
		"api",
		"--method", "POST",
		endpoint,
		"-f", "body=" + body,
		"-f", "position[position_type]=text",
		"-f", "position[base_sha]=" + commitSHA,
		"-f", "position[start_sha]=" + commitSHA,
		"-f", "position[head_sha]=" + commitSHA,
		"-f", "position[new_path]=" + file,
		"-f", "position[new_line]=" + strconv.Itoa(line),
	}
	out, err := exec.Command(g.bin, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%v (%s)", err, strings.TrimSpace(string(out)))
	}
	return nil
}
