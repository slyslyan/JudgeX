#!/usr/bin/env bash
# JudgeX k3s deployment script (Helm)
# Usage: ./k8s/k3s/deploy.sh [registry]
#   registry: optional Docker registry prefix (e.g., "ccr.ccs.tencentyun.com/judgex")
#             omit to use local images (must pre-load into containerd)

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$(dirname "$SCRIPT_DIR")")"
HELM_DIR="$PROJECT_DIR/helm/judgex"
REGISTRY="${1:-}"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

say() { echo -e "${GREEN}[k3s-deploy]${NC} $*"; }
warn() { echo -e "${YELLOW}[k3s-deploy] WARN:${NC} $*"; }
die() { echo -e "${RED}[k3s-deploy] ERROR:${NC} $*" >&2; exit 1; }

# ---- Prerequisites ----
command -v kubectl >/dev/null 2>&1 || die "kubectl not found"
command -v helm >/dev/null 2>&1 || die "helm not found. Install: https://helm.sh/docs/intro/install/"

if [ -n "$REGISTRY" ]; then
    # ---- Build & Push to Registry ----
    say "Building Docker images..."
    DOCKER_REGISTRY="$REGISTRY" make -C "$PROJECT_DIR" docker-build

    say "Pushing images to $REGISTRY ..."
    DOCKER_REGISTRY="$REGISTRY" make -C "$PROJECT_DIR" docker-push
else
    say "No registry specified — assuming images are pre-loaded into containerd."
    say "If not, run: docker save judgex-backend:latest | sudo k3s ctr images import -"
fi

# ---- Helm Deploy ----
say "Deploying JudgeX via Helm..."
helm upgrade --install judgex "$HELM_DIR" \
  --values "$HELM_DIR/values-prod.yaml" \
  --set "image.registry=$REGISTRY" \
  --namespace judgex --create-namespace \
  --wait --timeout 5m

# ---- Post-deploy ----
say "------------------------------"
say "Deployment complete."
say ""
say "Pod status:"
kubectl -n judgex get pods
say ""
say "Services:"
kubectl -n judgex get svc
say ""
say "Ingress:"
kubectl -n judgex get ingress
say ""
warn "Next steps:"
warn "  1. Edit helm/judgex/values-prod.yaml with real passwords before production use"
warn "  2. Set up DNS A record pointing to this server's IP"
warn "  3. For HTTPS, install cert-manager or use Traefik's built-in Let's Encrypt"
warn "  4. Create first super_admin:"
warn "     kubectl -n judgex exec -it deploy/mysql -- mysql -u judgex -pjudgex123 -e \"UPDATE users SET role='super_admin' WHERE id=1;\" judgex"
