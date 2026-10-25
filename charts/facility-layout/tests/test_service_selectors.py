#!/usr/bin/env python3
"""Assert this chart's Service selectors isolate each component.

Why this exists: before the frontend workload was added, every Service in this
chart selected on the base selector labels alone (name + instance). Those labels
are identical on the OLTP, analytics projector, analytics reports and MCP pods,
so the OLTP Service's EndpointSlice genuinely contained all of them -- verified
live in the kind cluster, where a Kong request for an OLTP /healthz was
answered by the analytics reports pod.

Adding another workload (the frontend) to that set would mean the SPA could
answer an API request and vice versa, which silently breaks the fleet's
Nginx-serves-assets / Kong-serves-APIs separation. This test fails if any two
Services in the chart can select the same pod.

Run: python3 charts/facility-layout/tests/test_service_selectors.py
"""

from __future__ import annotations

import subprocess
import sys
from pathlib import Path

CHART_DIR = Path(__file__).resolve().parents[1]

REQUIRED = [
    # Mandatory since ADR 0039 (the chart refuses to render without them);
    # dummy values, never dialled.
    "--set", "database.url=postgres://u@example.invalid:5432/db",
    "--set", "kafka.brokers=kafka.example.invalid:9092",
]

ENABLE_EVERYTHING = [
    *REQUIRED,
    "--set", "analytics.database.projectorUrl=postgres://projector@example.invalid:5432/a",
    "--set", "frontend.enabled=true",
    "--set", "analytics.enabled=true",
    "--set", "mcp.enabled=true",
]


def render(extra_args: list[str]) -> list[dict]:
    out = subprocess.run(
        ["helm", "template", "facility-layout", str(CHART_DIR), *extra_args],
        capture_output=True, text=True, check=True,
    ).stdout
    try:
        import yaml  # type: ignore
    except ModuleNotFoundError:  # pragma: no cover - environment guard
        print("SKIP: PyYAML not available; cannot assert selectors", file=sys.stderr)
        raise SystemExit(0)
    return [d for d in yaml.safe_load_all(out) if d]


def selector_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("selector") or {}


def pod_labels_of(doc: dict) -> dict:
    return doc.get("spec", {}).get("template", {}).get("metadata", {}).get("labels") or {}


def matches(selector: dict, labels: dict) -> bool:
    return bool(selector) and all(labels.get(k) == v for k, v in selector.items())


def main() -> int:
    failures: list[str] = []

    docs = render(ENABLE_EVERYTHING)
    services = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Service"}
    deployments = {d["metadata"]["name"]: d for d in docs if d.get("kind") == "Deployment"}

    if "facility-layout" not in services:
        failures.append("the OLTP Service was not rendered")
    elif selector_of(services["facility-layout"]).get("app.kubernetes.io/component") != "api":
        failures.append("the OLTP Service selector must pin component=api")

    if "facility-layout-frontend" not in services:
        failures.append("the frontend Service was not rendered with frontend.enabled=true")
    elif selector_of(services["facility-layout-frontend"]).get("app.kubernetes.io/component") != "frontend":
        failures.append("the frontend Service selector must pin component=frontend")

    # The real invariant: each Service selects exactly one Deployment.
    for svc_name, svc in services.items():
        sel = selector_of(svc)
        hit = [d for d, dep in deployments.items() if matches(sel, pod_labels_of(dep))]
        if len(hit) != 1:
            failures.append(
                f"Service {svc_name} selects {len(hit)} Deployments {sorted(hit)}; expected exactly 1"
            )

    fe = services.get("facility-layout-frontend")
    if fe and fe["spec"].get("type") != "ClusterIP":
        failures.append("frontend Service must be ClusterIP")

    # Frontend routing belongs to the Nginx web gateway, not this chart.
    for d in docs:
        if d.get("kind") in {"Ingress", "HTTPRoute"}:
            name = d["metadata"]["name"]
            if "frontend" in name:
                failures.append(f"{d['kind']} {name}: frontend routing must not live in this chart")

    # Default values must not deploy the frontend at all.
    stray = [
        d["metadata"]["name"]
        for d in render(REQUIRED)
        if d.get("metadata", {}).get("name", "").endswith("-frontend")
    ]
    if stray:
        failures.append(f"frontend resources rendered with default values: {stray}")

    if failures:
        for f in failures:
            print(f"FAIL: {f}")
        return 1

    print(f"PASS: {len(services)} Services each select exactly one Deployment; "
          "frontend is ClusterIP, unrouted, and off by default")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
