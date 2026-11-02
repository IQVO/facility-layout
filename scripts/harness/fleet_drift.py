#!/usr/bin/env python3
"""Fail if a repo's .claude/rules/fleet/*.md differ from the canonical copy in IQVO/warehouse-docs.

Two canonical sets live in warehouse-docs main (read with `gh api`):
  agents/fleet/          the warehouse fleet rules
  agents/retailnetwork/  the retail-network variant (a network CloudEvents rule; the rest shared)
The repo's own .claude/rules/fleet/cloudevents.md picks the set: an exact match against one
canonical cloudevents.md chooses that set, then every file of that set is compared. A
cloudevents.md matching neither set is drift, reported against the closer set (more exact
file matches). A repo whose cloudevents.md is the retailnetwork variant while
agents/retailnetwork/ is not published yet is a hard error (exit 1), never a silent pass.

Repos never hand-edit these files: edit warehouse-docs first, then `python3
tools/migrate_v3.py <repo>` from an updated template.
Usage: fleet_drift.py [--repo-root .]   exit 0 = in sync (or no fleet dir), 1 = drift,
2 = canonical rules unreadable.
"""
import argparse, base64, json, os, subprocess, sys

DISCRIMINATOR = "cloudevents.md"
SET_NAMES = ("fleet", "retailnetwork")

ap = argparse.ArgumentParser()
ap.add_argument("--repo-root", default=".")
ap.add_argument("--docs-repo", default="IQVO/warehouse-docs")
a = ap.parse_args()
local = os.path.join(a.repo_root, ".claude/rules/fleet")
if not os.path.isdir(local):
    print("fleet-drift: no .claude/rules/fleet here; nothing to check")
    sys.exit(0)


def gh(path, allow_missing=False):
    r = subprocess.run(["gh", "api", f"repos/{a.docs_repo}/contents/{path}?ref=main"], capture_output=True, text=True)
    if r.returncode:
        if allow_missing and "Not Found" in r.stderr:
            return None
        print(f"fleet-drift: cannot read {path} from {a.docs_repo} ({r.stderr.strip()[:120]})")
        sys.exit(2)
    return json.loads(r.stdout)


def fetch_set(name):
    """-> {filename: content} for agents/<name>/, or None if that set is not published."""
    listing = gh(f"agents/{name}", allow_missing=(name != "fleet"))
    if listing is None:
        return None
    return {e["name"]: base64.b64decode(gh(e["path"])["content"]).decode()
            for e in listing if e["name"] != "README.md" and e["name"].endswith(".md")}


canon = {name: fetch_set(name) for name in SET_NAMES}
mine = {f: open(os.path.join(local, f)).read() for f in os.listdir(local) if f.endswith(".md")}


def exact_matches(canon_set):
    return sum(1 for n, c in canon_set.items() if mine.get(n) == c)


def drift_lines(set_name):
    c = canon[set_name]
    return [f"{n}: {'missing locally' if n not in mine else f'differs from agents/{set_name}'}"
            for n in c if mine.get(n) != c[n]] + \
           [f"{n}: not in canonical set agents/{set_name}" for n in mine if n not in c]


def fail(what, fix):
    print("FLEET DRIFT\n  WHAT: " + what + "\n"
          "  WHY:  these rules are one standard per namespace; hand edits silently fork it.\n"
          "  FIX:  " + fix)
    sys.exit(1)


# Pick the canonical set by content: the local cloudevents.md decides.
chosen = next((name for name in SET_NAMES
               if canon[name] is not None and DISCRIMINATOR in mine
               and mine[DISCRIMINATOR] == canon[name].get(DISCRIMINATOR)), None)

if chosen is None and DISCRIMINATOR in mine and "retailnetwork" in mine[DISCRIMINATOR] \
        and canon["retailnetwork"] is None:
    fail("canonical set agents/retailnetwork not published yet in "
         f"{a.docs_repo} (this repo's {DISCRIMINATOR} is the retailnetwork variant, so it is not "
         "checked against agents/fleet)",
         f"merge the warehouse-docs PR that adds agents/retailnetwork/, then re-run; do NOT "
         f"point this repo at agents/fleet -- its CloudEvents rule is the warehouse variant")

if chosen is None:
    # Drift: cloudevents.md matches no canonical set (or is absent locally). Report
    # against the closer set -- more exact file matches; ties go to fleet.
    published = [n for n in SET_NAMES if canon[n] is not None]
    chosen = max(published, key=lambda n: (exact_matches(canon[n]), n == "fleet"))
    bad = drift_lines(chosen)
    fail(f"{DISCRIMINATOR} matches no canonical set; closest is agents/{chosen} -- "
         + "; ".join(bad),
         f"edit {a.docs_repo} agents/{chosen}/ first, then re-run tools/migrate_v3.py from the "
         "updated template; if this repo belongs to the other namespace, its canonical set must "
         "exist in warehouse-docs")

bad = drift_lines(chosen)
if bad:
    fail("; ".join(bad),
         f"edit {a.docs_repo} agents/{chosen}/ first, then re-run tools/migrate_v3.py from the updated template.")
print(f"fleet-drift: {len(mine)} rule(s) in sync with {a.docs_repo} agents/{chosen}")
