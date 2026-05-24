"""Steps specific to the GitHub Action / post-comments feature.

The fake `gh` binary that environment.py drops into workdir/bin/ is what we
exercise. Removing it (or shadowing PATH so it cannot be found) is how we
test the "preflight: gh missing" branch — we cannot rely on the host's
actual PATH because most dev machines have a real `gh` installed and the
scenario must be deterministic everywhere.
"""

import json
import os
from pathlib import Path

from behave import given, when, then

from environment import CLI_BIN, REPO_ROOT, run_cli


@given('the gh CLI is removed from PATH')
def step_remove_gh(context):
    # Delete the fake `gh` from the per-scenario PATH directory. Then run
    # the CLI with a minimal PATH that contains only that directory, so
    # the binary really cannot find `gh` regardless of how the host is
    # configured.
    fake_gh = context.fake_bin_dir / "gh"
    if fake_gh.exists():
        fake_gh.unlink()
    context.extra_env = getattr(context, "extra_env", {})
    context.extra_env["PATH"] = str(context.fake_bin_dir)


@given('a JSON report at "{path}" with one critical finding on "{loc}"')
def step_json_one_critical(context, path, loc):
    file, line = loc.split(":")
    report = {
        "schema_version": 1,
        "blocking_severity": "critical",
        "reviewed": [file],
        "findings": [
            {
                "file": file,
                "line": int(line),
                "severity": "critical",
                "title": "SQL injection",
                "why": "concatenated input",
                "cwe": "CWE-89",
                "worker": "security",
            }
        ],
    }
    (context.workdir / path).write_text(json.dumps(report), encoding="utf-8")


@given('a JSON report at "{path}" with one low finding on "{loc1}" and one critical finding on "{loc2}"')
def step_json_low_and_critical(context, path, loc1, loc2):
    f1, l1 = loc1.split(":")
    f2, l2 = loc2.split(":")
    report = {
        "schema_version": 1,
        "blocking_severity": "critical",
        "reviewed": [f1, f2],
        "findings": [
            {"file": f1, "line": int(l1), "severity": "low",
             "title": "Style nit", "why": "trivial", "worker": "logic"},
            {"file": f2, "line": int(l2), "severity": "critical",
             "title": "SQL injection", "why": "concatenated input",
             "cwe": "CWE-89", "worker": "security"},
        ],
    }
    (context.workdir / path).write_text(json.dumps(report), encoding="utf-8")


@given('I run "{cmdline}"')
def step_given_i_run(context, cmdline):
    # Promoted to @given so a scenario can chain "And I run …" inside the
    # Given block (post-comments has a setup step that runs the binary
    # once to populate the dedupe cache before the assertion-bearing run).
    extra_env = getattr(context, "extra_env", None)
    run_cli(context, cmdline, extra_env=extra_env)


@then('the file "{path}" exists at the repo root')
def step_repo_root_file_exists(context, path):
    full = REPO_ROOT / path
    assert full.exists(), f"missing repo-root file: {full}"


@then('the repo-root file "{path}" contains "{needle}"')
def step_repo_root_file_contains(context, path, needle):
    content = (REPO_ROOT / path).read_text(encoding="utf-8")
    assert needle in content, f"{path} missing {needle!r}\n---\n{content}"


@then('no file "{path}" exists in the working directory')
def step_no_file(context, path):
    full = context.workdir / path
    assert not full.exists(), f"unexpected file at {full}"
