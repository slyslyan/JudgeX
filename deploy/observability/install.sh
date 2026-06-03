#!/usr/bin/env bash
# Install observability stack (Prometheus + Grafana + Loki + Promtail)
# Run this after deploying JudgeX via Helm.
#
# Usage: ./deploy/observability/install.sh [on|off]
#   on  - enable and deploy observability stack (default)
#   off - disable observability stack

set -euo pipefail

cd "$(dirname "$0")/../.."
ACTION="${1:-on}"
HELM="helm"
RELEASE="judgex"

case "$ACTION" in
  on)
    echo "Enabling observability stack..."
    $HELM upgrade --install "$RELEASE" ./helm/judgex \
      --reuse-values \
      --set "observability.prometheus.enabled=true" \
      --set "observability.grafana.enabled=true" \
      --set "observability.loki.enabled=true" \
      --set "observability.promtail.enabled=true" \
      --namespace judgex \
      --wait --timeout 5m
    echo "Observability stack deployed."
    echo ""
    echo "Access:"
    echo "  Prometheus: kubectl port-forward -n judgex svc/prometheus 9090:9090"
    echo "  Grafana:    kubectl port-forward -n judgex svc/grafana 3000:3000"
    echo "  Loki:       kubectl port-forward -n judgex svc/loki 3100:3100"
    echo ""
    echo "Grafana login: admin / admin"
    ;;
  off)
    echo "Disabling observability stack..."
    $HELM upgrade --install "$RELEASE" ./helm/judgex \
      --reuse-values \
      --set "observability.prometheus.enabled=false" \
      --set "observability.grafana.enabled=false" \
      --set "observability.loki.enabled=false" \
      --set "observability.promtail.enabled=false" \
      --namespace judgex
    echo "Observability stack disabled."
    ;;
  *)
    echo "Usage: $0 [on|off]" >&2
    exit 1
    ;;
esac
