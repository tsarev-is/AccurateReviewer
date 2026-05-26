"""Steps shared by the @gitlab and @bitbucket post-comments features.

The @action feature already covers GitHub end-to-end through the same
fake-CLI harness; these steps add the small extras the non-GitHub
platforms need (removing the platform CLI from PATH, materialising a
git remote URL so the auto-detector resolves something deterministic).
"""

import subprocess

from behave import given


@given('the glab CLI is removed from PATH')
def step_remove_glab(context):
    fake = context.fake_bin_dir / "glab"
    if fake.exists():
        fake.unlink()
    context.extra_env = getattr(context, "extra_env", {})
    # Same trick as the gh-missing scenario: shrink PATH to just the
    # fake-bin dir so the host's real glab (if any) is invisible.
    context.extra_env["PATH"] = str(context.fake_bin_dir)


@given('the bb CLI is removed from PATH')
def step_remove_bb(context):
    fake = context.fake_bin_dir / "bb"
    if fake.exists():
        fake.unlink()
    context.extra_env = getattr(context, "extra_env", {})
    context.extra_env["PATH"] = str(context.fake_bin_dir)


@given('a git repo with origin "{url}"')
def step_git_repo_origin(context, url):
    """Initialise a git repo in the workdir with the given origin URL.

    The auto-detect dispatcher runs `git remote get-url origin` to pick
    the platform when --platform is omitted; without an actual repo,
    git exits 128 and the detection falls through to the github
    default. This step makes the detection branch executable.
    """
    subprocess.run(["git", "init", "--quiet"], cwd=context.workdir, check=True)
    subprocess.run(
        ["git", "remote", "add", "origin", url],
        cwd=context.workdir,
        check=True,
    )
