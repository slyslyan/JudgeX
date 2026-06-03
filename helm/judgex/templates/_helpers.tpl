{{- define "judgex.namespace" -}}
{{- .Values.global.namespace | default "judgex" -}}
{{- end -}}

{{- define "judgex.backendImage" -}}
{{- $registry := .Values.image.registry | default "" -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry .Values.backend.image.repository .Values.backend.image.tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.backend.image.repository .Values.backend.image.tag -}}
{{- end -}}
{{- end -}}

{{- define "judgex.frontendImage" -}}
{{- $registry := .Values.image.registry | default "" -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry .Values.frontend.image.repository .Values.frontend.image.tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.frontend.image.repository .Values.frontend.image.tag -}}
{{- end -}}
{{- end -}}

{{- define "judgex.workerImage" -}}
{{- $registry := .Values.image.registry | default "" -}}
{{- if $registry -}}
{{- printf "%s/%s:%s" $registry .Values.judgeWorker.image.repository .Values.judgeWorker.image.tag -}}
{{- else -}}
{{- printf "%s:%s" .Values.judgeWorker.image.repository .Values.judgeWorker.image.tag -}}
{{- end -}}
{{- end -}}

{{- define "judgex.labels" -}}
app.kubernetes.io/name: judgex
app.kubernetes.io/instance: {{ .Release.Name }}
app.kubernetes.io/version: {{ .Chart.AppVersion }}
{{- end -}}

{{- define "judgex.sandboxMode" -}}
{{- if eq .Values.sandbox.mode "gvisor" -}}gvisor
{{- else -}}native
{{- end -}}
{{- end -}}
