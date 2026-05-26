"""Steps specific to the @apply-fixes feature.

Three classes of helpers here:
  - JSON-report fixtures with a synthetic `fix` payload (the existing
    action_steps.py fixtures never set the fix field);
  - a minimal git-repo + tracked-file pair, because `git apply` only runs
    from inside a repo and only patches index/working-tree files cleanly
    when they're tracked;
  - a JSON-path absence assertion (`does NOT contain the key ...`)
    counterpart to the existing `the JSON file ... contains` step.
"""

import json
import subprocess

from behave import given, then

from environment import REPO_ROOT  # noqa: F401 — kept for symmetry with sister steps


@given('a JSON report at "{path}" with one fix replacing line {ln:d} of "{file}" with "{new_text}"')
def step_json_report_with_fix(context, path, ln, file, new_text):
    # behave's regex matchers don't unescape \n, so the feature passes a
    # literal "\n" that we expand here. This keeps the Gherkin readable
    # (one line per replacement) without an ad-hoc doc-string parameter.
    new_text = new_text.replace("\\n", "\n")
    report = {
        "schema_version": 1,
        "blocking_severity": "critical",
        "reviewed": [file],
        "findings": [
            {
                "file": file,
                "line": ln,
                "severity": "critical",
                "title": "demo finding",
                "why": "feature exercise",
                "cwe": "CWE-89",
                "worker": "security",
                "fix": {
                    "description": "test fix",
                    "replacements": [
                        {
                            "file": file,
                            "start_line": ln,
                            "end_line": ln,
                            "new_text": new_text,
                        }
                    ],
                },
            }
        ],
    }
    (context.workdir / path).write_text(json.dumps(report), encoding="utf-8")


@given('a git repo at the working directory')
def step_git_repo_here(context):
    # `git apply` runs from inside a repo. We initialise one and commit a
    # baseline so subsequent `git apply` calls see a clean index. Author
    # identity is set locally so the commit doesn't fail on machines
    # without a global git config (CI especially).
    wd = context.workdir
    subprocess.run(["git", "init", "--quiet"], cwd=wd, check=True)
    subprocess.run(["git", "config", "user.email", "test@example.invalid"], cwd=wd, check=True)
    subprocess.run(["git", "config", "user.name", "Test"], cwd=wd, check=True)


@given('a tracked file "{path}" with content')
def step_tracked_file(context, path):
    full = context.workdir / path
    full.parent.mkdir(parents=True, exist_ok=True)
    # behave strips the trailing newline of a doc-string. `git apply`
    # matches the pre-image byte-for-byte so we need the file to end with
    # the same newline the synthesised patch will emit; restore it here.
    body = context.text
    if not body.endswith("\n"):
        body += "\n"
    full.write_text(body, encoding="utf-8")
    subprocess.run(["git", "add", path], cwd=context.workdir, check=True)
    subprocess.run(
        ["git", "commit", "--quiet", "-m", f"add {path}"],
        cwd=context.workdir,
        check=True,
    )


@then('the JSON file "{path}" does NOT contain the key "{json_path}"')
def step_json_key_absent(context, path, json_path):
    full = context.workdir / path
    data = json.loads(full.read_text(encoding="utf-8"))
    try:
        _walk_json_path(data, json_path)
    except (KeyError, IndexError, TypeError):
        return  # the key is absent — that's what we want
    raise AssertionError(f"{path}: expected key {json_path!r} to be absent")


def _walk_json_path(data, path):
    import re
    cur = data
    parts = re.split(r"\.|(\[\d+\])", path)
    parts = [p for p in parts if p]
    for p in parts:
        if p.startswith("["):
            cur = cur[int(p[1:-1])]
        else:
            cur = cur[p]
    return cur
