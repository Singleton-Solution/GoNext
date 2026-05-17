{{/*
Common helpers for the GoNext chart.

These follow the Helm starter conventions (`name`, `fullname`, `chart`,
common labels, selector labels). Per-component variants append the
component name so resources can co-exist in one Release.
*/}}

{{/* ----------------------------------------------------------------- */}}
{{/* names */}}
{{/* ----------------------------------------------------------------- */}}

{{/*
Expand the name of the chart.
*/}}
{{- define "gonext.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
Truncated at 63 chars because some Kubernetes name fields are limited to that.
*/}}
{{- define "gonext.fullname" -}}
{{- if .Values.fullnameOverride -}}
{{- .Values.fullnameOverride | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- $name := default .Chart.Name .Values.nameOverride -}}
{{- if contains $name .Release.Name -}}
{{- .Release.Name | trunc 63 | trimSuffix "-" -}}
{{- else -}}
{{- printf "%s-%s" .Release.Name $name | trunc 63 | trimSuffix "-" -}}
{{- end -}}
{{- end -}}
{{- end -}}

{{/*
Per-component fullname: "<release-fullname>-<component>".
Used by api/worker/cron/web/admin resources.
Usage: {{ include "gonext.componentFullname" (dict "ctx" . "component" "api") }}
*/}}
{{- define "gonext.componentFullname" -}}
{{- $fullname := include "gonext.fullname" .ctx -}}
{{- printf "%s-%s" $fullname .component | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Chart name + version label.
*/}}
{{- define "gonext.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/* ----------------------------------------------------------------- */}}
{{/* labels */}}
{{/* ----------------------------------------------------------------- */}}

{{/*
Common labels (chart-wide). Includes Helm's recommended set plus chart.
*/}}
{{- define "gonext.labels" -}}
helm.sh/chart: {{ include "gonext.chart" . }}
{{ include "gonext.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: gonext
{{- end -}}

{{/*
Selector labels (chart-wide). Used by anything that selects across all
components (e.g. the chart-wide ServiceAccount).
*/}}
{{- define "gonext.selectorLabels" -}}
app.kubernetes.io/name: {{ include "gonext.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
Component-scoped labels. Adds app.kubernetes.io/component=<name>.
Usage: {{- include "gonext.componentLabels" (dict "ctx" . "component" "api") | nindent 4 }}
*/}}
{{- define "gonext.componentLabels" -}}
{{ include "gonext.labels" .ctx }}
app.kubernetes.io/component: {{ .component }}
app: {{ printf "core-%s" .component | replace "core-web" "public-web" | replace "core-admin" "admin-web" }}
{{- end -}}

{{/*
Component-scoped selector labels.
*/}}
{{- define "gonext.componentSelectorLabels" -}}
{{ include "gonext.selectorLabels" .ctx }}
app.kubernetes.io/component: {{ .component }}
{{- end -}}

{{/* ----------------------------------------------------------------- */}}
{{/* misc */}}
{{/* ----------------------------------------------------------------- */}}

{{/*
ServiceAccount name to use.
*/}}
{{- define "gonext.serviceAccountName" -}}
{{- if .Values.serviceAccount.create -}}
{{- default (include "gonext.fullname" .) .Values.serviceAccount.name -}}
{{- else -}}
{{- default "default" .Values.serviceAccount.name -}}
{{- end -}}
{{- end -}}

{{/*
Name of the Secret to envFrom.
*/}}
{{- define "gonext.secretName" -}}
{{- if .Values.secrets.create -}}
{{- printf "%s-secrets" (include "gonext.fullname" .) -}}
{{- else if .Values.secrets.existingSecret -}}
{{- .Values.secrets.existingSecret -}}
{{- else -}}
{{- printf "%s-secrets" (include "gonext.fullname" .) -}}
{{- end -}}
{{- end -}}

{{/*
Name of the ConfigMap to envFrom.
*/}}
{{- define "gonext.configMapName" -}}
{{- printf "%s-config" (include "gonext.fullname" .) -}}
{{- end -}}

{{/*
Resolve an image reference for a component.
Usage: {{ include "gonext.image" (dict "ctx" . "image" .Values.api.image) }}
*/}}
{{- define "gonext.image" -}}
{{- $registry := .ctx.Values.global.imageRegistry -}}
{{- $repository := .image.repository -}}
{{- $tag := default .ctx.Chart.AppVersion .image.tag -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry $repository $tag -}}
{{- else -}}
{{- printf "%s:%s" $repository $tag -}}
{{- end -}}
{{- end -}}

{{/*
imagePullSecrets block. Renders nothing if none are configured.
*/}}
{{- define "gonext.imagePullSecrets" -}}
{{- with .Values.global.imagePullSecrets -}}
imagePullSecrets:
{{- toYaml . | nindent 2 }}
{{- end -}}
{{- end -}}
