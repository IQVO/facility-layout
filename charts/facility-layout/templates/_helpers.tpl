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
