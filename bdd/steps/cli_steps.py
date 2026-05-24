"""Steps specific to the CLI surface feature."""

import subprocess
from pathlib import Path

from behave import given, when, then

from environment import run_cli


@given('a git repository with two commits, the latest adding a file "{path}"')
def step_git_repo_two_commits(context, path):
    cwd = context.workdir
    subprocess.run(["git", "init", "-q"], cwd=cwd, check=True)
    subprocess.run(["git", "config", "user.email", "test@example.com"], cwd=cwd, check=True)
    subprocess.run(["git", "config", "user.name", "Test"], cwd=cwd, check=True)
    (cwd / "seed").write_text("seed\n", encoding="utf-8")
    subprocess.run(["git", "add", "."], cwd=cwd, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "seed"], cwd=cwd, check=True)
    (cwd / path).write_text("package main\nfunc main() {}\n", encoding="utf-8")
    subprocess.run(["git", "add", "."], cwd=cwd, check=True)
    subprocess.run(["git", "commit", "-q", "-m", "add " + path], cwd=cwd, check=True)


@then('the output contains the file "{name}"')
def step_output_has_file(context, name):
    assert name in context.last_run["stdout"], context.last_run["stdout"]


@then('the output contains the value of the VERSION file')
def step_output_has_version_file_value(context):
    version_file = Path(__file__).resolve().parents[2] / "VERSION"
    assert version_file.exists(), f"missing VERSION file at {version_file}"
    version = version_file.read_text(encoding="utf-8").strip()
    assert version, f"VERSION file at {version_file} is empty"
    assert version in context.last_run["stdout"], (
        f'expected version {version!r} from {version_file} in stdout:\n'
        f'{context.last_run["stdout"]}'
    )
