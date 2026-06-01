{{- define "service.name" -}}
{{- .Values.service.name -}}
{{- end -}}

{{- define "service.labels" -}}
app.kubernetes.io/name: {{ include "service.name" . }}
app.kubernetes.io/managed-by: devedge
{{- end -}}
