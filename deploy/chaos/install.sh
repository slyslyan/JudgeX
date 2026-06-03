#!/usr/bin/env bash
# Chaos Mesh — Install and deploy resilience experiments for JudgeX
#
# Usage:
#   ./deploy/chaos/install.sh          # Install Chaos Mesh + apply experiments
#   ./deploy/chaos/install.sh clean    # Remove experiments + Chaos Mesh
#   ./deploy/chaos/install.sh apply    # Apply experiments only
#   ./deploy/chaos/install.sh stop     # Stop all running experiments
#
# Prerequisites: Helm 3, kubectl, cluster admin access

set -euo pipefail
cd "$(dirname "$0")"

NAMESPACE="judgex"
CHAOS_NS="chaos-mesh"
EXPERIMENTS=(
  "01-network-partition.yaml"
  "02-mysql-stress.yaml"
  "03-nsq-failure.yaml"
)

install_chaos_mesh() {
  echo "=== Installing Chaos Mesh ==="
  helm repo add chaos-mesh https://charts.chaos-mesh.org 2>/dev/null || true
  helm repo update
  kubectl create namespace "$CHAOS_NS" 2>/dev/null || true
  helm upgrade --install chaos-mesh chaos-mesh/chaos-mesh \
    --namespace="$CHAOS_NS" \
    --set chaosDaemon.runtime=containerd \
    --set chaosDaemon.socketPath=/run/k3s/containerd/containerd.sock \
    --wait --timeout 5m
  echo "  Waiting for Chaos Mesh pods to be ready..."
  kubectl wait --for=condition=Ready pods --all -n "$CHAOS_NS" --timeout=120s
  echo "  Done."
}

apply_experiments() {
  echo "=== Applying Chaos Mesh experiments ==="
  for exp in "${EXPERIMENTS[@]}"; do
    echo "  Applying $exp ..."
    kubectl apply -f "$exp" --namespace="$NAMESPACE"
  done
  echo "  Done. Experiments are running."
  echo ""
  echo "  To check experiment status:"
  echo "    kubectl get chaos -n judgex"
  echo ""
  echo "  To stop all experiments:"
  echo "    $0 stop"
}

stop_experiments() {
  echo "=== Stopping all experiments ==="
  for exp in "${EXPERIMENTS[@]}"; do
    echo "  Stopping $exp ..."
    kubectl delete -f "$exp" --namespace="$NAMESPACE" 2>/dev/null || true
  done
  echo "  Done."
}

clean_all() {
  stop_experiments
  echo "=== Uninstalling Chaos Mesh ==="
  helm uninstall chaos-mesh --namespace="$CHAOS_NS" 2>/dev/null || true
  echo "  Done."
}

case "${1:-install}" in
  install)
    install_chaos_mesh
    apply_experiments
    ;;
  apply)
    apply_experiments
    ;;
  stop)
    stop_experiments
    ;;
  clean)
    clean_all
    ;;
  *)
    echo "Usage: $0 {install|apply|stop|clean}"
    exit 1
    ;;
esac
