#!/usr/bin/env python3
"""agents.colocate: cmd/mcp and cmd/a2a as two containers of one <release>-agents Deployment.

Canonical test, the same file in every chart that has both an mcp and an a2a component (reference:
IQVO/warehouse-harness-template templates/packaging/charts/test_agents_colocation.py.tmpl). Only VALUES
differs per repo: the chart's every-component values file (VALUES) and, when that file leaves the mcp/a2a
routes off, the --set pairs that turn them on (EXTRA_SETS). Renders the chart three ways and asserts:
  (a) flag off: the standalone mcp and a2a Deployments exist (sync-wave -1 and PruneLast on both) and no
      -agents one; every ConfigMap the chart renders that an mcp/a2a pod consumes (volume, envFrom,
      configMapKeyRef) syncs at wave -2 or earlier, so a config change lands before the wave -1 pods roll;
  (b) flag on: exactly one -agents Deployment (sync-wave -1, PruneLast) whose containers mcp and a2a equal
      the flag-off containers except the port name (http -> mcp / a2a, probe port refs too); the -mcp and
      -a2a Services select only it and each named targetPort resolves to exactly one containerPort, the
      same number as with the flag off; every HTTPRoute and KongPlugin is byte-identical to the flag-off
      render, and no object other than the Deployments and the two Services changes;
  (c) flag on with a2a.enabled=false: the chart falls back to the standalone mcp Deployment;
  (d) both renders: every mcp and a2a container carries `lifecycle.preStop.sleep` of at least
      PRESTOP_MIN_SECONDS (Kong drops a terminating pod IP only on its next config sync, and the binaries
      close their listener at SIGTERM), and the pod's terminationGracePeriodSeconds exceeds that sleep plus
      the binaries' HTTP drain (HTTP_DRAIN_SECONDS), so the kubelet never SIGKILLs a draining server.

Run: python3 charts/facility-layout/tests/test_agents_colocation.py
Needs: helm (HELM overrides the binary), PyYAML. It never skips: a missing tool is a failure.
"""

from __future__ import annotations

import copy
import os
import subprocess
import sys
from pathlib import Path

import yaml

CHART_DIR = Path(__file__).resolve().parents[1]
RELEASE = "facility-layout"
# The per-repo lines. VALUES: the chart's every-component values file, relative to the chart ("" = the
# chart defaults). EXTRA_SETS: --set pairs on top of it, needed only when VALUES leaves the mcp/a2a routes
# off (the HTTPRoute identity check must bite), e.g. ["gatewayApi.enabled=true",
# "mcp.route.enabled=true", "mcp.route.parentRefs[0].name=gw", "a2a.route.enabled=true",
# "a2a.route.parentRefs[0].name=gw"].
VALUES = "tests/warehouse-infra-values.yaml"
EXTRA_SETS: list[str] = []
# Forced on top of VALUES so the three cases hold whatever the file says.
BASE_SETS = ["mcp.enabled=true", "a2a.enabled=true"]
HELM = os.environ.get("HELM", "helm")
COMP = "app.kubernetes.io/component"
PRUNE_LAST = ("argocd.argoproj.io/sync-options", "PruneLast=true")
SYNC_WAVE = "argocd.argoproj.io/sync-wave"
# cmd/mcp and cmd/a2a: shutdownTimeout bounds the HTTP drain (10 s). Kong's target lag on a roll measured
# >= 5 s (asrs-control 2026-11-21), so the preStop sleep must cover it.
HTTP_DRAIN_SECONDS = 10
PRESTOP_MIN_SECONDS = 5
DEFAULT_GRACE_SECONDS = 30  # Kubernetes default when the pod spec leaves it unset


def render(*sets: str) -> list[dict]:
    cmd = [HELM, "template", RELEASE, str(CHART_DIR)] + (["-f", str(CHART_DIR / VALUES)] if VALUES else [])
    for s in [*BASE_SETS, *EXTRA_SETS, *sets]:
        cmd += ["--set", s]
    result = subprocess.run(cmd, capture_output=True, text=True)
    if result.returncode != 0:
        raise SystemExit(f"FAIL: helm template {' '.join(sets)}:\n{result.stderr}")
    return [d for d in yaml.safe_load_all(result.stdout) if d]


def by_key(docs: list[dict]) -> dict[tuple[str, str], dict]:
    return {(d["kind"], d["metadata"]["name"]): d for d in docs}


def deployments(docs: list[dict], component: str) -> list[dict]:
    return [d for d in docs if d["kind"] == "Deployment"
            and d["spec"]["template"]["metadata"]["labels"].get(COMP) == component]


def selects(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def renamed(container: dict, comp: str) -> dict:
    """The flag-off container as the composition must emit it: port http -> comp, probe refs too."""
    c = copy.deepcopy(container)
    for p in c.get("ports", []):
        if p.get("name") == "http":
            p["name"] = comp
    for probe in ("startupProbe", "livenessProbe", "readinessProbe"):
        for kind in ("httpGet", "tcpSocket"):
            h = (c.get(probe) or {}).get(kind)
            if h and str(h.get("port")) == "http":
                h["port"] = comp
    return c


def resolve(service: dict, pod_spec: dict) -> list[list[int]]:
    """Per Service port: the containerPorts its targetPort resolves to in the selected pod."""
    out = []
    for sp in service["spec"]["ports"]:
        tp = sp.get("targetPort", sp["port"])
        out.append([q["containerPort"] for c in pod_spec["containers"] for q in c.get("ports", [])
                    if q.get("name") == tp or q.get("containerPort") == tp])
    return out


def consumed_configmaps(pod_spec: dict) -> set[str]:
    """Names of the ConfigMaps a pod reads: volumes (incl. projected), envFrom and configMapKeyRef."""
    names: set[str] = set()
    for v in pod_spec.get("volumes") or []:
        if v.get("configMap"):
            names.add(v["configMap"]["name"])
        for src in (v.get("projected") or {}).get("sources") or []:
            if src.get("configMap"):
                names.add(src["configMap"]["name"])
    for c in pod_spec.get("containers", []) + pod_spec.get("initContainers", []):
        for ef in c.get("envFrom") or []:
            if ef.get("configMapRef"):
                names.add(ef["configMapRef"]["name"])
        for e in c.get("env") or []:
            ref = (e.get("valueFrom") or {}).get("configMapKeyRef")
            if ref:
                names.add(ref["name"])
    return names


def wave(doc: dict) -> int:
    return int((doc["metadata"].get("annotations") or {}).get(SYNC_WAVE, "0"))


def strip_argocd(doc: dict) -> dict:
    d = copy.deepcopy(doc)
    ann = d["metadata"].get("annotations") or {}
    for k in [k for k in ann if k.startswith("argocd.argoproj.io/")]:
        del ann[k]
    if not ann:
        d["metadata"].pop("annotations", None)
    return d


def drain_failures(case: str, dep: dict, comps: tuple[str, ...]) -> list[str]:
    """preStop sleep on each agent container, and a grace period above sleep + HTTP drain."""
    out = []
    spec = dep["spec"]["template"]["spec"]
    grace = int(spec.get("terminationGracePeriodSeconds", DEFAULT_GRACE_SECONDS))
    for c in spec["containers"]:
        if c["name"] not in comps:
            continue
        sleep = (((c.get("lifecycle") or {}).get("preStop") or {}).get("sleep") or {}).get("seconds")
        if not isinstance(sleep, int) or sleep < PRESTOP_MIN_SECONDS:
            out.append(f"({case}) {dep['metadata']['name']} container {c['name']}: lifecycle.preStop.sleep.seconds is "
                       f"{sleep!r}, want >= {PRESTOP_MIN_SECONDS} (Kong keeps routing to a terminating pod until its "
                       f"next sync; without the sleep every roll costs a 502/timeout)")
            continue
        if grace <= sleep + HTTP_DRAIN_SECONDS:
            out.append(f"({case}) {dep['metadata']['name']}: terminationGracePeriodSeconds {grace} must exceed the "
                       f"{c['name']} preStop sleep {sleep} + HTTP drain {HTTP_DRAIN_SECONDS}")
    return out


def main() -> int:
    failures: list[str] = []
    off, on, fallback = render(), render("agents.colocate=true"), render("agents.colocate=true", "a2a.enabled=false")
    OFF, ON = by_key(off), by_key(on)

    # (a) flag off
    mcp, a2a = deployments(off, "mcp"), deployments(off, "a2a")
    if len(mcp) != 1 or len(a2a) != 1:
        failures.append(f"(a) flag off: want one mcp and one a2a Deployment, got {len(mcp)} / {len(a2a)}")
        print("\n".join(failures)); return 1
    if deployments(off, "agents") or any(k[1].endswith("-agents") for k in OFF):
        failures.append("(a) flag off renders an -agents object")
    for d in (mcp[0], a2a[0]):
        ann = d["metadata"].get("annotations") or {}
        if ann.get(PRUNE_LAST[0]) != PRUNE_LAST[1]:
            failures.append(f"(a) {d['metadata']['name']} lacks the {PRUNE_LAST[0]}: {PRUNE_LAST[1]} annotation")
        # Rollback (un-flip): the standalone Deployment must be Healthy before the wave-0 Services
        # re-select it, or both agents answer 503 while the -agents pod is pruned.
        if ann.get(SYNC_WAVE) != "-1":
            failures.append(f"(a) {d['metadata']['name']} lacks the {SYNC_WAVE}: \"-1\" annotation")
        for cm in sorted(consumed_configmaps(d["spec"]["template"]["spec"])):
            doc = OFF.get(("ConfigMap", cm))
            if doc is not None and wave(doc) > -2:
                failures.append(f"(a) ConfigMap {cm} (read by {d['metadata']['name']}) syncs at wave {wave(doc)}: "
                                f"want {SYNC_WAVE} \"-2\" or earlier, so it lands before the wave -1 pods roll")

    # (d) on the standalone Deployments
    failures += drain_failures("d", mcp[0], ("mcp",)) + drain_failures("d", a2a[0], ("a2a",))

    # (b) flag on
    agents = deployments(on, "agents")
    if len(agents) != 1 or not agents[0]["metadata"]["name"].endswith("-agents"):
        failures.append(f"(b) want exactly one -agents Deployment, got {[d['metadata']['name'] for d in agents]}")
        print("\n".join(failures)); return 1
    ag = agents[0]
    if deployments(on, "mcp") or deployments(on, "a2a"):
        failures.append("(b) flag on still renders a standalone mcp or a2a Deployment")
    ann = ag["metadata"].get("annotations") or {}
    if ann.get("argocd.argoproj.io/sync-wave") != "-1" or ann.get(PRUNE_LAST[0]) != PRUNE_LAST[1]:
        failures.append(f"(b) -agents annotations {ann}: want sync-wave \"-1\" and PruneLast=true")
    if ag["spec"]["selector"]["matchLabels"].get(COMP) != "agents":
        failures.append("(b) -agents selector does not pin component=agents")
    spec = ag["spec"]["template"]["spec"]
    got = {c["name"]: c for c in spec["containers"]}
    if sorted(got) != ["a2a", "mcp"]:
        failures.append(f"(b) -agents containers {sorted(got)}, want [a2a, mcp]")
    for comp, standalone in (("mcp", mcp[0]), ("a2a", a2a[0])):
        for c in standalone["spec"]["template"]["spec"]["containers"]:
            if renamed(c, comp) != got.get(c["name"]):
                failures.append(f"(b) container {c['name']} differs from the flag-off {comp} container beyond the port name")
    failures += drain_failures("d", ag, ("mcp", "a2a"))
    all_deps = [d for d in on if d["kind"] == "Deployment"]
    for comp, standalone in (("mcp", mcp[0]), ("a2a", a2a[0])):
        name = standalone["metadata"]["name"]
        svc_on, svc_off = ON.get(("Service", name)), OFF.get(("Service", name))
        if not svc_on or not svc_off:
            failures.append(f"(b) Service {name} missing (on={bool(svc_on)}, off={bool(svc_off)})")
            continue
        hit = [d["metadata"]["name"] for d in all_deps
               if selects(svc_on["spec"]["selector"], d["spec"]["template"]["metadata"]["labels"])]
        if hit != [ag["metadata"]["name"]]:
            failures.append(f"(b) Service {name} selects {hit}, want only {ag['metadata']['name']}")
        want = resolve(svc_off, standalone["spec"]["template"]["spec"])
        have = resolve(svc_on, spec)
        if any(len(h) != 1 for h in have) or have != want:
            failures.append(f"(b) Service {name} targetPort resolves to {have}, want exactly {want}")
        if strip_argocd({**svc_on, "spec": {k: v for k, v in svc_on["spec"].items() if k not in ("ports", "selector")}}) != \
           strip_argocd({**svc_off, "spec": {k: v for k, v in svc_off["spec"].items() if k not in ("ports", "selector")}}):
            failures.append(f"(b) Service {name} changed beyond selector/targetPort")
    # Only the mcp/a2a/agents Deployments and the two agent Services may change; the api and every
    # other workload must render byte-identically.
    agents_keys = {("Deployment", d["metadata"]["name"]) for d in (mcp[0], a2a[0], ag)}
    for key in sorted(set(OFF) | set(ON)):
        if key[0] in ("HTTPRoute", "KongPlugin") and OFF.get(key) != ON.get(key):
            failures.append(f"(b) {key[0]} {key[1]} differs between flag off and on")
        elif key[0] == "Service" or key in agents_keys:
            continue
        elif OFF.get(key) != ON.get(key):
            failures.append(f"(b) {key[0]} {key[1]} changes with the flag")
    routes = [k for k in OFF if k[0] == "HTTPRoute"]
    if not routes:
        failures.append("(b) VALUES renders no HTTPRoute: enable the mcp/a2a routes (EXTRA_SETS) so the identity check bites")

    # (c) flag on, a2a off: standalone mcp
    if deployments(fallback, "agents") or len(deployments(fallback, "mcp")) != 1 or deployments(fallback, "a2a"):
        failures.append("(c) agents.colocate with a2a.enabled=false must render only the standalone mcp Deployment")
    fb_svc = [d for d in fallback if d["kind"] == "Service" and d["metadata"]["name"] == mcp[0]["metadata"]["name"]]
    if not fb_svc or fb_svc[0]["spec"]["selector"].get(COMP) != "mcp" or fb_svc[0]["spec"]["ports"][0].get("targetPort") != "http":
        failures.append("(c) the mcp Service must keep component=mcp and targetPort http when not colocated")

    if failures:
        print("FAIL:\n  " + "\n  ".join(failures))
        return 1
    print(f"PASS: agents.colocate ({len(spec['containers'])} containers in {ag['metadata']['name']}, "
          f"{len(routes)} HTTPRoutes unchanged, fallback without a2a ok, preStop drain ok)")
    return 0


if __name__ == "__main__":
    sys.exit(main())
