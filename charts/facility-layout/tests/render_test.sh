#!/usr/bin/env bash
# Chart render assertions for charts/facility-layout (run in CI by the
# chart-tests job, next to test_service_selectors.py and test_kong_config.py).
#
# The chart deploys the REAL runtime of this context (ADR 0039: Postgres and
# Kafka are the only runtime, no in-memory mode, no log sink), so the mandatory
# configuration has no silent default, no permissive/off mode is ever rendered
# and the env the binaries read is the env the chart renders.
#
# Run: bash charts/facility-layout/tests/render_test.sh   (needs helm)
set -euo pipefail

chart_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
repo_dir="$(cd "$chart_dir/../.." && pwd)"
release=facility-layout
# Dummy values, no password: the chart refuses to render without a database
# source and without Kafka brokers; neither is ever dialled.
required=(--set "database.url=postgres://u@example.invalid:5432/db" --set "kafka.brokers=kafka.example.invalid:9092")
analytics=(--set analytics.enabled=true --set analytics.database.projectorUrl=postgres://projector@example.invalid:5432/a)

fail() {
  echo "FAIL: $*" >&2
  exit 1
}

render() {
  helm template "$release" "$chart_dir" "${required[@]}" "$@"
}

env_value() { # env_value <rendered> <NAME>
  grep -A1 "name: $2\$" <<<"$1" | grep -m1 'value:' | sed -E 's/.*value: "?//; s/"$//'
}

default_render="$(render)"
enabled_render="$(render --set frontend.enabled=true)"
api_deployment="$(render --show-only templates/deployment.yaml)"
api_service="$(render --show-only templates/service.yaml)"
frontend_deployment="$(render --set frontend.enabled=true --show-only templates/frontend-deployment.yaml)"
frontend_service="$(render --set frontend.enabled=true --show-only templates/frontend-service.yaml)"
configmap="$(render --show-only templates/configmap.yaml)"

if grep -q 'name: facility-layout-frontend' <<<"$default_render"; then
  fail "frontend resources rendered while frontend.enabled=false"
fi

[[ "$(grep -c 'app.kubernetes.io/component: api' <<<"$api_deployment")" -ge 3 ]] || fail "api Deployment labels"
[[ "$(grep -c 'app.kubernetes.io/component: api' <<<"$api_service")" -ge 2 ]] || fail "api Service labels"
[[ "$(grep -c 'app.kubernetes.io/component: frontend' <<<"$frontend_deployment")" -ge 3 ]] || fail "frontend Deployment labels"
[[ "$(grep -c 'app.kubernetes.io/component: frontend' <<<"$frontend_service")" -ge 2 ]] || fail "frontend Service labels"

grep -q 'type: ClusterIP' <<<"$frontend_service" || fail "the frontend Service must be ClusterIP"
if grep -Eq 'kind: (Ingress|HTTPRoute)|type: (NodePort|LoadBalancer)' <<<"$frontend_deployment"$'\n'"$frontend_service"; then
  fail "frontend render exposes a forbidden public resource"
fi

all_docs="$(render --set frontend.enabled=true --set mcp.enabled=true "${analytics[@]}")"
for component in api analytics-projector analytics-reports mcp frontend; do
  grep -q "app.kubernetes.io/component: $component" <<<"$all_docs" || fail "component $component is not rendered when enabled"
done

api_selector="$(grep -A4 '^  selector:$' <<<"$api_service")"
frontend_selector="$(grep -A4 '^  selector:$' <<<"$frontend_service")"
grep -q 'app.kubernetes.io/component: api' <<<"$api_selector" || fail "api Service selector"
grep -q 'app.kubernetes.io/component: frontend' <<<"$frontend_selector" || fail "frontend Service selector"
[[ "$api_selector" != "$frontend_selector" ]] || fail "api and frontend Services share a selector"
grep -q 'name: facility-layout-frontend' <<<"$enabled_render" || fail "frontend not rendered when enabled"

# D14 (2026-10-12): every config.* value is rendered into the chart ConfigMap,
# and the api Deployment -- the only workload that reads those variables
# (OUTBOX_RELAY_INTERVAL, OUTBOX_MAX_ATTEMPTS, HOUSEKEEPING_INTERVAL, SITE_CAPABILITY_HEARTBEAT_INTERVAL,
# IDEMPOTENCY_KEY_TTL, OUTBOX_RETENTION, ...) -- must mount it with envFrom, or
# the values silently do nothing.
configmap_name="$(awk '/^  name:/ {print $2; exit}' <<<"$configmap")"
[[ -n "$configmap_name" ]] || fail "could not read the ConfigMap name"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q 'configMapRef:' || fail "api Deployment does not mount the ConfigMap via envFrom"
grep -A3 'envFrom:' <<<"$api_deployment" | grep -q "name: $configmap_name" || fail "api Deployment envFrom does not reference ConfigMap $configmap_name"
tuned="$(render --set config.outboxRelayInterval=7s --set config.outboxMaxAttempts=5 --show-only templates/configmap.yaml)"
grep -q 'OUTBOX_RELAY_INTERVAL: "7s"' <<<"$tuned" || fail "config.outboxRelayInterval is not rendered into the ConfigMap"
grep -q 'OUTBOX_MAX_ATTEMPTS: "5"' <<<"$tuned" || fail "config.outboxMaxAttempts is not rendered into the ConfigMap"
# ADR 0041: the site capability heartbeat defaults to 5m (half of NIP's 10 minute
# freshness window) with no infra value required, is tunable, and "0s" disables it.
grep -q 'SITE_CAPABILITY_HEARTBEAT_INTERVAL: "5m"' <<<"$configmap" || fail "SITE_CAPABILITY_HEARTBEAT_INTERVAL must default to 5m (ADR 0041)"
beat_tuned="$(render --set config.siteCapabilityHeartbeatInterval=90s --show-only templates/configmap.yaml)"
grep -q 'SITE_CAPABILITY_HEARTBEAT_INTERVAL: "90s"' <<<"$beat_tuned" || fail "config.siteCapabilityHeartbeatInterval is not rendered into the ConfigMap"
beat_off="$(render --set config.siteCapabilityHeartbeatInterval=0s --show-only templates/configmap.yaml)"
grep -q 'SITE_CAPABILITY_HEARTBEAT_INTERVAL: "0s"' <<<"$beat_off" || fail "config.siteCapabilityHeartbeatInterval=0s must render so the heartbeat can be disabled"
# D12 (2026-10-12, ADR 0036): the outbox relay attempt cap defaults to 20.
grep -q 'OUTBOX_MAX_ATTEMPTS: "20"' <<<"$configmap" || fail "OUTBOX_MAX_ATTEMPTS must default to 20 (ADR 0036)"

# --- ADR 0039: the mandatory configuration is rendered from values -----------------
[[ "$(env_value "$api_deployment" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "KAFKA_BROKERS is not rendered from kafka.brokers"
[[ "$(env_value "$api_deployment" EVENT_PUBLISHER)" == "kafka" ]] || fail "EVENT_PUBLISHER must default to kafka"
grep -q '^  EVENT_PUBLISHER: "kafka"' <<<"$configmap" || fail "the ConfigMap must carry EVENT_PUBLISHER=kafka"
grep -q '^  KAFKA_BROKERS: "kafka.example.invalid:9092"' <<<"$configmap" || fail "the ConfigMap must carry KAFKA_BROKERS"
grep -A4 'name: DATABASE_URL$' <<<"$api_deployment" | grep -q 'secretKeyRef:' || fail "DATABASE_URL must come from a Secret (secretKeyRef)"
if grep -qE '^  (MIGRATIONS_)?DATABASE_URL:' <<<"$configmap"; then fail "a DSN must never land in the ConfigMap"; fi
# warehouse-infra still passes the pre-ADR-0039 switch kafka.enabled: it must keep rendering.
render --set kafka.enabled=true --show-only templates/deployment.yaml >/dev/null || fail "kafka.enabled=true (set by warehouse-infra) must keep rendering"

# --- no in-memory / permissive / log mode, ever --------------------------------------
if grep -qiE 'value: "?(permissive|log)"?$' <<<"$all_docs"; then fail "the render carries permissive/log, which the binaries reject at boot"; fi
if grep -q 'DATABASE_URL: ""' <<<"$all_docs"; then fail "an empty DATABASE_URL must never be rendered"; fi
if grep -qi 'in-memory adapters\|IN-MEMORY adapters' <<<"$all_docs"$'\n'"$(render --show-only templates/NOTES.txt 2>/dev/null || true)"; then fail "the render mentions an in-memory mode"; fi
if grep -E '(name: |^  )[A-Z_]+_MODE' <<<"$all_docs" | grep -q .; then
  fail "the render carries a *_MODE variable: facility-layout has none"
fi

# --- probes: /healthz liveness (and startup), /readyz readiness ----------------------
probe_block() { # probe_block <probeName> <rendered deployment>
  awk -v p="$1:" '$1 == p {f=1; next} f && /^          [a-zA-Z]/ {exit} f {print}' <<<"$2"
}
grep -q 'path: /healthz' <<<"$(probe_block livenessProbe "$api_deployment")" || fail "livenessProbe must hit /healthz"
grep -q 'path: /healthz' <<<"$(probe_block startupProbe "$api_deployment")" || fail "startupProbe must hit /healthz"
grep -q 'path: /readyz' <<<"$(probe_block readinessProbe "$api_deployment")" || fail "readinessProbe must hit /readyz"

# --- graceful shutdown room ----------------------------------------------------------
grace="$(awk '/terminationGracePeriodSeconds:/ {print $2; exit}' <<<"$api_deployment")"
(( grace >= 20 )) || fail "terminationGracePeriodSeconds is $grace; want at least 20 (10 s shared drain budget + 5 s telemetry flush)"

# The other workloads read their own explicit env and must NOT inherit the
# api's HTTP_ADDR (reports would bind :8080) or OTEL_SERVICE_NAME from it.
for template in reports-deployment.yaml projector-deployment.yaml mcp-deployment.yaml; do
  rendered="$(render --set mcp.enabled=true "${analytics[@]}" --show-only "templates/$template")"
  if grep -q 'configMapRef:' <<<"$rendered"; then
    fail "$template must not mount the api ConfigMap"
  fi
done

# --- the mandatory env of every binary is rendered -----------------------------------
mcp_dep="$(render --set mcp.enabled=true --show-only templates/mcp-deployment.yaml)"
grep -A4 'name: DATABASE_URL$' <<<"$mcp_dep" | grep -q 'secretKeyRef:' || fail "the mcp DATABASE_URL must come from a Secret"
proj="$(render "${analytics[@]}" --show-only templates/projector-deployment.yaml)"
reports="$(render "${analytics[@]}" --show-only templates/reports-deployment.yaml)"
[[ "$(env_value "$proj" ANALYTICS_CONSUMER_GROUP)" == "facility-analytics" ]] || fail "ANALYTICS_CONSUMER_GROUP must default to the projector's historical group facility-analytics"
[[ "$(env_value "$proj" KAFKA_BROKERS)" == "kafka.example.invalid:9092" ]] || fail "the projector KAFKA_BROKERS is not rendered from kafka.brokers"
renamed="$(render "${analytics[@]}" --set analytics.projector.consumerGroup=g-analytics --show-only templates/projector-deployment.yaml)"
[[ "$(env_value "$renamed" ANALYTICS_CONSUMER_GROUP)" == "g-analytics" ]] || fail "analytics.projector.consumerGroup does not reach ANALYTICS_CONSUMER_GROUP"

# --- chart vs binary ---------------------------------------------------------------------
# Every mandatory variable a binary's boot config demands (ADR 0039) is
# rendered by its Deployment: a new mandatory variable fails until the chart
# carries it.
for var in DATABASE_URL KAFKA_BROKERS EVENT_PUBLISHER; do
  grep -q "name: $var\$" <<<"$api_deployment" || fail "the api Deployment does not render $var"
done
grep -q 'name: DATABASE_URL$' <<<"$mcp_dep" || fail "the mcp Deployment does not render DATABASE_URL"
grep -q 'name: ANALYTICS_DATABASE_URL$' <<<"$proj" || fail "the projector Deployment does not render ANALYTICS_DATABASE_URL"
grep -q 'name: ANALYTICS_DATABASE_URL$' <<<"$reports" || fail "the reports Deployment does not render ANALYTICS_DATABASE_URL"
for bin in facility-projector facility-reports; do
  case "$bin" in facility-projector) rendered="$proj" ;; *) rendered="$reports" ;; esac
  vars="$(grep -ho 'errors.New("[A-Z_]* is required' "$repo_dir"/cmd/"$bin"/*.go | grep -v _test | sed -E 's/errors.New\("//; s/ is required//' | sort -u)"
  [[ -n "$vars" ]] || fail "found no mandatory variables in cmd/$bin; the scan is looking in the wrong place"
  for var in $vars; do
    grep -q "name: $var\$" <<<"$rendered" || fail "the $bin Deployment does not render $var, which cmd/$bin requires"
  done
done

# --- refusals: the mandatory values name themselves ----------------------------------
expect_refusal() { # expect_refusal <why> <needle> <helm args...>
  local why="$1" needle="$2" err; shift 2
  if err="$(helm template "$release" "$chart_dir" "$@" 2>&1)"; then
    fail "the chart must refuse to render: $why"
  fi
  grep -qF -- "$needle" <<<"$err" || fail "the refusal for '$why' does not name '$needle':
$err"
}
base=("${required[@]}")
expect_refusal "no database source and no brokers" "database.url or database.existingSecret"
expect_refusal "no database source" "database.url or database.existingSecret" --set kafka.brokers=k:9092
expect_refusal "no brokers" "kafka.brokers" --set database.url=postgres://u@h/db
expect_refusal "the log publisher" "config.eventPublisher" "${base[@]}" --set config.eventPublisher=log
expect_refusal "an unknown publisher" "the only publisher" "${base[@]}" --set config.eventPublisher=rabbit
expect_refusal "kafka switched off" "Kafka cannot be switched off" "${base[@]}" --set kafka.enabled=false
expect_refusal "a short grace period" "terminationGracePeriodSeconds" "${base[@]}" --set terminationGracePeriodSeconds=10
expect_refusal "the log publisher through extraEnv" "EVENT_PUBLISHER" "${base[@]}" --set 'extraEnv[0].name=EVENT_PUBLISHER' --set 'extraEnv[0].value=log'
expect_refusal "a *_MODE through extraEnv" "*_MODE" "${base[@]}" --set 'extraEnv[0].name=LAYOUT_MODE' --set 'extraEnv[0].value=permissive'
expect_refusal "analytics without a DSN source" "analytics.database.projectorUrl" "${base[@]}" --set analytics.enabled=true
expect_refusal "analytics without a consumer group" "analytics.projector.consumerGroup" "${base[@]}" "${analytics[@]}" --set analytics.projector.consumerGroup=

# --- what warehouse-infra sets keeps working -----------------------------------------
# tests/warehouse-infra-values.yaml mirrors helm-values/facility-layout.yaml +
# _platform.yaml + kind-resources/facility-layout.yaml of warehouse-infra:
# kafka.enabled='true', eventPublisher=kafka, the existing database secret, the
# analytics secret, mcp and frontend on, empty extraEnv, the probes that point at
# /healthz and the platform brokers. Infra needs NO values change.
infra=(-f "$chart_dir/tests/warehouse-infra-values.yaml")
infra_all="$(helm template "$release" "$chart_dir" "${infra[@]}")" || fail "the warehouse-infra values no longer render"
infra_api="$(helm template "$release" "$chart_dir" "${infra[@]}" --show-only templates/deployment.yaml)"
[[ "$(env_value "$infra_api" KAFKA_BROKERS)" == "kafka.warehouse-systems.svc.cluster.local:9092" ]] || fail "infra values: KAFKA_BROKERS"
[[ "$(env_value "$infra_api" EVENT_PUBLISHER)" == "kafka" ]] || fail "infra values: EVENT_PUBLISHER"
grep -q 'facility-layout-db' <<<"$infra_api" || fail "infra values: DATABASE_URL must come from the facility-layout-db secret"
infra_proj="$(helm template "$release" "$chart_dir" "${infra[@]}" --show-only templates/projector-deployment.yaml)"
[[ "$(env_value "$infra_proj" ANALYTICS_CONSUMER_GROUP)" == "facility-analytics" ]] || fail "infra values: ANALYTICS_CONSUMER_GROUP"
for component in api analytics-projector analytics-reports mcp frontend; do
  grep -q "app.kubernetes.io/component: $component" <<<"$infra_all" || fail "infra values: component $component not rendered"
done

echo "PASS: facility-layout chart render assertions"
