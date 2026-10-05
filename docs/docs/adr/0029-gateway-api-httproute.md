---
id: 0029-gateway-api-httproute
title: 29. Gateway API HTTPRoute exposure template
sidebar_label: 29. Gateway API HTTPRoute
sidebar_position: 29
description: "The chart ships an optional Gateway API HTTPRoute template (default off) instead of a hard-coded Ingress, matching the estate's move to Gateway API."
---

# 29. Gateway API HTTPRoute exposure template

## Status

**Accepted.** Recorded retroactively 2026-10 (the template shipped as
`charts/facility-layout/templates/httproute.yaml` without its own ADR).

## Context

The estate moved from per-service `Ingress` objects to the Gateway API:
one shared `Gateway` per cluster, services attaching `HTTPRoute`s. A
chart that hard-codes an `Ingress` forces every environment to either
not use the shared gateway or fight the template.

## Decision

The chart renders an optional `HTTPRoute`
(`gateway.enabled`, default `false`) attaching this service's ports to a
caller-named `Gateway` (`gateway.gatewayName`) + parent ref, with hostname
and path-prefix values. When disabled nothing is rendered —
`warehouse-infra` owns whether and how a service is exposed, this chart
only makes it possible. The legacy `ingress` block remains for
environments still on Ingress.

## Consequences

- Exposure is a deployment decision, kept in the infra repo, not baked
  into the service chart.
- Two exposure mechanisms (`ingress` + `gateway`) coexist in values.yaml
  until the last Ingress environment migrates; enabling both is a
  configuration error the chart does not try to detect.
- REST and MCP remain unauthenticated (ADR-0015) — exposure via the
  gateway does not change the auth model; when a fresh auth decision
  lands fleet-wide, this template is where policy attaches.

## Related

- ADR-0015 — unauthenticated REST/MCP (the model this exposes).
