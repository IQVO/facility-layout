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

    # ---- R8: docs built on pull requests ---------------------------------------------------------------
    DOCS_PUSH_ONLY = """
        name: Docs
        on:
          push:
            branches: [main]
            paths: ["docs/**"]
          workflow_dispatch: {}
        jobs:
          build:
            runs-on: ubuntu-latest
            steps:
              - run: npm run build
          deploy:
            needs: build
            runs-on: ubuntu-latest
            steps:
              - uses: actions/deploy-pages@v5
    """
    DOCS_OK = """
        name: Docs
        on:
          push:
            branches: [main]
            paths: ["docs/**"]
          pull_request:
            paths: ["docs/**"]
          workflow_dispatch: {}
        jobs:
          build:
            runs-on: ubuntu-latest
            steps:
              - run: npm run build
          deploy:
            if: github.event_name != 'pull_request'
            needs: build
            runs-on: ubuntu-latest
            steps:
              - uses: actions/deploy-pages@v5
    """

    def test_r8_incident_docs_workflow_runs_only_on_push_to_main(self):
        code, out = lint(make({".github/workflows/docs.yml": self.DOCS_PUSH_ONLY}))
        self.assertEqual(code, 1, out)
        self.assertIn("[R8]", out)
        self.assertIn("pull_request", out)
        self.assertIn("FIX:", out)

    def test_r8_pull_request_trigger_and_gated_deploy_is_fine(self):
        code, out = lint(make({".github/workflows/docs.yml": self.DOCS_OK}))
        self.assertEqual(code, 0, out)

    def test_r8_pull_request_trigger_but_deploy_not_gated(self):
        wf = self.DOCS_OK.replace("            if: github.event_name != 'pull_request'\n", "")
        code, out = lint(make({".github/workflows/docs.yml": wf}))
        self.assertEqual(code, 1, out)
        self.assertEqual(out.count("[R8]"), 1, out)
        self.assertIn("`deploy`", out)

    def test_r8_inline_trigger_list_and_pages_workflow_under_another_name(self):
        wf = self.DOCS_PUSH_ONLY.replace("on:\n          push:\n            branches: [main]\n            paths: [\"docs/**\"]\n"
                                         "          workflow_dispatch: {}\n", "on: [push, pull_request_target]\n")
        code, out = lint(make({".github/workflows/pages.yml": wf}))
        self.assertEqual(code, 1, out)          # pull_request_target is not a build-on-PR trigger
        self.assertIn("[R8]", out)
        wf_ok = wf.replace("on: [push, pull_request_target]", "on: [push, pull_request]").replace(
            "          deploy:\n", "          deploy:\n            if: github.ref == 'refs/heads/main'\n")
        code, out = lint(make({".github/workflows/pages.yml": wf_ok}))
        self.assertEqual(code, 0, out)

    def test_r8_not_a_docs_workflow(self):
        wf = "name: CI\non:\n  push:\n    branches: [main]\njobs:\n  a:\n    runs-on: x\n    steps:\n      - run: true\n"
        self.assertEqual(lint(make({".github/workflows/ci.yml": wf}))[0], 0)

    # ---- R9: chart tests run in CI ----------------------------------------------------------------------
    CI_NO_CHART_TESTS = """
        name: CI
        on: push
        jobs:
          test:
            runs-on: ubuntu-latest
            steps:
              - uses: actions/checkout@v7
              - run: go test ./...
    """

    def test_r9_incident_chart_tests_exist_but_no_workflow_runs_them(self):
        files = {".github/workflows/ci.yml": self.CI_NO_CHART_TESTS,
                 "charts/svc/tests/test_service_selectors.py": "print('ok')\n",
                 "charts/svc/tests/render_test.sh": "echo ok\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 1, out)
        self.assertEqual(out.count("[R9]"), 2, out)
        self.assertIn("charts/svc/tests/test_service_selectors.py", out)
        self.assertIn("chart-tests", out)       # the FIX names the job to copy

    def test_r9_tests_named_in_run_steps_are_fine(self):
        wf = self.CI_NO_CHART_TESTS.rstrip(" ") + (
            "          chart-tests:\n"
            "            runs-on: ubuntu-latest\n"
            "            steps:\n"
            "              - uses: actions/checkout@v7\n"
            "              - run: bash charts/svc/tests/render_test.sh\n"
            "              - run: python3 charts/svc/tests/test_service_selectors.py\n")
        files = {".github/workflows/ci.yml": wf,
                 "charts/svc/tests/test_service_selectors.py": "print('ok')\n",
                 "charts/svc/tests/render_test.sh": "echo ok\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 0, out)

    def test_r9_glob_loop_and_test_run_by_another_test(self):
        wf = self.CI_NO_CHART_TESTS.rstrip(" ") + (
            "          chart-tests:\n"
            "            runs-on: ubuntu-latest\n"
            "            steps:\n"
            "              - uses: actions/checkout@v7\n"
            "              - run: |\n"
            "                  for t in charts/svc/tests/test_*.py; do python3 \"$t\"; done\n"
            "                  bash charts/svc/tests/render_test.sh\n")
        files = {".github/workflows/ci.yml": wf,
                 "charts/svc/tests/test_a.py": "print('ok')\n",
                 "charts/svc/tests/test_b.py": "print('ok')\n",
                 "charts/svc/tests/render_test.sh": "sh \"$(dirname \"$0\")/render-assertions.sh\"\n",
                 "charts/svc/tests/render-assertions.sh": "echo ok\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 0, out)

    def test_r9_comment_mention_does_not_count_but_make_target_does(self):
        wf = self.CI_NO_CHART_TESTS.rstrip(" ") + "          # chart tests: charts/svc/tests/render_test.sh (not wired yet)\n"
        files = {".github/workflows/ci.yml": wf, "charts/svc/tests/render_test.sh": "echo ok\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 1, out)
        self.assertIn("[R9]", out)
        wf_make = self.CI_NO_CHART_TESTS.replace("go test ./...", "make check chart-test")
        files = {".github/workflows/ci.yml": wf_make, "charts/svc/tests/render_test.sh": "echo ok\n",
                 "Makefile": "check: vet\n\tgo test ./...\n\nvet:\n\tgo vet ./...\n\nchart-test: helm-dep\n\tbash charts/svc/tests/render_test.sh\n\nhelm-dep:\n\ttrue\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 0, out)

    def test_r9_make_target_that_ci_does_not_run_is_not_enough(self):
        files = {".github/workflows/ci.yml": self.CI_NO_CHART_TESTS.replace("go test ./...", "make check"),
                 "charts/svc/tests/render_test.sh": "echo ok\n",
                 "Makefile": "check:\n\tgo test ./...\n\nchart-test:\n\tbash charts/svc/tests/render_test.sh\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 1, out)
        self.assertIn("[R9]", out)

    def test_r9_helpers_and_ignored_files_are_not_tests(self):
        files = {".github/workflows/ci.yml": self.CI_NO_CHART_TESTS,
                 "charts/svc/tests/helpers.py": "def f(): pass\n",
                 "charts/svc/tests/README.md": "x\n",
                 "charts/svc/tests/manual_test.sh": "# repo-lint: ignore needs a live cluster\necho\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 0, out)

    # ---- R10: chart ConfigMaps are mounted --------------------------------------------------------------
    CM = """
        apiVersion: v1
        kind: ConfigMap
        metadata:
          name: {{ include "svc.fullname" . }}
          labels:
            app: svc
        data:
          OUTBOX_RELAY_INTERVAL: {{ .Values.config.outboxRelayInterval | quote }}
    """
    DEPLOY_NO_ENVFROM = """
        apiVersion: apps/v1
        kind: Deployment
        metadata:
          name: {{ include "svc.fullname" . }}
        spec:
          template:
            spec:
              containers:
                - name: api
                  env:
                    - name: PORT
                      value: "8080"
    """

    def test_r10_incident_configmap_no_deployment_mounts(self):
        files = {"charts/svc/templates/configmap.yaml": self.CM,
                 "charts/svc/templates/deployment.yaml": self.DEPLOY_NO_ENVFROM}
        code, out = lint(make(files))
        self.assertEqual(code, 1, out)
        self.assertIn("[R10]", out)
        self.assertIn("charts/svc/templates/configmap.yaml", out)
        self.assertIn("envFrom", out)

    def test_r10_envfrom_configmapref_mounts_it(self):
        deploy = self.DEPLOY_NO_ENVFROM.replace(
            "                  env:\n",
            "                  envFrom:\n"
            "                    - configMapRef:\n"
            "                        name: {{ include \"svc.fullname\" . }}\n"
            "                  env:\n")
        files = {"charts/svc/templates/configmap.yaml": self.CM, "charts/svc/templates/deployment.yaml": deploy}
        code, out = lint(make(files))
        self.assertEqual(code, 0, out)

    def test_r10_suffix_names_volume_printf_and_no_cross_match(self):
        # base ConfigMap + a -path-catalogue one; only the catalogue is mounted (volume, printf form)
        cat = self.CM.replace('{{ include "svc.fullname" . }}', '{{ include "svc.fullname" . }}-path-catalogue')
        deploy = self.DEPLOY_NO_ENVFROM.rstrip(" ") + (
            "              volumes:\n"
            "                - name: catalogue\n"
            "                  configMap:\n"
            "                    name: {{ .Values.pathCatalogue.existingConfigMap | default (printf \"%s-path-catalogue\" (include \"svc.fullname\" .)) }}\n")
        files = {"charts/svc/templates/configmap.yaml": self.CM,
                 "charts/svc/templates/configmap-pathcatalogue.yaml": cat,
                 "charts/svc/templates/deployment.yaml": deploy}
        code, out = lint(make(files))
        self.assertEqual(code, 1, out)
        self.assertEqual(out.count("[R10]"), 1, out)              # the base one, not the catalogue
        self.assertIn("charts/svc/templates/configmap.yaml", out)
        self.assertNotIn("configmap-pathcatalogue.yaml", out)

    def test_r10_configmapkeyref_cronjob_and_multi_document(self):
        two = self.CM.rstrip(" ") + "        ---\n" + self.CM.replace('{{ include "svc.fullname" . }}', "{{ include \"svc.fullname\" . }}-jobs")
        cron = """
            apiVersion: batch/v1
            kind: CronJob
            spec:
              jobTemplate:
                spec:
                  template:
                    spec:
                      containers:
                        - name: sweep
                          env:
                            - name: X
                              valueFrom:
                                configMapKeyRef:
                                  name: {{ include "svc.fullname" . }}-jobs
                                  key: X
                          envFrom:
                            - configMapRef: {name: '{{ include "svc.fullname" . }}'}
        """
        files = {"charts/svc/templates/configmap.yaml": two, "charts/svc/templates/cronjob.yaml": cron}
        code, out = lint(make(files))
        self.assertEqual(code, 0, out)

    def test_r10_reference_from_a_non_workload_does_not_count_and_ignore_marker(self):
        # a ConfigMap that only another ConfigMap/Service mentions is still unmounted
        other = "apiVersion: v1\nkind: Service\nmetadata:\n  name: x\n  annotations:\n    configMap:\n      name: {{ include \"svc.fullname\" . }}\n"
        files = {"charts/svc/templates/configmap.yaml": self.CM, "charts/svc/templates/service.yaml": other}
        code, out = lint(make(files))
        self.assertEqual(code, 1, out)
        dash = self.CM.replace("kind: ConfigMap", "kind: ConfigMap  # repo-lint: ignore Grafana sidecar picks it up by label")
        self.assertEqual(lint(make({"charts/svc/templates/dash.yaml": dash}))[0], 0)

    # ---- R11: coverage scope has one source of truth (the Makefile) ------------------------------------
    MK_COVERAGE = ("COVERAGE_PKGS := ./internal/domain/...,./internal/application/...,./internal/analytics/...\n\n"
                   "coverage:\n\tgo test ./... -race -coverprofile=coverage.out -coverpkg=$(COVERAGE_PKGS)\n\n"
                   "check-all: check coverage\n\ncheck:\n\tgo vet ./...\n")
    CI_INLINE_COVERPKG = """
        name: CI
        on: push
        jobs:
          test:
            runs-on: ubuntu-latest
            steps:
              - uses: actions/checkout@v7
              - name: Unit tests with coverage
                run: |
                  go test ./... -race \\\\
                    -coverprofile=coverage.out \\\\
                    -coverpkg=./internal/domain/...,./internal/application/...
    """

    def test_r11_incident_ci_defines_its_own_coverpkg_set(self):
        # 16d: CI measured domain+application while `make coverage` also measured analytics
        code, out = lint(make({".github/workflows/ci.yml": self.CI_INLINE_COVERPKG, "Makefile": self.MK_COVERAGE}))
        self.assertEqual(code, 1, out)
        self.assertEqual(out.count("[R11]"), 2, out)   # the -coverpkg line AND the gate CI never calls
        self.assertIn("[R11] .github/workflows/ci.yml:12", out)
        self.assertIn("[R11] Makefile:3", out)
        self.assertIn("make coverage", out)              # the FIX names the step to use

    def test_r11_ci_calling_make_coverage_is_clean(self):
        wf = """
            name: CI
            on: push
            jobs:
              test:
                runs-on: ubuntu-latest
                steps:
                  - uses: actions/checkout@v7
                  # the scope lives in the Makefile, never -coverpkg here
                  - run: make coverage
                  - run: go test ./internal/domain/... -coverprofile=/tmp/cov.out -covermode=atomic
        """
        code, out = lint(make({".github/workflows/ci.yml": wf, "Makefile": self.MK_COVERAGE}))
        self.assertEqual(code, 0, out)

    def test_r11_gate_reached_through_a_make_prerequisite_counts(self):
        wf = "name: CI\non: push\njobs:\n  t:\n    runs-on: x\n    steps:\n      - run: make check-all\n"
        code, out = lint(make({".github/workflows/ci.yml": wf, "Makefile": self.MK_COVERAGE}))
        self.assertEqual(code, 0, out)

    def test_r11_makefile_gate_never_run_by_ci(self):
        wf = "name: CI\non: push\njobs:\n  t:\n    runs-on: x\n    steps:\n      - run: make check\n"
        code, out = lint(make({".github/workflows/ci.yml": wf, "Makefile": self.MK_COVERAGE}))
        self.assertEqual(code, 1, out)
        self.assertEqual(out.count("[R11]"), 1, out)
        self.assertIn("[R11] Makefile:3", out)

    def test_r11_no_coverage_target_or_no_makefile_is_not_a_finding(self):
        # a skeleton without a coverage gate (or without a Makefile) must not false-positive
        wf = "name: CI\non: push\njobs:\n  t:\n    runs-on: x\n    steps:\n      - run: go test ./... -race\n"
        mk_no_cov = "test:\n\tgo test ./... -race\n\ncheck: test\n"
        self.assertEqual(lint(make({".github/workflows/ci.yml": wf, "Makefile": mk_no_cov}))[0], 0)
        self.assertEqual(lint(make({".github/workflows/ci.yml": wf}))[0], 0)
        # a coverage target with no package scope is not the gate this rule protects
        mk_plain = "coverage:\n\tgo test ./... -coverprofile=coverage.out\n"
        self.assertEqual(lint(make({".github/workflows/ci.yml": wf, "Makefile": mk_plain}))[0], 0)
        # no workflow at all: nothing to compare
        self.assertEqual(lint(make({"Makefile": self.MK_COVERAGE}))[0], 0)

    def test_r11_template_repo_reads_ci_yml_template(self):
        # warehouse-harness-template: the CI a service gets is ci.yml.template, the Makefile gate lives beside it
        tpl_ok = "name: CI\non: push\njobs:\n  test:\n    runs-on: x\n    steps:\n      - run: make coverage\n"
        files = {".github/workflows/ci.yml.template": tpl_ok, "Makefile": self.MK_COVERAGE,
                 ".github/workflows/selftest.yml": "name: S\non: push\njobs:\n  s:\n    runs-on: x\n    steps:\n      - run: true\n"}
        code, out = lint(make(files))
        self.assertEqual(code, 0, out)
        files[".github/workflows/ci.yml.template"] = self.CI_INLINE_COVERPKG
        code, out = lint(make(files))
        self.assertEqual(code, 1, out)
        self.assertIn("[R11] .github/workflows/ci.yml.template:12", out)

    def test_r11_ignore_marker_and_comments(self):
        wf = ("name: CI\non: push\njobs:\n  t:\n    runs-on: x\n    steps:\n"
              "      # - run: go test ./... -coverpkg=./old/...\n"
              "      - run: go test ./... -coverpkg=./x/... # repo-lint: ignore one-off experiment\n")
        self.assertEqual(lint(make({".github/workflows/ci.yml": wf}))[0], 0)


if __name__ == "__main__":
    unittest.main(verbosity=1)
