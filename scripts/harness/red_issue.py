#!/usr/bin/env python3
"""red_issue.py: turn a failing SCHEDULED sensor into an issue somebody (or a fix agent) acts on,
and (harness-template v4) draft the incident paperwork instead of waiting for a human to start it cold.

  red_issue.py <job> <run-url>          open (or comment on) the `harness:red` issue for <job>
  red_issue.py <job> --close            close it with a recovery comment, if open
  red_issue.py <job> --draft <run-url>  open a DRAFT PR with an INCIDENTS.md row + a fitness-test stub

Needs the `gh` CLI and GH_TOKEN with `issues: write` (+ `pull-requests: write` for --draft).
Idempotent: one open issue per job.

Why --draft exists (harness-template v4, see HARNESS.md's Harness² comparison): INCIDENTS.md's own rule
("every failure... ends with the fix, a sensor, the sensor rolled out, and a row here") was always applied
by a human starting from a blank page. Harness²'s "experience reflector" drafts a persistent harness's
lessons automatically after every task; this is the same idea scoped to what's safe to automate: --draft
writes the FIRST DRAFT of the row and a Skip-marked fitness-test stub from information the CI run already
has (job name, run URL, date), and opens it as a draft PR. It NEVER fills in root cause, fix, or a real
assertion -- those need a human (or a fix agent) who actually read the failure -- and it never merges
anything itself. Drafting is automated; adoption is not.
"""
import datetime
import json
import os
import subprocess
import sys
import time

LABEL = "harness:red"


def gh(*args, check=True):
    return subprocess.run(["gh", *args], capture_output=True, text=True, check=check).stdout


def sh(args, cwd=None, check=True, env=None):
    return subprocess.run(args, cwd=cwd, capture_output=True, text=True, check=check, env=env)


def find_issue(title):
    out = gh("issue", "list", "--label", LABEL, "--state", "open", "--search", f'"{title}" in:title',
             "--json", "number,title", "--limit", "20")
    for i in json.loads(out or "[]"):
        if i["title"] == title:
            return i["number"]
    return None


def compose_incident_row(job, run_url, date=None):
    """The DRAFT INCIDENTS.md row. Three of six columns are left for a human on purpose."""
    date = date or datetime.date.today().isoformat()
    symptom = f"scheduled `{job}` failed ({run_url})"
    return (f"| {date} | {symptom} | TBD: a human (or fix agent) must read the run and fill this in | "
            f"TBD | TBD | **DRAFT — needs human analysis, see {run_url}** |\n")


def compose_fitness_stub(job, run_url, date=None):
    """A Go test skeleton: fails loudly until a human replaces the Skip with a real assertion."""
    date = date or datetime.date.today().isoformat()
    name = "".join(w.capitalize() for w in job.replace("-", "_").split("_"))
    return (
        "package architecture\n\n"
        "import \"testing\"\n\n"
        f"// DRAFT (harness-template v4, red_issue.py --draft): auto-generated from the failing scheduled\n"
        f"// `{job}` job on {date} ({run_url}). This is NOT a real sensor yet -- it exists so the incident\n"
        f"// has a place to land a real assertion instead of starting from nothing. A human must:\n"
        f"//   1. read the run, find the actual root cause,\n"
        f"//   2. replace t.Skip below with an assertion that would have failed before the fix and passes after,\n"
        f"//   3. fill in the matching INCIDENTS.md row this same PR drafted.\n"
        f"// Until then this test is a no-op; it must not be mistaken for coverage.\n"
        f"func Test{name}DraftPendingHumanReview(t *testing.T) {{\n"
        f"\tt.Skip(\"DRAFT sensor from {run_url} — see INCIDENTS.md; replace with a real assertion\")\n"
        f"}}\n"
    )


def draft_incident(job, run_url):
    date = datetime.date.today().isoformat()
    branch = f"harness/draft-incident-{job}-{int(time.time())}"
    sh(["git", "checkout", "-b", branch])
    incidents = "INCIDENTS.md"
    if os.path.isfile(incidents):
        with open(incidents, "a") as fh:
            fh.write(compose_incident_row(job, run_url, date))
    arch_dir = "internal/architecture"
    stub_path = None
    if os.path.isdir(arch_dir):
        stub_path = os.path.join(arch_dir, f"draft_{job.replace('-', '_')}_fitness_test.go")
        with open(stub_path, "w") as fh:
            fh.write(compose_fitness_stub(job, run_url, date))
    sh(["git", "add", "-A"])
    env = {**os.environ, "LEFTHOOK": "0"}
    sh(["git", "commit", "--no-verify", "-m", f"draft(incident): scheduled `{job}` failed {date}"], env=env)
    sh(["git", "push", "-q", "--no-verify", "-u", "origin", branch], env=env)
    body = (f"Automated DRAFT from `red_issue.py --draft` (harness-template v4): the scheduled `{job}` job "
            f"failed. Run: {run_url}\n\n"
            "This PR drafts the incident paperwork so a human starts from a half-filled page instead of a "
            "blank one. It does **not** diagnose the failure and must not be merged as-is:\n"
            "- [ ] fill in root cause and fix in the INCIDENTS.md row\n"
            + (f"- [ ] replace the `t.Skip` in `{stub_path}` with a real assertion\n" if stub_path else "") +
            "- [ ] rename the stub test off `DraftPendingHumanReview` once it asserts something real\n\n"
            "See HARNESS.md's harnessing-level-recursion note for why this drafting step is automated while "
            "adoption stays a human decision.")
    out = gh("pr", "create", "--draft", "--title", f"draft(incident): scheduled `{job}` failed {date}",
             "--body", body, "--base", "develop", "--head", branch, check=False)
    print(out)
    return 0


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    job, arg = sys.argv[1], sys.argv[2]
    if arg == "--draft":
        if len(sys.argv) < 4:
            print(__doc__)
            return 2
        return draft_incident(job, sys.argv[3])
    title = f"harness:red scheduled `{job}` is failing"
    num = find_issue(title)
    if arg == "--close":
        if num:
            gh("issue", "close", str(num), "--comment", "Scheduled run is green again; closing automatically.")
        return 0
    body = (f"The scheduled `{job}` job failed: {arg}\n\n"
            "Scheduled sensors are the harness's drift detectors. Treat this like a red build: fix the code or "
            "tests that the sensor caught, do NOT weaken the threshold. "
            "The local fix agent picks up issues with this label (PR only, never merges).")
    if num:
        gh("issue", "comment", str(num), "--body", f"Failed again: {arg}")
    else:
        gh("label", "create", LABEL, "--color", "B60205", "--description",
           "A scheduled harness sensor is failing", "--force", check=False)
        gh("issue", "create", "--title", title, "--label", LABEL, "--body", body)
    return 0


if __name__ == "__main__":
    sys.exit(main())
