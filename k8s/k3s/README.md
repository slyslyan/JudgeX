# JudgeX on k3s

Adapted Kubernetes manifests for k3s (lightweight K8s).

## Differences from standard k8s/

| Standard k8s | k3s |
|---|---|
| `06-runtimeclass.yaml` (gVisor) | **Skipped** — k3s has no RuntimeClass support |
| `22-judge-worker.yaml` (gVisor + unprivileged) | `judge-worker.yaml` (native sandbox + elevated caps) |
| `40-ingress.yaml` (nginx ingress) | `ingress.yaml` (Traefik + Middleware CRD) |
| `23-judge-worker-scaledobject.yaml` (KEDA) | **Skipped** — install KEDA separately if needed |
| `01-configmap.yaml` (SANDBOX_MODE=gvisor) | `configmap.yaml` (SANDBOX_MODE=native) |

## Quick Start on Tencent Cloud CVM

### 1. Install k3s

```bash
curl -sfL https://rancher-mirror.rancher.cn/k3s/k3s-install.sh | sh -
# 国内用镜像加速
curl -sfL https://rancher-mirror.rancher.cn/k3s/k3s-install.sh | INSTALL_K3S_MIRROR=cn sh -
```

Get kubeconfig:
```bash
sudo k3s kubectl get nodes
# 或者
sudo cat /etc/rancher/k3s/k3s.yaml > ~/.kube/config
chmod 600 ~/.kube/config
```

### 2. Install Docker (for building images)

```bash
curl -fsSL https://get.docker.com | bash
sudo usermod -aG docker $USER
# 重新登录后生效
```

### 3. Build images + push to registry (or import locally)

**Option A: Import to containerd (单机最简单)**
```bash
cd /opt/judgex
make docker-build
docker save judgex-backend:latest | sudo k3s ctr images import -
docker save judgex-frontend:latest | sudo k3s ctr images import -
```

**Option B: Push to Tencent Cloud TCR**
```bash
# 先在腾讯云控制台创建 TCR 仓库
docker tag judgex-backend:latest ccr.ccs.tencentyun.com/<namespace>/judgex-backend:latest
docker push ccr.ccs.tencentyun.com/<namespace>/judgex-backend:latest
```

### 4. Edit secrets

```bash
vim k8s/01-secret.yaml
# 修改 JWT_SECRET, ADMIN_PASSWORD, DB_PASSWORD
```

### 5. Deploy

```bash
# 用本地镜像（已导入 containerd）
./k8s/k3s/deploy.sh

# 或从 registry 拉取
./k8s/k3s/deploy.sh ccr.ccs.tencentyun.com/judgex
```

### 6. Access

- 测试: `kubectl -n judgex port-forward svc/frontend 8081:80`
- 或通过 Ingress + Traefik: 把域名解析到服务器 IP，修改 `ingress.yaml` 的 `host`

### 7. Create super_admin

```bash
kubectl -n judgex exec -it deploy/mysql -- mysql -u judgex -pjudgex123 -e \
  "UPDATE users SET role='super_admin' WHERE id=1;" judgex
```

## cgroup v2 Setup

每台 worker 节点启动时需要 enable cgroup subtree:

```bash
echo "+cpu +memory +pids" | sudo tee /sys/fs/cgroup/judgex/cgroup.subtree_control
```

可以在 `/etc/rc.local` 或 systemd unit 里自动执行。
