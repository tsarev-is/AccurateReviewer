"""Behave hooks for the AccurateReviewer BDD suite.

Each scenario runs in a fresh temp dir, with a fake LLM CLI placed in a
`bin/` subdir of that tempdir. The fake CLI (`bdd/_fake_cli.py`) is exposed
under three names — `claude`, `codex`, `ar-mock-cli` — so any of the three
production-valid provider settings exercises the same controllable stand-in.
"""

import json
import os
import shutil
import subprocess
import tempfile
import time
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent
BIN_DIR = REPO_ROOT / "bin"
CLI_BIN = BIN_DIR / "accurate-reviewer"
FAKE_CLI_SRC = Path(__file__).resolve().parent / "_fake_cli.py"
TASK_FAKE_CLI_SRC = Path(__file__).resolve().parent / "_task_fake_cli.py"
OSV_FAKE_SRC = Path(__file__).resolve().parent / "_fake_osv.py"

# Names under which the fake is exposed in PATH. Any provider the feature
# files name (`claude`, `codex`, `mock`) finds an executable to spawn.
FAKE_NAMES = ("ar-mock-cli", "claude", "codex")

# Task-fetch fakes: the names the integrations CLI commands would resolve
# to in production. Shadowing real binaries on the test workdir's PATH
# lets scenarios script their output without networking.
#
# - gh/jira/linear: task-context trackers.
# - glab/bb: post-comments platform CLIs (GitLab, Bitbucket).
# All routes go through the same `_task_fake_cli.py` which records argv
# and emits scripted output keyed by argv[0] basename.
TASK_FAKE_NAMES = ("gh", "jira", "linear", "glab", "bb")


def before_all(context):
    if not CLI_BIN.exists():
        raise RuntimeError(f"binary not built — run `make build` first. Missing: {CLI_BIN}")
    if not FAKE_CLI_SRC.exists():
        raise RuntimeError(f"missing fake CLI source: {FAKE_CLI_SRC}")
    if not TASK_FAKE_CLI_SRC.exists():
        raise RuntimeError(f"missing task fake CLI source: {TASK_FAKE_CLI_SRC}")
    if not OSV_FAKE_SRC.exists():
        raise RuntimeError(f"missing osv-scanner fake source: {OSV_FAKE_SRC}")


def before_scenario(context, scenario):
    context.workdir = Path(tempfile.mkdtemp(prefix="ar-bdd-"))
    context.repo_root = REPO_ROOT

    # Copy testdata/ into the scenario's workdir so features can reference
    # fixtures verbatim while side-effect writes stay scoped to the scenario.
    testdata_src = REPO_ROOT / "testdata"
    if testdata_src.exists():
        shutil.copytree(testdata_src, context.workdir / "testdata", symlinks=False)

    # Place the fake CLI under every supported name so any provider works.
    fake_bin = context.workdir / "bin"
    fake_bin.mkdir(parents=True, exist_ok=True)
    for name in FAKE_NAMES:
        dst = fake_bin / name
        shutil.copy2(FAKE_CLI_SRC, dst)
        dst.chmod(0o755)

    # Task-fetch fakes shadow real `gh` / `jira` on PATH so scenarios can
    # script integration output without spawning the real CLIs.
    for name in TASK_FAKE_NAMES:
        dst = fake_bin / name
        shutil.copy2(TASK_FAKE_CLI_SRC, dst)
        dst.chmod(0o755)

    # osv-scanner fake — same trick. Scenarios that exercise the CVE
    # pre-flight set $ACCURATE_REVIEWER_OSV_SCRIPT (a path holding the
    # JSON payload the fake should echo) and $ACCURATE_REVIEWER_OSV_EXIT.
    # Scenarios that DON'T touch CVEs still get the fake installed; with
    # the default empty results it returns "{results: []}" → no findings.
    osv_dst = fake_bin / "osv-scanner"
    shutil.copy2(OSV_FAKE_SRC, osv_dst)
    osv_dst.chmod(0o755)

    # Per-scenario script + prompt-log files. The fake CLI reads these
    # paths from the environment; helpers below mutate them.
    context.mock_script_path = context.workdir / ".ar-mock-script.json"
    context.mock_prompt_log = context.workdir / ".ar-mock-prompts.jsonl"
    context.mock_script_path.write_text("[]", encoding="utf-8")
    context.mock_prompt_log.write_text("", encoding="utf-8")

    # Per-scenario script + invocation-log for the task-fetch fakes.
    context.task_fake_script = context.workdir / ".ar-task-fake-script.json"
    context.task_fake_invocations = context.workdir / ".ar-task-fake-invocations.jsonl"
    context.task_fake_script.write_text("{}", encoding="utf-8")
    context.task_fake_invocations.write_text("", encoding="utf-8")

    # osv-scanner fake state. Default script: empty results.
    context.osv_script = context.workdir / ".ar-osv-script.json"
    context.osv_invocations = context.workdir / ".ar-osv-invocations.jsonl"
    context.osv_script.write_text('{"results": []}', encoding="utf-8")
    context.osv_invocations.write_text("", encoding="utf-8")
    context.osv_exit = "0"

    context.fake_bin_dir = fake_bin
    context.last_run = None
    context.last_diff = None
    context.last_units = None
    context.last_findings = None
    context.extra_env = {}


def after_scenario(context, scenario):
    # Any backgrounded `serve` processes must die before we wipe the
    # workdir, otherwise the child keeps a handle on a vanished file
    # and may emit confusing errors on shutdown. The hook lives in
    # html_steps.py so the cleanup logic stays next to the step that
    # spawned the process. Only ImportError is swallowed here — the
    # cleanup itself runs unguarded so real bugs surface instead of
    # being silently dropped.
    try:
        from steps.html_steps import after_scenario_hook
    except ImportError:
        after_scenario_hook = None
    if after_scenario_hook is not None:
        after_scenario_hook(context, scenario)
    if getattr(context, "workdir", None) and context.workdir.exists():
        shutil.rmtree(context.workdir, ignore_errors=True)


def run_cli(context, argv, *, stdin=None, cwd=None, extra_env=None):
    """Run the accurate-reviewer binary and capture everything."""
    env = os.environ.copy()
    env["PATH"] = f"{context.fake_bin_dir}{os.pathsep}{env.get('PATH', '')}"
    env["ACCURATE_REVIEWER_MOCK_SCRIPT"] = str(context.mock_script_path)
    env["ACCURATE_REVIEWER_MOCK_PROMPT_LOG"] = str(context.mock_prompt_log)
    env["ACCURATE_REVIEWER_TASK_FAKE_SCRIPT"] = str(context.task_fake_script)
    env["ACCURATE_REVIEWER_TASK_FAKE_INVOCATIONS"] = str(context.task_fake_invocations)
    env["ACCURATE_REVIEWER_OSV_SCRIPT"] = str(context.osv_script)
    env["ACCURATE_REVIEWER_OSV_INVOCATIONS"] = str(context.osv_invocations)
    env["ACCURATE_REVIEWER_OSV_EXIT"] = getattr(context, "osv_exit", "0")
    if getattr(context, "extra_env", None):
        env.update(context.extra_env)
    if extra_env:
        env.update(extra_env)
    cwd = cwd or context.workdir

    if isinstance(argv, str):
        parts = argv.split()
        assert parts[0] == "accurate-reviewer"
        argv = parts[1:]

    t0 = time.time()
    proc = subprocess.run(
        [str(CLI_BIN), *argv],
        input=stdin,
        capture_output=True,
        cwd=str(cwd),
        env=env,
    )
    elapsed = time.time() - t0
    context.last_run = {
        "argv": argv,
        "returncode": proc.returncode,
        "stdout": proc.stdout.decode("utf-8", errors="replace"),
        "stderr": proc.stderr.decode("utf-8", errors="replace"),
        "elapsed": elapsed,
        "cwd": str(cwd),
    }
    return context.last_run


def mock_script(context, entries):
    """Write the per-worker scripted responses the fake CLI will return."""
    context.mock_script_path.write_text(json.dumps(entries), encoding="utf-8")


def mock_reset(context):
    """Clear the script and the prompt log between scripted blocks."""
    context.mock_script_path.write_text("[]", encoding="utf-8")
    context.mock_prompt_log.write_text("", encoding="utf-8")


def mock_prompts(context):
    """Return every prompt the fake CLI has seen, in call order."""
    raw = context.mock_prompt_log.read_text(encoding="utf-8").strip()
    if not raw:
        return []
    return [json.loads(line) for line in raw.splitlines() if line.strip()]
