"""Steps for the HTML report and the `serve` command.

The serve command is a real long-running HTTP server. We launch it via
subprocess.Popen, poll stdout until it prints its URL, drive a couple of
requests against it, then terminate it. Killing happens in after_scenario
as a safety net even if a scenario step fails midway.
"""

import os
import re
import signal
import subprocess
import time
import urllib.error
import urllib.request
from pathlib import Path

from behave import when, then

from environment import CLI_BIN


def _ensure_serve_state(context):
    # behave's Context.__getattr__ raises KeyError (not AttributeError) for
    # missing attrs, so hasattr/getattr both blow up. Probe via __dict__.
    if "_serve_procs" not in context.__dict__:
        context._serve_procs = []


def _kill_serve_procs(context):
    procs = context.__dict__.get("_serve_procs", [])
    for proc in procs:
        try:
            proc.terminate()
            try:
                proc.wait(timeout=2)
            except subprocess.TimeoutExpired:
                proc.kill()
                proc.wait(timeout=2)
        except Exception:
            pass
    context._serve_procs = []


@when('I serve "{path}" in the background')
def step_serve_in_background(context, path):
    _ensure_serve_state(context)
    env = os.environ.copy()
    env["PATH"] = f"{context.fake_bin_dir}{os.pathsep}{env.get('PATH', '')}"
    proc = subprocess.Popen(
        [str(CLI_BIN), "serve", "--report", path, "--addr", "127.0.0.1:0"],
        stdout=subprocess.PIPE,
        stderr=subprocess.PIPE,
        cwd=str(context.workdir),
        env=env,
        text=True,
    )
    context._serve_procs.append(proc)

    # Poll stdout for the URL the server prints on a successful bind.
    deadline = time.time() + 5.0
    url = None
    buf = ""
    while time.time() < deadline:
        line = proc.stdout.readline()
        if not line:
            if proc.poll() is not None:
                break
            time.sleep(0.05)
            continue
        buf += line
        m = re.search(r"http://(127\.0\.0\.1:\d+)/", line)
        if m:
            url = "http://" + m.group(1)
            break
    if not url:
        rc = proc.poll()
        stderr_extra = proc.stderr.read() if proc.stderr else ""
        raise AssertionError(
            f"serve did not announce a URL within 5s (exit={rc})\nstdout:\n{buf}\nstderr:\n{stderr_extra}"
        )
    context.serve_url = url


@then('GET {route} returns {code:d} and contains "{needle}"')
def step_get_returns(context, route, code, needle):
    url = context.serve_url + route
    try:
        with urllib.request.urlopen(url, timeout=3) as resp:
            body = resp.read().decode("utf-8", errors="replace")
            actual = resp.status
    except urllib.error.HTTPError as e:
        actual = e.code
        body = e.read().decode("utf-8", errors="replace")
    assert actual == code, f"GET {route} -> {actual}, expected {code}\n{body}"
    assert needle in body, f"GET {route} body missing {needle!r}:\n{body}"


@then("GET {route} returns {code:d}")
def step_get_returns_only(context, route, code):
    url = context.serve_url + route
    try:
        with urllib.request.urlopen(url, timeout=3) as resp:
            actual = resp.status
    except urllib.error.HTTPError as e:
        actual = e.code
    assert actual == code, f"GET {route} -> {actual}, expected {code}"


def before_scenario_hook(context, scenario):
    # Hooked from environment.py — placeholder kept for clarity.
    pass


def after_scenario_hook(context, scenario):
    _kill_serve_procs(context)
