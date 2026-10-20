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
  R6 dependabot-cover a repo with go.mod needs a gomod entry for `/`, one with .github/workflows needs github-actions.
                      (incident: warehouse-planning shipped with no dependabot.yml: Go modules and pinned
                      action SHAs were never updated, and nothing reported it)

  R7 workflow-scripts a `run:` step that invokes a repo script (python3|bash|sh scripts/x, ./scripts/x) must run where
                      that file exists: it must exist, and in a job that checks THIS repo out into a subdirectory
                      the step needs `working-directory: <that dir>`.
                      (incident: e2e-tests' scheduled run went red although the scenario passed: the red_issue
                      steps ran from the workspace root, where scripts/harness/red_issue.py does not exist; a
                      manual dispatch never ran those schedule-only steps, so nobody saw it)

  R8 docs-pr          the docs workflow (docs.yml, or any workflow that deploys GitHub Pages) runs on `pull_request`
                      (build only), and its Pages deploy job is gated off pull requests.
                      (incident: docs built only on push to main in 9 repos; a broken sidebar id / link / MDX
                      reached main and was found after the release, ecosystem gaps 2026-10, D25)
  R9 chart-tests-ci   every chart test (`charts/<c>/tests/test_*.py`, `charts/<c>/tests/*.sh`) is run by a workflow:
                      named in a `run:` step (path or glob), through a `make` target a workflow runs, or by another
                      chart test that is.
                      (incident: Service-selector and render tests existed in 5 repos and no CI job ran them, D25)
  R10 configmap-mount every ConfigMap a chart template renders is referenced by a workload in that chart
                      (`envFrom.configMapRef`, `configMapKeyRef`, or a `configMap` volume).
                      (incident: 8 charts rendered `config.*` values into a ConfigMap no Deployment mounted, so
                      every value silently did nothing, ecosystem gaps 2026-10, D14)
  R11 coverage-scope  the coverage package set (`-coverpkg`) is defined ONLY in the Makefile `coverage` target and CI
                      runs it: no workflow `run:` line may pass `-coverpkg` itself, and when the Makefile has a
                      coverage gate some workflow must run `make coverage` (directly or through a target that has
                      it as a prerequisite). In this template repository `ci.yml.template` counts as a workflow.
                      (incident 16d: process-path-management and warehouse-ops-agent measured a different package
                      set in CI than `make coverage`, so the local gate and the PR gate disagreed silently)

Usage: repo_lint.py [--root .] [--stale-owners claudioed,other]   exit 0 clean, 1 findings.
Suppress one line with the text `repo-lint: ignore` on it (use sparingly, say why).
"""
import argparse
import fnmatch
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


SCRIPT_RX = re.compile(
    r"""(?:^|[\s;&|(`"'])(?:(?:python3?|bash|sh)\s+|\./)((?:scripts|tools|hack|ci|\.github/scripts)/[A-Za-z0-9_./-]+\.(?:py|sh|js|mjs))""")


def check_scripts(root, rp, jid, first, buf, self_paths, ext_paths):
    """R7: repo scripts invoked from `run:` steps must resolve from the directory the step runs in."""
    jm = re.search(r"^    defaults:\s*\n(?:\s+.*\n)*?\s+working-directory:\s*(\S+)", "\n".join(buf) + "\n", re.M)
    job_wd = jm.group(1).strip("\"'") if jm else ""
    for off, blk in steps_of(buf):
        body = "\n".join(blk)
        if "repo-lint: ignore" in body:
            continue
        sm = re.search(r"^\s+working-directory:\s*(\S+)", body, re.M)
        wd = (sm.group(1) if sm else job_wd).strip("\"'") if (sm or job_wd) else ""
        if "${{" in wd or (wd.split("/", 1)[0] in ext_paths and wd):
            continue  # an expression or another repo's checkout: cannot judge statically
        for i, line in enumerate(blk):
            if line.lstrip().startswith(("#", "- name:", "name:")):
                continue
            for m in SCRIPT_RX.finditer(line):
                script = m.group(1)
                if "${{" in script:
                    continue
                lineno = first + off + i
                seg = wd.split("/", 1)[0] if wd else ""
                if self_paths and seg not in self_paths:
                    d = sorted(self_paths)[0]
                    err("R7", rp, lineno,
                        f"job `{jid}` runs `{script}` from the workspace root, but this job checks the repository out into `{d}/`",
                        "the file is at `" + d + "/" + script + "`, so the step dies with `can't open file` / `No such file or "
                        "directory`; on a schedule-only step nobody sees it until the weekly run fails (the scenario can pass "
                        "and the job still goes red)",
                        f"add `working-directory: {d}` to the step (or `defaults.run.working-directory` to the job)")
                    continue
                local = wd.split("/", 1)[1] if (seg in self_paths and "/" in wd) else ("" if seg in self_paths else wd)
                if not os.path.exists(os.path.join(root, local, script)):
                    err("R7", rp, lineno, f"job `{jid}` runs `{script}`, which does not exist in this repository"
                        + (f" (relative to `{local}`)" if local else ""),
                        "the step fails at runtime with `No such file or directory`",
                        "ship the file (managed harness scripts: run tools/migrate_v3.py or sync_managed.py), correct the path, "
                        "or delete the step")


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
        check_scripts(root, rp, jid, first, buf, self_paths, ext_paths)
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
    need = []
    if os.path.isfile(os.path.join(root, "go.mod")):
        need.append(("gomod", "/"))
    if glob.glob(os.path.join(root, ".github", "workflows", "*.y*ml")):
        need.append(("github-actions", "/"))
    if not cfg:
        if need:
            err("R6", ".github/dependabot.yml", 0,
                "no dependabot.yml, so " + " and ".join(e for e, _ in need) + " are never updated",
                "dependency and action-pin drift (and the CVEs that come with it) goes unnoticed until a scanner shouts",
                "add .github/dependabot.yml with weekly entries for: " + ", ".join(f"{e} `{d}`" for e, d in need))
        return
    rp = rel(root, cfg)
    covered = {(m.group(1), m.group(2)) for m in re.finditer(
        r"package-ecosystem:\s*[\"']?([A-Za-z_-]+)[\"']?\s*\n(?:\s*#[^\n]*\n)*\s*directory:\s*[\"']?([^\"'\s#]+)", read(cfg))}
    for eco, d in need:
        if (eco, d) not in covered:
            err("R6", rp, 0, f"no `{eco}` entry for `{d}`",
                f"{eco} dependencies of this repo are never updated by Dependabot",
                f"add a weekly `{eco}` entry with directory `{d}` (copy the block from a sibling repo's dependabot.yml)")
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


# ---- R8: docs built on pull requests --------------------------------------------------------------------
def triggers(text):
    """-> the text of the workflow's top-level `on:` block (inline value or indented mapping)."""
    lines = text.splitlines()
    for i, l in enumerate(lines):
        m = re.match(r"""^(?:on|"on"|'on'|true):\s*(.*)$""", l)
        if not m:
            continue
        block = [m.group(1)]
        for l2 in lines[i + 1:]:
            if re.match(r"^\S", l2):
                break
            block.append(l2)
        return "\n".join(block)
    return ""


def is_docs_workflow(path, text):
    return os.path.basename(path) in ("docs.yml", "docs.yaml") or "actions/deploy-pages" in text


def check_docs_pr(root, path):
    text = read(path)
    if not is_docs_workflow(path, text):
        return
    rp = rel(root, path)
    trig = re.sub(r"#.*", "", triggers(text))
    if not re.search(r"\bpull_request\b(?!_target)", trig):
        err("R8", rp, 0, "the docs workflow does not run on `pull_request`",
            "the site is built only after merge (push to main), so a broken sidebar id, link or MDX page is found "
            "after the release instead of in review",
            "add a build-only `pull_request:` trigger with the same `paths:` as `push:`, and gate the Pages deploy "
            "job with `if: github.event_name != 'pull_request'` (copy inventory-storage's docs.yml)")
        return
    for jid, (first, buf) in split_jobs(text).items():
        body = "\n".join(buf)
        if "actions/deploy-pages" not in body or "repo-lint: ignore" in body:
            continue
        cond = re.search(r"^    if:\s*(.+)$", body, re.M)
        if not cond or not re.search(r"pull_request|refs/heads/|event_name\s*==\s*'(?:push|workflow_dispatch)'",
                                     cond.group(1)):
            err("R8", rp, first, f"job `{jid}` deploys GitHub Pages and also runs on pull requests",
                "a pull request would try to publish the site (the github-pages environment rejects non-default "
                "branches, so every docs PR goes red, or worse, publishes unreviewed content)",
                f"add `if: github.event_name != 'pull_request'` to job `{jid}` (and to the upload-pages-artifact step)")


# ---- R9: chart tests run in CI --------------------------------------------------------------------------
CHART_REF_RX = re.compile(r"""(charts/[^\s"';|&()<>`]*tests/[^\s"';|&()<>`]*)""")


def chart_tests(root):
    out = []
    for p in sorted(glob.glob(os.path.join(root, "charts", "*", "tests", "*"))):
        n = os.path.basename(p)
        if not os.path.isfile(p) or not (n.endswith(".sh") or (n.startswith("test_") and n.endswith(".py"))):
            continue
        if "repo-lint: ignore" in "\n".join(read(p).splitlines()[:5]):
            continue
        out.append(rel(root, p))
    return out


def uncommented(text):
    return "\n".join(l for l in text.splitlines() if not l.lstrip().startswith("#"))


def make_recipes(root):
    """-> {target: (prerequisites, recipe text)} from the top-level Makefile (simple rules only)."""
    rules, cur = {}, None
    for l in read(os.path.join(root, "Makefile")).splitlines():
        m = re.match(r"^([A-Za-z0-9_.-]+(?:\s+[A-Za-z0-9_.-]+)*)\s*:(?!=)\s*([^#;]*)", l)
        if m and not l.startswith("\t"):
            names = m.group(1).split()
            for n in names:
                rules.setdefault(n, [[], []])[0].extend(m.group(2).split())
            cur = names
            continue
        if l.startswith("\t") and cur:
            for n in cur:
                rules[n][1].append(l)
        elif l.strip() and not l.startswith("\t"):
            cur = None
    return {k: (v[0], "\n".join(v[1])) for k, v in rules.items()}


def make_targets_run_by(ci_text, recipes):
    """-> [target] for every make target a workflow runs (and their prerequisites, transitively), in visit order."""
    todo = []
    for m in re.finditer(r"\bmake[ \t]+((?:-[A-Za-z][ \t]+\S+[ \t]+|[A-Za-z0-9_.-]+[ \t]*)+)", ci_text):
        todo += [t for t in m.group(1).split() if not t.startswith("-") and "=" not in t]
    seen, out = set(), []
    while todo:
        t = todo.pop()
        if t in seen or t not in recipes:
            continue
        seen.add(t)
        todo += recipes[t][0]
        out.append(t)
    return out


def make_text_run_by(ci_text, recipes):
    """Recipe text of every make target a workflow runs (and their prerequisites, transitively)."""
    return "\n".join(recipes[t][1] for t in make_targets_run_by(ci_text, recipes))


def covered_by(text, test, root):
    if test in text:
        return True
    for ref in CHART_REF_RX.findall(text):
        if "${{" not in ref and fnmatch.fnmatch(test, ref):
            return True
    return False


def check_chart_tests(root):
    tests = chart_tests(root)
    if not tests:
        return
    ci_text = "\n".join(uncommented(read(w)) for w in workflows(root))
    reach = ci_text + "\n" + make_text_run_by(ci_text, make_recipes(root))
    run = {t for t in tests if covered_by(reach, t, root)}
    changed = True
    while changed:  # a test run by an already-covered test (render_test.sh -> render-assertions.sh) is covered
        changed = False
        for t in tests:
            if t in run:
                continue
            for r in list(run):
                body = uncommented(read(os.path.join(root, r)))
                if t in body or re.search(r"(?<![A-Za-z0-9_.-])" + re.escape(os.path.basename(t)) + r"\b", body):
                    run.add(t)
                    changed = True
                    break
    for t in tests:
        if t not in run:
            err("R9", t, 0, "chart test is not run by any workflow",
                "a chart test that CI never runs proves nothing: a broken Service selector or an unmounted ConfigMap "
                "is found after the release (or in the cluster) instead of in review",
                "run it in a `chart-tests` job on every push and PR (copy facility-layout's or inventory-storage's "
                "`chart-tests` job; install PyYAML hash-pinned with `pip install --require-hashes -r "
                ".github/requirements/chart-tests.txt` so a selector test cannot pass by printing SKIP), or put "
                "`repo-lint: ignore <why>` in the first lines of the test if it needs something CI cannot provide")


# ---- R10: chart ConfigMaps are mounted ------------------------------------------------------------------
WORKLOAD_RX = re.compile(r"^kind:\s*(Deployment|StatefulSet|DaemonSet|Job|CronJob)\b", re.M)
CM_REF_RX = re.compile(
    r"(?<![A-Za-z])(?:configMapRef|configMapKeyRef|configMap):[ \t]*"
    r"(?:\{[ \t]*name:[ \t]*(.+?)[ \t]*\}[ \t]*$|\n(?:[ \t]*(?!name:)\S.*\n){0,3}?[ \t]*name:[ \t]*(.+))", re.M)


def yaml_docs(text):
    return re.split(r"(?m)^---\s*$", text)


def norm(expr):
    e = expr.strip().strip("\"'")
    e = re.sub(r"\{\{-?\s*", "{{ ", e)
    e = re.sub(r"\s*-?\}\}", " }}", e)
    return re.sub(r"\s+", " ", e)


def configmaps(root, tpl):
    """-> [(chart-relative path, line, name expression)] for every ConfigMap document in one template."""
    text = read(tpl)
    out, offset = [], 0
    for doc in yaml_docs(text):
        km = re.search(r"^kind:\s*ConfigMap\b.*$", doc, re.M)
        if km and "repo-lint: ignore" not in km.group(0):
            nm = re.search(r"^metadata:\s*\n(?:[ \t]+.*\n)*?  name:\s*(.+)$", doc, re.M)
            if nm:
                line = offset + doc[:km.start()].count("\n") + 1
                out.append((rel(root, tpl), line, norm(nm.group(1))))
        offset += doc.count("\n") + 1
    return out


def cm_referenced(name, refs):
    """A reference matches the same literal name, or the same include helper with the same literal suffix
    (`{{ include "x.fullname" . }}-path-catalogue` == `printf "%s-path-catalogue" (include "x.fullname" .)`)."""
    if name in refs:
        return True
    m = re.match(r'^\{\{ include "([^"]+)" \S+ \}\}(.*)$', name)
    if not m:
        return False
    helper, suffix = m.group(1), m.group(2)
    if not suffix:
        return False  # the bare fullname must be referenced exactly, or a suffixed reference would satisfy it
    return any(f'"{helper}"' in r and (f"%s{suffix}" in r or r.endswith(suffix)) for r in refs)


def check_configmaps_mounted(root):
    for chart in sorted(glob.glob(os.path.join(root, "charts", "*"))):
        tpls = sorted(glob.glob(os.path.join(chart, "templates", "**", "*.yaml"), recursive=True))
        refs = set()
        for t in tpls:
            for doc in yaml_docs(read(t)):
                if WORKLOAD_RX.search(doc):
                    refs |= {norm(a or b) for a, b in CM_REF_RX.findall(doc)}
        for t in tpls:
            for rp, line, name in configmaps(root, t):
                if not cm_referenced(name, refs):
                    err("R10", rp, line, f"ConfigMap `{name}` is not referenced by any Deployment/workload in this chart",
                        "values rendered into it (`config.*`) never reach a process: the chart looks configurable and "
                        "silently is not",
                        "mount it where the binary reads those keys: `envFrom: [{configMapRef: {name: <same name>}}]` "
                        "on the Deployment (explicit `env:` still wins), or a `configMap` volume for files; delete keys "
                        "no binary reads; add a render test asserting the mount. A ConfigMap consumed outside the chart's "
                        "workloads (e.g. a Grafana sidecar) takes `# repo-lint: ignore <why>` on its `kind:` line")


# ---- R11: the coverage scope has one source of truth (the Makefile) -------------------------------------
COVERPKG_RX = re.compile(r"(?:^|\s)-coverpkg[=\s]")


def coverage_workflows(root):
    """Workflows that define a service's CI. In the template repository ci.yml.template IS the service CI."""
    out = workflows(root)
    out += sorted(glob.glob(os.path.join(root, ".github", "workflows", "*.y*ml.template")))
    return out


def makefile_line(root, target):
    for i, l in enumerate(read(os.path.join(root, "Makefile")).splitlines(), 1):
        if re.match(rf"^(?:[A-Za-z0-9_.-]+\s+)*{re.escape(target)}(?:\s+[A-Za-z0-9_.-]+)*\s*:(?!=)", l):
            return i
    return 0


def check_coverage_scope(root):
    """R11: the Makefile `coverage` target owns the coverage package set; CI runs it instead of copying it."""
    wfs = coverage_workflows(root)
    for w in wfs:
        rp = rel(root, w)
        for i, l in enumerate(read(w).splitlines(), 1):
            if l.lstrip().startswith("#") or "repo-lint: ignore" in l:
                continue
            if COVERPKG_RX.search(l):
                err("R11", rp, i, "a workflow step defines its own coverage package set (`-coverpkg`)",
                    "the Makefile `coverage` target is the single source of the coverage scope; a second copy here "
                    "drifts (incident 16d: CI and `make coverage` measured different package sets, so the local and "
                    "PR gates disagreed without anyone noticing)",
                    "replace the coverage test + gate steps with `run: make coverage` and change the scope or the "
                    "threshold only in the Makefile")
    recipes = make_recipes(root)
    if not wfs or "coverage" not in recipes or "-coverpkg" not in recipes["coverage"][1]:
        return  # no workflow to compare, or no scoped coverage gate to protect
    ci_text = "\n".join(uncommented(read(w)) for w in wfs)
    if "coverage" in make_targets_run_by(ci_text, recipes):
        return
    err("R11", "Makefile", makefile_line(root, "coverage"),
        "the Makefile defines a coverage gate (`coverage` target with `-coverpkg`) but no workflow runs it",
        "the gate then runs only on laptops, or CI re-implements it with its own package set and drifts (incident 16d)",
        "run the gate in CI with a `run: make coverage` step in the unit-test job (or a `make` target that has "
        "`coverage` as a prerequisite)")


def main():
    ap = argparse.ArgumentParser()
    ap.add_argument("--root", default=".")
    ap.add_argument("--stale-owners", default=os.environ.get("HARNESS_STALE_OWNERS", ",".join(STALE_OWNERS_DEFAULT)))
    a = ap.parse_args()
    root = os.path.abspath(a.root)
    owners = [o for o in a.stale_owners.split(",") if o]
    for w in workflows(root):
        check_workflow(root, w)
        check_docs_pr(root, w)
    check_dependabot(root)
    check_owner_drift(root, owners)
    check_placeholders(root)
    check_chart_tests(root)
    check_configmaps_mounted(root)
    check_coverage_scope(root)
    for e in errors:
        print(e)
    print(f"repo-lint: {len(errors)} error(s)")
    return 1 if errors else 0


if __name__ == "__main__":
    sys.exit(main())
