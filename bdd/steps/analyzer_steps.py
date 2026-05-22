"""Steps specific to the project-startup analysis feature.

All sample-project fixtures live inside the scenario's temporary workdir.
We never write into the actual repo's testdata/ — that directory holds
checked-in fixtures and is only copied (read-only) into each workdir by
the before_scenario hook.
"""

import time
from pathlib import Path

from behave import given, when, then

from environment import run_cli


def _materialise(context, rel):
    """Resolve `rel` to a path inside the scenario workdir, creating it
    fresh. Anything previously copied from REPO_ROOT/testdata/{rel} stays
    in place — callers add to it; the wipe is only needed when there is no
    `rel` (the unnamed 'sample' subdir)."""
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    return base


@given('a sample project at "{rel}" containing')
def step_sample_project_rel(context, rel):
    base = _materialise(context, rel)
    _write_table(context, base)
    context.sample_project_dir = base


@given('a sample project containing')
def step_sample_project_unnamed(context):
    base = context.workdir / "sample"
    base.mkdir(parents=True, exist_ok=True)
    _write_table(context, base)
    context.sample_project_dir = base


def _write_table(context, base: Path) -> None:
    for row in context.table:
        path = base / row["path"]
        path.parent.mkdir(parents=True, exist_ok=True)
        content = row["content"].replace("\\n", "\n")
        if content == "(empty)":
            path.touch()
        else:
            path.write_text(content, encoding="utf-8")


@given('a sample Go project at "{rel}"')
def step_sample_go(context, rel):
    base = _materialise(context, rel)
    (base / "go.mod").write_text("module example.com/sample\n\ngo 1.22\n", encoding="utf-8")
    (base / "main.go").write_text("package main\nfunc main() {}\n", encoding="utf-8")
    context.sample_project_dir = base


@given('a sample Go project at "{rel}" with an existing snapshot')
def step_sample_go_with_snapshot(context, rel):
    step_sample_go(context, rel)
    run_cli(context, "accurate-reviewer analyze", cwd=context.sample_project_dir)


@given('I have already run "{cmdline}" inside it')
def step_already_ran(context, cmdline):
    run_cli(context, cmdline, cwd=context.sample_project_dir)


@given('I record the file "{path}"\'s mtime as T0')
def step_record_mtime(context, path):
    full = context.sample_project_dir / path
    context.mtime_t0 = full.stat().st_mtime


@when('I run "{cmdline}" again inside it without changing any source file')
def step_run_again(context, cmdline):
    # ensure the wall-clock has moved on so a re-write would register a different mtime
    time.sleep(0.05)
    run_cli(context, cmdline, cwd=context.sample_project_dir)


@when('I run "{cmdline}" again inside it')
def step_run_again_simple(context, cmdline):
    run_cli(context, cmdline, cwd=context.sample_project_dir)


@then('the mtime of "{path}" is still T0')
def step_mtime_unchanged(context, path):
    full = context.sample_project_dir / path
    assert full.stat().st_mtime == context.mtime_t0, "snapshot was rewritten unexpectedly"


@then('the file "{path}" was rewritten')
def step_file_rewritten(context, path):
    full = context.sample_project_dir / path
    if hasattr(context, "mtime_t0"):
        assert full.stat().st_mtime > context.mtime_t0
    else:
        assert full.exists()
