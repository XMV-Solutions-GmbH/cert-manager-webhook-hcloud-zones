{{/*
SPDX-License-Identifier: MIT OR Apache-2.0
SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
SPDX-FileContributor: David Koller <david.koller@xmv.de>
*/}}

{{/*
Expand the name of the chart.
*/}}
{{- define "cert-manager-webhook-hcloud-zones.name" -}}
{{- default .Chart.Name .Values.nameOverride | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Create a default fully qualified app name.
Truncated at 63 chars because some Kubernetes name fields are limited to this
by the DNS naming spec. If the release name already contains the chart name it
is used directly.
*/}}
{{- define "cert-manager-webhook-hcloud-zones.fullname" -}}
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
Chart name and version, suitable for a label value.
*/}}
{{- define "cert-manager-webhook-hcloud-zones.chart" -}}
{{- printf "%s-%s" .Chart.Name .Chart.Version | replace "+" "_" | trunc 63 | trimSuffix "-" -}}
{{- end -}}

{{/*
Common labels applied to every resource the chart renders.
*/}}
{{- define "cert-manager-webhook-hcloud-zones.labels" -}}
helm.sh/chart: {{ include "cert-manager-webhook-hcloud-zones.chart" . }}
{{ include "cert-manager-webhook-hcloud-zones.selectorLabels" . }}
{{- if .Chart.AppVersion }}
app.kubernetes.io/version: {{ .Chart.AppVersion | quote }}
{{- end }}
app.kubernetes.io/managed-by: {{ .Release.Service }}
app.kubernetes.io/part-of: cert-manager
{{- end -}}

{{/*
Selector labels — stable across upgrades; used in pod selectors.
*/}}
{{- define "cert-manager-webhook-hcloud-zones.selectorLabels" -}}
app.kubernetes.io/name: {{ include "cert-manager-webhook-hcloud-zones.name" . }}
app.kubernetes.io/instance: {{ .Release.Name }}
{{- end -}}

{{/*
ServiceAccount name used by the webhook pod.
*/}}
{{- define "cert-manager-webhook-hcloud-zones.serviceAccountName" -}}
{{ include "cert-manager-webhook-hcloud-zones.fullname" . }}
{{- end -}}

{{/*
PKI helpers — names of the Issuer, root Certificate and serving Certificate
that wire up the webhook's TLS material via cert-manager itself.
*/}}
{{- define "cert-manager-webhook-hcloud-zones.selfSignedIssuer" -}}
{{ printf "%s-selfsign" (include "cert-manager-webhook-hcloud-zones.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-hcloud-zones.rootCAIssuer" -}}
{{ printf "%s-ca" (include "cert-manager-webhook-hcloud-zones.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-hcloud-zones.rootCACertificate" -}}
{{ printf "%s-ca" (include "cert-manager-webhook-hcloud-zones.fullname" .) }}
{{- end -}}

{{- define "cert-manager-webhook-hcloud-zones.servingCertificate" -}}
{{ printf "%s-webhook-tls" (include "cert-manager-webhook-hcloud-zones.fullname" .) }}
{{- end -}}

{{/*
Image reference, defaulting tag to the chart's appVersion.
*/}}
{{- define "cert-manager-webhook-hcloud-zones.image" -}}
{{- $tag := default .Chart.AppVersion .Values.image.tag -}}
{{- printf "%s:%s" .Values.image.repository $tag -}}
{{- end -}}
