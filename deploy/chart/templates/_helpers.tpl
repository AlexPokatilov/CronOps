{{- define "cronops.labels" -}}
app.kubernetes.io/part-of: cronops
app.kubernetes.io/managed-by: {{ .Release.Service }}
helm.sh/chart: {{ .Chart.Name }}-{{ .Chart.Version }}
{{- end }}
