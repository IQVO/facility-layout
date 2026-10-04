#!/usr/bin/env python3
"""repo_lint: configuration sensors (harness v3). Stdlib only, so it runs on any CI runner.

Every check here exists because a real incident reached CI that a cheap static check would have caught
before the PR was opened. Each finding says WHAT is wrong, WHY it matters and the exact FIX, so an agent
can self-correct from the message alone (sensor output is a prompt).

Checks
  R1 workflow-paths   `working-directory` / `cache-dependency-path` in .github/workflows must exist.
                      (incident: a CI job ran in `warehouse-planning/web`, a directory the repo never had)
  R2 workflow-needs   `needs:` must name jobs that exist in the same workflow.
  R3 dependabot       every update directory exists and has the manifest its ecosystem needs; an npm
                      directory must not depend on a path OUTSIDE the repository (`file:../../x`):
                      Dependabot cannot fetch it, so the job fails on every run.
                      (incident: 7 repos, `npm_and_yarn in /web` red weekly)
  R4 owner-drift      no stale GitHub owner in URLs/registry names in .github/, charts/, docusaurus config.
                      (incident: GHCR pushes to a namespace that no longer exists; docs sitemap on a dead host)
  R5 placeholders     no unsubstituted {{TEMPLATE_PLACEHOLDER}} outside *.template files.

Usage: repo_lint.py [--root .] [--stale-owners claudioed,other]   exit 0 clean, 1 findings.
Suppress one line with the text `repo-lint: ignore` on it (use sparingly, say why).
"""
import argparse
import glob
import json
import os
import re
import sys

STALE_OWNERS_DEFAULT = ["claudioed"]
errors = []


def err(rule, path, line, what, why, fix):
    loc = f"{path}:{line}" if line else path
    errors.append(f"error [{rule}] {loc}: {what}\n  WHY: {why}\n  FIX: {fix}")


def read(p):
    try:
        return open(p, encoding="utf-8", errors="replace").read()
    except OSError:
        return ""


def rel(root, p):
    return os.path.relpath(p, root)


def workflows(root):
    out = []
    for p in sorted(glob.glob(os.path.join(root, ".github", "workflows", "*"))):
        if p.endswith((".yml", ".yaml")):
            out.append(p)
    return out


# ---- minimal, indentation-based workflow reader (no PyYAML) -------------------------------------------
def split_jobs(text):
    """-> {job_id: (first_line_no, [lines])} for the top-level `jobs:` mapping (2-space indented ids)."""
    lines = text.splitlines()
    try:
        start = next(i for i, l in enumerate(lines) if re.match(r"^jobs:\s*$", l))
    except StopIteration:
        return {}
    jobs, cur, buf, first = {}, None, [], 0
    for i in range(start + 1, len(lines)):
        l = lines[i]
        m = re.match(r"^  ([A-Za-z0-9_-]+):\s*$", l)
        if m:
            if cur:
                jobs[cur] = (first, buf)
            cur, buf, first = m.group(1), [], i + 2
            continue
        if re.match(r"^\S", l):  # a new top-level key ends `jobs:`
            break
        if cur is not None:
            buf.append(l)
    if cur:
        jobs[cur] = (first, buf)
    return jobs


def steps_of(buf):
    """Split a job body into step blocks (each starts at `      - `)."""
    steps, cur = [], None
    for i, l in enumerate(buf):
        if re.match(r"^      - ", l):
            cur = [i, [l]]
            steps.append(cur)
        elif cur is not None:
            cur[1].append(l)
    return steps


def val(line):
    v = line.split(":", 1)[1].strip()
    v = re.sub(r"\s+#.*$", "", v).strip().strip("\"'")
    return v


def check_workflow(root, path):
    text = read(path)
    jobs = split_jobs(text)
    rp = rel(root, path)
    for jid, (first, buf) in jobs.items():
        # checkouts of THIS repo into a sub-path make `<path>/...` valid prefixes; other repos are external
        self_paths, ext_paths = set(), set()
        for _, blk in steps_of(buf):
            body = "\n".join(blk)
            if "uses: actions/checkout" in body:
                p = re.search(r"^\s+path:\s*(\S+)", body, re.M)
                has_repo = re.search(r"^\s+repository:\s*\S+", body, re.M)
                if p:
                    (ext_paths if has_repo else self_paths).add(p.group(1).strip("\"'"))
        for off, l in enumerate(buf):
            if "repo-lint: ignore" in l:
                continue
            m = re.match(r"^\s*(?:- )?(working-directory|cache-dependency-path):\s*(.+)$", l)
            if not m:
                continue
            kind, raw = m.group(1), val(l)
            lineno = first + off
            if not raw or "${{" in raw or "*" in raw:
                continue
            targets = [raw]
            if kind == "cache-dependency-path":
                targets = [raw]
            for t in targets:
                first_seg = t.split("/", 1)[0]
                if first_seg in ext_paths:
                    continue  # lives in another repo's checkout
                local = t
                if first_seg in self_paths:
                    local = t.split("/", 1)[1] if "/" in t else "."
                if not os.path.exists(os.path.join(root, local)):
                    prefix_note = (f" (the job checks this repo out into `{first_seg}/`, so the path inside the repo is `{local}`)"
                                   if first_seg in self_paths else "")
                    err("R1", rp, lineno,
                        f"job `{jid}` {kind} `{t}` does not exist in this repository{prefix_note}",
                        "the job fails at runtime (`No such file or directory` / `unable to cache dependencies`) and, "
                        "if it is not a required check, stays red on develop unnoticed",
                        f"create `{local}`, point {kind} at a path that exists, or delete the job/step if this repo "
                        "does not have that component (a backend context has no web/ or docs/ unless you add it)")
        # R2: needs
        need_blob = " ".join(buf)
        for off, l in enumerate(buf):
            m = re.match(r"^    needs:\s*(.*)$", l)
            if not m:
                continue
            names = []
            inline = m.group(1).strip()
            if inline.startswith("["):
                names = [n.strip().strip("\"'") for n in inline.strip("[]").split(",") if n.strip()]
            elif inline:
                names = [inline.strip("\"'")]
            else:
                for l2 in buf[off + 1:]:
                    mm = re.match(r"^      - (\S+)", l2)
                    if mm:
                        names.append(mm.group(1).strip("\"'"))
                    else:
                        break
            for n in names:
                if n and n not in jobs:
                    err("R2", rp, first + off, f"job `{jid}` needs `{n}`, which is not a job in this workflow",
                        "GitHub rejects the whole workflow at parse time, so NO job runs",
                        f"rename the reference to an existing job ({', '.join(sorted(jobs))}) or restore the job")


def check_dependabot(root):
    cfg = next((p for p in (os.path.join(root, ".github", "dependabot.yml"),
                            os.path.join(root, ".github", "dependabot.yaml")) if os.path.isfile(p)), None)
    if not cfg:
        return
    rp = rel(root, cfg)
    text = read(cfg)
    blocks = re.split(r"(?m)^(?=\s*-\s*package-ecosystem:)", text)
    base_line = 1
    for blk in blocks:
        m = re.search(r"package-ecosystem:\s*[\"']?([A-Za-z_-]+)", blk)
        d = re.search(r"^\s*directory:\s*[\"']?([^\"'\s#]+)", blk, re.M)
        n_before = text[:text.find(blk)].count("\n") + 1 if blk and text.find(blk) >= 0 else base_line
        if not m or not d or "repo-lint: ignore" in blk:
            continue
        eco, directory = m.group(1), d.group(1)
        abs_dir = os.path.normpath(os.path.join(root, directory.lstrip("/")))
        if not os.path.isdir(abs_dir):
            err("R3", rp, n_before, f"{eco} entry points at directory `{directory}` that does not exist",
                "Dependabot reports an error for this entry on every run",
                f"fix the directory or delete the `{eco}` entry for `{directory}`")
            continue
        manifest = {"npm": "package.json", "gomod": "go.mod", "pip": "requirements.txt", "docker": "Dockerfile",
                    "terraform": None, "github-actions": None}.get(eco)
        if eco == "npm":
            pj = os.path.join(abs_dir, "package.json")
            if not os.path.isfile(pj):
                err("R3", rp, n_before, f"npm entry `{directory}` has no package.json",
                    "nothing to update; the job fails", "remove the entry or add the manifest")
                continue
            try:
                data = json.loads(read(pj))
            except ValueError:
                continue
            for sect in ("dependencies", "devDependencies", "optionalDependencies", "peerDependencies"):
                for name, spec in (data.get(sect) or {}).items():
                    if isinstance(spec, str) and spec.startswith(("file:", "link:")):
                        target = os.path.normpath(os.path.join(abs_dir, spec.split(":", 1)[1]))
                        if os.path.relpath(target, root).startswith(".."):
                            err("R3", rp, n_before,
                                f"npm entry `{directory}` depends on `{name}` via `{spec}`, a path OUTSIDE this repository",
                                "Dependabot cannot fetch path dependencies outside the repo "
                                "(\"path based dependencies could not be retrieved\"); the update job fails on every run",
                                f"delete the npm entry for `{directory}` (security alerts for it still arrive from the "
                                "dependency graph and are fixed with scoped npm `overrides`), or vendor the dependency "
                                "inside the repo")
        elif manifest and not os.path.isfile(os.path.join(abs_dir, manifest)):
            err("R3", rp, n_before, f"{eco} entry `{directory}` has no {manifest}",
                "nothing to update; the job fails", "remove the entry or add the manifest")


def stale_regex(owners):
    o = "|".join(re.escape(x) for x in owners)
    return re.compile(
        rf"(ghcr\.io/(?:{o})\b|github\.com/(?:{o})\b|(?:{o})\.github\.io|raw\.githubusercontent\.com/(?:{o})\b"
        rf"|repository:\s*(?:{o})/|organizationName:\s*[\"']?(?:{o})\b|oci://[^\s]*(?:{o})\b|\b(?:{o})/[a-z0-9-]+[:@])")


def scan_files(root, patterns):
    seen = set()
    for pat in patterns:
        for p in glob.glob(os.path.join(root, pat), recursive=True):
            if os.path.isfile(p) and p not in seen:
                seen.add(p)
                yield p


def check_owner_drift(root, owners):
    rx = stale_regex(owners)
    pats = [".github/**/*.yml", ".github/**/*.yaml", ".github/**/*.md", "charts/**/*", "docs/docusaurus.config.*",
            "Dockerfile", "Makefile", "terraform/**/*.tf", "terraform/**/*.tftpl", "helm-values/**/*"]
    for p in scan_files(root, pats):
        if p.endswith(("CODEOWNERS", ".template")) or "/node_modules/" in p:
            continue
        for i, l in enumerate(read(p).splitlines(), 1):
            if "repo-lint: ignore" in l:
                continue
            m = rx.search(l)
            if m:
                err("R4", rel(root, p), i, f"stale GitHub owner in `{m.group(0)}`",
                    "the repositories moved to the IQVO organisation: image pushes, chart URLs and docs URLs that "
                    "still name the old owner fail (`installation does not exist`) or point at a host that no longer serves them",
                    "replace the owner with IQVO (registry names lower-case: ghcr.io/iqvo/...)")


def check_placeholders(root):
    if glob.glob(os.path.join(root, ".github", "workflows", "*.template")):
        return  # the template repository itself ships placeholders by design
    rx = re.compile(r"(?<!\$)\{\{[A-Z][A-Z0-9_]{2,}\}\}")
    pats = [".github/**/*.yml", ".github/**/*.yaml", "Makefile", "Dockerfile", "charts/**/*", "docs/docusaurus.config.*",
            "lefthook.yml", ".golangci.yml", ".gremlins.yaml"]
    for p in scan_files(root, pats):
        if p.endswith(".template") or "/node_modules/" in p:
            continue
        for i, l in enumerate(read(p).splitlines(), 1):
            if "repo-lint: ignore" in l:
                continue
            m = rx.search(l)
            if m:
                err("R5", rel(root, p), i, f"unsubstituted template placeholder `{m.group(0)}`",
                    "the scaffold step that fills placeholders did not run for this file; builds and paths are wrong",
                    "replace it with the real value (see scripts/new-service.sh in warehouse-harness-template)")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=".")
    ap.add_argument("--stale-owners", default=os.environ.get("HARNESS_STALE_OWNERS", ",".join(STALE_OWNERS_DEFAULT)))
    a = ap.parse_args()
    root = os.path.abspath(a.root)
    owners = [o for o in a.stale_owners.split(",") if o]
    for w in workflows(root):
        check_workflow(root, w)
    check_dependabot(root)
    check_owner_drift(root, owners)
    check_placeholders(root)
    for e in errors:
        print(e)
    print(f"repo-lint: {len(errors)} error(s)")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
