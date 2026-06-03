# JudgeX — 在线判题系统

## 项目是什么

JudgeX 是一个类似 LeetCode 的在线判题系统。用户可以在线编写代码、提交运行，系统在安全沙箱中编译执行并给出判定结果。支持竞赛功能（ACM 和 IOI 两种赛制）。

## 技术栈

| 层 | 技术 |
|----|------|
| 语言 | Go 1.24（后端）、Vue 3 + TypeScript（前端） |
| 框架 | Gin（HTTP 路由）、GORM（ORM）、Tailwind v4（CSS） |
| 数据库 | MySQL 8（主库）、Redis 7（缓存/队列/排名/PubSub） |
| 消息队列 | NSQ（默认）、Redis Streams（备选）、Local Channel（开发） |
| 容器化 | Docker + K3s + Helm |
| 监控 | Prometheus + Grafana + OpenTelemetry + eBPF |
| 沙箱 | cgroup v2 + chroot + seccomp-BPF（Native）/ gVisor（K8s） |
| AI | LLM 流式对话（OpenAI 协议）、7 种 Agent、注入攻击防护 |
| 存储 | 本地磁盘 / MinIO / S3 |
| CI/CD | GitHub Actions |

## 核心功能

- **题目管理**：CRUD + 标签分类 + 全文搜索 + 三级缓存防穿透
- **代码提交**：提交 → NSQ 异步判题 → 沙箱隔离执行 → SSE 实时推送结果
- **沙箱安全**：3 层隔离 — cgroup 限制 CPU/内存/进程数、chroot 隔离文件系统、seccomp-BPF 白名单过滤系统调用
- **竞赛系统**：ACM 模式（二进制判题，罚时公式）+ IOI 模式（部分分），Redis ZSet 实时排名 + SSE 推送
- **AI Agent 系统**：7 种 AI 角色（错误诊断、引导教学、代码 Debug、SRE 监控等），SSE 流式输出，断路器保护，注入防护正则库
- **监控系统**：Prometheus 指标 + Grafana 仪表盘 + 分布式追踪（OpenTelemetry）+ eBPF 网络追踪
- **SRE Agent**：智能运维助手，5 个工具（系统快照、告警规则、节点重启、分析报告、eBPF 指标）
- **部署**：Docker Compose（开发）/ K3s（生产），KEDA 自动扩缩容

## 项目结构

```
cmd/
├── server/main.go           # API 服务器（Gin HTTP）
├── judge-worker/main.go     # 判题 Worker（NSQ 消费者）

internal/
├── handler/                 # HTTP 请求处理器
├── middleware/              # JWT 认证 / 日志 / 追踪
├── judge/judge.go          # 判题引擎（编译 + 运行 + 比对）
├── sandbox/                # 沙箱（cgroup + chroot + seccomp）
├── queue/queue.go          # 消息队列抽象（NSQ / Redis / Local）
├── cache/redis.go          # 缓存 + PubSub + ZSet + Streams
├── model/model.go          # GORM 数据模型
├── ai/                     # LLM 集成（客户端/提示词/注入防护/断路器）
├── worker/                 # 判题 Worker 核心逻辑 + LRU 缓存
├── diagnostics/            # 系统监控快照
└── storage/storage.go      # 测试用例存储抽象

frontend/                   # Vue 3 SPA
k8s/                        # K3s 部署清单
```
