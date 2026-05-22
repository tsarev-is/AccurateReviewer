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

# Names under which the fake is exposed in PATH. Any provider the feature
# files name (`claude`, `codex`, `mock`) finds an executable to spawn.
FAKE_NAMES = ("ar-mock-cli", "claude", "codex")


def before_all(context):
    if not CLI_BIN.exists():
        raise RuntimeError(f"binary not built — run `make build` first. Missing: {CLI_BIN}")
    if not FAKE_CLI_SRC.exists():
        raise RuntimeError(f"missing fake CLI source: {FAKE_CLI_SRC}")


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

    # Per-scenario script + prompt-log files. The fake CLI reads these
    # paths from the environment; helpers below mutate them.
    context.mock_script_path = context.workdir / ".ar-mock-script.json"
    context.mock_prompt_log = context.workdir / ".ar-mock-prompts.jsonl"
    context.mock_script_path.write_text("[]", encoding="utf-8")
    context.mock_prompt_log.write_text("", encoding="utf-8")

    context.fake_bin_dir = fake_bin
    context.last_run = None
    context.last_diff = None
    context.last_units = None
    context.last_findings = None
    context.extra_env = {}


def after_scenario(context, scenario):
    if getattr(context, "workdir", None) and context.workdir.exists():
        shutil.rmtree(context.workdir, ignore_errors=True)


def run_cli(context, argv, *, stdin=None, cwd=None, extra_env=None):
    """Run the accurate-reviewer binary and capture everything."""
    env = os.environ.copy()
    env["PATH"] = f"{context.fake_bin_dir}{os.pathsep}{env.get('PATH', '')}"
    env["ACCURATE_REVIEWER_MOCK_SCRIPT"] = str(context.mock_script_path)
    env["ACCURATE_REVIEWER_MOCK_PROMPT_LOG"] = str(context.mock_prompt_log)
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
