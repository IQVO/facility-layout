#!/usr/bin/env python3
"""guide-lint: a computational sensor for the agent GUIDES (harness-template v3).

Guides rot silently: a rule cites a file that was renamed, a skill is a flat
file the runtime never loads, an always-on CLAUDE.md grows to 1,000 lines.
This lint fails fast on all three, at PR time, with LLM-actionable messages.

Checks
  skills     .claude/skills/<name>/SKILL.md with frontmatter `name` (== dir) and
             `description`. A flat .claude/skills/*.md NEVER LOADS in Claude Code,
             OpenCode or Codex -> error. Description <= 1024 chars (OpenCode cap).
  rules      .claude/rules/*.md `paths:` frontmatter, if present, must be a list.
  refs       backticked repo paths, `make <target>` and ADR-NNNN cited by
             CLAUDE.md / .claude/** must exist (fenced code blocks and lines that
             talk about removed/planned things are skipped).
  budget     size of the ALWAYS-LOADED context (CLAUDE.md + rules without
             `paths:`); warning above --max-claude-lines / --max-always-lines,
             error with --strict-budget.

Usage:  python3 scripts/harness/guide_lint.py [--root .] [--strict-budget] [--json]
Exit:   0 clean (warnings allowed), 1 errors.
Suppress one line with the HTML comment  <!-- guide-lint: ignore -->
"""
from __future__ import annotations

import argparse
import glob
import json
import os
import re
import sys

PATH_ROOTS = ("internal", "cmd", "apis", "charts", "web", "docs", "scripts", "migrations", "features",
              ".github", ".claude", ".opencode", ".codex", ".agents", "deploy", "pkg", "test", "terraform",
              "templates", "evals")
TOP_FILES = {"Makefile", "Dockerfile", "go.mod", "go.sum", "lefthook.yml", "CLAUDE.md", "AGENTS.md",
             ".gremlins.yaml", ".golangci.yml", "HARNESS.md", "opencode.json"}
HISTORY_WORDS = re.compile(
    r"\b(removed|deleted|retired|no longer|formerly|previously|planned|future|not yet|deferred|proposed|"
    r"would|e\.g\.|for example|example|such as|renamed|legacy|used to|was)\b", re.I)
IGNORE = "guide-lint: ignore"


def read(path: str) -> list[str]:
    with open(path, encoding="utf8") as fh:
        return fh.read().splitlines()


def frontmatter(lines: list[str]) -> tuple[dict, int]:
    """Minimal YAML-subset frontmatter parser. Returns (fields, body_start_index)."""
    if not lines or lines[0].strip() != "---":
        return {}, 0
    fm: dict = {}
    key = None
    for i in range(1, len(lines)):
        ln = lines[i]
        if ln.strip() == "---":
            return fm, i + 1
        m = re.match(r"^([A-Za-z0-9_-]+):\s*(.*)$", ln)
        if m:
            key, val = m.group(1), m.group(2).strip()
            if val in ("|", ">", "|-", ">-"):
                fm[key] = ""
            elif val.startswith("[") and val.endswith("]"):
                fm[key] = [v.strip().strip("\"'") for v in val[1:-1].split(",") if v.strip()]
            else:
                fm[key] = val.strip("\"'")
        elif key is not None and ln.startswith(("  - ", "- ")):
            if not isinstance(fm.get(key), list):
                fm[key] = []
            fm[key].append(ln.split("-", 1)[1].strip().strip("\"'"))
        elif key is not None and ln.startswith((" ", "\t")) and isinstance(fm.get(key), str):
            fm[key] = (fm[key] + " " + ln.strip()).strip()
    return {}, 0  # unterminated


class Lint:
    def __init__(self, root: str):
        self.root = root
        self.errors: list[tuple[str, int, str]] = []
        self.warnings: list[tuple[str, int, str]] = []
        mk = os.path.join(root, "Makefile")
        self.make_targets = set()
        if os.path.isfile(mk):
            for ln in read(mk):
                m = re.match(r"^([A-Za-z0-9_.%-][A-Za-z0-9_.% -]*):(?!=)", ln)
                if m:
                    self.make_targets.update(m.group(1).split())
                m = re.match(r"^\.PHONY:\s*(.*)", ln)
                if m:
                    self.make_targets.update(m.group(1).split())
        adr_dir = os.path.join(root, "docs", "docs", "adr")
        self.adr_dir_exists = os.path.isdir(adr_dir)
        self.adrs = set(re.findall(r"(\d{4})", " ".join(os.listdir(adr_dir)))) if self.adr_dir_exists else set()

    def err(self, f, n, m):
        self.errors.append((os.path.relpath(f, self.root), n, m))

    def warn(self, f, n, m):
        self.warnings.append((os.path.relpath(f, self.root), n, m))

    # ------------------------------------------------------------------ skills
    def check_skills(self):
        base = os.path.join(self.root, ".claude", "skills")
        if not os.path.isdir(base):
            return
        for entry in sorted(os.listdir(base)):
            p = os.path.join(base, entry)
            if os.path.isfile(p) and entry.endswith(".md"):
                self.err(p, 1, f"flat skill file never loads in any runtime. FIX: move it to "
                               f".claude/skills/{entry[:-3]}/SKILL.md and add frontmatter (name, description).")
                continue
            if not os.path.isdir(p):
                continue
            sk = os.path.join(p, "SKILL.md")
            if not os.path.isfile(sk):
                self.err(p, 1, "skill directory without SKILL.md. FIX: add SKILL.md with name+description frontmatter.")
                continue
            fm, _ = frontmatter(read(sk))
            if not fm:
                self.err(sk, 1, "missing/unterminated YAML frontmatter. FIX: start the file with "
                                "'---\\nname: <dir>\\ndescription: <what it does + when to use it>\\n---'.")
                continue
            if fm.get("name") != entry:
                self.err(sk, 1, f"frontmatter name {fm.get('name')!r} must equal the directory name {entry!r}.")
            if not re.match(r"^[a-z0-9]+(-[a-z0-9]+)*$", entry) or len(entry) > 64:
                self.err(sk, 1, "skill name must be lowercase-hyphen, <= 64 chars.")
            d = str(fm.get("description", "")).strip()
            if not d:
                self.err(sk, 1, "empty description: it is the ONLY routing signal (OpenCode/Codex show nothing else). "
                                "FIX: say what the skill does AND when to use it.")
            elif len(d) > 1024:
                self.err(sk, 1, f"description is {len(d)} chars (> 1024 OpenCode cap). FIX: shorten it.")
            elif fm.get("disable-model-invocation") != "true" and not re.search(r"\b(use when|use for|when)\b", d, re.I):
                self.warn(sk, 1, "description has no 'Use when ...' trigger clause; routing quality will be poor.")

    # ------------------------------------------------------------------- rules
    def always_loaded_lines(self) -> tuple[int, int]:
        claude = 0
        cm = os.path.join(self.root, "CLAUDE.md")
        if os.path.isfile(cm):
            claude = len(read(cm))
        rules = 0
        for f in sorted(glob.glob(os.path.join(self.root, ".claude", "rules", "**", "*.md"), recursive=True)):
            lines = read(f)
            fm, start = frontmatter(lines)
            if "paths" in fm:
                if not isinstance(fm["paths"], list) or not fm["paths"]:
                    self.err(f, 1, "`paths:` must be a non-empty YAML list of globs.")
                continue
            rules += len(lines)
        return claude, rules

    def check_budget(self, max_claude: int, max_always: int, strict: bool):
        claude, rules = self.always_loaded_lines()
        add = self.err if strict else self.warn
        cm = os.path.join(self.root, "CLAUDE.md")
        if claude > max_claude:
            add(cm, 1, f"CLAUDE.md is {claude} lines (budget {max_claude}). FIX: keep it a map: purpose, hard rules, "
                       "commands, pointers. Move detail into path-scoped .claude/rules/ or skills.")
        if claude + rules > max_always:
            add(cm, 1, f"always-loaded context is {claude + rules} lines (CLAUDE.md {claude} + unscoped rules "
                       f"{rules}; budget {max_always}). FIX: add `paths:` frontmatter to rules that only matter "
                       "for part of the tree.")
        return claude, rules

    # -------------------------------------------------------------------- refs
    def guide_files(self) -> list[str]:
        files = []
        for pat in ("CLAUDE.md", ".claude/rules/**/*.md", ".claude/skills/**/*.md"):
            files += glob.glob(os.path.join(self.root, pat), recursive=True)
        return sorted(set(files))

    def check_refs(self):
        for f in self.guide_files():
            in_fence = False
            for n, ln in enumerate(read(f), 1):
                if ln.lstrip().startswith("```"):
                    in_fence = not in_fence
                    continue
                if in_fence or IGNORE in ln or "{{" in ln or HISTORY_WORDS.search(ln):
                    continue
                for tok in re.findall(r"`([^`\n]+)`", ln):
                    self.check_token(f, n, tok.strip())
                if self.adr_dir_exists:
                    for num in re.findall(r"\bADR[- ]?(\d{4})\b", ln, re.I):
                        if num not in self.adrs:
                            self.err(f, n, f"cites ADR-{num} but docs/docs/adr has no such file. FIX: correct the "
                                           "number or remove the reference.")

    def check_token(self, f, n, tok):
        m = re.match(r"^make\s+([A-Za-z0-9_.-]+)", tok)
        if m:
            t = m.group(1)
            if self.make_targets and t not in self.make_targets and not t.startswith("-"):
                self.err(f, n, f"`make {t}` is not a Makefile target. FIX: use a real target "
                               f"({', '.join(sorted(self.make_targets)[:8])} ...) or drop the reference.")
            return
        if any(c in tok for c in "*<>{}$|()=\" ") or tok.startswith(("http", "/", "~", "-")) \
                or "NNNN" in tok or ".." in tok:
            return
        path = re.sub(r"(:\d+(-\d+)?|#[\w-]+)$", "", tok).rstrip("/.,;")
        first = path.split("/")[0]
        is_path = (first in PATH_ROOTS and "/" in path) or path in TOP_FILES
        if not is_path:
            return
        full = os.path.join(self.root, path)
        if not os.path.exists(full) and "." not in os.path.basename(path) and glob.glob(full + "*"):
            return  # prefix of a numbered file, e.g. docs/docs/adr/0001
        sym = re.match(r"^(.*?)\.([A-Z][A-Za-z0-9_]*)$", path)
        if not os.path.exists(full) and sym and os.path.exists(os.path.join(self.root, sym.group(1))):
            return  # Go symbol reference: internal/domain/charge.NewChargeForecast
        if not os.path.exists(full):
            self.err(f, n, f"cites `{path}` which does not exist. FIX: update the reference to the current path "
                           "(grep/git log --follow) or delete it; stale guides mislead agents.")


def main() -> int:
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("--root", default=".")
    ap.add_argument("--max-claude-lines", type=int, default=150)
    ap.add_argument("--max-always-lines", type=int, default=450)
    ap.add_argument("--strict-budget", action="store_true")
    ap.add_argument("--json", action="store_true")
    a = ap.parse_args()
    root = os.path.abspath(a.root)
    lint = Lint(root)
    lint.check_skills()
    claude, rules = lint.check_budget(a.max_claude_lines, a.max_always_lines, a.strict_budget)
    lint.check_refs()
    if a.json:
        print(json.dumps({"errors": lint.errors, "warnings": lint.warnings, "claude_lines": claude,
                          "unscoped_rule_lines": rules}, indent=2))
    else:
        for f, n, m in lint.warnings:
            print(f"warning: {f}:{n}: {m}")
        for f, n, m in lint.errors:
            print(f"error: {f}:{n}: {m}")
        print(f"guide-lint: {len(lint.errors)} error(s), {len(lint.warnings)} warning(s); "
              f"always-loaded context = CLAUDE.md {claude} + unscoped rules {rules} lines")
    return 1 if lint.errors else 0


if __name__ == "__main__":
    sys.exit(main())
