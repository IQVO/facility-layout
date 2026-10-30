{{/*
Expand the name of the chart.
*/}}
{{- define "facility-layout.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Create a default fully qualified app name.
*/}}
{{- define "facility-layout.fullname" -}}
{{- if .Values.fullnameOverride }}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- $name := default .Chart.Name .Values.nameOverride }}
{{- if contains $name .Release.Name }}
{{- .Release.Name | trunc 63 | trimSuffix "-" }}
{{- else }}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" }}
{{- end }}
{{- end }}
{{- end }}

{{/*
Chart name and version as used by the chart label.
*/}}
{{- define "facility-layout.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" }}
{{- end }}

{{/*
Common labels
*/}}
{{- define "facility-layout.labels" -}}
helm.sh/chart: {{ include "facility-layout.chart" . }}
{{ include "facility-layout.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
{{- end }}

{{/*
Selector labels
*/}}
{{- define "facility-layout.selectorLabels" -}}
app.kubernetes.io/name: {{ include "facility-layout.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end }}

{{/*
Create the name of the service account to use
*/}}
{{- define "facility-layout.serviceAccountName" -}}
{{- if .Values.serviceAccount.create }}
{{- default (include "facility-layout.fullname" .) .Values.serviceAccount.name }}
{{- else }}
{{- default "default" .Values.serviceAccount.name }}
{{- end }}
{{- end }}

{{/*
Name of the Secret holding DATABASE_URL, when the chart creates its own.
*/}}
{{- define "facility-layout.databaseSecretName" -}}
{{- if .Values.database.existingSecret }}
{{- .Values.database.existingSecret }}
{{- else }}
{{- include "facility-layout.fullname" . }}-database
{{- end }}
{{- end }}

{{/*
MANDATORY (ADR 0039: Postgres is the only runtime). cmd/facility and cmd/mcp
exit at boot without DATABASE_URL; there is no in-memory mode.
*/}}
{{- define "facility-layout.requireDatabase" -}}
{{- if not (or .Values.database.url .Values.database.existingSecret) -}}
{{- fail "facility-layout requires database.url or database.existingSecret to be set (DATABASE_URL): the service runs on real Postgres only and exits at boot without it (ADR 0039)." -}}
{{- end -}}
{{- end -}}

{{/*
MANDATORY (ADR 0039: Kafka is the only runtime). cmd/facility exits at boot
without KAFKA_BROKERS and rejects EVENT_PUBLISHER=log; there is no default
broker, no log sink and no way to switch Kafka off. kafka.enabled is kept only
so values written before ADR 0039 still render (warehouse-infra sets it to
true): it may be true, never false.
*/}}
{{- define "facility-layout.requireKafka" -}}
{{- $_ := required "facility-layout requires kafka.brokers to be set (KAFKA_BROKERS, comma-separated): the service runs on real Kafka only and exits at boot without it (ADR 0039)." .Values.kafka.brokers -}}
{{- if and (hasKey .Values.kafka "enabled") (not .Values.kafka.enabled) -}}
{{- fail "kafka.enabled is false — Kafka cannot be switched off: the service runs on real Kafka only (ADR 0039). Drop the key or set it to true and set kafka.brokers." -}}
{{- end -}}
{{- if ne (toString .Values.config.eventPublisher) "kafka" -}}
{{- fail (printf "config.eventPublisher is %q — the only publisher is \"kafka\": the log sink was removed (ADR 0039); the binary rejects EVENT_PUBLISHER=log at boot." (toString .Values.config.eventPublisher)) -}}
{{- end -}}
{{- end -}}

{{/*
Shutdown is sequential: readiness flips, then ONE 10s budget drains HTTP and
lets the outbox relay finish its pass, then a 5s telemetry flush. A grace
period under 20s gets the pod SIGKILLed mid-drain (the default is 30s).
*/}}
{{- define "facility-layout.requireGracePeriod" -}}
{{- if lt (int .Values.terminationGracePeriodSeconds) 20 -}}
{{- fail (printf "terminationGracePeriodSeconds is %v — want at least 20: 10s HTTP drain + relay stop (one shared budget) + 5s telemetry flush." .Values.terminationGracePeriodSeconds) -}}
{{- end -}}
{{- end -}}

{{/*
extraEnv may not bring back an in-memory or log mode (ADR 0039): no
EVENT_PUBLISHER other than kafka and no *_MODE variable at all. facility-layout
reads no *_MODE switch (it consumes no sibling context over REST or Kafka), so
any one in extraEnv is a leftover of a permissive/off mode that no longer
exists.
*/}}
{{- define "facility-layout.requireNoInMemoryEnv" -}}
{{- range .Values.extraEnv -}}
{{- $name := toString .name -}}
{{- $value := toString (default "" .value) -}}
{{- if and (eq $name "EVENT_PUBLISHER") (ne $value "kafka") -}}
{{- fail (printf "extraEnv sets EVENT_PUBLISHER=%s — the only publisher is \"kafka\" (ADR 0039)." $value) -}}
{{- end -}}
{{- if hasSuffix "_MODE" $name -}}
{{- fail (printf "extraEnv sets %s — *_MODE variables are not configurable: facility-layout runs on real Postgres and real Kafka only, with no permissive or off mode (ADR 0039)." $name) -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
analytics.enabled needs an analytical DSN source (the projector and reports
binaries refuse to boot without ANALYTICS_DATABASE_URL) and a stable consumer
group (the projector refuses to boot without ANALYTICS_CONSUMER_GROUP). Its
broker is the one kafka.brokers that requireKafka already makes mandatory.
Fail at render instead of crash-looping.
*/}}
{{- define "facility-layout.requireAnalyticsConfig" -}}
{{- if not (or .Values.analytics.database.projectorUrl .Values.analytics.database.existingSecret) -}}
{{- fail "analytics.enabled is true but neither analytics.database.projectorUrl nor analytics.database.existingSecret is set — the projector and reports binaries refuse to boot without ANALYTICS_DATABASE_URL." -}}
{{- end -}}
{{- $_ := required "analytics.enabled is true but analytics.projector.consumerGroup is empty: the projector exits at boot without ANALYTICS_CONSUMER_GROUP, a stable Kafka group id (ADR 0039)." .Values.analytics.projector.consumerGroup -}}
{{- end -}}

{{/*
Every render-time requirement of the real runtime, in one include. The api
Deployment (always rendered) includes it, so a mis-set release fails
`helm template` / `helm install` whichever component is enabled.
*/}}
{{- define "facility-layout.requireRealRuntime" -}}
{{- include "facility-layout.requireDatabase" . -}}
{{- include "facility-layout.requireKafka" . -}}
{{- include "facility-layout.requireGracePeriod" . -}}
{{- include "facility-layout.requireNoInMemoryEnv" . -}}
{{- if .Values.analytics.enabled -}}
{{- include "facility-layout.requireAnalyticsConfig" . -}}
{{- end -}}
{{- end -}}

{{/*
Fully qualified name of the analytics projector deployment (ADR-0010).
*/}}
{{- define "facility-layout.projectorFullname" -}}
{{- include "facility-layout.fullname" . }}-projector
{{- end }}

{{/*
Fully qualified name of the analytics reports deployment/service (ADR-0010).
*/}}
{{- define "facility-layout.reportsFullname" -}}
{{- include "facility-layout.fullname" . }}-reports
{{- end }}

{{/*
Name of the Secret holding the analytics DSNs, when the chart creates its own.
*/}}
{{- define "facility-layout.analyticsSecretName" -}}
{{- if .Values.analytics.database.existingSecret }}
{{- .Values.analytics.database.existingSecret }}
{{- else }}
{{- include "facility-layout.fullname" . }}-analytics
{{- end }}
{{- end }}

{{/*
Fully qualified name of the MCP server deployment/service (ADR-0007).
*/}}
{{- define "facility-layout.mcpFullname" -}}
{{- include "facility-layout.fullname" . }}-mcp
{{- end }}

{{/*
Fully qualified name of the A2A agent Deployment/Service (<release>-a2a, ADR-0043).
*/}}
{{- define "facility-layout.a2aFullname" -}}
{{- include "facility-layout.fullname" . }}-a2a
{{- end }}

{{/*
Fully qualified name of the frontend Module Federation remote deployment/service.

The remote is served by its own nginx pod and reached through warehouse-infra's
Nginx web gateway at /mfes/facility-layout/. It is deliberately a separate
workload from the API: Kong never routes to it, and the OLTP Service must never
select it.
*/}}
{{- define "facility-layout.frontendFullname" -}}
{{- include "facility-layout.fullname" . }}-frontend
{{- end }}

{{/*
Comma-joined names of this chart's enabled KongPlugins, for the
konghq.com/plugins annotation on every HTTPRoute this chart renders.
Empty string (annotation omitted) when none are enabled — standalone
installs without Kong are unaffected.
*/}}
{{- define "facility-layout.kongPluginNames" -}}
{{- $names := list -}}
{{- range $name, $cfg := .Values.kongPlugins -}}
{{- if $cfg.enabled -}}
{{- $names = append $names (printf "%s-%s" (include "facility-layout.fullname" $) $name) -}}
{{- end -}}
{{- end -}}
{{- join "," $names -}}
{{- end }}
