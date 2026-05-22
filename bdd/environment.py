"""Behave hooks for the AccurateReviewer BDD suite.

Each scenario:
  - runs in its own temporary working directory (so .review.yml / .review-cache
    side effects do not bleed between tests),
  - starts the mock-llm server on a free port and exposes the URL to the CLI
    via ACCURATE_REVIEWER_MOCK_URL,
  - exposes the path to the compiled binaries.
"""

import json
import os
import shutil
import socket
import subprocess
import tempfile
import time
import urllib.request
from pathlib import Path


REPO_ROOT = Path(__file__).resolve().parent.parent
BIN_DIR = REPO_ROOT / "bin"
CLI_BIN = BIN_DIR / "accurate-reviewer"
MOCK_BIN = BIN_DIR / "mock-llm"


def _free_port() -> int:
    s = socket.socket()
    s.bind(("127.0.0.1", 0))
    port = s.getsockname()[1]
    s.close()
    return port


def _wait_healthy(url: str, timeout: float = 5.0) -> None:
    deadline = time.time() + timeout
    last_err = None
    while time.time() < deadline:
        try:
            with urllib.request.urlopen(url + "/healthz", timeout=0.5) as r:
                if r.status == 200:
                    return
        except Exception as e:
            last_err = e
            time.sleep(0.05)
    raise RuntimeError(f"mock-llm not healthy at {url}: {last_err}")


def before_all(context):
    if not CLI_BIN.exists():
        raise RuntimeError(f"binary not built — run `make build` first. Missing: {CLI_BIN}")
    if not MOCK_BIN.exists():
        raise RuntimeError(f"binary not built — run `make build` first. Missing: {MOCK_BIN}")


def before_scenario(context, scenario):
    context.workdir = Path(tempfile.mkdtemp(prefix="ar-bdd-"))
    context.repo_root = REPO_ROOT

    # Copy testdata/ into the scenario's workdir so feature files can
    # reference fixtures like "testdata/diffs/empty.diff" verbatim while
    # writes (e.g. `.review-cache/` from `analyze`) stay isolated per scenario.
    testdata_src = REPO_ROOT / "testdata"
    if testdata_src.exists():
        shutil.copytree(testdata_src, context.workdir / "testdata", symlinks=False)

    port = _free_port()
    context.mock_url = f"http://127.0.0.1:{port}"
    context.mock_proc = subprocess.Popen(
        [str(MOCK_BIN), "-addr", f"127.0.0.1:{port}"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
    )
    _wait_healthy(context.mock_url)

    # Cached holders used across steps.
    context.last_run = None        # dict: cmd, stdout, stderr, returncode, elapsed
    context.last_diff = None       # bytes
    context.last_units = None      # list of dict
    context.last_findings = None   # list of dict


def after_scenario(context, scenario):
    if getattr(context, "mock_proc", None):
        context.mock_proc.terminate()
        try:
            context.mock_proc.wait(timeout=2)
        except subprocess.TimeoutExpired:
            context.mock_proc.kill()
    if getattr(context, "workdir", None) and context.workdir.exists():
        shutil.rmtree(context.workdir, ignore_errors=True)


def run_cli(context, argv, *, stdin=None, cwd=None, extra_env=None):
    """Run the accurate-reviewer binary and capture everything."""
    env = os.environ.copy()
    env["ACCURATE_REVIEWER_MOCK_URL"] = context.mock_url
    if extra_env:
        env.update(extra_env)
    cwd = cwd or context.workdir

    if isinstance(argv, str):
        # parse a 'accurate-reviewer ...' string into argv
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
    body = json.dumps(entries).encode()
    req = urllib.request.Request(
        context.mock_url + "/script",
        data=body,
        headers={"Content-Type": "application/json"},
        method="POST",
    )
    urllib.request.urlopen(req, timeout=2)


def mock_reset(context):
    req = urllib.request.Request(context.mock_url + "/reset", method="POST")
    urllib.request.urlopen(req, timeout=2)


def mock_prompts(context):
    with urllib.request.urlopen(context.mock_url + "/prompts", timeout=2) as r:
        return json.loads(r.read())
