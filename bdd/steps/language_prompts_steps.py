"""Steps specific to the @language-prompts feature.

These scenarios need to:
  - stage a snapshot for a chosen language (analyze writes
    .review-cache/project.json inside the chosen subdir),
  - run review from inside that same subdir (so the master reads the
    snapshot we just wrote),
  - assert on a single worker's prompts in isolation (the existing
    generic `every worker prompt contains` step matches every worker
    including ones the language-specific text deliberately does not
    target).

The only Python here that is NOT a step is the small `_write_review_yml`
helper — kept local so the YAML shape stays close to the steps that use
it.
"""

from pathlib import Path

from behave import given, when, then

from environment import mock_prompts, run_cli


def _write_review_yml(base: Path, body: str) -> None:
    (base / ".review.yml").write_text(body, encoding="utf-8")


@given('a sample Python project at "{rel}"')
def step_sample_python(context, rel):
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    (base / "requirements.txt").write_text("flask==3.0.0\n", encoding="utf-8")
    (base / "app.py").write_text("from flask import Flask\napp = Flask(__name__)\n", encoding="utf-8")
    context.sample_project_dir = base


@given('a sample JavaScript project at "{rel}"')
def step_sample_javascript(context, rel):
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    (base / "package.json").write_text(
        '{"name":"sample","version":"1.0.0","dependencies":{"express":"^4.0.0"}}\n',
        encoding="utf-8",
    )
    (base / "index.js").write_text("module.exports = function(){};\n", encoding="utf-8")
    context.sample_project_dir = base


@given('a sample TypeScript project at "{rel}"')
def step_sample_typescript(context, rel):
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    (base / "package.json").write_text(
        '{"name":"sample","version":"1.0.0","devDependencies":{"typescript":"^5.0.0"}}\n',
        encoding="utf-8",
    )
    # Two .ts files so typescript wins the LOC race over the (almost empty)
    # package.json-derived "javascript" detection.
    (base / "index.ts").write_text("export const x: number = 1;\nexport const y: number = 2;\n", encoding="utf-8")
    (base / "util.ts").write_text("export function f(): void {}\nexport function g(): void {}\n", encoding="utf-8")
    context.sample_project_dir = base


@given('a sample Rust project at "{rel}"')
def step_sample_rust(context, rel):
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    (base / "Cargo.toml").write_text(
        '[package]\nname = "sample"\nversion = "0.1.0"\nedition = "2021"\n',
        encoding="utf-8",
    )
    (base / "main.rs").write_text("fn main() {}\n", encoding="utf-8")
    context.sample_project_dir = base


@given('a sample Java project at "{rel}"')
def step_sample_java(context, rel):
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    (base / "pom.xml").write_text(
        '<?xml version="1.0"?>\n'
        '<project>\n'
        '  <modelVersion>4.0.0</modelVersion>\n'
        '  <groupId>example</groupId>\n'
        '  <artifactId>sample</artifactId>\n'
        '  <version>1.0.0</version>\n'
        '</project>\n',
        encoding="utf-8",
    )
    (base / "App.java").write_text("public class App { public static void main(String[] a) {} }\n", encoding="utf-8")
    context.sample_project_dir = base


@given('a sample C# project at "{rel}"')
def step_sample_csharp(context, rel):
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    (base / "App.csproj").write_text(
        '<Project Sdk="Microsoft.NET.Sdk">\n'
        "  <PropertyGroup>\n"
        "    <OutputType>Exe</OutputType>\n"
        "    <TargetFramework>net8.0</TargetFramework>\n"
        "  </PropertyGroup>\n"
        "</Project>\n",
        encoding="utf-8",
    )
    (base / "Program.cs").write_text(
        "using System;\n"
        "public static class Program {\n"
        "    public static void Main() { Console.WriteLine(\"hi\"); }\n"
        "}\n",
        encoding="utf-8",
    )
    context.sample_project_dir = base


@given('a .review.yml inside "{rel}" with llm.provider set to "{provider}"')
def step_review_yml_provider_inside(context, rel, provider):
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    _write_review_yml(base,
        "version: 1\n"
        "checks: { security: true, logic: true }\n"
        "severity: { blocking: critical }\n"
        f"llm: {{ provider: {provider} }}\n"
        "sanitizer: { enabled: true }\n"
        "secrets: { enabled: true, entropy_threshold: 4.5 }\n"
    )


@given('a .review.yml that disables language-specific prompts inside "{rel}"')
def step_review_yml_disable_lang_inside(context, rel):
    base = context.workdir / rel
    base.mkdir(parents=True, exist_ok=True)
    _write_review_yml(base,
        "version: 1\n"
        "checks: { security: true, logic: true, language_specific_prompts: false }\n"
        "severity: { blocking: critical }\n"
        "llm: { provider: mock }\n"
        "sanitizer: { enabled: true }\n"
        "secrets: { enabled: true, entropy_threshold: 4.5 }\n"
    )


@when('I run "{cmdline}" with that diff on stdin inside "{rel}"')
def step_run_with_diff_inside(context, cmdline, rel):
    base = context.workdir / rel
    extra_env = getattr(context, "extra_env", None)
    run_cli(context, cmdline, stdin=context.last_diff or b"", cwd=base, extra_env=extra_env)


@then('every {worker} worker prompt contains "{needle}"')
def step_every_worker_prompt_contains(context, worker, needle):
    prompts = [p for p in mock_prompts(context) if p.get("worker") == worker]
    assert prompts, f"no prompts were captured for worker {worker!r}"
    for p in prompts:
        assert needle in p["prompt"], (
            f"{worker} prompt missing {needle!r}:\n{p['prompt']}"
        )
