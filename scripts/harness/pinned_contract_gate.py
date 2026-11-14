#!/usr/bin/env python3
"""Fail when a contract copy pinned in this repo differs from the producer's develop.

A consumer's contract test validates against a COPY of the producer's spec. A copy
nobody refreshes keeps the test green while the producer moves on: on 2026-11-01
vendor-connectivity changed every collection to { items, nextCursor } and this
repo's test, still pinned to the 2026-10-24 copy, stayed green while the poller
would have read 200 and zero purchase orders. This gate compares each pin in
contracts/pinned.json with the producer's branch (read with `gh api`, the raw media
type) and fails on any byte difference.

Usage: pinned_contract_gate.py [--repo-root .]
Exit 0 = every pin equals its producer, 1 = a pin is stale, 2 = a producer could not be read
(an unreadable producer is NOT a pass: a gate that cannot look must say so).
"""
import argparse
import difflib
import json
import os
import subprocess
import sys


def fetch(repo, path, ref):
    r = subprocess.run(
        ["gh", "api", "-H", "Accept: application/vnd.github.raw", f"repos/{repo}/contents/{path}?ref={ref}"],
        capture_output=True, text=True,
    )
    if r.returncode:
        raise RuntimeError(f"cannot read {repo}:{path}@{ref}: {r.stderr.strip()[:160]}")
    return r.stdout


def compare(pin, local_text, producer_text):
    """Return the lines of a human report; empty when the copy equals the producer."""
    if local_text == producer_text:
        return []
    diff = list(difflib.unified_diff(
        local_text.splitlines(), producer_text.splitlines(),
        fromfile=f"pinned {pin['local']}", tofile=f"{pin['repo']}:{pin['path']}@{pin['ref']}", lineterm="", n=0))
    head = diff[:30]
    more = [f"... {len(diff) - 30} more diff lines"] if len(diff) > 30 else []
    return [
        f"STALE PIN {pin['local']}",
        f"  WHAT: differs from {pin['repo']} {pin['path']} on {pin['ref']} ({len(diff)} diff lines)",
        "  WHY:  the contract test validates against this copy, so it passes while the producer has moved on",
        "  FIX:  copy the producer's file over the pin, update the provenance note in the contract test and the ADR, fix what fails",
        *head, *more,
    ]


def main(argv=None):
    ap = argparse.ArgumentParser()
    ap.add_argument("--repo-root", default=".")
    ap.add_argument("--fetch", default=None, help=argparse.SUPPRESS)
    a = ap.parse_args(argv)
    with open(os.path.join(a.repo_root, "contracts", "pinned.json")) as fh:
        pins = json.load(fh)["pins"]
    bad, unreadable = [], []
    for pin in pins:
        with open(os.path.join(a.repo_root, pin["local"])) as fh:
            local = fh.read()
        try:
            producer = fetch(pin["repo"], pin["path"], pin["ref"])
        except RuntimeError as e:
            unreadable.append(str(e))
            continue
        bad += compare(pin, local, producer)
    if unreadable:
        print("pinned-contract-gate: " + "; ".join(unreadable))
        return 2
    if bad:
        print("\n".join(bad))
        return 1
    print(f"pinned-contract-gate: {len(pins)} pin(s) equal their producer's branch")
    return 0


if __name__ == "__main__":
    sys.exit(main())
