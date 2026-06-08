# JudgeX — 现代在线评测系统

JudgeX 是一套面向编程教育的现代在线评测系统（Online Judge）。Apple 极简风格中文界面，Monaco 代码编辑器，支持 C++/Python/Java/Go/Rust 多语言自动判题，ACM 和 IOI 双赛制，Playground 多文件编程工作区，以及 7-Agent AI 矩阵（含全自动代码 Debug 和 SRE 运维助手）。后端 Go + 前端 Vue 3，沙箱基于 cgroup v2 / chroot / seccomp-BPF（K8s 下 gVisor），K3s/K8s 分布式部署。

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
- 用户系统：注册/登录（JWT），三级角色
- 题库：CRUD + 分页 + 搜索 + 标签，Markdown 描述，Redis 缓存
- 代码提交：5 语言支持，NSQ/Redis Streams/本地队列
- 比赛：ACM + IOI 双赛制，Redis ZSet 实时排行榜，SSE 推送
- 排行榜：全局 AC 排行
- Playground：VS Code 风格多文件编程工作区，实时语法检查，文件下载
- 个人信息：资料编辑、密码修改、代码模板
- 管理后台：左侧栏系统监控 + 用户管理（升/降/删除）+ 题目质量反馈（AI 自动检测问题，管理员审核删除）
- 移动端适配：汉堡菜单导航抽屉、响应式网格布局、Touch 友好交互
- 404 页面：未匹配路由显示友好引导页，一键返回首页

### AI 助手（7 Agent）

| Agent | 触发方式 | 功能 |
|-------|----------|------|
| **错误诊断** | 提交详情页「AI 诊断」按钮 | WA/TLE/RE/CE 精准分析 |
| **苏格拉底导师** | 题目详情页「需要提示？」按钮 | 引导式提问，不给答案 |
| **测试数据生成** | 管理后台测试用例页 | AI 生成 Python 测试脚本 |
| **虚拟教练** | 浮动 AI 聊天（全页面） | 上下文感知对话 + 建议 Chip |
| **SRE 诊断** | 管理后台系统监控 | 系统快照采集 + AI 健康分析 |
| **AI Debug Agent** | 提交详情页 / 全屏编辑器 | 全自动 Debug 闭环：加载题面 → 运行 10 组测试 → LLM 分析 → 提取修复代码 → 验证通过。LLM prompt 中质量检查优先级高于代码修复——先检查测试数据是否与题目描述一致、样例是否正确、描述有无歧义；若 AI 高置信度检测到问题，立即停止修复流程并写入 feedback 表供 `/admin/problem-feedback` 审核，不提用户代码 |
| **SRE 运维助手** | 管理后台系统监控 | ReAct 风格 4 工具：系统快照、告警规则、节点重启、24h 分析报告 |

### 安全
- cgroup v2 + chroot + seccomp BPF 三层沙箱隔离（K8s 下 gVisor 用户态内核）
- Prompt 注入检测（15 条规则，三级威胁评估，高风险自动拦截）
- SSE 流式 AI 响应，首 Token < 1.5s
- LLM API 断路器（gobreaker），连续失败自动熔断降级

### 缓存策略
- 题目详情 `problem:{id}` 缓存 10 分钟 TTL，编辑时主动删除保证一致性
- 空值标记 `problem:null:{id}` 缓存 5 分钟，防相同 ID 重复穿透
- **Bloom Filter** 内存布隆过滤器（~12KB，1% 误报率），启动时从数据库加载所有题目 ID，定期重建，快速拦截不存在的 ID
- **IP 限流** `RateLimit(60/min)` 挂载在所有公开 API 上，防遍历攻击
- **Singleflight** 同一时刻多个并发请求只查一次数据库
- 判题 worker 使用两级测试数据缓存：Redis 版本号缓存 + 进程内 LRU（100 条，1 小时 TTL），结合 `test_case_version` 字段实现自动失效

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
| `/leaderboard` | 排行榜 | 前50名 |
| `/profile` | 个人资料 | 编辑资料 + 代码模板 |
| `/admin/dashboard` | 系统监控 | SRE + AI 诊断 |
| `/admin/users` | 用户管理 | 升/降级 + 删除用户 |
| `/admin/problems/*` | 题目管理 | 新建/编辑题目 + 测试用例 |
| `/admin/contests/*` | 比赛管理 | 新建/编辑比赛 |
| `/admin/problem-feedback` | 题目质量反馈 | AI 自动发现的题目问题列表（P1 紧急 / P2 一般），管理员审核修正后可删除 |
| `/*` | 404 | 未匹配路由显示友好 404 页面，引导返回首页 |
| `DELETE /api/admin/problem-feedback/:id` | 删除反馈 | 管理员处理完成后删除已解决的反馈条目 |

## API

完整 API 列表见 CLAUDE.md。

## 技术栈

| 层 | 技术 |
|----|------|
| 后端 | Go + Gin + GORM + MySQL |
| 缓存 | Redis（缓存/ZSet/Hash/PubSub/Streams） |
| 队列 | NSQ / Redis Streams / 本地通道 |
| 前端 | Vue 3 + TypeScript + Tailwind CSS v4 + Monaco Editor |
| 沙箱 | cgroup v2 + chroot + seccomp BPF / gVisor (runsc) |
| 存储 | 本地磁盘 / MinIO / S3 |
| 可观测 | Prometheus + Grafana + OpenTelemetry + Jaeger + Loki |
| AI | 兼容 OpenAI 协议（DeepSeek、通义千问等），7 Agent，断路器，注入防护 |
| 部署 | Docker Compose / Helm / K3s / K8s（KEDA 自动扩缩容） |
| CI/CD | GitHub Actions（lint + test + build + docker push） |

## 配色

Apple 极简风格：浅色 `#f5f5f7` / 深色 `#161616` 分层炭灰，品牌蓝 `#0071e3`，全中文界面。

## 相关文档

| 文档 | 说明 |
|------|------|
| [项目简介](PROJECT_INTRO.md) | 技术栈 + 核心功能 + 项目结构一览 |
| [面试学习指南](STUDY_GUIDE.md) | 代码级逐文件解读、算法详解、面试问答 |
| [部署指南](DEPLOY.md) | Docker Compose / 裸机 / K8s 部署步骤 |
| [SRE 路线图](SRE_ROADMAP.md) | 生产就绪 → 扩缩容 → 高级架构 |
