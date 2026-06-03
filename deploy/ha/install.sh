#!/usr/bin/env bash
# Deploy HA database infrastructure (MySQL + Redis)
#
# Usage: ./deploy/ha/install.sh

set -euo pipefail

cd "$(dirname "$0")/../.."
NAMESPACE="judgex"

echo "=== Adding Bitnami Helm repo ==="
helm repo add bitnami https://charts.bitnami.com/bitnami 2>/dev/null || true
helm repo update

echo ""
echo "=== Deploying MySQL with replication ==="
helm upgrade --install mysql bitnami/mysql \
  --values ./deploy/ha/mysql-values.yaml \
  --namespace "$NAMESPACE" \
  --wait --timeout 10m

echo ""
echo "=== Deploying Redis with Sentinel ==="
helm upgrade --install redis bitnami/redis \
  --values ./deploy/ha/redis-values.yaml \
  --namespace "$NAMESPACE" \
  --wait --timeout 10m

echo ""
echo "=== Done ==="
echo ""
echo "Update judgex config:"
echo "  MySQL primary:  mysql-primary.judgex.svc.cluster.local:3306"
echo "  MySQL replica:  mysql-secondary.judgex.svc.cluster.local:3306"
echo "  Redis:          redis.judgex.svc.cluster.local:6379 (via Sentinel)"
echo ""
echo "Then update helm/judgex/values.yaml config:"
echo "  config.db.host: mysql-primary.judgex.svc.cluster.local"
echo "  config.redisAddr: redis.judgex.svc.cluster.local:6379"
echo ""
echo "NOTE: After deploying HA databases, run judgex Helm upgrade:"
echo "  helm upgrade --install judgex ./helm/judgex --values helm/judgex/values-prod.yaml"
