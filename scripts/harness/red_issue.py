#!/usr/bin/env python3
"""red_issue.py: turn a failing SCHEDULED sensor into an issue somebody (or a fix agent) acts on.

  red_issue.py <job> <run-url>     open (or comment on) the `harness:red` issue for <job>
  red_issue.py <job> --close       close it with a recovery comment, if open

Needs the `gh` CLI and GH_TOKEN with `issues: write`. Idempotent: one open issue per job.
Why: an advisory sensor with no reader is blindness (weekly mutation was red for a week in
three repos and nobody noticed). The local fix agent (scripts/harness/fix-agent.sh in the
harness template) consumes these issues.
"""
import json
import subprocess
import sys

LABEL = "harness:red"


def gh(*args, check=True):
    return subprocess.run(["gh", *args], capture_output=True, text=True, check=check).stdout


def find_issue(title):
    out = gh("issue", "list", "--label", LABEL, "--state", "open", "--search", f'"{title}" in:title',
             "--json", "number,title", "--limit", "20")
    for i in json.loads(out or "[]"):
        if i["title"] == title:
            return i["number"]
    return None


def main():
    if len(sys.argv) < 3:
        print(__doc__)
        return 2
    job, arg = sys.argv[1], sys.argv[2]
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
