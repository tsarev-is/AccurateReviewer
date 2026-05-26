package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/scaratec/accurate-reviewer/internal/config"
	"github.com/scaratec/accurate-reviewer/internal/report"
	"github.com/scaratec/accurate-reviewer/internal/severity"
	"github.com/scaratec/accurate-reviewer/internal/worker"
)

// platformPoster is the per-platform abstraction post-comments dispatches
// through. Each implementation shells out to a third-party CLI that
// already handles auth — the binary never opens an HTTP connection.
type platformPoster interface {
	Name() string
	// Preflight resolves the binary on PATH; returning a non-empty path
	// is purely informational (logged), the implementation calls the bin
	// directly afterwards. Error message must name the missing binary
	// and link to install docs.
	Preflight() (string, error)
	HeadSHA(pr int) (string, error)
	RepoSlug(override string) string
	PostInline(pr int, commitSHA, repoSlug, file string, line int, body string) error
}

func newPostCommentsCmd() *cobra.Command {
	var (
		reportPath string
		prNumber   int
		commitSHA  string
		repoSlug   string
		dryRun     bool
		minSev     string
		platform   string
	)
	cmd := &cobra.Command{
		Use:   "post-comments",
		Short: "Post review findings as inline comments on a PR/MR via the platform's CLI",
		Long: `Read a JSON review report (produced by 'review --output X.json') and
publish each finding as an inline comment using the platform's CLI
(--platform github|gitlab|bitbucket). The binary never speaks to any
forge directly — auth, proxies, and base URL stay where the platform's
CLI already manages them.

Re-running against the same findings is safe: each posted (platform,
file, line, title) tuple is hashed and stored in
.review-cache/posted-comments.json so the second run skips the
already-posted comments. This is what keeps force-push from spamming
the PR/MR.`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			logf := func(format string, args ...any) {
				fmt.Fprintf(cmd.ErrOrStderr(), "[post-comments] "+format+"\n", args...)
			}

			// Config is optional — running without .review.yml is fine,
			// every flag the command needs is settable on the CLI.
			cfg, _ := config.Load(".review.yml")
			resolvedPlatform := resolvePlatform(platform, cfg)

			poster, err := newPoster(resolvedPlatform, cfg)
			if err != nil {
				return Exit(2, "%v", err)
			}
			resolvedBin, err := poster.Preflight()
			if err != nil {
				return Exit(2, "%v", err)
			}
			logf("platform=%s using %s", poster.Name(), resolvedBin)

			if prNumber <= 0 {
				return Exit(2, "--pr is required and must be > 0")
			}
			if reportPath == "" {
				return Exit(2, "--report is required")
			}
			data, err := os.ReadFile(reportPath)
			if err != nil {
				return Exit(1, "read %s: %v", reportPath, err)
			}
			var rep report.JSONReport
			if err := json.Unmarshal(data, &rep); err != nil {
				return Exit(1, "parse %s: %v", reportPath, err)
			}
			if rep.SchemaVersion != 0 && rep.SchemaVersion != report.JSONSchemaVersion {
				return Exit(1, "report schema version %d not supported by this binary (expected %d)", rep.SchemaVersion, report.JSONSchemaVersion)
			}

			// Resolve the commit SHA we want to anchor inline comments to.
			// The user can pass it explicitly (CI knows it) or we ask the
			// platform's CLI for the PR/MR head SHA.
			if commitSHA == "" {
				commitSHA, err = poster.HeadSHA(prNumber)
				if err != nil {
					return Exit(1, "resolve PR/MR head SHA: %v", err)
				}
				logf("resolved PR/MR #%d head sha: %s", prNumber, commitSHA)
			}
			finalRepo := poster.RepoSlug(repoSlug)

			// Filter by --min-severity (default "info" = everything). The
			// pre-allocated slice deliberately does NOT alias rep.Findings —
			// the in-place [:0] idiom is correct here but easy to break in a
			// later refactor, so the explicit make is worth a few bytes.
			eligible := make([]worker.Finding, 0, len(rep.Findings))
			for _, f := range rep.Findings {
				if severity.AtLeast(f.Severity, minSev) {
					eligible = append(eligible, f)
				}
			}

			// Stable order so the dedupe file is human-friendly and so
			// the BDD assertions land deterministically.
			sort.SliceStable(eligible, func(i, j int) bool {
				if eligible[i].File != eligible[j].File {
					return eligible[i].File < eligible[j].File
				}
				return eligible[i].Line < eligible[j].Line
			})

			posted, err := loadPostedSet()
			if err != nil {
				logf("warning: could not read posted-comments cache: %v", err)
				posted = map[string]bool{}
			}

			var (
				newCount   int
				skippedDup int
				failures   []error
			)
			for _, f := range eligible {
				// A grouped finding (Occurrences populated) fans out to
				// one inline comment per location. Each comment body
				// references the sibling locations so a reviewer reading
				// any single comment can see the full footprint of the
				// issue. Dedupe still happens per (platform, pr, sha,
				// file, line, title) — siblings of one finding do NOT
				// stomp each other, and a future re-run won't re-post.
				for _, job := range expandFindingForPosting(f) {
					key := commentKey(poster.Name(), prNumber, commitSHA, job.file, job.line, f.Title)
					if posted[key] {
						skippedDup++
						continue
					}
					if dryRun {
						logf("dry-run: would post on %s:%d (%s)", job.file, job.line, f.Severity)
						newCount++
						continue
					}
					if err := poster.PostInline(prNumber, commitSHA, finalRepo, job.file, job.line, job.body); err != nil {
						failures = append(failures, fmt.Errorf("post on %s:%d: %v", job.file, job.line, err))
						continue
					}
					posted[key] = true
					newCount++
					logf("posted on %s:%d (%s)", job.file, job.line, f.Severity)
				}
			}

			if !dryRun {
				if err := savePostedSet(posted); err != nil {
					logf("warning: could not persist posted-comments cache: %v", err)
				}
			}

			// Surface failures BEFORE the success summary so CI tooling
			// that greps stdout for "posted N new comment" does not
			// declare success while comments silently failed to post.
			if len(failures) > 0 {
				for _, e := range failures {
					logf("error: %v", e)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "posted %d new comment(s), skipped %d already-posted, %d failed\n",
					newCount, skippedDup, len(failures))
				return Exit(1, "%d comment(s) failed to post", len(failures))
			}
			fmt.Fprintf(cmd.OutOrStdout(), "posted %d new comment(s), skipped %d already-posted\n", newCount, skippedDup)
			return nil
		},
	}
	cmd.Flags().StringVarP(&reportPath, "report", "r", "", "path to the JSON report from `review --output X.json`")
	cmd.Flags().IntVar(&prNumber, "pr", 0, "PR/MR number to post comments on")
	cmd.Flags().StringVar(&commitSHA, "commit-sha", "", "commit SHA the comments anchor to (default: PR/MR head)")
	cmd.Flags().StringVar(&repoSlug, "repo", "", "owner/repo (default: inferred from the current directory via the platform CLI)")
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "log what would be posted without calling the platform CLI")
	cmd.Flags().StringVar(&minSev, "min-severity", "info", "skip findings below this severity (info|low|medium|high|critical)")
	cmd.Flags().StringVar(&platform, "platform", "", "github | gitlab | bitbucket (default: auto-detect from git remote, else github)")
	return cmd
}

// resolvePlatform picks the effective platform name. Explicit --platform
// wins; absent that, comments.platform from .review.yml; absent that,
// auto-detect from the `origin` git remote URL host. If everything
// fails, default to github — the historical behaviour before this flag
// existed, so a single-flag rollback is always possible.
func resolvePlatform(flagValue string, cfg *config.Config) string {
	if flagValue != "" {
		return flagValue
	}
	if cfg != nil && cfg.Comments.Platform != "" {
		return cfg.Comments.Platform
	}
	if detected := detectPlatformFromGitRemote(); detected != "" {
		return detected
	}
	return "github"
}

func detectPlatformFromGitRemote() string {
	out, err := exec.Command("git", "remote", "get-url", "origin").CombinedOutput()
	if err != nil {
		return ""
	}
	url := strings.ToLower(strings.TrimSpace(string(out)))
	switch {
	case strings.Contains(url, "gitlab"):
		return "gitlab"
	case strings.Contains(url, "bitbucket"):
		return "bitbucket"
	case strings.Contains(url, "github"):
		return "github"
	default:
		return ""
	}
}

func newPoster(platform string, cfg *config.Config) (platformPoster, error) {
	var ghBin, glBin, bbBin string
	if cfg != nil {
		ghBin = cfg.Comments.GitHub.Bin
		glBin = cfg.Comments.GitLab.Bin
		bbBin = cfg.Comments.Bitbucket.Bin
	}
	switch platform {
	case "github":
		return newGithubPoster(ghBin), nil
	case "gitlab":
		return newGitlabPoster(glBin), nil
	case "bitbucket":
		return newBitbucketPoster(bbBin), nil
	default:
		return nil, fmt.Errorf("--platform: %q is not one of [github, gitlab, bitbucket]", platform)
	}
}

// renderCommentBody composes the markdown body posted to the forge. We
// lead with the severity badge and CWE so reviewers can triage at a
// glance; the "why" prose follows on the next line. The "[posted by
// accurate-reviewer]" trailer is what makes a future search-and-replace
// dedupe possible if the per-PR cache file is ever lost.
//
// `siblings` are the OTHER locations of a grouped finding — when empty
// the body is identical to the pre-grouping shape. When non-empty, a
// "Also reported at: ..." line is inserted above the trailer.
func renderCommentBody(f worker.Finding, siblings []worker.Location) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**[%s] %s**", strings.ToUpper(f.Severity), f.Title)
	if f.CWE != "" {
		fmt.Fprintf(&b, " · `%s`", f.CWE)
	}
	b.WriteString("\n\n")
	b.WriteString(f.Why)
	if len(siblings) > 0 {
		parts := make([]string, 0, len(siblings))
		for _, s := range siblings {
			parts = append(parts, fmt.Sprintf("`%s:%d`", s.File, s.Line))
		}
		fmt.Fprintf(&b, "\n\nAlso reported at: %s", strings.Join(parts, ", "))
	}
	b.WriteString("\n\n_posted by accurate-reviewer_")
	return b.String()
}

// commentJob is one (file, line, body) tuple ready to hand to the poster.
// Findings without occurrences produce exactly one job; grouped findings
// produce one job per location, each carrying a body that references the
// other locations as "Also reported at: ...".
type commentJob struct {
	file string
	line int
	body string
}

func expandFindingForPosting(f worker.Finding) []commentJob {
	if len(f.Occurrences) == 0 {
		return []commentJob{{file: f.File, line: f.Line, body: renderCommentBody(f, nil)}}
	}
	locs := make([]worker.Location, 0, 1+len(f.Occurrences))
	locs = append(locs, worker.Location{File: f.File, Line: f.Line})
	locs = append(locs, f.Occurrences...)
	jobs := make([]commentJob, 0, len(locs))
	for i, here := range locs {
		others := make([]worker.Location, 0, len(locs)-1)
		for j, o := range locs {
			if i == j {
				continue
			}
			others = append(others, o)
		}
		jobs = append(jobs, commentJob{
			file: here.File,
			line: here.Line,
			body: renderCommentBody(f, others),
		})
	}
	return jobs
}

// commentKey is the deduplication key. Including the platform means the
// same finding on a "PR #1" on GitHub and an "MR #1" on GitLab do not
// stomp each other in the cache. Including the commit SHA means a
// force-push that rewrites the flagged line re-posts the comment
// against the new commit instead of being silently swallowed — the old
// comment is anchored to a now-stale SHA and the developer needs to see
// the finding against the live code.
func commentKey(platform string, pr int, commitSHA, file string, line int, title string) string {
	h := sha256.New()
	fmt.Fprintf(h, "platform=%s|pr=%d|sha=%s|file=%s|line=%d|title=%s",
		platform, pr, commitSHA, file, line, strings.ToLower(strings.TrimSpace(title)))
	return hex.EncodeToString(h.Sum(nil))
}

const postedCachePath = ".review-cache/posted-comments.json"

func loadPostedSet() (map[string]bool, error) {
	data, err := os.ReadFile(postedCachePath)
	if err != nil {
		if os.IsNotExist(err) {
			return map[string]bool{}, nil
		}
		return nil, err
	}
	var keys []string
	if err := json.Unmarshal(data, &keys); err != nil {
		return map[string]bool{}, nil
	}
	out := make(map[string]bool, len(keys))
	for _, k := range keys {
		out[k] = true
	}
	return out, nil
}

func savePostedSet(set map[string]bool) error {
	dir := filepath.Dir(postedCachePath)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	keys := make([]string, 0, len(set))
	for k := range set {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	data, err := json.MarshalIndent(keys, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(postedCachePath, data, 0o644)
}
