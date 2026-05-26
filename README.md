# AccurateReviewer

Incremental AI-powered code review focused on security, precision, and
low-noise pull-request analysis — built **BDD-first**: the Gherkin
feature files in `bdd/` are the single source of truth.

The methodology is borrowed from
[scaratec/burn-your-code](https://github.com/scaratec/burn-your-code):
the `bdd/` directory is the specification, the Go code in `src/cmd/`
and `src/internal/` is one possible implementation. Replace the
implementation with another language and as long as the BDD suite
stays green, the system is still correct.

> **Current version: 1.0.0.** v0.1, v0.2 and v1.0 milestones are
> shipped. Next planned milestone is v1.5 — see [Roadmap](#roadmap).

## What this tool does

Catch problems that traditional linters miss: security vulnerabilities,
logic bugs, architectural smell — things that require semantic
understanding of the code. It is **not** a style-checker; ESLint, Ruff,
StyleCop already handle that.

Two LLM roles, no more (extra agents start to "compete" and waste
tokens):

- **Master** — coordinates, deduplicates, enforces the token budget,
  decides when to fall back to a cheaper model.
- **Workers** — one per check: `security`, `logic`, `architecture`
  (opt-in, requires a project snapshot).

Security is the priority. Two deterministic pre-flight scanners run
before any code leaves the developer's machine — leaked tokens never
reach the LLM, and known dependency CVEs are never left to LLM
guesswork:

- **Secrets scanner** — regex rules + Shannon entropy on suspicious
  assignments.
- **Dependency CVE scanner** — shells out to `osv-scanner`
  (https://github.com/google/osv-scanner) against your manifest files.

LLM access is exclusively via subprocess (`claude -p`, `codex exec`, or
the test fake `ar-mock-cli`). The binary contains **no HTTP client for
any vendor** — neither LLM providers, nor GitHub/GitLab/Bitbucket. Auth
lives wherever the upstream CLI already keeps it.

## What's in v1.0

The full feature set shipped today:

- **CLI subcommands**: `init`, `analyze`, `review`, `scan-secrets`,
  `scan-cves`, `parse-diff`, `sanitize`, `config show`, `serve`,
  `post-comments`, `apply-fixes`, `version`.
- **Two review modes**:
  - *Incremental* (`review --from … --to …`) — comments only on
    new/changed lines; can block merge on configurable severity.
  - *Full repo* (`review --full`) — walks the working directory.
    Informational only (never blocks); secrets pre-flight is skipped
    because the run audits existing code rather than gating leaks.
- **Multi-provider LLM**: per-worker `provider`/`model` overrides
  (e.g. `security` via Claude, `logic` via Codex in the same run).
- **Budget-driven fallback**: when token use crosses
  `budget.fallback_at * max_tokens`, subsequent workers switch to
  `llm.fallback.model` (sticky for the rest of the run).
- **Prompt-injection sanitizer**: wraps every code chunk in a delimiter
  and runs neutralisation passes before reaching the LLM.
- **Cache** keyed by `(unit hash, worker, provider, model, tool
  version)` under `.review-cache/findings/` — bumping the binary
  version invalidates the cache on purpose.
- **Inline suppressions**: `// noqa-review: <reason>` on added lines
  (outside string literals) silences findings on that line.
- **Cross-location dedupe**: same-worker findings sharing
  `(worker, normalised-title, CWE)` at multiple locations collapse
  into one primary finding with an `occurrences[]` list.
- **Suggested fixes**: workers can return structured
  `Replacements[]`; `apply-fixes` materialises them as a unified diff
  piped into `git apply` (never patches files directly).
- **Language-specific worker prompts**: Go race classes, Python pickle,
  JS prototype pollution, etc. — inserted only when the project
  snapshot identifies the primary language.
- **Reporters**: structured console output, JSON (`--output`), and a
  local HTML viewer (`serve`).
- **Forge integration**: `post-comments` publishes inline review
  comments to GitHub (`gh`), GitLab (`glab`), and Bitbucket Cloud
  (`bb`). Re-runs are idempotent — already-posted comments are tracked
  in `.review-cache/posted-comments.json`.
- **CI ready**: composite `action.yml` for GitHub Actions,
  `gitlab/.gitlab-ci.yml`, `bitbucket/bitbucket-pipelines.yml`.
- **Task context**: pull the PR/issue description from `gh`, `glab`,
  Linear, or Jira CLIs (or a local text file) and feed it into worker
  prompts via `--github`, `--jira`, `--linear`, `--task-file`.

## Project layout

```
.
├── bdd/                          # ← single source of truth (Gherkin)
│   ├── pipeline/                 # review engine: analyzer, diff, review,
│   │                             #   sanitizer, secrets, llm, cache, budget,
│   │                             #   dedupe_groups, language_prompts, multi_provider
│   ├── cli/                      # CLI surface: cli, config, progress,
│   │                             #   full_mode, apply_fixes
│   ├── reports/                  # output formats: html
│   ├── integrations/             # external CLIs: action, post_comments_*,
│   │                             #   task_context, cves
│   ├── _fake_cli.py              # mock LLM used by the BDD suite
│   ├── _fake_osv.py              # mock osv-scanner used by the BDD suite
│   ├── environment.py            # behave hooks (per-scenario tempdir + mock plumbing)
│   └── steps/                    # step adapters (single dir, shared by all groups)
├── src/
│   ├── cmd/accurate-reviewer/    # main CLI entry point
│   └── internal/
│       ├── analyzer/  cache/  cli/  config/  cves/  diff/
│       ├── llm/  master/  report/  sanitizer/  secrets/
│       ├── severity/  task/  worker/
├── testdata/                     # diff fixtures + sample repos for tests
├── action.yml                    # GitHub Actions composite action
├── gitlab/.gitlab-ci.yml         # GitLab CI integration
├── bitbucket/bitbucket-pipelines.yml  # Bitbucket Pipelines integration
├── .review.yml                   # default config shipped with `init`
├── description.txt               # full multi-version roadmap
├── Makefile
└── requirements.txt              # behave + pyyaml; pinned
```

## Quickstart (developing on AccurateReviewer itself)

```bash
make setup        # python venv + pinned deps + go mod tidy
make build        # compile the Go binary into bin/
make test-all     # run the full BDD suite (depends on build)
```

Per-feature targets for tight iteration (one Makefile target per
Gherkin `@tag`):

```bash
make test-cli              make test-cache
make test-secrets          make test-full
make test-sanitizer        make test-html
make test-diff             make test-action
make test-analyzer         make test-language-prompts
make test-review           make test-budget
make test-config           make test-cves
make test-llm              make test-gitlab
make test-multi-provider   make test-bitbucket
make test-dedupe-groups    make test-apply-fixes
```

Iterating on a single scenario:

```bash
source .venv/bin/activate
behave bdd/pipeline/review.feature -n "A diff with no issues"  # by name
behave bdd/ --tags=@review --tags=~@slow                       # combine tags
```

## Using `accurate-reviewer` in your repo

### 1. One-time setup

```bash
accurate-reviewer init                 # writes .review.yml
accurate-reviewer analyze              # builds .review-cache/project.json
```

The default `.review.yml` ships with `llm.provider: claude`. Pick the
provider matching the CLI you have authenticated locally:

| `llm.provider` | Spawns                         | Auth                                  |
|----------------|--------------------------------|---------------------------------------|
| `claude`       | `claude -p` (prompt on stdin)  | Whatever `claude login` left on disk  |
| `codex`        | `codex exec` (prompt on stdin) | Whatever `codex login` left on disk   |
| `mock`         | `ar-mock-cli`                  | None — used only by the BDD harness   |

Exec parameters can be overridden via `llm.cli.{bin, args, model_flag,
timeout_seconds, pass_env}`; an empty value falls back to the per-
provider default. `llm.api_key_env` names the env var that gets passed
through to the subprocess.

### 2. Reviewing changes

```bash
# Diff between two refs (typical pre-push check)
accurate-reviewer review --from HEAD~1 --to HEAD

# Read a unified diff from a file or stdin
git diff main... | accurate-reviewer review --diff -

# Audit the whole working directory (informational, never blocks)
accurate-reviewer review --full

# Persist the report as JSON for downstream commands (post-comments, apply-fixes)
accurate-reviewer review --from main --to HEAD --output findings.json

# Re-run every worker even when the cache is fresh
accurate-reviewer review --from main --to HEAD --no-cache
```

Add task context so the LLM knows what the change is *trying* to do:

```bash
# Pull the PR description from `gh`
accurate-reviewer review --from main --to HEAD --github 1234

# Pull the issue body from `glab` / Linear / Jira (those CLIs handle auth)
accurate-reviewer review --from main --to HEAD --jira PROJ-42

# Or just point at a local file
accurate-reviewer review --from main --to HEAD --task-file ./CHANGE_INTENT.md
```

Silence a finding on a single added line:

```go
result := db.Query("SELECT * FROM u WHERE id = " + id)  // noqa-review: parameterised in caller
```

### 3. Pre-flight scanners (no LLM, run them standalone too)

```bash
# Deterministic secrets scan over a file or diff (no API key ever leaves the host)
accurate-reviewer scan-secrets path/to/file.go

# Dependency CVE scan via osv-scanner (drops anything below `high` here)
accurate-reviewer scan-cves --min-severity high --json
```

When `checks.vulnerabilities: true` and `osv-scanner` is on `PATH`, the
CVE scan also runs automatically before workers in `review`. Missing
`osv-scanner` is silent in pre-flight; the standalone `scan-cves`
subcommand fails loudly when it is not installed.

### 4. Posting findings to a forge

`post-comments` re-publishes a JSON report as inline PR/MR comments via
the platform's CLI. Platform is auto-detected from the git remote.

```bash
accurate-reviewer review --from main --to HEAD --output findings.json

# GitHub (shells out to `gh`)
accurate-reviewer post-comments --report findings.json --pr 1234

# GitLab (shells out to `glab api`)
accurate-reviewer post-comments --report findings.json --pr 7 --platform gitlab

# Bitbucket Cloud (shells out to `bb`)
accurate-reviewer post-comments --report findings.json --pr 12 --platform bitbucket

# Dry-run: log what would be posted without calling the platform CLI
accurate-reviewer post-comments --report findings.json --pr 1234 --dry-run

# Filter out noise
accurate-reviewer post-comments --report findings.json --pr 1234 --min-severity medium
```

Re-runs (force-push, retries) are safe: posted
`(platform, file, line, title)` tuples are hashed into
`.review-cache/posted-comments.json` so already-posted comments are
skipped.

### 5. Applying suggested fixes

When a worker returns a structured fix, `apply-fixes` materialises it
as a unified diff and pipes the diff to `git apply` — the binary never
patches files directly, so `git apply`'s whitespace/exec-mode checks and
clean rejection on drift apply unchanged.

```bash
accurate-reviewer review --from main --to HEAD --output findings.json

# Inspect the synthesised diff first (does NOT touch the working tree)
accurate-reviewer apply-fixes --report findings.json --dry-run

# Apply for real
accurate-reviewer apply-fixes --report findings.json
```

### 6. Local HTML viewer

```bash
accurate-reviewer review --from main --to HEAD --output findings.json
accurate-reviewer serve --report findings.json
# → open the printed http://localhost:… URL
```

### 7. CI integration

| Platform  | File                                     | What it does                                   |
|-----------|------------------------------------------|------------------------------------------------|
| GitHub    | `action.yml`                             | Composite action: build (or accept prebuilt) → `review` → `post-comments`. Auth via `gh` token already exposed by `actions/checkout`. |
| GitLab    | `gitlab/.gitlab-ci.yml`                  | Pipeline template that runs the same flow via `glab`.                                                                                |
| Bitbucket | `bitbucket/bitbucket-pipelines.yml`      | Pipelines template that runs the same flow via `bb`.                                                                                 |

## Configuration highlights

`.review.yml` shipped by `accurate-reviewer init` documents every
field; see also [`description.txt`](./description.txt) for design
rationale. The fields you are most likely to touch:

```yaml
checks:
  security: true
  logic: true
  architecture: false               # turn on after running `analyze`
  language_specific_prompts: true   # Go/Python/JS-tuned guidance per worker
  vulnerabilities: true             # pre-flight osv-scanner

severity:
  blocking: critical                # exit non-zero at or above this level
  report_minimum: low

budget:
  max_tokens: 200000
  max_usd: 1.00
  fallback_at: 0.8                  # switch to llm.fallback.model at 80% usage

llm:
  provider: claude
  master:   { model: claude-opus-4-7,        max_output_tokens: 4096 }
  worker:   { model: claude-sonnet-4-6,      max_output_tokens: 2048 }
  fallback: { model: claude-haiku-4-5-20251001, max_output_tokens: 2048 }
  # Per-worker overrides (v1.0). Each worker can pick its own provider+model.
  # Operator llm.cli.* overrides apply only to the top-level provider.
  workers:
    security: { provider: claude, model: claude-opus-4-7 }
    logic:    { provider: codex }   # model empty -> uses codex defaults

comments:
  platform: github                  # github | gitlab | bitbucket (auto-detect when unset)
  github:    { bin: gh }
  gitlab:    { bin: glab }
  bitbucket: { bin: bb }

cve:
  bin: osv-scanner
  timeout_seconds: 60
  min_severity: medium
```

## The "burn the code" exercise

Per the original methodology: delete `src/cmd/` and `src/internal/`,
point an AI agent at `bdd/`, and ask it to make the suite pass in any
language. If it passes, the implementation is correct — whatever it
looks like.

## Roadmap

See [`description.txt`](./description.txt) for the full multi-version
plan. The `VERSION` file is the source of truth for what is shipped.

- ✅ **v0.1** — CLI core, master+worker pipeline, secrets scanner,
  sanitizer, project analyzer, console reporter.
- ✅ **v0.2** — GitHub Action, inline PR comments, HTML viewer, full-
  repo mode, severity gradation, cache, inline suppressions.
- ✅ **v1.0** — multi-language guidance, CVE pre-flight, multi-provider
  with per-worker overrides, budget fallback, suggested fixes,
  cross-location dedupe groups, GitLab + Bitbucket post-comments.
- ⏭ **v1.5** — RAG over the repo, test-coverage awareness, custom team
  rules, feedback loop on findings, quality metrics.

## Licence

See `LICENSE`.
