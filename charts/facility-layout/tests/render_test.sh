#!/usr/bin/env bash
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
release=facility-layout

default_render="$(helm template "$release" "$chart_dir")"
enabled_render="$(helm template "$release" "$chart_dir" --set frontend.enabled=true)"
api_deployment="$(helm template "$release" "$chart_dir" --show-only templates/deployment.yaml)"
api_service="$(helm template "$release" "$chart_dir" --show-only templates/service.yaml)"
frontend_deployment="$(helm template "$release" "$chart_dir" --set frontend.enabled=true --show-only templates/frontend-deployment.yaml)"
frontend_service="$(helm template "$release" "$chart_dir" --set frontend.enabled=true --show-only templates/frontend-service.yaml)"

if grep -q 'name: facility-layout-frontend' <<<"$default_render"; then
  echo "frontend resources rendered while frontend.enabled=false" >&2
  exit 1
fi

[[ "$(grep -c 'app.kubernetes.io/component: api' <<<"$api_deployment")" -ge 3 ]]
[[ "$(grep -c 'app.kubernetes.io/component: api' <<<"$api_service")" -ge 2 ]]
[[ "$(grep -c 'app.kubernetes.io/component: frontend' <<<"$frontend_deployment")" -ge 3 ]]
[[ "$(grep -c 'app.kubernetes.io/component: frontend' <<<"$frontend_service")" -ge 2 ]]

grep -q 'type: ClusterIP' <<<"$frontend_service"
if grep -Eq 'kind: (Ingress|HTTPRoute)|type: (NodePort|LoadBalancer)' <<<"$frontend_deployment"$'\n'"$frontend_service"; then
  echo "frontend render exposes a forbidden public resource" >&2
  exit 1
fi

for component in api analytics-projector analytics-reports mcp frontend; do
  grep -q "app.kubernetes.io/component: $component" <<<"$(helm template "$release" "$chart_dir" --set frontend.enabled=true --set analytics.enabled=true --set mcp.enabled=true --set analytics.database.projectorUrl=postgres://projector --set analytics.database.reportsUrl=postgres://reports)"
done

api_selector="$(grep -A4 '^  selector:$' <<<"$api_service")"
frontend_selector="$(grep -A4 '^  selector:$' <<<"$frontend_service")"
grep -q 'app.kubernetes.io/component: api' <<<"$api_selector"
grep -q 'app.kubernetes.io/component: frontend' <<<"$frontend_selector"
[[ "$api_selector" != "$frontend_selector" ]]

grep -q 'name: facility-layout-frontend' <<<"$enabled_render"

# D14 (2026-10-12): every config.* value is rendered into the chart ConfigMap,
# and the api Deployment -- the only workload that reads those variables
# (OUTBOX_RELAY_INTERVAL, OUTBOX_MAX_ATTEMPTS, HOUSEKEEPING_INTERVAL, IDEMPOTENCY_KEY_TTL,
# OUTBOX_RETENTION, ...) -- must mount it with envFrom, or the values
# silently do nothing.
configmap_name="$(helm template "$release" "$chart_dir" --show-only templates/configmap.yaml | awk '/^  name:/ {print $2; exit}')"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' || { echo "api Deployment does not mount the ConfigMap via envFrom" >&2; exit 1; }
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" || { echo "api Deployment envFrom does not reference ConfigMap $configmap_name" >&2; exit 1; }
tuned_env="$(helm template "$release" "$chart_dir" --set config.outboxRelayInterval=7s --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned_env" || { echo "config.outboxRelayInterval is not rendered into the ConfigMap" >&2; exit 1; }
# D12 (2026-10-12, ADR 0036): the outbox relay attempt cap is a chart value,
# rendered as OUTBOX_MAX_ATTEMPTS, default 20 (0 = unlimited).
default_env="$(helm template "$release" "$chart_dir" --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$default_env" || { echo "OUTBOX_MAX_ATTEMPTS must default to 20 (ADR 0036)" >&2; exit 1; }
capped_env="$(helm template "$release" "$chart_dir" --set config.outboxMaxAttempts=5 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$capped_env" || { echo "config.outboxMaxAttempts is not rendered into the ConfigMap" >&2; exit 1; }
# The other workloads read their own explicit env and must NOT inherit the
# api's HTTP_ADDR from the ConfigMap.
for template in reports-deployment.yaml projector-deployment.yaml mcp-deployment.yaml; do
  if helm template "$release" "$chart_dir" --set analytics.enabled=true --set mcp.enabled=true --set analytics.database.projectorUrl=postgres://projector --show-only "templates/$template" | grep -q 'configMapRef:'; then
    echo "$template must not mount the api ConfigMap" >&2
    exit 1
  fi
done

echo "facility-layout chart render assertions passed"
