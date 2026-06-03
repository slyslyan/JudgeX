# JudgeX 服务器文档

## 服务器信息

| 项目 | 值 |
|------|-----|
| IP | 150.158.113.146 |
| 系统 | Ubuntu (K3s 单节点) |
| SSH | `ssh ubuntu@150.158.113.146` |
| 密码 | Sly050810 |

## 访问地址

- **网站**: http://150.158.113.146:8080
- **API**: http://150.158.113.146:8080/api
- **管理员账号**: admin / adminadmin

## 架构

系统运行在 K3s (轻量 Kubernetes) 上，所有服务在 `judgex` 命名空间：

```
judgex/
├── backend (2 副本)     — Go API 服务器 :8080
├── frontend (1 副本)     — Nginx + Vue SPA :80
├── judge-worker (1 副本) — 判题 Worker
├── mysql-0               — MySQL 数据库
├── redis                  — Redis 缓存
├── nsqd-0                 — NSQ 消息队列
├── nsqlookupd             — NSQ 服务发现
└── nsqadmin               — NSQ 管理界面
```

> **KEDA 自动扩缩容：** 项目提供了 `k8s/23-judge-worker-scaledobject.yaml` 用于 KEDA 自动扩缩，
> 但当前服务器未安装 KEDA（ghcr.io 镜像拉取受限）。如需启用，先安装 KEDA，再 `kubectl apply -f k8s/23-judge-worker-scaledobject.yaml`。
> 手动扩缩容：`kubectl scale deployment -n judgex judge-worker --replicas=N`

流量入口：Traefik Ingress (端口 80) + Backend LoadBalancer (端口 8080)
- 端口 8080 的 backend 同时提供 API 和前端静态文件

## 常用命令

```bash
# SSH 连接
ssh ubuntu@150.158.113.146

# 查看 Pod 状态
kubectl get pods -n judgex

# 查看服务
kubectl get svc -n judgex

# 查看日志
kubectl logs -n judgex deployment/backend --tail=50
kubectl logs -n judgex deployment/judge-worker --tail=50

# 重启服务
kubectl rollout restart deployment -n judgex backend
kubectl rollout restart deployment -n judgex frontend
kubectl rollout restart deployment -n judgex judge-worker

# MySQL 连接
kubectl exec -n judgex mysql-0 -- mysql -u judgex -p judgex
# 密码来自 Secret: kubectl get secret -n judgex judgex-secret -o jsonpath='{.data.DB_PASSWORD}' | base64 -d

# 查看 ConfigMap
kubectl get configmap -n judgex judgex-config -o yaml
```

## 更新部署

### 方式：Docker 镜像更新（推荐）

```bash
# 1. 构建前端
cd frontend && npx vite build && cd ..

# 2. 构建 Docker 镜像
docker build -t localhost/judgex-backend:latest .
docker build -t localhost/judgex-frontend:latest ./frontend

# 3. 导出并上传
docker save localhost/judgex-backend:latest localhost/judgex-frontend:latest -o /tmp/judgex-images.tar
scp /tmp/judgex-images.tar ubuntu@150.158.113.146:/tmp/

# 4. SSH 到服务器，导入并重启
ssh ubuntu@150.158.113.146
sudo k3s ctr images import /tmp/judgex-images.tar
kubectl rollout restart deployment -n judgex backend
kubectl rollout restart deployment -n judgex frontend
kubectl rollout restart deployment -n judgex judge-worker
kubectl rollout status deployment -n judgex backend
kubectl rollout status deployment -n judgex frontend
kubectl rollout status deployment -n judgex judge-worker
```

### Makefile 快捷命令

```bash
# 本地构建
make build-server    # 编译后端
make build-worker    # 编译判题 worker

# Docker 镜像
make docker-build    # 构建 backend + frontend 镜像

# Helm (K8s)
make helm-template   # 渲染 Helm 模板
make helm-upgrade    # 部署到 K3s
make helm-status    # 查看服务状态
```

## 环境变量

`judgex-config` ConfigMap 中的关键配置：

| 变量 | 值 |
|------|-----|
| DB_HOST | mysql.judgex.svc.cluster.local |
| DB_PORT | 3306 |
| DB_USER | judgex |
| REDIS_ADDR | redis.judgex.svc.cluster.local:6379 |
| NSQD_ADDR | nsqd.judgex.svc.cluster.local:4150 |
| SANDBOX_MODE | native |
| LLM_API_URL | https://dashscope.aliyuncs.com/compatible-mode/v1 |
| LLM_MODEL | qwen-plus |

密码等敏感信息在 `judgex-secret` Secret 中。

## 数据备份

```bash
# 导出数据库
kubectl exec -n judgex mysql-0 -- mysqldump -u root -p judgex > judgex_backup.sql

# 测试用例文件在 PVC testcases 中
kubectl get pvc -n judgex
```
