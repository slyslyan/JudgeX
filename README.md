# JudgeX — 现代在线评测系统

JudgeX 是一套面向编程教育的现代在线评测系统（Online Judge）。Apple 极简风格中文界面，Monaco 代码编辑器，支持 C++/Python/Java/Go/Rust 多语言自动判题，ACM 和 IOI 双赛制，Playground 多文件编程工作区，以及 AI 多 Agent 系统（诊断助手 + 苏格拉底导师 + SRE 诊断）。后端 Go + 前端 Vue 3，沙箱基于 cgroup v2 / chroot / seccomp-BPF（K8s 下 gVisor），K3s/K8s 分布式部署。

## 快速启动

```bash
# 终端 1 — 后端
cd /home/sly/Downloads/oj && go build -o server ./cmd/server && INSECURE=1 SANDBOX_MODE=native JUDGEX_NAMESPACE=0 ./server

# 终端 2 — 前端
cd /home/sly/Downloads/oj/frontend && npx vite --host
```

后端 `http://localhost:8080` | 前端 `http://localhost:5173`

## 功能

### 核心
- 用户系统：注册/登录（JWT），三级角色（普通用户 / 管理员 / 超级管理员）
- 题库：CRUD + 分页 + 搜索 + 标签，Markdown 描述，Redis 缓存
- 代码提交：5 语言支持，NSQ/Redis Streams/本地队列
- 比赛：ACM + IOI 双赛制，Redis ZSet 实时排行榜，SSE 推送
- 排行榜：全局 AC 排行
- Playground：VS Code 风格多文件编程工作区，实时语法检查，文件下载
- 个人信息：资料编辑、密码修改、代码模板
- 公告系统：管理员发布系统公告
- 管理后台：系统监控（SRE 面板）+ 用户管理 + 题目管理 + 比赛管理 + 题目质量反馈审核 + 公告管理
- 移动端适配：汉堡菜单导航抽屉、响应式网格布局、Touch 友好交互
- 404 页面：未匹配路由显示友好引导页，一键返回首页

### AI 多 Agent 系统

JudgeX 内置了多个 AI Agent，覆盖判题诊断、教学辅导和智能运维场景：

#### AI 诊断助手（Verdict-Aware Debugger）
在提交详情页点击「AI 诊断」，根据判题结果自动选择诊断策略：

| 判题结果 | 诊断策略 | 具体行为 |
|----------|----------|----------|
| **CE**（编译错误） | 静态分析 | 将代码 + 编译器错误输出发给 LLM 分析语法/语义错误 |
| **TLE**（超时） | 参考解验证 + 复杂度分析 | 用参考解答重新运行样例验证题目数据一致性；分析代码时间复杂度 |
| **WA/RE**（答案错误/运行时错误） | 插桩动态执行 + 追踪分析 | AST 插桩注入 Printf 调试语句，对失败测试用例重新运行，将执行追踪发给 LLM 进行因果分析 |

安全特性：
- **AST 插桩引擎**（Go）：使用 `go/parser` + `go/ast` 对 Go 代码自动插入调试追踪语句，非 Go 语言跳过插桩
- **IO 保护**：所有程序输出截断至 1000 行，超限 SIGKILL
- **并发限制**：最多 4 个并发插桩诊断任务，超限返回 503
- **用户限流**：每用户每分钟最多 5 次诊断请求
- **超时控制**：单次诊断最长 180 秒

#### AI 质量检测
诊断助手在分析代码错误的同时，并行执行题目质量检测：
- **样例验证**：使用参考解答运行题目样例，检测样例输入/输出是否一致，发现问题创建 P1（紧急）反馈
- **测试数据验证**：对 WA 的测试点用参考解答重新运行，若参考解答也无法通过则标记测试数据异常
- **去重机制**：写入 `problem_feedback` 表前检查是否已存在同类反馈

所有质量反馈在管理后台 `/admin/problem-feedback` 审核处理。

#### Prompt 注入防护
15 条正则规则、三级威胁评估（none / low / high），高风险自动拦截并返回教育性回复。

#### LLM 客户端基础设施
- 流式 SSE 响应，首 Token < 1.5s
- 断路器（Circuit Breaker）：连续失败自动熔断降级，返回友好提示
- 兼容 OpenAI Chat Completion API，支持 DeepSeek / 通义千问 / OpenAI 等
- 上下文控制 + 超时管理

### SRE 诊断与可观测性

**SRE 系统监控面板**（`/admin/dashboard`）采集多维度数据：

| 维度 | 采集内容 |
|------|----------|
| **队列** | 后端类型（NSQ/Redis/Local）、积压长度、Worker 数量 |
| **提交统计** | 近 1 小时提交量、AC 率、按状态分布 |
| **沙箱** | cgroup 路径、运行模式、健康状态 |
| **数据库** | 连接池状态（最大/打开/在用/空闲连接数）、连通性 |
| **运行时** | Go 协程数、内存使用、GC 次数、服务运行时间 |
| **eBPF 追踪** | 网络延迟异常边、自动缓解措施记录（需 K3s 集群中部署 eBPF 追踪器）|
| **最近错误** | 按题目/状态聚合的错误统计 |

AI 健康分析入口（`POST /api/admin/sre/diagnose`）将系统快照发给 LLM 生成诊断报告。

### 安全
- cgroup v2 + chroot + seccomp BPF 三层沙箱隔离（K8s 下 gVisor 用户态内核）
- Prompt 注入检测（15 条规则，三级威胁评估，高风险自动拦截）
- SSE 流式 AI 响应，首 Token < 1.5s
- LLM API 断路器（gobreaker），连续失败自动熔断降级

### 缓存策略
- 题目详情 `problem:{id}` 缓存 10 分钟 TTL，编辑时主动删除保证一致性
- 空值标记 `problem:null:{id}` 缓存 5 分钟，防相同 ID 重复穿透
- **Bloom Filter** 内存布隆过滤器（~12KB，1% 误报率），启动时从数据库加载所有题目 ID，定期重建
- **IP 限流** `RateLimit(60/min)` 挂载在所有公开 API 上，防遍历攻击
- **Singleflight** 同一时刻多个并发请求只查一次数据库
- 判题 worker 使用两级测试数据缓存：Redis 版本号缓存 + 进程内 LRU（100 条，1 小时 TTL）

### 可观测性
- Prometheus 自研指标（/metrics）：提交计数、API 请求/延迟/错误、队列深度、活跃判题数
- OpenTelemetry 分布式追踪：支持 Jaeger/Tempo 后端，W3C TraceContext 传播
- 结构化日志
- Grafana 仪表盘

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
| `/admin/dashboard` | 系统监控 | SRE 面板（队列/沙箱/数据库/eBPF 多维度监控） + AI 健康诊断 |
| `/admin/users` | 用户管理 | 升/降级 + 删除用户 |
| `/admin/problems/*` | 题目管理 | 新建/编辑题目 + 测试用例 |
| `/admin/contests/*` | 比赛管理 | 新建/编辑比赛 |
| `/admin/problem-feedback` | 题目质量反馈 | AI 自动检测的题目问题列表（P1 紧急 / P2 一般），管理员审核删除 |
| `/admin/announcements` | 公告管理 | 发布/编辑/删除系统公告 |
| `/*` | 404 | 友好引导页 |

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go 1.24 + Gin + GORM + MySQL |
| 缓存 | Redis（缓存/ZSet/Hash/PubSub/Streams/Bloom Filter） |
| 队列 | NSQ / Redis Streams / 本地通道 |
| 前端 | Vue 3 + TypeScript + Tailwind CSS v4 + Monaco Editor |
| 沙箱 | cgroup v2 + chroot + seccomp BPF / gVisor (runsc) |
| AI | LLM 流式对话（兼容 OpenAI 协议），3 个 AI Agent（诊断助手/苏格拉底导师/SRE 诊断），AST 插桩引擎，断路器，注入防护 |
| 存储 | 本地磁盘 / MinIO / S3 |
| 可观测 | Prometheus 指标 + OpenTelemetry 追踪（Jaeger）+ Grafana + Loki + eBPF |
| 部署 | Docker Compose / Helm / K3s / K8s（KEDA 自动扩缩容） |
| CI/CD | GitHub Actions（lint + test + build + docker push） |

## 项目结构

```
cmd/
├── server/main.go              # API 服务器入口（Gin HTTP）
├── judge-worker/main.go        # 判题 Worker（NSQ 消费者）
├── worker/main.go              # 通用 Worker（消息队列处理）
├── importer/main.go            # Codeforces 格式题目批量导入
└── stress/main.go              # 压力测试工具

internal/
├── handler/                    # HTTP 请求处理器
│   ├── ai.go                   # SRE 诊断接口
│   ├── ai_socratic_debugger.go # ★ AI 诊断助手（Verdict-Aware + AST 插桩）
│   ├── announcement.go        # 公告 CRUD
│   └── ...
├── middleware/                 # JWT 认证 / 日志 / 追踪
├── judge/                      # 判题引擎（编译 + 运行 + 比对）
├── sandbox/                    # 沙箱（cgroup + chroot + seccomp）
│   ├── sandbox.go
│   ├── seccomp.go              # seccomp-BPF 系统调用白名单
│   └── sandbox_test.go
├── queue/                      # 消息队列抽象（NSQ / Redis / Local）
├── cache/                      # 缓存层
│   ├── redis.go                # Redis 缓存 + PubSub + ZSet + Streams
│   └── bloom.go                # Bloom Filter 防遍历穿透
├── model/                      # GORM 数据模型
├── ai/                         # ★ LLM 集成层
│   ├── client.go               # 流式 SSE 客户端
│   ├── config.go               # 配置加载（12-factor）
│   ├── breaker.go              # 断路器（自动熔断降级）
│   ├── guard.go                # Prompt 注入防护（15 条规则）
│   ├── instrumenter.go         # ★ AST 源代码插桩引擎（Go）
│   └── prompt.go               # 上下文组装 + 提示词构建
├── worker/                     # 判题 Worker 核心逻辑 + LRU 缓存
├── diagnostics/                # ★ SRE 系统诊断
│   └── collector.go            # 多维度系统快照采集
├── bpf/                        # eBPF 追踪器指标采集
│   └── bpf.go                  # Prometheus 指标抓取 + 解析
├── tracing/                    # OpenTelemetry 分布式追踪
│   └── tracing.go              # OTLP gRPC / stdout 导出
├── metrics/                    # 轻量级 Prometheus 自研指标
│   └── metrics.go              # 原子操作计数器 + 文本格式输出
├── config/                     # 配置加载
├── database/                   # MySQL 初始化 + AutoMigrate
└── storage/                    # 测试用例存储抽象（本地/MinIO/S3）

frontend/                       # Vue 3 SPA
├── src/
│   ├── views/                  # 页面组件
│   │   ├── admin/
│   │   │   ├── Dashboard.vue      # SRE 系统监控面板
│   │   │   ├── ProblemFeedback.vue # 题目质量反馈管理
│   │   │   ├── Announcements.vue   # 公告管理
│   │   │   └── ...
│   │   └── ...
│   ├── components/             # 通用组件
│   │   ├── AiDiagnoseAssistant.vue # AI 诊断助手 UI
│   │   └── ...
│   └── ...

k8s/                            # K3s 部署清单
helm/                           # Helm Chart
deploy/                         # 部署辅助脚本
```

## 配色

Apple 极简风格：浅色 `#f5f5f7` / 深色 `#161616` 分层炭灰，品牌蓝 `#0071e3`，全中文界面。

## 相关文档

| 文档 | 说明 |
|------|------|
| [部署指南](DEPLOY.md) | Docker Compose / 裸机 / K8s 部署步骤 |
| [服务器信息](SERVER.md) | 生产服务器部署状态（K3s + HTTPS） |
| [CLAUDE.md](CLAUDE.md) | Claude Code 项目约定（部署加速、常见问题） |
