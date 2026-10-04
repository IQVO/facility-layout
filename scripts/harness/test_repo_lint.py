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


def make(files):
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

    def test_empty_repo_is_clean(self):
        self.assertEqual(lint(make({"README.md": "x\n"}))[0], 0)


if __name__ == "__main__":
    unittest.main(verbosity=1)
