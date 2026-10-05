#!/usr/bin/env python3
"""Tests for repo_lint.py. Run: python3 scripts/harness/test_repo_lint.py

Two cases are the real incidents that motivated the sensor (warehouse-planning's `web` job and the
Dependabot /web entry depending on ../../warehouse-ui-kit); they must keep failing the lint.
"""
import json
import os
import subprocess
import sys
import tempfile
import textwrap
import unittest

LINT = os.path.join(os.path.dirname(os.path.abspath(__file__)), "repo_lint.py")


DEP_GOMOD = """  - package-ecosystem: "gomod"
    directory: "/"
"""
DEP_ACTIONS = """  - package-ecosystem: "github-actions"
    directory: "/"
"""


def make(files, auto_dep=True):
    """auto_dep: add a valid dependabot.yml when the fixture has workflows/go.mod and none of its own, so tests of
    the OTHER rules are not tripped by R6 (the R6 tests pass auto_dep=False)."""
    files = dict(files)
    has_dep = any(k.startswith(".github/dependabot") for k in files)
    wf = any(k.startswith(".github/workflows/") and k.endswith((".yml", ".yaml")) for k in files)
    gomod = "go.mod" in files
    if auto_dep and not has_dep and (wf or gomod):
        files[".github/dependabot.yml"] = "version: 2\nupdates:\n" + (DEP_GOMOD if gomod else "") + (DEP_ACTIONS if wf else "")
    root = tempfile.mkdtemp(prefix="rl-")
    for rel, body in files.items():
        p = os.path.join(root, rel)
        os.makedirs(os.path.dirname(p), exist_ok=True)
        with open(p, "w") as f:
            f.write(textwrap.dedent(body).lstrip("\n") if isinstance(body, str) else json.dumps(body))
    return root


def lint(root, *extra):
    r = subprocess.run([sys.executable, LINT, "--root", root, *extra], capture_output=True, text=True)
    return r.returncode, r.stdout


WF_WEB_PREFIXED = """
    name: CI
    on: push
    jobs:
      web:
        runs-on: ubuntu-latest
        defaults:
          run:
            working-directory: "warehouse-planning/web"
        steps:
          - uses: actions/checkout@v7
            with:
              path: "warehouse-planning"
          - uses: actions/checkout@v7
            with:
              repository: IQVO/warehouse-ui-kit
              path: warehouse-ui-kit
          - run: npm ci
"""


class RepoLint(unittest.TestCase):
    # ---- R1 ----
    def test_r1_incident_web_dir_missing_behind_checkout_prefix(self):
        root = make({".github/workflows/ci.yml": WF_WEB_PREFIXED, "go.mod": "module x\n"})
        code, out = lint(root)
        self.assertEqual(code, 1, out)
        self.assertIn("[R1]", out)
        self.assertIn("`web`", out)           # the path INSIDE the repo is reported, not the prefixed one
        self.assertIn("WHY:", out)
        self.assertIn("FIX:", out)

    def test_r1_prefixed_web_dir_exists_is_fine(self):
        root = make({".github/workflows/ci.yml": WF_WEB_PREFIXED, "web/package.json": {"name": "w"}})
        self.assertEqual(lint(root)[0], 0, lint(root)[1])

    def test_r1_cache_dependency_path_missing(self):
        wf = """
            name: CI
            on: push
            jobs:
              docs:
                runs-on: ubuntu-latest
                steps:
                  - uses: actions/setup-node@v7
                    with:
                      cache: npm
                      cache-dependency-path: docs/package-lock.json
        """
        code, out = lint(make({".github/workflows/ci.yml": wf}))
        self.assertEqual(code, 1)
        self.assertIn("docs/package-lock.json", out)
        code, _ = lint(make({".github/workflows/ci.yml": wf, "docs/package-lock.json": {}}))
        self.assertEqual(code, 0)

    def test_r1_external_checkout_and_expressions_are_skipped(self):
        wf = """
            name: CI
            on: push
            jobs:
              web:
                runs-on: ubuntu-latest
                steps:
                  - uses: actions/checkout@v7
                    with:
                      repository: IQVO/warehouse-ui-kit
                      path: warehouse-ui-kit
                  - run: npm ci
                    working-directory: warehouse-ui-kit
                  - run: echo
                    working-directory: ${{ matrix.dir }}
        """
        self.assertEqual(lint(make({".github/workflows/ci.yml": wf}))[0], 0)

    def test_ignore_marker(self):
        wf = WF_WEB_PREFIXED.replace('working-directory: "warehouse-planning/web"',
                                     'working-directory: "warehouse-planning/web" # repo-lint: ignore')
        self.assertEqual(lint(make({".github/workflows/ci.yml": wf}))[0], 0)

    # ---- R2 ----
    def test_r2_needs_unknown_job(self):
        wf = """
            name: CI
            on: push
            jobs:
              test:
                runs-on: ubuntu-latest
                steps:
                  - run: true
              publish:
                runs-on: ubuntu-latest
                needs: [test, lnt]
                steps:
                  - run: true
        """
        code, out = lint(make({".github/workflows/ci.yml": wf}))
        self.assertEqual(code, 1)
        self.assertIn("[R2]", out)
        self.assertIn("`lnt`", out)

    def test_r2_needs_ok(self):
        wf = """
            name: CI
            on: push
            jobs:
              test:
                runs-on: ubuntu-latest
                steps:
                  - run: true
              publish:
                runs-on: ubuntu-latest
                needs:
                  - test
                steps:
                  - run: true
        """
        self.assertEqual(lint(make({".github/workflows/ci.yml": wf}))[0], 0)

    # ---- R3 ----
    def test_r3_incident_npm_web_entry_with_outside_path_dependency(self):
        dep = """
            version: 2
            updates:
              - package-ecosystem: "npm"
                directory: "/web"
                schedule:
                  interval: "weekly"
        """
        files = {".github/dependabot.yml": dep,
                 "web/package.json": {"name": "w", "dependencies": {"@warehouse/ui-kit": "file:../../warehouse-ui-kit"}}}
        code, out = lint(make(files))
        self.assertEqual(code, 1, out)
        self.assertIn("[R3]", out)
        self.assertIn("OUTSIDE", out)
        self.assertIn("overrides", out)       # the FIX tells how security alerts are still handled

    def test_r3_inside_path_dependency_is_fine(self):
        dep = """
            version: 2
            updates:
              - package-ecosystem: "npm"
                directory: "/web"
                schedule:
                  interval: "weekly"
        """
        files = {".github/dependabot.yml": dep,
                 "web/package.json": {"name": "w", "dependencies": {"local": "file:./vendor/local"}}}
        self.assertEqual(lint(make(files))[0], 0)

    def test_r3_missing_directory_and_manifest(self):
        dep = """
            version: 2
            updates:
              - package-ecosystem: "gomod"
                directory: "/"
                schedule:
                  interval: "weekly"
              - package-ecosystem: "npm"
                directory: "/docs"
                schedule:
                  interval: "weekly"
        """
        code, out = lint(make({".github/dependabot.yml": dep}))
        self.assertEqual(code, 1)
        self.assertIn("go.mod", out)
        self.assertIn("`/docs`", out)

    # ---- R4 ----
    def test_r4_stale_owner_in_registry_and_docs(self):
        files = {".github/workflows/ci.yml": """
                    name: CI
                    on: push
                    jobs:
                      pub:
                        runs-on: ubuntu-latest
                        steps:
                          - run: docker push ghcr.io/claudioed/svc:latest
                 """,
                 "docs/docusaurus.config.ts": "export default {\n  url: 'https://claudioed.github.io',\n  organizationName: 'claudioed',\n};\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 1)
        self.assertEqual(out.count("[R4]"), 3, out)  # ghcr, github.io, organizationName

    def test_r4_clean_and_codeowners_and_custom_owner(self):
        files = {".github/CODEOWNERS": "* @claudioed\n",
                 ".github/workflows/ci.yml": "name: CI\non: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: docker push ghcr.io/iqvo/svc\n"}
        self.assertEqual(lint(make(files))[0], 0)
        files[".github/workflows/ci.yml"] += "      - run: echo ghcr.io/oldco/svc\n"
        self.assertEqual(lint(make(files), "--stale-owners", "oldco")[0], 1)

    # ---- R5 ----
    def test_r5_placeholder_found_but_github_expressions_are_not(self):
        files = {"Makefile": "PKG := ./internal/domain/{{RICHEST_AGGREGATE}}\n",
                 ".github/workflows/ci.yml": "name: CI\non: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: echo ${{ github.sha }}\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 1)
        self.assertEqual(out.count("[R5]"), 1, out)
        self.assertIn("RICHEST_AGGREGATE", out)

    def test_r5_skipped_in_the_template_repo(self):
        files = {".github/workflows/ci.yml.template": "x: {{SERVICE}}\n", "Makefile": "PKG := {{RICHEST_AGGREGATE}}\n"}
        self.assertEqual(lint(make(files))[0], 0)

    # ---- R6 ----
    def test_r6_incident_no_dependabot_config_at_all(self):
        files = {"go.mod": "module x\n", ".github/workflows/ci.yml": "name: CI\non: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: true\n"}
        code, out = lint(make(files, auto_dep=False))
        self.assertEqual(code, 1, out)
        self.assertIn("[R6]", out)
        self.assertIn("gomod", out)
        self.assertIn("github-actions", out)

    def test_r6_partial_coverage_reports_only_the_missing_ecosystem(self):
        dep = """
            version: 2
            updates:
              - package-ecosystem: "gomod"
                directory: "/"
                schedule:
                  interval: "weekly"
        """
        files = {"go.mod": "module x\n", ".github/dependabot.yml": dep,
                 ".github/workflows/ci.yml": "name: CI\non: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: true\n"}
        code, out = lint(make(files, auto_dep=False))
        self.assertEqual(code, 1)
        self.assertEqual(out.count("[R6]"), 1, out)
        self.assertIn("github-actions", out)

    def test_r6_full_coverage_and_comment_between_keys(self):
        dep = """
            version: 2
            updates:
              - package-ecosystem: "gomod"
                directory: "/"
              - package-ecosystem: "github-actions"
                # actions are pinned by SHA
                directory: "/"
        """
        files = {"go.mod": "module x\n", ".github/dependabot.yml": dep,
                 ".github/workflows/ci.yml": "name: CI\non: push\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: true\n"}
        self.assertEqual(lint(make(files))[0], 0, lint(make(files))[1])

    def test_r6_not_required_without_go_or_workflows(self):
        self.assertEqual(lint(make({"README.md": "x\n"}))[0], 0)

    # ---- R7: scripts invoked from workflow steps ------------------------------------------------------
    E2E_WF = """
        name: E2E
        on: schedule
        jobs:
          scenario:
            runs-on: ubuntu-latest
            steps:
              - uses: actions/checkout@v7
                with:
                  path: e2e-tests
              - uses: actions/checkout@v7
                with:
                  repository: IQVO/facility-layout
                  path: facility-layout
              - name: Close the harness:red issue
                if: success()
                run: python3 scripts/harness/red_issue.py bootstrap-scenario --close
    """

    def test_r7_incident_multi_repo_job_runs_repo_script_from_workspace_root(self):
        # e2e-tests: the scenario passed, the job still went red, because the step ran where the script is not
        root = make({".github/workflows/e2e.yml": self.E2E_WF, "scripts/harness/red_issue.py": "print(1)\n"})
        code, out = lint(root)
        self.assertEqual(code, 1, out)
        self.assertIn("[R7]", out)
        self.assertIn("e2e-tests/", out)
        self.assertIn("working-directory: e2e-tests", out)

    def test_r7_working_directory_on_the_step_fixes_it(self):
        wf = self.E2E_WF.replace("                run: python3", "                working-directory: e2e-tests\n                run: python3")
        root = make({".github/workflows/e2e.yml": wf, "scripts/harness/red_issue.py": "print(1)\n"})
        code, out = lint(root)
        self.assertEqual(code, 0, out)

    def test_r7_job_level_default_working_directory_counts(self):
        wf = """
        name: E2E
        on: schedule
        jobs:
          scenario:
            runs-on: ubuntu-latest
            defaults:
              run:
                working-directory: e2e-tests
            steps:
              - uses: actions/checkout@v7
                with:
                  path: e2e-tests
              - run: python3 scripts/harness/red_issue.py x --close
        """
        root = make({".github/workflows/e2e.yml": wf, "scripts/harness/red_issue.py": "print(1)\n"})
        code, out = lint(root)
        self.assertEqual(code, 0, out)

    def test_r7_missing_script_in_a_single_checkout_job(self):
        wf = """
        name: CI
        on: push
        jobs:
          t:
            runs-on: ubuntu-latest
            steps:
              - uses: actions/checkout@v7
              - run: bash scripts/does-not-exist.sh
        """
        root = make({".github/workflows/ci.yml": wf})
        code, out = lint(root)
        self.assertEqual(code, 1, out)
        self.assertIn("[R7]", out)
        self.assertIn("scripts/does-not-exist.sh", out)

    def test_r7_existing_script_expressions_and_ignore_marker_are_fine(self):
        wf = """
        name: CI
        on: push
        jobs:
          t:
            runs-on: ubuntu-latest
            steps:
              - uses: actions/checkout@v7
              - run: python3 scripts/ok.py
              - run: bash scripts/${{ matrix.x }}.sh
              - run: bash scripts/gone.sh # repo-lint: ignore generated at runtime
        """
        root = make({".github/workflows/ci.yml": wf, "scripts/ok.py": "print(1)\n"})
        code, out = lint(root)
        self.assertEqual(code, 0, out)

    def test_empty_repo_is_clean(self):
        self.assertEqual(lint(make({"README.md": "x\n"}))[0], 0)


if __name__ == "__main__":
    unittest.main(verbosity=1)
