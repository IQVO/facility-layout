#!/usr/bin/env python3
"""Unit tests for hook.py: the runtime-agnostic guard logic.

Run: python3 scripts/harness/test_hook.py   (also wired into `make harness-test`)
Proves every block rule FIRES (an un-fired sensor is an unproven sensor) and
that normal workflows are NOT blocked. Payload shapes cover Claude Code
(tool_name/tool_input.file_path/command), OpenCode (tool/args.filePath) and
Codex (apply_patch patch text).
"""
import json
import os
import subprocess
import sys
import tempfile
import unittest

HOOK = os.path.join(os.path.dirname(os.path.abspath(__file__)), "hook.py")


def run(mode, payload, cwd, env=None):
    e = dict(os.environ)
    e.pop("HARNESS_OFF", None)
    e.update(env or {})
    p = subprocess.run([sys.executable, HOOK, mode], input=json.dumps(payload), cwd=cwd,
                       capture_output=True, text=True, env=e)
    return p.returncode, p.stderr


def make_repo(branch="feature/x"):
    d = tempfile.mkdtemp(prefix="harness-test-")
    subprocess.run(["git", "init", "-q", "-b", branch, d], check=True)
    subprocess.run(["git", "-C", d, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q",
                    "--allow-empty", "-m", "init"], check=True)
    return d


def write(path, text):
    with open(path, "w") as fh:
        fh.write(text)


def bash(cmd):
    return {"tool_name": "Bash", "tool_input": {"command": cmd}}


class PreGuard(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.feat = make_repo("feature/x")
        cls.dev = make_repo("develop")

    def blocked(self, cmd, cwd=None, env=None):
        code, err = run("pre", bash(cmd), cwd or self.feat, env)
        self.assertEqual(code, 2, f"expected block for: {cmd}\n{err}")
        self.assertIn("FIX:", err)  # every block message carries a remediation
        return err

    def allowed(self, cmd, cwd=None, env=None):
        code, err = run("pre", bash(cmd), cwd or self.feat, env)
        self.assertEqual(code, 0, f"expected allow for: {cmd}\n{err}")

    def test_no_verify(self):
        self.blocked("git commit --no-verify -m x")
        self.blocked("git push --no-verify origin feature/x")
        self.blocked("git commit -n -m x")

    def test_push_protected(self):
        self.blocked("git push origin develop")
        self.blocked("git push origin main")
        self.blocked("git push origin HEAD:develop")
        self.blocked("make check && git push origin develop")
        self.blocked("git push", cwd=self.dev)  # implicit push while ON develop
        self.allowed("git push -u origin feature/x")
        self.allowed("git push", cwd=self.feat)

    def test_force_push(self):
        self.blocked("git push --force origin feature/x")
        self.blocked("git push -f origin feature/x")
        self.blocked("git push --force-with-lease origin feature/x")
        self.allowed("git push --force origin feature/x", env={"HARNESS_ALLOW_FORCE_PUSH": "1"})

    def test_destructive_git(self):
        self.blocked("git reset --hard origin/develop")
        self.blocked("git clean -fd")
        self.blocked("git checkout -- .")
        self.allowed("git reset --soft HEAD~1")
        self.allowed("git checkout feature/y")
        self.allowed("git clean -n")

    def test_rm_rf(self):
        self.blocked("rm -rf internal/domain/stock")
        self.blocked("rm -fr ./docs")
        self.blocked("rm -r -f web/src")
        self.allowed("rm -rf /tmp/scratch-123")
        self.allowed("rm -rf web/node_modules")
        self.allowed("rm -f foo.log")
        self.allowed("git rm -r internal/old")
        self.allowed("rm -rf internal/domain/stock", env={"HARNESS_ALLOW_DESTRUCTIVE": "1"})

    def test_normal_workflow_not_blocked(self):
        for c in ("make check", "go test ./...", "git status", "git diff origin/develop..HEAD",
                  "gh pr create --base develop --title t --body-file b.md", "git commit -m 'feat: x'",
                  "git switch -c feature/z", "ls -la"):
            self.allowed(c)

    def test_generated_paths(self):
        for path in ("docs/docs/api/foo/bar.api.mdx", "docs/static/asyncapi/x/index.html",
                     "internal/x/api.pb.go", "internal/x/thing_gen.go"):
            for payload in ({"tool_name": "Edit", "tool_input": {"file_path": os.path.join(self.feat, path)}},
                            {"tool": "edit", "args": {"filePath": path}},
                            {"tool_name": "apply_patch",
                             "tool_input": {"command": f"*** Begin Patch\n*** Update File: {path}\n@@\n-a\n+b\n"}}):
                code, err = run("pre", payload, self.feat)
                self.assertEqual(code, 2, f"{path} {payload}\n{err}")
                self.assertIn("FIX:", err)
        code, _ = run("pre", {"tool_name": "Edit", "tool_input": {"file_path": "internal/domain/a.go"}}, self.feat)
        self.assertEqual(code, 0)

    def test_threshold_protection_only_in_fix_agent_mode(self):
        p = {"tool_name": "Edit", "tool_input": {"file_path": ".gremlins.yaml"}}
        self.assertEqual(run("pre", p, self.feat)[0], 0)
        code, err = run("pre", p, self.feat, {"HARNESS_PROTECT_THRESHOLDS": "1"})
        self.assertEqual(code, 2)
        self.assertIn("fix-agent mode", err)
        for path in ("internal/architecture/fitness_test.go", ".golangci.yml", ".github/workflows/ci.yml"):
            code, _ = run("pre", {"tool_name": "Write", "tool_input": {"file_path": path}}, self.feat,
                          {"HARNESS_PROTECT_THRESHOLDS": "1"})
            self.assertEqual(code, 2, path)

    def test_local_protected_file(self):
        d = make_repo()
        os.makedirs(os.path.join(d, "scripts/harness"))
        write(os.path.join(d, "scripts/harness/protected-paths.txt"), "# c\nschema/generated/*\n")
        code, _ = run("pre", {"tool_name": "Edit", "tool_input": {"file_path": "schema/generated/x.sql"}}, d)
        self.assertEqual(code, 2)

    def test_malformed_payload_never_blocks(self):
        e = dict(os.environ)
        p = subprocess.run([sys.executable, HOOK, "pre"], input="not json", capture_output=True, text=True,
                           cwd=self.feat, env=e)
        self.assertEqual(p.returncode, 0)

    def test_harness_off(self):
        code, _ = run("pre", bash("git push origin develop"), self.feat, {"HARNESS_OFF": "1"})
        # HARNESS_OFF is stripped by run(); call directly
        e = dict(os.environ, HARNESS_OFF="1")
        p = subprocess.run([sys.executable, HOOK, "pre"], input=json.dumps(bash("git push origin develop")),
                           capture_output=True, text=True, cwd=self.feat, env=e)
        self.assertEqual(p.returncode, 0)


@unittest.skipUnless(subprocess.run(["which", "go"], capture_output=True).returncode == 0, "go not installed")
class PostAndStop(unittest.TestCase):
    def setUp(self):
        self.d = make_repo()
        write(os.path.join(self.d, "go.mod"), "module example.com/t\n\ngo 1.22\n")
        os.makedirs(os.path.join(self.d, "pkg"))
        self.f = os.path.join(self.d, "pkg", "a.go")

    def edit(self):
        return {"tool_name": "Edit", "tool_input": {"file_path": self.f}}

    def test_unformatted_go_fed_back(self):
        write(self.f, "package pkg\nfunc  A( ) {  }\n")
        code, err = run("post", self.edit(), self.d)
        self.assertEqual(code, 2)
        self.assertIn("gofmt -w", err)

    def test_vet_failure_fed_back(self):
        write(self.f, 'package pkg\n\nimport "fmt"\n\nfunc A() {\n\tfmt.Printf("%d", "x")\n}\n')
        code, err = run("post", self.edit(), self.d)
        self.assertEqual(code, 2)
        self.assertIn("go vet failed", err)

    def test_clean_go_passes(self):
        write(self.f, "package pkg\n\nfunc A() {}\n")
        self.assertEqual(run("post", self.edit(), self.d)[0], 0)

    def test_post_ignores_non_edit_tools(self):
        write(self.f, "package pkg\nfunc  A( ) {  }\n")
        self.assertEqual(run("post", bash("ls"), self.d)[0], 0)

    def test_opencode_shape_without_paths_uses_git_state(self):
        write(self.f, "package pkg\nfunc  A( ) {  }\n")
        code, err = run("post", {"tool": "edit", "args": {}}, self.d)
        self.assertEqual(code, 2)

    def test_stop_gate_red_and_green_and_no_loop(self):
        write(self.f, "package pkg\n\nfunc A() {}\n")
        write(os.path.join(self.d, "Makefile"), "check-fast:\n\t@echo boom >&2; exit 1\n")
        code, err = run("stop", {}, self.d)
        self.assertEqual(code, 2)
        self.assertIn("check-fast", err)
        self.assertEqual(run("stop", {"stop_hook_active": True}, self.d)[0], 0)  # never loops
        self.assertEqual(run("stop", {}, self.d, {"HARNESS_SKIP_STOP": "1"})[0], 0)
        write(os.path.join(self.d, "Makefile"), "check-fast:\n\t@true\n")
        self.assertEqual(run("stop", {}, self.d)[0], 0)

    def test_stop_gate_skips_readonly_turn(self):
        write(os.path.join(self.d, "Makefile"), "check-fast:\n\t@exit 1\n")
        subprocess.run(["git", "-C", self.d, "add", "-A"], check=True)
        subprocess.run(["git", "-C", self.d, "-c", "user.email=t@t", "-c", "user.name=t", "commit", "-q", "-m", "m"],
                       check=True)
        self.assertEqual(run("stop", {}, self.d)[0], 0)  # clean tree -> nothing to gate

    def test_changed_pkgs(self):
        write(self.f, "package pkg\n\nfunc A() {}\n")
        p = subprocess.run([sys.executable, HOOK, "changed-pkgs"], cwd=self.d, capture_output=True, text=True)
        self.assertEqual(p.stdout.strip(), "./pkg")


if __name__ == "__main__":
    unittest.main(verbosity=2)
