# JudgeX — 现代在线评测系统

JudgeX 是一套面向编程教育的现代在线评测系统（Online Judge），类似 LeetCode 但可自托管。支持 C++/Python/Java/Go/Rust 多语言自动判题，ACM 和 IOI 双赛制，Playground 多文件编程工作区，以及 AI 诊断系统（苏格拉底式调试引导 + 题目质量检测 + SRE 诊断）。后端 Go + 前端 Vue 3，沙箱基于 cgroup v2 / chroot / seccomp-BPF，生产环境 K3s 分布式部署，Docker Compose 一键本地运行。

生产环境：https://joyan.site

## 快速启动

```bash
# 后端（终端 1）
go build -o server ./cmd/server && INSECURE=1 SANDBOX_MODE=native JUDGEX_NAMESPACE=0 ./server

# 前端（终端 2）
cd frontend && npx vite --host
```

后端 `http://localhost:8080` | 前端 `http://localhost:5173`

### 或使用 Docker Compose 一键启动

```bash
docker compose up -d --build
```

自动启动 MySQL、Redis、NSQ、后端、判题 Worker、前端、Prometheus、Grafana、Alertmanager 共 9 个服务。

## 核心功能

### 判题系统
- **题库管理**：CRUD + 标签分类 + 全文搜索，4 级缓存防穿透（Bloom Filter → Redis → 空值标记 → Singleflight + DB）
- **代码提交**：提交 → NSQ 异步判题 → 沙箱隔离执行 → SSE 实时推送结果
- **沙箱安全**：3 层隔离 — cgroup v2 限制 CPU/内存/进程数、chroot 隔离文件系统、seccomp-BPF 白名单过滤系统调用。K8s 环境可切换 gVisor 用户态内核运行
- **竞赛系统**：ACM 模式（二进制判题，罚时公式）+ IOI 模式（部分分），Redis ZSet 实时排名 + SSE 推送
- **5 语言支持**：C++、Python、Java、Go、Rust

### AI 诊断系统

**AI 诊断助手** — 苏格拉底式调试引导。根据判题结果自动选择诊断策略：

| 判题结果 | 诊断策略 |
|----------|----------|
| **CE** | 将代码 + 编译器错误输出发给 LLM 分析语法/语义错误 |
| **TLE** | 参考解验证题目数据一致性 + 代码时间复杂度分析 |
| **WA/RE** | 自愈修复循环：LLM 生成修复代码 → 沙箱验证 → 通过则成功，否则重试（最多 3 次）。同时自动检查参考解是否也在该测试点上失败，判断是否可能是测试数据有误 |

**题目质量检测**（诊断中自动触发）：
- 样例验证：参考解运行样例，检测样例输入/输出是否一致，发现问题创建 P1 反馈
- 测试数据验证：对 WA 的测试点用参考解重新运行，若参考解也无法通过则标记测试数据异常
- 结果写入 `problem_feedback` 表，管理后台审核处理

**SRE 诊断**（独立端点）：
- `POST /api/admin/sre/diagnose` — 采集系统快照（队列/沙箱/数据库/运行时/eBPF），发给 LLM 生成诊断报告

AI 基础设施：
- 兼容 OpenAI Chat Completion API，支持 DeepSeek / 通义千问 / OpenAI 等
- 流式 SSE 响应，首 Token < 1.5s
- 断路器（Circuit Breaker）：连续失败自动熔断降级
- Prompt 注入防护：15 条正则规则，三级威胁评估（none/low/high），高风险自动拦截

### SRE 诊断与可观测性

**系统监控面板**（`/admin/dashboard`）采集多维度数据：

| 维度 | 采集内容 |
|------|----------|
| 队列 | 后端类型（NSQ/Redis/Local）、积压长度、Worker 数量 |
| 提交统计 | 近 1 小时提交量、AC 率、按状态分布 |
| 沙箱 | cgroup 路径、运行模式、健康状态 |
| 数据库 | 连接池状态（最大/打开/在用/空闲连接数）、连通性 |
| 运行时 | Go 协程数、内存使用、GC 次数、服务运行时间 |
| eBPF 追踪 | 网络延迟异常边、自动缓解措施记录（需 K3s 集群中部署 eBPF 追踪器）|
| 最近错误 | 按题目/状态聚合的错误统计 |

AI 健康分析入口（`POST /api/admin/sre/diagnose`）将系统快照发给 LLM 生成诊断报告。

监控栈：
- Prometheus 自研指标 + Alertmanager 告警
- OpenTelemetry 分布式追踪，支持 Jaeger/Tempo
- Grafana 仪表盘 + 结构化日志

### 管理后台
- 系统监控面板（SRE 多维度实时快照）
- 用户管理（升/降级 + 删除）
- 题目管理（新建/编辑 + 测试用例上传）
- 比赛管理
- 题目质量反馈审核
- 公告管理

### 用户功能
- 注册/登录（JWT），三级角色（普通用户/管理员/超级管理员）
- 题库浏览：双栏题面 + Monaco 编辑器 + 代码提交
- Playground：VS Code 风格多文件编程工作区，实时语法检查，文件下载
- 全局 AC 排行
- 个人信息编辑 + 代码模板
- 移动端适配：汉堡菜单、响应式网格、Touch 友好交互
- 404 友好引导页

## 前端页面

| 路由 | 页面 | 说明 |
|------|------|------|
| `/` | 首页 | JudgeX |
| `/login` | 登录 | 登录/注册 |
| `/playground` | Playground | 多文件编程工作区 |
| `/problems` | 题库 | 题目列表（搜索/标签/分页） |
| `/problems/:id` | 题目详情 | 双栏：题面 + 在线编辑 + 调试 |
| `/problems/:id/code` | 全屏编辑器 | 全屏代码编辑 |
| `/contests` | 比赛 | 比赛列表 |
| `/contests/:id` | 比赛详情 | 题目表 + 实时排行榜 |
| `/contests/:cid/problems/:id` | 比赛作答 | 比赛题目页面 |
| `/submissions` | 提交记录 | 提交列表 |
| `/submissions/:id` | 提交详情 | 评测结果 + AI 诊断 |
| `/leaderboard` | 排行榜 | 前 50 名 |
| `/profile` | 个人资料 | 编辑资料 + 代码模板 |
| `/admin/dashboard` | 系统监控 | SRE 面板 + AI 健康诊断 |
| `/admin/users` | 用户管理 | 升/降级 + 删除用户 |
| `/admin/problems/*` | 题目管理 | 新建/编辑题目 + 测试用例 |
| `/admin/contests/*` | 比赛管理 | 新建/编辑比赛 |
| `/admin/problem-feedback` | 题目质量反馈 | AI 自动检测的问题列表 |
| `/admin/announcements` | 公告管理 | 发布/编辑/删除系统公告 |
| `/*` | 404 | 友好引导页 |

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.24 + Gin + GORM + MySQL 8 |
| 缓存 | Redis 7（缓存/ZSet/Hash/PubSub/Streams/Bloom Filter） |
| 队列 | NSQ（默认）/ Redis Streams / 本地通道 |
| 前端 | Vue 3 + TypeScript + Tailwind CSS v4 + Monaco Editor |
| 沙箱 | cgroup v2 + chroot + seccomp BPF（Native）/ gVisor runsc（K8s）|
| AI | LLM 流式对话（兼容 OpenAI 协议），多 Agent 系统，AST 插桩引擎，断路器，注入防护 |
| 存储 | 本地磁盘 / MinIO / S3 |
| 可观测 | Prometheus + Alertmanager + OpenTelemetry（Jaeger/Tempo）+ Grafana + Loki |
| 部署 | Docker Compose / Helm / K3s / K8s（KEDA 自动扩缩容）|
| CI/CD | GitHub Actions（lint + test + build + docker push）|

## 项目结构

```
cmd/
├── server/main.go              # API 服务器入口（Gin HTTP）
├── judge-worker/main.go        # 判题 Worker（NSQ 消费者）
├── worker/main.go              # 通用 Worker
├── importer/main.go            # Codeforces 格式题目批量导入
└── stress/main.go              # 压力测试工具

internal/
├── handler/                    # HTTP 请求处理器
├── middleware/                 # JWT 认证 / 日志 / 追踪
├── judge/                      # 判题引擎（编译 + 运行 + 比对）
├── sandbox/                    # 沙箱（cgroup + chroot + seccomp）
├── queue/                      # 消息队列抽象（NSQ / Redis / Local）
├── cache/                      # 缓存层（Redis + Bloom Filter）
├── model/                      # GORM 数据模型
├── ai/                         # LLM 集成层（客户端/断路器/注入防护/AST 插桩）
├── worker/                     # 判题 Worker 核心 + LRU 缓存
├── diagnostics/                # SRE 系统诊断快照采集
├── bpf/                        # eBPF 追踪器指标采集
├── tracing/                    # OpenTelemetry 分布式追踪
├── metrics/                    # Prometheus 自研指标
├── config/                     # 配置加载
├── database/                   # MySQL 初始化 + AutoMigrate
└── storage/                    # 测试用例存储抽象（本地/MinIO/S3）

frontend/                       # Vue 3 SPA
├── src/
│   ├── views/                  # 页面组件
│   └── components/             # 通用组件

deploy/                         # 部署辅助脚本
k8s/                            # K3s 部署清单
helm/                           # Helm Chart
```

## 相关文档

| 文档 | 说明 |
|------|------|
| [部署指南](DEPLOY.md) | 云服务器从零部署（Docker Compose / 裸机 / K8s）|
| [服务器信息](SERVER.md) | 生产服务器状态（K3s + HTTPS）|
| [CLAUDE.md](CLAUDE.md) | Claude Code 项目约定（部署加速、常见问题）|
