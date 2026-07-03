# JudgeX 云服务器部署指南

> 适用场景：一台全新的云服务器（Ubuntu 22.04 / 24.04），从零开始部署 JudgeX 在线判题系统。本文档面向新手，每一步都有解释。
>
> **生产服务器：** `https://joyan.site`（腾讯云 CVM，Docker Compose + Nginx 反代）

---

## 目录

- [1. 前置准备](#1-前置准备)
- [2. 快速部署（Docker Compose，推荐）](#2-快速部署docker-compose推荐)
- [3. 手动部署（裸机安装）](#3-手动部署裸机安装)
- [4. Kubernetes 部署（进阶）](#4-kubernetes-部署进阶)
- [5. 验证系统是否正常](#5-验证系统是否正常)
- [6. 常见问题排障](#6-常见问题排障)

---

## 1. 前置准备

### 1.1 买好云服务器之后，先 SSH 登录

```bash
# 在你自己电脑的终端里执行（不是服务器上）：
ssh root@你的服务器IP

# 示例：
ssh root@123.456.789.0
```

云服务商（阿里云/腾讯云/AWS 等）会给你一个公网 IP 和 root 密码。

### 1.2 创建一个普通用户（不要一直用 root）

```bash
# 登录后，在服务器上执行：
adduser judgex
usermod -aG sudo judgex

# 切换到新用户
su - judgex
```

之后的步骤都在 `judgex` 用户下操作，需要权限时加 `sudo`。

### 1.3 更新系统 + 安装基础工具

```bash
sudo apt update && sudo apt upgrade -y
sudo apt install -y curl wget git vim ufw
```

### 1.4 配置防火墙

```bash
# 开放 SSH 端口（必须先做，否则可能断连）
sudo ufw allow 22/tcp

# 开放 Web 端口
sudo ufw allow 80/tcp
sudo ufw allow 443/tcp
sudo ufw allow 8080/tcp   # 后端 API（直接访问时用）

# 开启防火墙
sudo ufw enable

# 查看状态
sudo ufw status
```

> **云服务器安全组**：除了服务器内部的 ufw，还要在云服务商控制台的「安全组」里开放 22、80、443、8080 端口。不同云服务商操作方式不同，一般在控制台搜索「安全组」就能找到。

### 1.5 安装 Docker（所有部署方式都需要）

```bash
# 添加 Docker 官方 GPG 密钥
sudo apt update
sudo apt install -y ca-certificates curl
sudo install -m 0755 -d /etc/apt/keyrings
sudo curl -fsSL https://download.docker.com/linux/ubuntu/gpg -o /etc/apt/keyrings/docker.asc
sudo chmod a+r /etc/apt/keyrings/docker.asc

# 添加 Docker 仓库
echo "deb [arch=$(dpkg --print-architecture) signed-by=/etc/apt/keyrings/docker.asc] https://download.docker.com/linux/ubuntu $(. /etc/os-release && echo "$VERSION_CODENAME") stable" | sudo tee /etc/apt/sources.list.d/docker.list > /dev/null

# 安装 Docker
sudo apt update
sudo apt install -y docker-ce docker-ce-cli containerd.io docker-buildx-plugin docker-compose-plugin

# 把当前用户加入 docker 组（之后不用 sudo 也能用 docker）
sudo usermod -aG docker $USER

# 重新登录使 docker 组生效
exit
ssh judgex@你的服务器IP   # 重新登录

# 验证 Docker
docker --version
docker compose version
```

### 1.6 上传项目代码到服务器

在**你自己电脑**上执行（不是在服务器上）：

```bash
# 方法一：用 scp 上传整个项目
cd /home/sly/Downloads
scp -r oj judgex@你的服务器IP:~/oj

# 方法二：如果代码在 GitHub/Gitee 上，直接在服务器上 clone
# ssh judgex@你的服务器IP
# git clone https://github.com/你的用户名/judgex.git ~/oj
```

---

## 2. 快速部署（Docker Compose，推荐）

这是最简单的方式，一条命令启动全部服务。

### 2.1 检查 Docker 是否正常

```bash
docker run hello-world
# 看到 "Hello from Docker!" 就说明正常
```

### 2.2 启动所有服务

```bash
cd ~/oj

# 启动（首次会下载镜像+构建，需要几分钟）
docker compose up -d --build
```

这个命令会自动启动 7 个服务：

| 服务 | 作用 | 端口 |
|------|------|------|
| mysql | 数据库 | 3307（外部） |
| redis | 缓存 | 6379 |
| nsqd | 消息队列 | 4150, 4151 |
| nsqlookupd | NSQ 服务发现 | 4160, 4161 |
| nsqadmin | NSQ 管理界面 | 4171 |
| backend | Go API 服务 | 8080 |
| judge-worker | 判题工作进程（2 个） | 无 |
| frontend | 前端页面 | 8081 |

### 2.3 查看启动状态

```bash
# 查看所有容器运行状态
docker compose ps

# 查看后端日志
docker compose logs backend

# 查看所有日志
docker compose logs -f
```

### 2.4 访问系统

- 前端页面：`http://你的服务器IP:8081`
- 后端 API：`http://你的服务器IP:8080`
- NSQ 管理界面：`http://你的服务器IP:4171`
- 健康检查：`http://你的服务器IP:8080/health`

### 2.5 停止/重启

```bash
# 停止
docker compose down

# 重启
docker compose up -d
```

### 2.6 配置 Nginx 反代（可选，但建议做）

如果不想带端口号访问（想让 `http://你的IP` 直接访问前端），安装 nginx：

```bash
sudo apt install -y nginx

# 创建配置
sudo tee /etc/nginx/sites-available/judgex << 'EOF'
server {
    listen 80;
    server_name _;

    # 前端
    location / {
        proxy_pass http://127.0.0.1:8081;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
    }

    # 后端 API
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 120s;
    }

    # 健康检查
    location /health {
        proxy_pass http://127.0.0.1:8080;
    }
}
EOF

# 启用配置
sudo ln -s /etc/nginx/sites-available/judgex /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t && sudo systemctl reload nginx
```

之后直接访问 `http://你的服务器IP` 就能看到前端了。

---

## 3. 手动部署（裸机安装）

如果想深入理解每个组件，或者 Docker 不可用，可以用这种方式。

### 3.1 安装 Go

```bash
# 下载 Go 1.24
wget https://go.dev/dl/go1.24.2.linux-amd64.tar.gz
sudo tar -C /usr/local -xzf go1.24.2.linux-amd64.tar.gz
rm go1.24.2.linux-amd64.tar.gz

# 添加到 PATH
echo 'export PATH=$PATH:/usr/local/go/bin' >> ~/.bashrc
echo 'export GOPATH=$HOME/go' >> ~/.bashrc
source ~/.bashrc

go version  # 确认安装成功
```

### 3.2 安装 MySQL 8.0

```bash
sudo apt install -y mysql-server

# 启动 MySQL
sudo systemctl enable mysql
sudo systemctl start mysql

# 安全设置（设 root 密码，其他全部选 Y）
sudo mysql_secure_installation

# 创建数据库和用户
sudo mysql -u root <<SQL
CREATE DATABASE IF NOT EXISTS judgex CHARACTER SET utf8mb4 COLLATE utf8mb4_unicode_ci;
CREATE USER IF NOT EXISTS 'judgex'@'localhost' IDENTIFIED BY 'judgex123';
GRANT ALL PRIVILEGES ON judgex.* TO 'judgex'@'localhost';
FLUSH PRIVILEGES;
SQL
```

### 3.3 安装 Redis

```bash
sudo apt install -y redis-server

sudo systemctl enable redis-server
sudo systemctl start redis-server

# 验证
redis-cli ping   # 返回 PONG
```

### 3.4 安装 NSQ

```bash
# 下载 NSQ 二进制文件
wget https://github.com/nsqio/nsq/releases/download/v1.3.0/nsq-1.3.0.linux-amd64.go1.22.2.tar.gz
tar xzf nsq-1.3.0.linux-amd64.go1.22.2.tar.gz
sudo cp nsq-1.3.0.linux-amd64.go1.22.2/bin/* /usr/local/bin/
rm -rf nsq-1.3.0.linux-amd64.go1.22.2*

# 创建 systemd 服务

# nsqlookupd
sudo tee /etc/systemd/system/nsqlookupd.service << 'EOF'
[Unit]
Description=NSQ Lookupd
After=network.target

[Service]
ExecStart=/usr/local/bin/nsqlookupd
Restart=always

[Install]
WantedBy=multi-user.target
EOF

# nsqd
sudo tee /etc/systemd/system/nsqd.service << 'EOF'
[Unit]
Description=NSQ Daemon
After=network.target nsqlookupd.service

[Service]
ExecStart=/usr/local/bin/nsqd --lookupd-tcp-address=127.0.0.1:4160 --data-path=/var/lib/nsq
Restart=always

[Install]
WantedBy=multi-user.target
EOF

sudo mkdir -p /var/lib/nsq
sudo systemctl daemon-reload
sudo systemctl enable nsqlookupd nsqd
sudo systemctl start nsqlookupd nsqd

# 验证
curl http://127.0.0.1:4151/ping   # 返回 OK
```

### 3.5 配置 cgroups v2（判题沙箱需要）

```bash
# 检查 cgroup 版本
mount | grep cgroup
# 应该看到 "cgroup2 on /sys/fs/cgroup type cgroup2"

# 创建判题专用 cgroup
sudo mkdir -p /sys/fs/cgroup/judgex

# 启用 cpu、memory、pids 控制器
echo "+cpu +memory +pids" | sudo tee /sys/fs/cgroup/judgex/cgroup.subtree_control

# 创建子 cgroup
sudo mkdir -p /sys/fs/cgroup/judgex/sandbox

# 设置持久化（重启后生效）
echo "+cpu +memory +pids" | sudo tee /sys/fs/cgroup/cgroup.subtree_control

# 添加到开机启动
sudo tee /etc/systemd/system/judgex-cgroup.service << 'EOF'
[Unit]
Description=JudgeX Cgroup Setup
After=local-fs.target

[Service]
Type=oneshot
ExecStart=/bin/bash -c 'mkdir -p /sys/fs/cgroup/judgex/sandbox && echo "+cpu +memory +pids" > /sys/fs/cgroup/cgroup.subtree_control && echo "+cpu +memory +pids" > /sys/fs/cgroup/judgex/cgroup.subtree_control'
RemainAfterExit=yes

[Install]
WantedBy=multi-user.target
EOF

sudo systemctl enable judgex-cgroup
```

### 3.6 编译并启动后端

```bash
cd ~/oj

# 创建测试用例存储目录
sudo mkdir -p /data/testcases
sudo chown -R $USER:$USER /data/testcases

# 配置环境变量
cat > ~/.judgex-env << 'EOF'
# 把这些写入 ~/.bashrc 也可
export SERVER_PORT=8080
export DB_HOST=127.0.0.1
export DB_PORT=3306
export DB_USER=judgex
export DB_PASSWORD=judgex123
export DB_NAME=judgex
export REDIS_ADDR=127.0.0.1:6379
export NSQD_ADDR=127.0.0.1:4150
export JWT_SECRET=改成你自己的随机字符串
export TEST_DATA_PATH=/data/testcases
export ADMIN_PASSWORD=改成你自己的密码
export LOG_LEVEL=info
EOF

source ~/.judgex-env

# 编译后端
go build -o server ./cmd/server

# 编译判题进程
go build -o judge-worker ./cmd/judge-worker

# 启动 API 服务
./server &

# 启动判题进程（可以开多个）
./judge-worker &
```

### 3.7 构建并部署前端

```bash
cd ~/oj/frontend

# 安装 Node.js 22（使用 NodeSource）
curl -fsSL https://deb.nodesource.com/setup_22.x | sudo -E bash -
sudo apt install -y nodejs

# 安装依赖
npm install

# 构建生产版本
npm run build

# 用 nginx 托管前端
sudo apt install -y nginx

sudo tee /etc/nginx/sites-available/judgex << 'NGINX'
server {
    listen 80;
    server_name _;

    root /home/judgex/oj/frontend/dist;
    index index.html;

    # API 反代
    location /api/ {
        proxy_pass http://127.0.0.1:8080;
        proxy_set_header Host $host;
        proxy_set_header X-Real-IP $remote_addr;
        proxy_set_header X-Forwarded-For $proxy_add_x_forwarded_for;
        proxy_read_timeout 120s;
    }

    location /health {
        proxy_pass http://127.0.0.1:8080;
    }

    # SPA 回退
    location / {
        try_files $uri $uri/ /index.html;
    }
}
NGINX

sudo ln -s /etc/nginx/sites-available/judgex /etc/nginx/sites-enabled/
sudo rm -f /etc/nginx/sites-enabled/default
sudo nginx -t && sudo systemctl reload nginx
```

---

## 4. Kubernetes 部署（进阶）

如果需要完整的分布式部署、自动扩缩容和面试展示，使用 K8s。

### 4.1 安装 Kubernetes 集群

**选项 A：单机开发用 minikube**

```bash
# 安装 minikube
curl -LO https://storage.googleapis.com/minikube/releases/latest/minikube-linux-amd64
sudo install minikube-linux-amd64 /usr/local/bin/minikube
rm minikube-linux-amd64

# 启动集群
minikube start --driver=docker --cpus=4 --memory=4096

# 启用 ingress
minikube addons enable ingress
```

**选项 B：生产环境用 kubeadm 或云服务商托管 K8s（ACK/TKE/EKS）**

这部分比较长，建议先用 minikube 熟悉，生产环境再考虑云厂商托管集群。

### 4.2 安装 gVisor（安全沙箱运行时，强烈建议）

```bash
# 在每个 K8s 节点上安装 runsc
curl -fsSL https://gvisor.dev/archive.key | sudo gpg --dearmor -o /usr/share/keyrings/gvisor-archive-keyring.gpg
echo "deb [signed-by=/usr/share/keyrings/gvisor-archive-keyring.gpg] https://storage.googleapis.com/gvisor/releases release main" | sudo tee /etc/apt/sources.list.d/gvisor.list
sudo apt-get update && sudo apt-get install -y runsc

# 配置 containerd 使用 runsc
sudo runsc install
sudo systemctl restart containerd

# 给节点打标签（让 judge-worker Pod 调度到有 gVisor 的节点）
kubectl label node <节点名> sandbox=gvisor
```

> **为什么需要 gVisor？** 判题系统需要执行用户提交的任意代码。gVisor 在应用和宿主机内核之间增加了一层用户态拦截，所有用户代码的 Syscall 都被 gVisor 接管，无法触碰宿主机内核。这彻底消灭了 `privileged: true` 的安全隐患。

### 4.3 安装 KEDA（事件驱动自动扩缩容）

KEDA 让判题 Worker 根据 CPU 负载自动扩缩容，无需人工干预。

```bash
# 安装 KEDA
helm repo add kedacore https://kedacore.github.io/charts
helm repo update
helm install keda kedacore/keda --namespace keda --create-namespace

# 验证
kubectl get pods -n keda
```

> **为什么用 KEDA 而不是 CPU HPA？** 判题机拿到一个 C++ 编译任务瞬间就会打满单核，基于 CPU 扩缩容会造成无效的频繁扩容。KEDA 直接监听队列深度或 CPU 利用率，更精准。

> **中国大陆服务器注意：** KEDA 镜像在 `ghcr.io`，国内拉取可能很慢。替代方案：
> - 使用代理镜像：`ghcr.dockerproxy.com`
> - 或用 `kubectl apply --server-side -f https://github.com/kedacore/keda/releases/download/v2.16.1/keda-2.16.1.yaml` 安装
> - 如果实在拉不动，可以跳过 KEDA，手动扩缩容：`kubectl scale deployment -n judgex judge-worker --replicas=3`

### 4.4 安装 kubectl

```bash
curl -LO "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl"
sudo install -o root -g root -m 0755 kubectl /usr/local/bin/kubectl
rm kubectl

# 验证
kubectl version --client
```

### 4.5 构建 Docker 镜像

```bash
cd ~/oj

# 构建后端镜像（包含 API 服务和判题进程两个二进制）
docker build -t judgex-backend:latest .

# 构建前端镜像
docker build -t judgex-frontend:latest ./frontend

# 如果用 minikube，需要加载镜像到 minikube
minikube image load judgex-backend:latest
minikube image load judgex-frontend:latest
```

### 4.6 部署到 Kubernetes

```bash
cd ~/oj

# 一键部署所有资源
make k8s-apply

# 或者手动逐个部署：
kubectl apply -f k8s/00-namespace.yaml
kubectl apply -f k8s/01-configmap.yaml
kubectl apply -f k8s/01-secret.yaml
kubectl apply -f k8s/06-runtimeclass.yaml
kubectl apply -f k8s/10-mysql.yaml
kubectl apply -f k8s/11-redis.yaml
kubectl apply -f k8s/12-nsq.yaml
kubectl apply -f k8s/50-pvc.yaml
kubectl apply -f k8s/20-backend.yaml
kubectl apply -f k8s/21-backend-hpa.yaml
kubectl apply -f k8s/22-judge-worker.yaml
kubectl apply -f k8s/23-judge-worker-scaledobject.yaml
kubectl apply -f k8s/31-frontend-configmap.yaml
kubectl apply -f k8s/30-frontend.yaml
kubectl apply -f k8s/40-ingress.yaml
```

### 4.7 查看部署状态

```bash
# 查看所有 Pod
kubectl -n judgex get pods -w

# 查看所有资源（包含 KEDA ScaledObject 和 gVisor RuntimeClass）
kubectl -n judgex get all,pvc,ingress,scaledobject,hpa,runtimeclass

# 查看 Pod 日志
kubectl -n judgex logs deployment/backend
kubectl -n judgex logs deployment/judge-worker
```

### 4.6 访问系统（minikube）

```bash
# 获取 minikube IP
minikube ip

# 开启 ingress 隧道
minikube tunnel
```

然后浏览器访问 `http://<minikube-ip>` 即可。

---

## 5. 验证系统是否正常

部署完成后，按以下步骤验证：

### 5.1 健康检查

```bash
# 后端存活检查
curl http://localhost:8080/health
# 返回 {"status":"ok","ts":"..."}

# 后端就绪检查（检查 MySQL、Redis、NSQ 连接）
curl http://localhost:8080/ready
# 返回 {"status":"ok","backend":"nsq","checks":{"mysql":"healthy","redis":"healthy","queue":"healthy (nsq)"},"ts":"..."}
```

### 5.2 注册用户

```bash
curl -X POST http://localhost:8080/api/auth/register \
  -H "Content-Type: application/json" \
  -d '{"username":"test","email":"test@test.com","password":"test123","password_confirm":"test123"}'
```

### 5.3 登录

```bash
curl -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"test","password":"test123"}'
# 复制返回的 token
```

### 5.4 创建一道题目（用 admin 账户）

```bash
# 先登录 admin（默认密码 adminadmin，建议立即修改）
TOKEN=$(curl -s -X POST http://localhost:8080/api/auth/login \
  -H "Content-Type: application/json" \
  -d '{"username":"admin","password":"adminadmin"}' | grep -o '"token":"[^"]*' | cut -d'"' -f4)

# 创建题目
curl -X POST http://localhost:8080/api/problems \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "title": "A+B Problem",
    "description": "计算两个整数的和",
    "time_limit": 1000,
    "memory_limit": 128,
    "sample_cases": [{"input": "1 2\n", "output": "3\n"}]
  }'
```

### 5.5 上传测试数据

```bash
# 创建测试数据目录
mkdir -p /tmp/testdata

# 创建 1.in 和 1.out
echo "1 2" > /tmp/testdata/1.in
echo "3" > /tmp/testdata/1.out

# 打包成 zip
cd /tmp/testdata && zip testdata.zip *.in *.out

# 上传（题目 ID 是 1）
curl -X POST http://localhost:8080/api/admin/problems/1/testcases \
  -H "Authorization: Bearer $TOKEN" \
  -F "file=@/tmp/testdata/testdata.zip"
```

### 5.6 提交代码测试

访问前端页面 `http://你的IP:8081`，登录后打开刚创建的题目，在 Monaco 编辑器中写代码并提交。

或者用 API：

```bash
curl -X POST http://localhost:8080/api/submissions \
  -H "Content-Type: application/json" \
  -H "Authorization: Bearer $TOKEN" \
  -d '{
    "problem_id": 1,
    "language": "python",
    "code": "a, b = map(int, input().split())\nprint(a + b)"
  }'
```

---

## 6. 常见问题排障

### Docker 方式

**Q：`docker compose up` 报权限错误**
```bash
# 确保用户在 docker 组
sudo usermod -aG docker $USER
# 然后退出重新登录
exit
ssh judgex@你的IP
```

**Q：后端连不上 MySQL**
```bash
# 查看 MySQL 日志
docker compose logs mysql

# 常见原因：端口被占用
sudo lsof -i :3307
```

**Q：判题进程报 sandbox 错误**
```bash
# Docker Compose 方式下判题需要 cgroup v2 和 Linux capabilities（已在 compose 文件中配置）
# K8s 方式下判题运行在 gVisor 沙箱中，通过 SANDBOX_MODE=gvisor 环境变量控制。
# 如果仍然报错，检查宿主机 cgroup 版本
mount | grep cgroup
# 必须是 cgroup2，如果是 cgroup v1 需要升级内核或换 Ubuntu 22.04+
```

### 裸机方式

**Q：`go build` 报错**
```bash
# 检查 Go 版本
go version  # 需要 1.24+

# 检查依赖
cd ~/oj && go mod tidy
```

**Q：MySQL 连接报错 `Access denied`**
```bash
# 检查 MySQL 用户是否存在
sudo mysql -u root -e "SELECT user,host FROM mysql.user;"

# 重新创建用户
sudo mysql -u root <<SQL
DROP USER IF EXISTS 'judgex'@'localhost';
CREATE USER 'judgex'@'localhost' IDENTIFIED BY 'judgex123';
GRANT ALL PRIVILEGES ON judgex.* TO 'judgex'@'localhost';
FLUSH PRIVILEGES;
SQL
```

**Q：NSQ 连接失败**
```bash
# 检查 NSQ 是否在运行
systemctl status nsqlookupd nsqd

# 重启 NSQ
sudo systemctl restart nsqlookupd nsqd
```

**Q：判题沙箱报 `cgroup` 相关错误**
```bash
# 检查 cgroup 版本（必须是 v2）
stat -fc %T /sys/fs/cgroup/
# 输出 cgroup2fs 才是 v2

# 如果是 tmpfs（v1），需要在内核启动参数加 systemd.unified_cgroup_hierarchy=1
# 编辑 /etc/default/grub，在 GRUB_CMDLINE_LINUX 中加入 systemd.unified_cgroup_hierarchy=1
# 然后 sudo update-grub && sudo reboot
```

**Q：编译代码的执行环境不存在（g++/python3/java 等）**
```bash
# 安装各语言运行时
sudo apt install -y g++ python3 default-jdk

# Go 和 Rust 如果也需要：
# Go 已装在 /usr/local/go
# Rust: curl --proto '=https' --tlsv1.2 -sSf https://sh.rustup.rs | sh
```

### 通用问题

**Q：端口被占用**
```bash
# 查看是谁占用了端口
sudo lsof -i :8080
sudo kill -9 <PID>
```

**Q：内存不够**
```bash
# 查看内存使用
free -h

# Docker 方式：减少 judge-worker 副本数
# 编辑 docker-compose.yml，把 replicas: 2 改成 1

# 裸机方式：只开一个 judge-worker 进程
```

**Q：云服务器外网无法访问**
- 检查云服务商控制台的「安全组」是否开放了对应端口
- 检查服务器防火墙：`sudo ufw status`
- 检查服务是否监听 `0.0.0.0` 而不是 `127.0.0.1`

### 修改默认密码（重要！）

默认 admin 密码是 `adminadmin`，默认 JWT 密钥也是弱密钥。生产环境务必修改：

```bash
# 编辑 .env.example 复制为 .env
cd ~/oj
cp .env.example .env

# 生成随机 JWT 密钥
openssl rand -base64 32

# 编辑 .env，修改以下值：
# JWT_SECRET=<上面生成的随机串>
# ADMIN_PASSWORD=<你自己设的强密码>
```

---

## 资源要求

| 配置 | 最低 | 推荐 |
|------|------|------|
| CPU | 2 核 | 4 核 |
| 内存 | 2 GB | 4 GB+ |
| 硬盘 | 20 GB | 40 GB+ |
| 系统 | Ubuntu 22.04+ | Ubuntu 24.04 |

> 云服务器推荐：阿里云 ECS / 腾讯云 CVM 2核4G 起步，约 ¥100-200/月。学生可申请优惠。

---

## 快速命令速查

```bash
# === Docker Compose 方式 ===
docker compose up -d --build    # 启动
docker compose ps                # 查看状态
docker compose logs -f backend   # 看后端日志
docker compose down              # 停止
docker compose restart           # 重启

# === 裸机方式 ===
cd ~/oj
./server &                       # 启动 API
./judge-worker &                 # 启动判题进程
sudo systemctl status mysql redis nginx nsqlookupd nsqd  # 查看各服务状态

# === K8s 方式 ===
kubectl -n judgex get pods       # 查看 Pod
kubectl -n judgex get scaledobject  # 查看 KEDA 扩缩容状态
kubectl -n judgex logs deploy/backend  # 看日志
kubectl -n judgex describe pod <pod名>  # 查看详情
make k8s-apply                   # 部署
make k8s-delete                  # 删除
```
