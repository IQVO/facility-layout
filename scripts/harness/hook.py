#!/usr/bin/env python3
"""Runtime-agnostic harness hook logic (harness-template v3).

ONE implementation shared by every coding-agent runtime in this fleet:

  Claude Code   .claude/settings.json          -> hook.py pre|post|stop
  Codex         .codex/hooks.json              -> hook.py pre|post|stop
  OpenCode      .opencode/plugins/harness.ts   -> hook.py pre|post|stop

Contract (identical to Claude Code's command hooks): JSON payload on stdin,
exit 0 = allow / no finding, exit 2 = block (PreToolUse, Stop) or feed the
message back to the model (PostToolUse), message on stderr. Every message is
written for an LLM reader: WHAT is wrong, WHY (the incident), and the exact
FIX. stdlib only. Never raises: an internal error exits 0 (a broken guard must
not brick the agent), the lefthook/CI gates remain the hard backstop.

Subcommands
  pre             PreToolUse guard (dangerous shell, protected paths)
  post            PostToolUse feedback after an edit (gofmt + go vet, scoped)
  stop            Stop gate: `make check-fast` must be green before "done"
  changed-pkgs    print Go packages touched vs HEAD (used by `make check-fast`)

Environment knobs
  HARNESS_OFF=1                 disable everything (debugging the harness itself)
  HARNESS_PROTECT_THRESHOLDS=1  also protect gates (fix-agent mode): the agent
                                may not edit .gremlins.yaml/.golangci.yml/
                                fitness tests/CI to make a red check green
  HARNESS_ALLOW_FORCE_PUSH=1    permit force pushes (user explicitly authorised)
  HARNESS_ALLOW_DESTRUCTIVE=1   permit reset --hard / clean -f / rm -rf
  HARNESS_SKIP_STOP=1           skip the stop gate
"""
from __future__ import annotations

import fnmatch
import json
import os
import re
import shlex
import subprocess
import sys

PROTECTED_BRANCHES = ("develop", "main")

# Paths an agent must never hand-edit: they are generated from a source of truth.
GENERATED_GLOBS = [
    "docs/docs/api/*",          # docusaurus-plugin-openapi-docs output
    "docs/static/asyncapi/*",   # generated AsyncAPI HTML
    "*.pb.go",
    "*_gen.go",
    "*/zz_generated*",
]
# Extra per-repo globs, one per line, '#' comments allowed.
LOCAL_PROTECTED_FILE = "scripts/harness/protected-paths.txt"

# Gates that fix-agent mode may not touch (making a red check green by
# weakening the check is the classic failure of an unsupervised agent).
THRESHOLD_GLOBS = [
    ".gremlins.yaml",
    ".golangci.yml",
    "internal/architecture/*",
    "lefthook.yml",
    ".github/workflows/*",
    "scripts/harness/*",
    ".claude/settings.json",
    ".codex/*",
    ".opencode/*",
]

SHELL_TOOLS = {"bash", "shell", "local_shell", "exec_command", "run_shell_command"}
EDIT_TOOLS = {"edit", "write", "multiedit", "apply_patch", "patch", "notebookedit"}

SCRATCH_PREFIXES = ("/tmp/", "/private/tmp/", "/var/folders/", "node_modules", "dist", ".tmp")


def eprint(msg: str) -> None:
    print(msg.rstrip() + "\n", file=sys.stderr)


def run(cmd: list[str], cwd: str, timeout: int = 120) -> tuple[int, str]:
    try:
        p = subprocess.run(cmd, cwd=cwd, capture_output=True, text=True, timeout=timeout)
        return p.returncode, (p.stdout or "") + (p.stderr or "")
    except subprocess.TimeoutExpired:
        return 124, f"timed out after {timeout}s: {' '.join(cmd)}"
    except FileNotFoundError as e:
        return 127, str(e)


def repo_root() -> str:
    code, out = run(["git", "rev-parse", "--show-toplevel"], os.getcwd(), 10)
    return out.strip() if code == 0 and out.strip() else os.getcwd()


def current_branch(root: str) -> str:
    code, out = run(["git", "branch", "--show-current"], root, 10)
    return out.strip() if code == 0 else ""


def load_payload() -> dict:
    try:
        raw = sys.stdin.read()
        return json.loads(raw) if raw.strip() else {}
    except Exception:
        return {}


def normalise(payload: dict) -> tuple[str, str, list[str]]:
    """-> (lower-case tool name, shell command or '', touched paths)."""
    tool = str(payload.get("tool_name") or payload.get("tool") or "").lower()
    ti = payload.get("tool_input") or payload.get("args") or {}
    if not isinstance(ti, dict):
        ti = {}
    command = ""
    for k in ("command", "cmd"):
        v = ti.get(k)
        if isinstance(v, str):
            command = v
        elif isinstance(v, list):
            command = " ".join(str(x) for x in v)
        if command:
            break
    paths: list[str] = []
    for k in ("file_path", "filePath", "path", "notebook_path"):
        v = ti.get(k)
        if isinstance(v, str) and v:
            paths.append(v)
    # apply_patch (Codex) / patch: paths live inside the patch text
    blob = " ".join(str(v) for v in ti.values() if isinstance(v, str))
    for m in re.finditer(r"\*\*\* (?:Add|Update|Delete) File: (.+)|\*\*\* Move to: (.+)", blob):
        paths.append((m.group(1) or m.group(2)).strip())
    return tool, command, paths


def rel(root: str, p: str) -> str:
    p = os.path.realpath(p if os.path.isabs(p) else os.path.join(root, p))
    try:
        return os.path.relpath(p, os.path.realpath(root))
    except ValueError:
        return p


def local_globs(root: str) -> list[str]:
    path = os.path.join(root, LOCAL_PROTECTED_FILE)
    if not os.path.isfile(path):
        return []
    out = []
    for line in open(path, encoding="utf8"):
        line = line.strip()
        if line and not line.startswith("#"):
            out.append(line)
    return out


def matches(path: str, globs: list[str]) -> str | None:
    for g in globs:
        if fnmatch.fnmatch(path, g) or fnmatch.fnmatch(path, g.rstrip("*") + "*"):
            return g
    return None


# --------------------------------------------------------------------------- pre
def check_shell(root: str, command: str) -> str | None:
    if not command.strip():
        return None
    # Split on shell separators so `a && git push origin develop` is seen.
    segments = [s.strip() for s in re.split(r"&&|\|\||;|\n|\|", command) if s.strip()]
    for seg in segments:
        try:
            toks = shlex.split(seg)
        except ValueError:
            toks = seg.split()
        if not toks:
            continue
        # strip env assignments and `sudo`
        while toks and (re.match(r"^[A-Za-z_][A-Za-z0-9_]*=", toks[0]) or toks[0] == "sudo"):
            toks = toks[1:]
        if not toks:
            continue
        if toks[0] == "git":
            sub = next((t for t in toks[1:] if not t.startswith("-")), "")
            args = toks[toks.index(sub) + 1:] if sub in toks else []
            if "--no-verify" in toks or (sub == "commit" and "-n" in toks):
                return ("BLOCKED: --no-verify skips the lefthook gates.\n"
                        "WHY: the fleet's quality gates (fmt/vet/lint pre-commit, make check pre-push) "
                        "are the contract; bypassing them lands red code on a branch.\n"
                        "FIX: run `make check`, fix what it reports, then commit/push normally.")
            if sub == "push":
                force = any(t in ("-f", "--force", "--force-with-lease") or t.startswith("--force-with-lease=")
                            for t in args)
                if force and os.environ.get("HARNESS_ALLOW_FORCE_PUSH") != "1":
                    return ("BLOCKED: force-push needs the user's explicit authorisation.\n"
                            "WHY: force-push is irreversible; the user authorises it per occasion.\n"
                            "FIX: stop and ask the user for a one-word go (they set HARNESS_ALLOW_FORCE_PUSH=1 "
                            "if they approve). Prefer a new commit or a rebase-free fix.")
                refspecs = [t for t in args if not t.startswith("-")]
                # first positional is the remote; the rest are refspecs
                specs = refspecs[1:] if len(refspecs) > 1 else []
                hit = [s for s in specs
                       if re.sub(r"^(HEAD:|\+)?(refs/heads/)?", "", s.split(":")[-1]) in PROTECTED_BRANCHES]
                implicit = not specs and current_branch(root) in PROTECTED_BRANCHES
                if hit or implicit:
                    return ("BLOCKED: direct push to a protected branch (develop/main).\n"
                            "WHY: this fleet uses GitFlow with branch protection: feature/* -> PR into develop; "
                            "main is release-only. CI must be green before merge.\n"
                            "FIX: `git switch -c feature/<topic>`, push that branch, open a PR with "
                            "`gh pr create --base develop --body-file <file>`.")
            if sub in ("reset",) and "--hard" in args and os.environ.get("HARNESS_ALLOW_DESTRUCTIVE") != "1":
                return _destructive("git reset --hard")
            if sub == "clean" and any(re.match(r"^-[a-z]*f", a) or a == "--force" for a in args) \
                    and os.environ.get("HARNESS_ALLOW_DESTRUCTIVE") != "1":
                return _destructive("git clean -f")
            if sub == "checkout" and ("--" in args and any(a in (".", ":/") for a in args)) \
                    and os.environ.get("HARNESS_ALLOW_DESTRUCTIVE") != "1":
                return _destructive("git checkout -- .")
        if toks[0] == "rm":
            flags = "".join(t for t in toks[1:] if t.startswith("-") and not t.startswith("--"))
            recursive = "r" in flags or "R" in flags or "--recursive" in toks
            force = "f" in flags or "--force" in toks
            if recursive and force and os.environ.get("HARNESS_ALLOW_DESTRUCTIVE") != "1":
                targets = [t for t in toks[1:] if not t.startswith("-")]
                tmp = os.environ.get("TMPDIR", "").rstrip("/")
                def scratch(t: str) -> bool:
                    t = t.rstrip("/")
                    return (t.startswith(SCRATCH_PREFIXES) or (tmp and t.startswith(tmp))
                            or t.split("/")[-1] in ("node_modules", "dist", "bin", ".tmp"))
                if targets and not all(scratch(t) for t in targets):
                    return ("BLOCKED: bare `rm -rf` on non-scratch paths.\n"
                            "WHY: fleet rule: deletes of tracked files go through git so they are reviewable "
                            "and recoverable; `rm -rf` is denied by the environment anyway.\n"
                            "FIX: `git rm -r <path>` for tracked files; use a path under $TMPDIR for scratch; "
                            "node_modules/dist/bin are allowed.")
    return None


def _destructive(what: str) -> str:
    return (f"BLOCKED: `{what}` discards uncommitted work.\n"
            "WHY: other worktrees/sessions in this fleet routinely have unrelated dirty state; a discard is "
            "unrecoverable.\nFIX: `git stash` (or commit to a WIP branch) instead; ask the user if a discard "
            "is really intended.")


def check_paths(root: str, paths: list[str]) -> str | None:
    globs = GENERATED_GLOBS + local_globs(root)
    protect = os.environ.get("HARNESS_PROTECT_THRESHOLDS") == "1"
    for p in paths:
        r = rel(root, p)
        g = matches(r, globs)
        if g:
            return (f"BLOCKED: `{r}` is a generated artefact (matches `{g}`).\n"
                    "WHY: hand edits are overwritten on the next regeneration and cause docs/API drift.\n"
                    "FIX: edit the source of truth (apis/openapi.yaml, apis/asyncapi*.yaml, the .proto, or the "
                    "template) and regenerate (`make docs` / `make generate` / see CLAUDE.md).")
        if protect:
            g = matches(r, THRESHOLD_GLOBS)
            if g:
                return (f"BLOCKED: `{r}` is a quality gate (matches `{g}`) and this run is in fix-agent mode.\n"
                        "WHY: a red check must be fixed by fixing the code or tests, never by weakening the "
                        "gate that caught it.\n"
                        "FIX: change production code or add tests instead. If the gate itself is wrong, "
                        "label the issue `needs-human` and stop.")
    return None


def cmd_pre(payload: dict) -> int:
    root = repo_root()
    tool, command, paths = normalise(payload)
    msg = None
    if tool in SHELL_TOOLS or (command and tool not in EDIT_TOOLS):
        msg = check_shell(root, command)
    if msg is None and (tool in EDIT_TOOLS or paths):
        msg = check_paths(root, paths)
    if msg:
        eprint(msg)
        return 2
    return 0


# -------------------------------------------------------------------------- post
def git_changed(root: str) -> list[str]:
    code, out = run(["git", "status", "--porcelain", "--untracked-files=all"], root, 20)
    files = []
    if code == 0:
        for line in out.splitlines():
            path = line[3:].strip()
            if " -> " in path:
                path = path.split(" -> ")[-1]
            files.append(path.strip('"'))
    return files


def go_files(root: str, paths: list[str]) -> list[str]:
    cand = [rel(root, p) for p in paths] if paths else git_changed(root)
    return sorted({f for f in cand if f.endswith(".go") and os.path.isfile(os.path.join(root, f))
                   and not f.startswith(("node_modules/", "vendor/"))})


def cmd_post(payload: dict) -> int:
    root = repo_root()
    tool, _, paths = normalise(payload)
    if tool and tool not in EDIT_TOOLS:
        return 0
    files = go_files(root, [p for p in paths if p.endswith(".go")])
    if files:
        code, out = run(["gofmt", "-l", *files], root, 30)
        bad = [l for l in out.splitlines() if l.strip()] if code == 0 else []
        if bad:
            eprint("gofmt: these files are not formatted:\n  " + "\n  ".join(bad) +
                   "\nWHY: `make fmt-check` (pre-commit and CI) fails on unformatted Go.\n"
                   "FIX: run `gofmt -w " + " ".join(bad) + "` now, then continue.")
            return 2
        pkgs = sorted({"./" + os.path.dirname(f) if os.path.dirname(f) else "." for f in files})
        code, out = run(["go", "vet", *pkgs], root, 90)
        if code not in (0, 127, 124):
            tail = "\n".join(out.strip().splitlines()[-30:])
            eprint(f"go vet failed on {' '.join(pkgs)}:\n{tail}\n"
                   "WHY: `make vet` is part of `make check` (pre-commit/CI).\n"
                   "FIX: resolve the finding(s) above in the edited package before moving on.")
            return 2
    ts = [rel(root, p) for p in paths if p.endswith((".ts", ".tsx")) and rel(root, p).startswith("web/")]
    eslint = os.path.join(root, "web", "node_modules", ".bin", "eslint")
    if ts and os.path.isfile(eslint):
        code, out = run([eslint, "--no-warn-ignored", *[t[len("web/"):] for t in ts]], os.path.join(root, "web"), 90)
        if code not in (0, 124):
            eprint("eslint failed:\n" + "\n".join(out.strip().splitlines()[-30:]) +
                   "\nFIX: resolve the lint errors (`cd web && npm run lint`).")
            return 2
    return 0


# -------------------------------------------------------------------------- stop
def cmd_stop(payload: dict) -> int:
    if os.environ.get("HARNESS_SKIP_STOP") == "1" or payload.get("stop_hook_active"):
        print("{}")  # Codex requires JSON on stdout when a Stop hook exits 0; Claude accepts it
        return 0  # never loop: the host re-fires Stop after we block once
    root = repo_root()
    changed = [f for f in git_changed(root) if f.endswith((".go", ".ts", ".tsx", ".mod", ".sum"))]
    if not changed:
        print("{}")
        return 0  # read-only / docs-only turn
    if not os.path.isfile(os.path.join(root, "Makefile")):
        print("{}")
        return 0
    code, out = run(["make", "-s", "check-fast"], root, 600)
    if code == 0:
        print("{}")
        return 0
    tail = "\n".join(out.strip().splitlines()[-60:])
    eprint(f"`make check-fast` is RED (exit {code}); do not declare the task done.\n{tail}\n"
           "WHY: the local gate must be green before handing work back; CI/pre-push run the same checks.\n"
           "FIX: fix the failures above (fmt/vet/arch-test/changed-package tests), re-run `make check-fast`.")
    return 2


# ------------------------------------------------------------------ changed-pkgs
def cmd_changed_pkgs() -> int:
    root = repo_root()
    files = [f for f in git_changed(root) if f.endswith(".go")]
    pkgs = sorted({"./" + os.path.dirname(f) if os.path.dirname(f) else "." for f in files
                   if os.path.isfile(os.path.join(root, f))})
    # also packages whose files were deleted still need no test; skip.
    print(" ".join(pkgs))
    return 0


def main() -> int:
    if os.environ.get("HARNESS_OFF") == "1":
        return 0
    mode = sys.argv[1] if len(sys.argv) > 1 else ""
    try:
        if mode == "changed-pkgs":
            return cmd_changed_pkgs()
        payload = load_payload()
        if mode == "pre":
            return cmd_pre(payload)
        if mode == "post":
            return cmd_post(payload)
        if mode == "stop":
            return cmd_stop(payload)
        print(__doc__)
        return 0
    except Exception as e:  # a broken guard must not brick the agent
        print(f"[harness] internal error in `{mode}` (ignored): {e}", file=sys.stderr)
        return 0


if __name__ == "__main__":
    sys.exit(main())
