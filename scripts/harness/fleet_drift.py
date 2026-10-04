#!/usr/bin/env python3
"""Fail if a repo's .claude/rules/fleet/*.md differ from the canonical copy in IQVO/warehouse-docs.

Canonical: warehouse-docs main, agents/fleet/*.md (read with `gh api`). Repos never hand-edit these files:
edit warehouse-docs first, then `python3 tools/migrate_v3.py <repo>` from an updated template.
Usage: fleet_drift.py [--repo-root .]   exit 0 = in sync (or no fleet dir), 1 = drift.
"""
import argparse, base64, json, os, subprocess, sys

ap = argparse.ArgumentParser()
ap.add_argument("--repo-root", default=".")
ap.add_argument("--docs-repo", default="IQVO/warehouse-docs")
a = ap.parse_args()
local = os.path.join(a.repo_root, ".claude/rules/fleet")
if not os.path.isdir(local):
    print("fleet-drift: no .claude/rules/fleet here; nothing to check")
    sys.exit(0)

def gh(path):
    r = subprocess.run(["gh", "api", f"repos/{a.docs_repo}/contents/{path}?ref=main"], capture_output=True, text=True)
    if r.returncode:
        print(f"fleet-drift: cannot read {path} from {a.docs_repo} ({r.stderr.strip()[:120]})")
        sys.exit(2)
    return json.loads(r.stdout)

canon = {e["name"]: base64.b64decode(gh(e["path"])["content"]).decode() for e in gh("agents/fleet") if e["name"] != "README.md" and e["name"].endswith(".md")}
mine = {f: open(os.path.join(local, f)).read() for f in os.listdir(local) if f.endswith(".md")}
bad = [f"{n}: {'missing locally' if n not in mine else 'differs'}" for n in canon if mine.get(n) != canon[n]] + [f"{n}: not in canonical set" for n in mine if n not in canon]
if bad:
    print("FLEET DRIFT\n  WHAT: " + "; ".join(bad) + "\n  WHY:  these rules are one fleet-wide standard; hand edits silently fork it.\n  FIX:  edit IQVO/warehouse-docs agents/fleet/ first, then re-run tools/migrate_v3.py from the updated template.")
    sys.exit(1)
print(f"fleet-drift: {len(mine)} rule(s) in sync with {a.docs_repo}")
