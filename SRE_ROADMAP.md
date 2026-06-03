# JudgeX SRE Roadmap

## Overview

This document defines the phased SRE improvement plan for JudgeX, progressing from
minimum-viable production readiness to advanced self-healing architecture. Each phase
builds on the previous one. Items within a phase are ordered by priority.

---

## Phase 1 — Production Readiness (Pre-Launch)

**Goal:** Survive public traffic safely. When something breaks, you can pinpoint the
root cause within minutes, not hours.

### 1.1 Sandbox Security — Kill `privileged: true` ✅ DONE

**Why this is first:** Running user-submitted arbitrary code inside a privileged
container is a catastrophic security risk. A single container-escape exploit gives an
attacker root on the K8s node and lateral movement into your internal network.

**Solution — gVisor (kernel-level isolation):**

Google's gVisor (`runsc`) provides a user-space kernel that intercepts system calls
before they reach the host kernel. This blocks malicious syscalls at the boundary.

**Implemented in:**
- `k8s/06-runtimeclass.yaml` — gVisor RuntimeClass (handler: runsc)
- `k8s/22-judge-worker.yaml` — `runtimeClassName: gvisor`, unprivileged container
- `internal/sandbox/sandbox.go` — gVisor-aware sandbox mode (skips unshare/cgroups,
  keeps chroot + seccomp for defense-in-depth)
- `k8s/01-configmap.yaml` — `SANDBOX_MODE: gvisor`
- `docker-compose.yml` — `SANDBOX_MODE: native` for local dev, documented tradeoffs

**Files:**
- `k8s/06-runtimeclass.yaml` — gVisor RuntimeClass ✅
- `k8s/22-judge-worker.yaml` — gVisor + unprivileged + CAP_SYS_CHROOT ✅
- `internal/sandbox/sandbox.go` — gVisor mode detection + execution path ✅
- `internal/config/config.go` — `SandboxMode` field ✅
- `docker-compose.yml` — native vs gVisor docs ✅

---

### 1.2 Observability Stack — The Three Pillars

**Why this is second:** When a user says "my submission is stuck on Pending", you
currently have no way to trace that request through API → NSQ → Judge → DB. The
observability stack gives you that power.

#### 1.2.1 Metrics (Prometheus + Grafana)

- Deploy `kube-prometheus-stack` via Helm (Prometheus, NodeExporter, KubeStateMetrics)
- Expose `/metrics` at `:8080/metrics` ✅
- **Golden Signals** for JudgeX:
  - **Traffic:** submissions/minute, API requests/second ✅
  - **Errors:** 5xx rate, submission failure rate (WA/TLE/RE rate) ✅
  - **Latency:** API latency histogram (p50/p95/p99 via bucket counters) ✅
  - **Saturation:** queue depth, active judgements, DB connection pool usage ✅
- API latency histogram added to Gin middleware (`judgex_api_latency_bucket{le="..."}`) ✅

#### 1.2.2 Logs (Loki + Promtail) ✅

- Helm templates created: `helm/judgex/templates/loki.yaml` — ConfigMap + Service + Deployment + PVC ✅
- Helm templates created: `helm/judgex/templates/promtail.yaml` — ConfigMap (JSON log pipeline,
  extracts `level`, `request_id`) + DaemonSet + ServiceAccount + ClusterRole + ClusterRoleBinding ✅
- Both disabled by default in `helm/judgex/values.yaml` (toggle via `observability.loki.enabled`)
- Install script: `deploy/observability/install.sh` — installs both with `helm upgrade --install --set` flags ✅
- All backend services emit structured JSON logs via `internal/metrics/metrics.go` (`DefaultJSONLog`) ✅
- `request_id` propagated through middleware for log correlation ✅
- LogQL queries become possible: `{app="backend"} |= "submission_id:12345"`

#### 1.2.3 Traces (OpenTelemetry + Jaeger) ✅ DONE

- OpenTelemetry SDK integrated (`internal/tracing/tracing.go`)
- OTLP gRPC export when `OTEL_EXPORTER_OTLP_ENDPOINT` is set, stdout in dev
- W3C TraceContext propagated through NSQ/Redis to judge worker
- Gin middleware, NSQ publisher, and judge worker all instrumented

**SLI & SLO Definition:**

| SLI | Target | Measurement |
|-----|--------|-------------|
| Submission → first result latency | p95 < 5s | Trace span: submit → status update |
| API availability | 99.9% (monthly) | HTTP 5xx / total requests |
| Judge queue wait time | p95 < 2s | NSQ message timestamp diff |
| DB query latency | p99 < 100ms | GORM auto-instrumented spans |

**Files to change:**
- `internal/middleware/` — add `Tracing()` middleware
- `internal/handler/submission.go` — inject trace context into NSQ task
- `internal/worker/worker.go` — extract trace context, create child span
- `internal/database/mysql.go` — add OTel GORM plugin
- `internal/cache/redis.go` — add OTel Redis instrumentation
- `cmd/server/main.go` — init OTel SDK + exporter
- `cmd/judge-worker/main.go` — init OTel SDK + exporter
- `go.mod` — add `go.opentelemetry.io/otel` and related packages

**New K8s resources (not applied locally, config-only):**
- `k8s/observability/` directory with Helm values files for:
  - `prometheus-values.yaml`
  - `loki-values.yaml`
  - `tempo-values.yaml`
  - `grafana-dashboard-judgex.json`

---

### 1.3 Automated Deployment & Basic HA

#### 1.3.1 Helm Chart ✅

Moved from raw `kubectl apply` to a Helm chart with templated, versioned configuration.

- `helm/judgex/` chart with `values.yaml` + all templates ✅
- `helm/judgex/values-prod.yaml` — production overrides (replicas, resources, ingress, HPA, KEDA) ✅
- Support for different environments via values files ✅
- Loki + Promtail templates included as sub-chart templates ✅
- Chaos Mesh, MySQL HA, Redis HA deploy scripts in `deploy/` directory ✅

#### 1.3.2 CI/CD Pipeline ✅

GitHub Actions workflow (`.github/workflows/ci.yml`):
- Push/PR to `main` → lint, vet, test (MySQL + Redis service containers), frontend type-check + build ✅
- Docker build & push to GHCR on main branch ✅
- Optional K8s deploy step (requires `KUBE_CONFIG` secret) ✅

#### 1.3.3 Database HA (MySQL + Redis) ✅

Deployment scripts and Helm values for HA database infrastructure:

- `deploy/ha/install.sh` — one-script deploy: Bitnami MySQL + Redis via Helm ✅
- `deploy/ha/mysql-values.yaml` — replication architecture (1 primary + 1 secondary, 10GB each) ✅
- `deploy/ha/redis-values.yaml` — Sentinel enabled (quorum=2, 1 master + 2 replicas, 1GB master) ✅
- Config supports HA via `DB_HOST` and `REDIS_ADDR` env vars (set in ConfigMap) ✅

---

### 1.4 Production Hardening (New)

#### 1.4.1 Graceful Shutdown + HTTP Timeouts ✅

- HTTP server configured with `ReadTimeout=15s`, `WriteTimeout=120s`, `IdleTimeout=60s`
- SIGINT/SIGTERM handler drains connections with 30s timeout
- Prevents dropped requests during K8s rolling updates

#### 1.4.2 DB Connection Pool Configuration ✅

- `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME` env vars
- Sensible defaults: 50 open / 10 idle / 1h lifetime
- Pool stats exposed in `/ready` and diagnostics snapshot

#### 1.4.3 Global IP Rate Limiter ✅

- Public endpoints protected with per-IP rate limiting:
  - Auth (register/login): 20/min
  - Problems/Contests/Leaderboard: 60/min
- Uses existing Redis-based `IncrWithTTL` with in-memory fallback

#### 1.4.4 Comprehensive Health Checks ✅

- `/ready` checks: MySQL (critical), Redis, NSQ, disk space, goroutine count
- Disk space: critical at <10%, degraded at <20%
- Goroutine threshold: degraded at >5000

#### 1.4.5 Enhanced Diagnostics Snapshot ✅

- System runtime: goroutine count, memory alloc (MB), GC cycles, uptime
- DB pool stats: max/open/in-use/idle connections
- Exposed via `/api/admin/sre/snapshot` and admin dashboard

---

## Phase 2 — Scaling & Performance (Post-Launch Optimization)

**Goal:** Handle a 2000-person contest without the system falling over.

### 2.1 Storage Architecture Overhaul — Kill the Shared PVC ✅

**Problem:** Test cases are on a shared PVC mounted to all judge workers. Under load,
reading thousands of small `.in`/`.out` files through a network filesystem creates an
IO bottleneck that slows every single judgement.

**Solution: Object Storage + Local Disk Cache**

```
┌──────────────┐     ┌──────────────┐     ┌──────────────┐
│ Judge Worker │     │ Judge Worker │     │ Judge Worker │
│  ┌────────┐  │     │  ┌────────┐  │     │  ┌────────┐  │
│  │LRU Cache│  │     │  │LRU Cache│  │     │  │LRU Cache│  │
│  │(tmpfs)  │  │     │  │(tmpfs)  │  │     │  │(tmpfs)  │  │
│  └────┬───┘  │     │  └────┬───┘  │     │  └────┬───┘  │
│       │ miss │     │       │ miss │     │       │ miss │
│  ┌────▼───┐  │     │  ┌────▼───┐  │     │  ┌────▼───┐  │
│  │SSD Path│  │     │  │SSD Path│  │     │  │SSD Path│  │
│  │(host)  │  │     │  │(host)  │  │     │  │(host)  │  │
│  └────┬───┘  │     │  └────┬───┘  │     │  └────┬───┘  │
└───────┼──────┘     └───────┼──────┘     └───────┼──────┘
        │                    │                    │
        └────────────────────┼────────────────────┘
                             │
                    ┌────────▼────────┐
                    │   MinIO / S3    │
                    │  (test case     │
                    │   ZIP archives) │
                    └─────────────────┘
```

- S3/MinIO integration complete: `storage.Backend` interface with `S3Backend` + `LocalBackend` ✅
- Upload handler wired to `storage.Default.SaveTestCases()` instead of direct disk writes ✅
- List/delete handlers use `storage.Default.ListTestCases()` / `DeleteTestCases()` ✅
- Judge worker reads via `readTestCasesFromStorage()` → filesystem fallback ✅
- Config via env vars: `S3_ENDPOINT`, `S3_ACCESS_KEY`, `S3_SECRET_KEY`, `S3_BUCKET` ✅
- LRU disk cache in `internal/worker/cache.go` with version-based invalidation ✅
- Redis version caching for test case staleness detection ✅

**Files changed:**
- `internal/storage/storage.go` — `Backend` interface + `LocalBackend` ✅
- `internal/storage/s3.go` — `S3Backend` (MinIO/AWS S3 compatible) ✅
- `internal/worker/cache.go` — LRU local disk cache with versioning ✅
- `internal/worker/worker.go` — load from cache → storage → disk fallback ✅
- `internal/handler/problem.go` — upload/list/delete wired to storage layer ✅
- `internal/config/config.go` — S3/MinIO config fields ✅

**Key terms:** Compute-storage separation, IOPS bottleneck, multi-level cache,
cache miss handling

---

### 2.2 Intelligent Autoscaling — Replace CPU HPA with KEDA ✅ DONE

**Problem:** Judge workers peg CPU at 100% during compilation and execution. Using
CPU utilization for scaling decisions causes **thrashing** — the system scales up
reactively when it's already saturated.

**Solution:** Scale based on actual work waiting, not CPU busyness.

- Install KEDA (Kubernetes Event-driven Autoscaling) via kubectl or Helm
- Create a `ScaledObject` that watches CPU utilization
- KEDA polls the Kubernetes metrics API
- Scale rules:
  - Target: CPU > 60% utilization
  - Min replicas: 1
  - Max replicas: 10
  - Polling interval: 15s
  - Scale-down cooldown: 120s (prevents flapping)

> **Note:** The original design used a Prometheus trigger on `judgex_queue_depth`,
> but was changed to CPU trigger because Prometheus is not deployed in the current
> environment. The Prometheus-based approach is still viable if `kube-prometheus-stack`
> is installed.

```
┌─────────────────────────────────────────────────┐
│                   KEDA                           │
│  ┌──────────┐     ┌──────────────┐              │
│  │  Scaler  │────▶│ /metrics     │ (every 5s)   │
│  │Prometheus│     │ backend:8080 │              │
│  └────┬─────┘     └──────────────┘              │
│       │ queue depth > 20                          │
│       ▼                                          │
│  ┌──────────┐                                    │
│  │ Scale    │ → HPA → Deployment replicas++      │
│  │ Decision │                                    │
│  └──────────┘                                    │
└─────────────────────────────────────────────────┘
```

**Implemented in:**
- `k8s/23-judge-worker-scaledobject.yaml` — KEDA ScaledObject (CPU trigger)
- `internal/metrics/metrics.go` — `judgex_queue_depth` gauge
- `cmd/server/main.go` — polls `NSQDepth()` every 5s for the metric
- `internal/queue/queue.go` — `NSQDepth()` measures NSQ/Redis/local depth

**Files:**
- `k8s/23-judge-worker-scaledobject.yaml` — KEDA ScaledObject ✅
- `internal/metrics/metrics.go` — queue depth metric ✅
- `internal/queue/queue.go` — queue depth measurement ✅

---

### 2.3 Cache Stampede Defense — Singleflight + Multi-Level Cache

**Problem:** At contest start, 1000 users simultaneously open Problem A. If Redis
has no cached entry, 1000 DB queries fire at once — this is a **cache stampede**
(more specifically, cache miss storm / thundering herd).

The project already has a `singleflight` implementation in `internal/cache/redis.go`
(`Do()` function). This needs to be wired into the hot paths.

**Changes:**

1. **Problem detail (hottest path):**
   - `internal/handler/problem.go` `Get()` — use `cache.Do("problem:"+id, fn)` to
     ensure only one DB query per cold key, all concurrent requests share the result
   - Already has Redis cache with 10min TTL — add local in-memory cache (30s TTL)
     as L1, Redis as L2

2. **Leaderboard:**
   - Add local cache (5s TTL) in front of Redis for contest leaderboard
   - During active contest, 5s staleness is acceptable for the leaderboard display

3. **Rate limiting at the API layer:**
   - Already have `RateLimitSubmission()` middleware (10/min per user)
   - Consider adding a global concurrency limiter for the problem detail endpoint
     using a token bucket or semaphore pattern

**Files to change:**
- `internal/handler/problem.go` — wrap DB query in `cache.Do()`
- `internal/handler/leaderboard.go` — add local cache layer
- `internal/cache/redis.go` — enhance local cache with size limit and stats

**Key terms:** Cache avalanche/breakdown/penetration, singleflight, hot key
governance, thundering herd

---

## Phase 3 — Advanced Architecture (Expert-Level)

**Goal:** Differentiate yourself. These are the items that prove you're not just
a CRUD developer but a full-stack high-availability SRE expert.

### 3.1 Chaos Engineering — Prove the System Survives ✅

Use **Chaos Mesh** to inject failures and verify resilience.

- Created `deploy/chaos/` directory with 3 experiment YAML files + install script ✅

**Experiment 1 — Network partition (backend ↔ MySQL/Redis):**
- `deploy/chaos/01-network-partition.yaml` — latency (300ms), packet loss (20%), full partition
- Tests: backend resilience to DB latency, connection pool, query timeouts, Redis failover

**Experiment 2 — Slow MySQL / MySQL crash:**
- `deploy/chaos/02-mysql-stress.yaml` — pod-kill (30s), CPU stress (80%), I/O latency (100ms)
- Tests: GORM reconnection, query queueing, degrated operation, replication lag

**Experiment 3 — NSQ failure:**
- `deploy/chaos/03-nsq-failure.yaml` — nsqd pod-kill (1m), pod-failure (3m), network delay (500ms)
- Tests: queue fallback (local buffer), producer retry, backlog reprocessing

**Install script:** `deploy/chaos/install.sh` — Chaos Mesh install + experiment lifecycle (install/apply/stop/clean)

#### 3.1.1 手动混沌测试记录 (2026-06-02)

在生产 K3s 集群上执行了两次手动混沌实验，验证系统韧性：

**测试 1: 杀 Pod 测试 — 验证 K8s 自动恢复 ✅**

| 操作 | 结果 |
|------|------|
| 随机删除一个 backend Pod | Deployment ReplicaSet 在 ~10s 内自动创建新 Pod |
| 再次删除另一个 backend Pod | 同样自动恢复，两个副本均恢复正常 |
| 服务健康检查 | 全程 HTTP 200（另一副本承接流量，无中断） |

**结论:** K8s Deployment + 多副本保障了单 Pod 故障不中断服务。

**测试 2: 网络延迟注入 — 验证依赖降级韧性 ✅**

用 `tc` (traffic control) 在 `cni0` 和 `eth0` 接口注入延迟：

| 场景 | 注入方式 | 延迟 | 系统表现 |
|------|----------|------|----------|
| MySQL 延迟 | cni0 filter dst MySQL pod | 2000ms ± 500ms | Go 连接池吸收延迟，Readiness 仍为 healthy |
| Pod 间延迟 | cni0 filter dst backend pods | 3000ms ± 500ms | localhost 接口不受影响，服务正常 |
| 外网延迟 | eth0 netem | 200ms | 内部通信正常，用户体验下降（需从外部验证） |

**结论:**
- Go MySQL 连接池有效隔离 DB 延迟，池化连接不受单次 Ping 超时影响
- `/health`（纯存活检查）不受依赖延迟影响
- `/ready`（完整依赖检查）设计合理，可配合 K8s 探针超时实现自动摘除
- 单节点架构下网络延迟影响范围有限，分布式部署需更全面的混沌网格

**真实故障复盘（同日上午）:**

| 项目 | 内容 |
|------|------|
| 故障现象 | OJ 网站无法访问，HTTP 503 |
| 根因 | `ebpf-oj-monitor` 每秒数百行 TCP 延迟日志 → systemd journald → rsyslog → `/var/log/syslog` 文件 21.8G，磁盘 91% |
| 触发机制 | `/ready` Readiness 探针检测磁盘 < 10% 剩余 → 返回 503 → K8s 摘除 Pod |
| 修复 | 紧急：清空 syslog + 清理 journald + 重启 backend。最终：三层限流（见下方） |
| 预防 | 三层限流方案：① systemd LogRateLimitBurst=5/s ② rsyslog 过滤 ebpf 日志不进 syslog ③ journald 上限 500M |

```bash
# 第三层：systemd 限流
sudo mkdir -p /etc/systemd/system/ebpf-tracer.service.d
cat > /etc/systemd/system/ebpf-tracer.service.d/limits.conf << 'EOF'
[Service]
LogRateLimitIntervalSec=1s
LogRateLimitBurst=5
EOF

# 第二层：rsyslog 过滤（00- 优先级高于 50-default.conf）
cat > /etc/rsyslog.d/00-drop-ebpf.conf << 'EOF'
:programname, isequal, "ebpf-oj-monitor" stop
EOF

# 第一层：journald 兜底
echo 'SystemMaxUse=500M' >> /etc/systemd/journald.conf

sudo systemctl daemon-reload
sudo systemctl restart rsyslog systemd-journald ebpf-tracer
```

---

### 3.2 Real-Time Push — SSE Already Implemented ✅

**Status:** Backend `StreamEvents` SSE endpoint (`GET /api/submissions/:id/events`) uses Redis Pub/Sub. Contest leaderboard SSE (`GET /api/contests/:id/leaderboard/events`) also implemented. Frontend ContestDetail.vue now uses EventSource for leaderboard (replaced polling), with fetch fallback. SubmissionDetail.vue uses ReadableStream (fetch-based SSE). ✅

**Key terms:** Push vs pull, connection overhead reduction, Server-Sent Events,
WebSocket alternatives

---

### 3.3 AIOps — Closed-Loop Self-Healing ✅

**Problem:** The SRE AI Agent (already implemented in `internal/handler/ai.go`
`SREDiagnose`) is passive — someone has to manually trigger it.

**Solution:** Make it proactive and closed-loop.

**Architecture:**

```
┌──────────────────┐
│  Prometheus      │
│  AlertManager    │
│  (webhook)       │
└────────┬─────────┘
         │ alert: "p95 judge latency > 10s"
         ▼
┌──────────────────┐     ┌──────────────────┐
│  Alert Receiver   │────▶│  SRE AI Agent    │
│  (new endpoint)   │     │  (diagnose)      │
└──────────────────┘     └────────┬─────────┘
                                  │ diagnosis + recommendation
                                  ▼
                         ┌──────────────────┐
                         │  JSON Response   │
                         │  (future: IM bot)│
                         └──────────────────┘
```

**Implemented:**
- `POST /api/admin/alerts/webhook` — receives AlertManager webhook payload ✅
  - Parses AlertManager JSON format (firing/resolved alerts)
  - Auto-collects `diagnostics.Collect()` system snapshot
  - Feeds to SRE AI agent with 60s timeout
  - Returns structured JSON: `{status, ai_diagnosis, alert_count, snapshot}`
  - Route requires admin auth
- Auto-remediation (IM bot integration, auto-scale) is future work

**Files changed:**
- `internal/handler/ai.go` — added `AlertWebhook` handler, `alertManagerWebhook` types, `buildAlertSummary`
- `cmd/server/main.go` — registered `POST /api/admin/alerts/webhook`

**Key terms:** AIOps, self-healing, closed-loop automation, AlertManager webhook

---

### 3.4 External Dependency Circuit Breaker

**Problem:** If the LLM API (OpenAI/DeepSeek) is down or rate-limited, the SSE
streaming handler holds goroutines open for 60-90 seconds. Enough concurrent AI
requests could exhaust the goroutine pool and degrade the main API.

**Solution:** Circuit breaker + graceful degradation.

**Pattern:**

```
                    ┌──────────────────┐
                    │  Circuit Breaker  │
                    │  (gobreaker)      │
                    │                   │
  AI Request ──────▶│  State: CLOSED   │──▶ LLM API
                    │  State: OPEN     │──▶ Return "AI unavailable"
                    │  State: HALF_OPEN│──▶ Probe with 1 request
                    └──────────────────┘
```

- Closed: normal operation, requests pass through
- Open: after 5 consecutive failures within 60s, circuit opens — all requests
  immediately return a graceful degradation message without calling the LLM
- Half-open: after 30s, allow 1 probe request. If it succeeds → closed. If it
  fails → back to open.

**Graceful degradation:** When the circuit is open, the AI chat endpoint returns:
> "AI assistant is temporarily unavailable. You can still submit code and view
> results. Please try again in a moment."

The judge system continues working normally — AI is an enhancement, not a dependency.

**Implementation:**

```go
// internal/ai/breaker.go
var cb *gobreaker.CircuitBreaker

func init() {
    cb = gobreaker.NewCircuitBreaker(gobreaker.Settings{
        Name:        "llm-api",
        MaxRequests: 1,
        Interval:    30 * time.Second,
        Timeout:     30 * time.Second,
        ReadyToTrip: func(counts gobreaker.Counts) bool {
            failureRatio := float64(counts.TotalFailures) / float64(counts.Requests)
            return counts.Requests >= 5 && failureRatio >= 0.6
        },
    })
}
```

**Files to change:**
- `internal/ai/breaker.go` — new file: circuit breaker wrapper
- `internal/ai/client.go` — wrap HTTP call in breaker
- `internal/handler/ai.go` — check breaker state, return degraded response
- `go.mod` — add `github.com/sony/gobreaker`

**Key terms:** Circuit breaker pattern, graceful degradation, bulkhead isolation,
cascading failure prevention

---

## Implementation Order Summary

```
Phase 1 (Pre-Launch — Do First)
├── 1.1 Sandbox Security: gVisor kernel-level isolation ✅
├── 1.2 Observability: Prometheus + Loki + OpenTelemetry + Jaeger ✅
│   ├── 1.2.1 Metrics: /metrics with latency histogram ✅
│   ├── 1.2.2 Logs: Structured JSON + Loki/Promtail deployment ✅
│   └── 1.2.3 Traces: OpenTelemetry + Jaeger ✅
├── 1.3 Deployment: Helm + CI/CD + MySQL/Redis HA ✅
└── 1.4 Production Hardening: graceful shutdown, timeouts, rate limit, health checks ✅

Phase 2 (Post-Launch Scaling)
├── 2.1 Storage: MinIO + Local LRU Cache (remove shared PVC) ✅
├── 2.2 Autoscaling: KEDA with CPU trigger ✅
└── 2.3 Cache Protection: Singleflight + Multi-level cache ✅

Phase 3 (Expert-Level Differentiators)
├── 3.1 Chaos Engineering: Chaos Mesh experiments ✅
├── 3.2 Real-Time Push: SSE instead of polling ✅
├── 3.3 AIOps: Closed-loop self-healing with LLM ✅
└── 3.4 Circuit Breaker: LLM degradation protection ✅
```

## Quick Reference Card

| Concern | Current | Target | Tool |
|---------|---------|--------|------|
| Sandbox isolation | gVisor (K8s) / native caps (compose) | Kernel-level isolation | gVisor (runsc) ✅ |
| Metrics | Prometheus counters + latency histogram | Golden signals dashboard | Prometheus + Grafana ✅ |
| Logs | Structured JSON + Loki/Promtail | Centralized log aggregation | Loki + Promtail ✅ |
| Traces | OpenTelemetry (OTLP) | Full API→NSQ→Judge→DB | OpenTelemetry + Jaeger ✅ |
| Server lifecycle | Graceful shutdown + timeouts | No dropped connections on rollout | `http.Server` + signal ✅ |
| Rate limiting | Per-IP on public endpoints | Abuse protection | Redis `IncrWithTTL` ✅ |
| Health checks | MySQL/Redis/NSQ/disk/goroutines | Comprehensive dependency check | `/ready` endpoint ✅ |
| DB connection pool | Configurable via env vars | Tune per environment | `DB_MAX_*` env vars ✅ |
| Deployment | Helm chart + GitHub Actions | GitOps pipeline | Helm + GitHub Actions ✅ |
| MySQL HA | Bitnami Helm (primary + replica) | Automated failover | Bitnami MySQL Helm ✅ |
| Redis HA | Bitnami Helm (Sentinel) | Sentinel cluster | Bitnami Redis Helm ✅ |
| Test case storage | S3/MinIO + local disk cache | Object store + local cache | MinIO + LRU on tmpfs ✅ |
| Autoscaling | KEDA CPU trigger | CPU utilization driven | KEDA + CPU ScaledObject ✅ |
| Cache protection | Singleflight + L1/L2 | Multi-level cache defense | `cache.Do()` + local ✅ |
| AI resilience | Circuit breaker + prompt guard | Circuit breaker + degrade | `gobreaker` ✅ |
| Real-time updates | SSE + Redis Pub/Sub | Push-based | EventSource + SSE ✅ |
| Failure testing | Chaos Mesh experiments | Planned chaos | Chaos Mesh ✅ |
| AIOps | AlertManager webhook + auto-diagnose | Auto-diagnose on alert | AlertManager webhook ✅ |
