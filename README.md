# AccurateReviewer

Incremental AI-powered code review focused on security, precision, and
low-noise pull-request analysis — built **BDD-first**: Gherkin feature files
in `bdd/` are the single source of truth.

The methodology is borrowed from
[scaratec/burn-your-code](https://github.com/scaratec/burn-your-code):
the `bdd/` directory is the specification, the Go code in `cmd/` and
`internal/` is one possible implementation. Replace the implementation
with another language and as long as the BDD suite stays green, the
system is still correct.

## What this tool does

Catch problems traditional linters miss: security vulnerabilities,
logic bugs, architectural smell — things that require semantic understanding
of the code. It is **not** a style-checker; ESLint, Ruff, StyleCop already
handle that.

Two LLM roles, no more (more agents start to "compete" and waste tokens):

- **Master** — coordinates, deduplicates, enforces the token budget.
- **Workers** — one per check: `security`, `logic`, (and `architecture` from v1.0+).

Security is the priority. A deterministic secrets pre-flight runs before
any code leaves the developer's machine — leaked tokens never reach the LLM.

## MVP v0.1 scope (this commit)

- CLI: `init`, `analyze`, `review`, plus the helper commands
  `scan-secrets`, `parse-diff`, `sanitize`, `config show`.
- Project startup analysis → `.review-cache/project.json`.
- Diff parsing with hunk-level context, exclude globs, renames, deletions, binaries.
- Master + worker pipeline with parallel worker execution,
  cross-worker deduplication, token-budget enforcement.
- Prompt-injection sanitizer (delimiter wrap + neutralisation passes).
- Secrets scanner (regex rules + shannon entropy on sensitive assignments).
- `.review.yml` parser/validator.
- Console reporter.
- LLM integration via local CLI subprocess (`claude`, `codex`, or the
  test fake `ar-mock-cli`) — no HTTP client compiled in.

## Project layout

```
.
├── bdd/                    # ← single source of truth (Gherkin)
│   ├── cli.feature
│   ├── secrets.feature
│   ├── sanitizer.feature
│   ├── diff.feature
│   ├── analyzer.feature
│   ├── review.feature
│   ├── config.feature
│   ├── environment.py      # behave hooks: per-scenario temp dir + mock LLM
│   └── steps/              # step adapters that drive the binary as a subprocess
├── cmd/
│   └── accurate-reviewer/  # main CLI (only one binary; LLM access is via subprocess)
├── internal/
│   ├── analyzer/  config/  diff/  llm/  master/
│   ├── report/    sanitizer/  secrets/  worker/  cli/
├── testdata/
│   ├── diffs/              # diff fixtures referenced from features
│   └── repos/              # tiny sample projects for the analyzer feature
├── .review.yml             # default config shipped with `init`
├── Makefile
└── requirements.txt        # behave + pyyaml; pinned
```

## Quickstart

```bash
make setup        # python venv + pinned deps + go mod tidy
make build        # compile the Go binary into bin/
make test-all     # run the full BDD suite
```

Per-feature targets exist for tight iteration:

```bash
make test-cli
make test-secrets
make test-sanitizer
make test-diff
make test-analyzer
make test-review
make test-config
```

## Running it for real

In an existing repo:

```bash
accurate-reviewer init                 # writes .review.yml
accurate-reviewer analyze              # builds the project snapshot
accurate-reviewer review --from HEAD~1 --to HEAD
```

The default `.review.yml` ships with `llm.provider: claude`. The tool only
talks to models via a local CLI subprocess — pick the provider matching the
CLI you already have authenticated:

| `llm.provider` | Spawns                       | Auth                                  |
|----------------|------------------------------|---------------------------------------|
| `claude`       | `claude -p` (prompt on stdin) | Whatever `claude login` left on disk  |
| `codex`        | `codex exec` (prompt on stdin)| Whatever `codex login` left on disk   |
| `mock`         | `ar-mock-cli`                 | None — used only by the BDD harness   |

The exec parameters can be overridden via `llm.cli.{bin,args,model_flag,
timeout_seconds,pass_env}`; an empty value falls back to the per-provider
default. `api_key_env` names the env var that gets passed through to the
subprocess — `claude`/`codex` read it themselves.

## The "burn the code" exercise

Per the original methodology: delete `cmd/` and `internal/`, point an AI
agent at `bdd/`, ask it to make the suite pass in any language. If it
passes, the implementation is correct — whatever it looks like.

## Roadmap

See [`description.txt`](./description.txt) for the full plan. Next up after
MVP v0.1:

- **v0.2** — GitHub Action, inline PR comments, HTML web viewer, full-repo
  mode, severity gradation, cache, inline suppressions.
- **v1.0** — multi-language, CVE lookup, multi-provider, suggested fixes,
  duplicate grouping, GitLab/Bitbucket.
- **v1.5** — RAG over the repo, test-coverage awareness, custom team rules,
  feedback loop on findings.

## Licence

See `LICENSE`.
