## 1 你自研了 cgroup v2 + chroot + seccomp-BPF 安全沙箱，详细讲下这三个组件各自的作用？
1. Cgroup v2 — 资源限制

  把用户程序的资源消耗关进"笼子"里。

  - CPU 限制：写 cpu.max，格式 "<quota> <period>"。比如 "2000000 100000" 表示每
  100ms 最多跑 2000ms 的 CPU 时间。超限后内核发 SIGKILL。
  - 内存限制：memory.max（硬限制，触发出 OOM Kill）+
  memory.high（软限制，触发回收）。运行后从 memory.peak
  读取精确峰值，即使进程已被 OOM 干掉也能读到，精度 KB。
  - 进程数限制：pids.max。C/C++/Go/Rust 限 16，JVM 有 GC 线程所以放宽到 32，防止
   fork bomb。

  层次结构是 /sys/fs/cgroup/judgex/judgex-<pid>-<ts>/，每个测试用例独立子
  cgroup。

  2. Chroot — 文件系统隔离

  把用户程序的"根目录"限制在一个最小化的 jail 里。

  Native 模式（setupChrootJail）：在 tmpfs 上用 MS_BIND|MS_REC 只读绑定挂载
  /usr、/lib、/lib64、/bin、/etc，挂载独立的 proc 实例，/dev 下创建
  null/zero/urandom 三个设备节点，/tmp 挂载 128MB 限额的 tmpfs
  作为唯一可写区。bind mount 方式零拷贝，不占额外磁盘。

  gVisor 模式（setupChrootJailSimple）：不能调 mount（gVisor 拦截），所以直接把
  musl 动态库 /lib/ld-musl-x86_64.so.1、libstdc++.so.6、libgcc_s.so.1 和
  /etc/resolv.conf、/etc/hosts 复制进 jail。

  3. Seccomp-BPF — 系统调用过滤

  在 chroot 之后、syscall.Exec 用户程序之前，通过 prctl(PR_SET_SECCOMP, 
  SECCOMP_MODE_FILTER) 安装一个 BPF 白名单。

  逻辑：从 seccomp_data 加载系统调用号（偏移 0），遍历约 66 个白名单项——匹配则
  ALLOW，不匹配则 SECCOMP_RET_KILL_THREAD（进程收到 SIGSYS 死掉）。在
  judge-worker 里被捕获并映射为 Runtime Error。

### 1.1 追问 1：为什么选用 cgroup v2 而不是 v1？两者有什么差异？
cgroup v1: 每个控制器有独立的 hierarchy，比如进程甲在 cpu 的 cgroup A
    里、memory 的 cgroup B 里，管理混乱
  cgroup v2: 统一 hierarchy，一个进程在一个 cgroup 节点下管控所有资源
  ────────────────────────────────────────
  cgroup v1: memory.memsw.usage_in_bytes 读取内存峰值不可靠
  cgroup v2: memory.peak 直接返回精确峰值，OOM killed 也能读到
  ────────────────────────────────────────
  cgroup v1: 需要自己协调多个子系统的层级一致性
  cgroup v2: subtree_control 让父级先放权，子级才能用，结构清晰
  ────────────────────────────────────────
  cgroup v1: 接口风格不统一
  cgroup v2: 统一接口文件命名

  核心动因就是代码里那两行：

  os.WriteFile("/sys/fs/cgroup/judgex/cgroup.subtree_control",
      []byte("+cpu +memory +pids"), 0644)

  v2 只需写一次 subtree_control 授权，所有子 cgroup 就同时受控。v1
  得分别操作三个子系统的目录树，代码复杂得多。另外 memory.peak 在 v1
  里不存在，而判题系统恰好需要准确的内存峰值来判定 MLE。


### 1.2 追问 2：seccomp-BPF 是如何过滤系统调用的？为了防止恶意代码攻击，你限制了哪些系统调用？
过滤流程（buildBPF()）：

  每条 BPF 指令就是一句 if (condition) goto X else goto 
  Y。编译出来的过滤程序长这样：

  1. LD [4]                    ← 加载 seccomp_data.arch
  2. JEQ AUDIT_ARCH_X86_64    ← 不是 x86_64 直接 KILL（防止 32 位兼容调用绕过）
  3. RET KILL_THREAD           ← 非 x86_64 杀死
  4. LD [0]                    ← 加载系统调用号
  5. JEQ SYS_READ  → ALLOW     ← 每个白名单项生成一对 JEQ + RET ALLOW
  6. JEQ SYS_WRITE → ALLOW
     ... (约 66 个)
  N. RET KILL_THREAD           ← 默认：杀死

  明确被拦截的系统调用（不在白名单，调用即死）：

  socket（虽然代码里意外列入了白名单，但 net namespace
  隔离了网络）、mount、umount、pivot_root、chroot、unshare、setns、ptrace、proce
  ss_vm_writev、bpf、perf_event_open、kexec_load、reboot、swapon——全部返回
  SIGSYS。


### 1.3 追问 3：chroot 的原理是什么？能做到彻底的安全隔离吗，有什么局限性？
原理：chroot() 系统调用把当前进程的根目录 / 改为指定的目录。代码中：

  syscall.Chdir(jailDir)
  syscall.Chroot(".")
  syscall.Chdir("/")

  先 cd 到 jail 目录，然后 chroot(".") 把当前目录变成新根。之后进程看到的 /
  实际就是 /tmp/jail-xxx/，访问 /etc/passwd 实际是
  /tmp/jail-xxx/etc/passwd（如果没有就是 "文件不存在"）。

  不能做到彻底隔离。 局限性：

  1. chroot 不是安全机制（它是 chroot，不是 jail）。如果用户程序有 root 权限且在
   jail 内能调用 mknod + 访问 /proc/self/root，或者调用 pivot_root、unshare +
  mount，就可以逃逸。
  2. 靠多层加固弥补：seccomp 拦截了
  chroot、pivot_root、mount、unshare（不在白名单，调用就
  SIGSYS）；命名空间隔离也让外面的 /proc 不可见。
  3. /proc 必须单独挂载：如果直接 bind mount 主机的 /proc，用户能通过
  /proc/1/root 读取宿主机文件系统。所以代码里挂载了全新的 proc 实例。
  4. 动态库依赖：被静态链接 CGO_ENABLED=0 编译的 Go 和 Rust 程序一旦复制进 jail
  就能直接跑，但 Python/Java 需要把解释器和动态库也放进去（或 bind mount），jail
   做不到完全和宿主机无关。
## 2 系统判题吞吐达到 200 次 / 秒，P99<3s，你做了哪些性能优化手段？
1. 三级缓存 + Bloom Filter — 防穿透

  最核心的是 problem handler 的四层防御：

  请求 → Bloom Filter(不存在直接404) → Redis(problem:{id}, TTL 10min)
        → Null Marker(problem:null:{id}, TTL 5min) → Singleflight + DB

  - Bloom Filter：12KB 的 []uint64 存约 1 万个 problem ID，FNV-1a 哈希，1%
  误判率。启动时全量加载，每 10 分钟指针交换重建（有题目被删除，新建一个bloom，把指针指向它）。如果问题不存在，在 Redis
  查询之前就返回 404。
  - Singleflight：自实现版用 sync.WaitGroup +
  map[string]*sfCall。同一时刻对同一个 key 的并发请求，只放一个去查 DB，其余通过
   WaitGroup 等结果复用——防止缓存失效瞬间 DB 被打爆。
  - Null Marker：DB 查出来也不存在的 key，写一个 5 分钟 TTL 的空标记进
  Redis，避免每次穿透到 DB。

  2. 缓存唯一性约束：单条 AC 自动失效

  性能优化的前提是缓存一致性。每条提交 AC 后，在 worker.go:293 会主动删除
  problem:{id} 缓存，确保下一请求准确命中新数据。（为了确保每次查询看到的是正确的过题人数，把缓存的删掉，直接去查看数据库）


  3. 提交去重 — 杜绝重复编译

  SHA256(userID:problemID:language:code) 放入 Redis，3 秒 TTL。handler
  侧先查重再落库，worker 侧也查一次。同一份代码 3
  秒内重复提交直接返回上次结果，省掉编译器 + 沙箱的完整链路。

  4. NSQ 双 Topic 路由 — 慢任务不阻塞快任务

  topicForLanguage() 把 C/C++/Go/Rust 路由到 judge_tasks_fast，Python/Java
  路由到 judge_tasks_slow。JVM 启动就要几百毫秒，如果和 C 语言混在一个队列里，C
  的秒级判题会被 Python/Java 的编译时间拖死。


  5. 并发架构

  - 4 个 goroutine 消费队列（Redis Streams consumer group 自动分配）
  - 沙箱执行走 /proc/self/exe reexec，天然不占 worker goroutine
  - KEDA 根据队列深度自动扩缩 worker（CPU 60%，1-10 副本）


  ---
  P99 能压在 3 秒以内的核心原因：判题系统的大部分路径（读题目、提交、查排名）是纯缓存 +索引查询，不需要进沙箱；真正进沙箱的提交通过 NSQ 分流 + 本地 LRU 省掉 IO 等待+ 双 Topic不让慢语言拖慢快语言，沙箱本身的启动开销被控制在纳秒级（/proc/self/exereexec）。

## 3 项目中用到 NSQ 消息队列承载判题任务，为什么选择 NSQ 而不是 RabbitMQ、Kafka？
  1.和项目技术栈同源——Go 原生

  ┌───────────────┬───────────────────────────────────────────────┐
  │     组件      │                     语言                      │
  ├───────────────┼───────────────────────────────────────────────┤
  │ 整个项目      │ Go                                            │
  ├───────────────┼───────────────────────────────────────────────┤
  │ NSQ           │ Go                                            │
  ├───────────────┼───────────────────────────────────────────────┤
  │ go-nsq 客户端 │ Go 原生                                       │
  ├───────────────┼───────────────────────────────────────────────┤
  │ RabbitMQ      │ Erlang（客户端库是 C 封装的 amqp 协议）       │
  ├───────────────┼───────────────────────────────────────────────┤
  │ Kafka         │ Java + JVM（客户端库是 CGO 或纯 Go 二次封装） │
  └───────────────┴───────────────────────────────────────────────┘

  用 NSQ 意味着整个判题链路从 API Server → Queue → Worker 全是 Go，go-nsq
  客户端直接导出一个 nsq.Handler 就能消费消息，不需要跨语言调用、不需要
  CGO、没有 JVM 依赖。对于部署在 K3s 上的系统，少一个 JVM 就少几百 MB 内存开销。

  2. 部署极简

  docker-compose.yml 里的 nsqd 配置就两行
  nsqd:
    image: nsqio/nsq
    command: nsqd

  单二进制启动，没有 Kafka 的 Zookeeper/KRaft 依赖，没有 RabbitMQ 的 Erlang
  虚拟机。对于线上 K3s 集群，NSQ 的 Pod 内存占用不到 50MB，而 Kafka（哪怕用
  Strimzi 或 Redpanda）至少 200-500MB。

  3. 判题场景的吞吐量要求——NSQ 绰绰有余

  ┌──────────┬───────────────┬────────────────────────┐
  │   队列   │   吞吐能力    │        典型场景        │
  ├──────────┼───────────────┼────────────────────────┤
  │ NSQ      │ ~10 万 msg/s  │ 够用（目标 200 req/s） │
  ├──────────┼───────────────┼────────────────────────┤
  │ Kafka    │ ~100 万 msg/s │ 过剩                   │
  ├──────────┼───────────────┼────────────────────────┤
  │ RabbitMQ │ ~数万 msg/s   │ 够用，但重             │
  └──────────┴───────────────┴────────────────────────┘

  200 req/s 对消息队列来说完全是低负载。NSQ 的瓶颈通常在几十万 msg/s
  级别，在这个量级上用 Kafka 就像开卡车去便利店买瓶水。

  4. HTTP API 直接暴露队列深度——KEDA 自动扩缩的关键

  // queue.go:120-150
  func nsqDepth() int64 {
      // NSQ 的 HTTP 管理端口是 4151
      resp, _ := http.Get(fmt.Sprintf("http://%s/stat", ...))
      // 解析 JSON 拿 depth
  }

  NSQ 每个 nsqd 节点自带 HTTP 管理接口，直接暴露每个 topic 的 depth。KEDA 的
  ScaledObject 正是通过这个 depth 决定扩缩：

  judge-worker-scaledobject.yaml
  spec:
    triggers:
      - type: prometheus
        metric: judgex_queue_depth  # ← NSQ depth 暴露为 Prometheus 指标
        targetValue: "10"

  RabbitMQ 的队列深度需要通过管理 API 或 Prometheus exporter 间接拿；Kafka 的
  consumer lag 需要 kafka-consumer-groups.sh 或 JMX 暴露——都比 NSQ 多一层中介。

  1. 三后端抽象降低了选型风险

  有意思的是，队列层设计时就没绑定死 NSQ：

  // QUEUE_BACKEND=nsq（默认）| redis | local

  NSQ 不行了可以切 Redis Streams（已有 Redis 基础设施），开发环境切 Local
  Channel（零依赖）。这种抽象让选 NSQ
  成了一个"最合适而非最正确"的决定——万一不合适，换后端不改代码。

  总结

  ┌──────────────┬───────────────┬────────────┬────────────┐
  │              │      NSQ      │  RabbitMQ  │   Kafka    │
  ├──────────────┼───────────────┼────────────┼────────────┤
  │ Go 生态      │ 原生          │ 非原生     │ 非原生     │
  ├──────────────┼───────────────┼────────────┼────────────┤
  │ 部署         │ 单二进制      │ Erlang VM  │ JVM + ZK   │
  ├──────────────┼───────────────┼────────────┼────────────┤
  │ 200 req/s    │ 轻松          │ 轻松       │ 杀鸡用牛刀 │
  ├──────────────┼───────────────┼────────────┼────────────┤
  │ 队列深度监控 │ 内置 HTTP API │ API + 插件 │ 需额外工具 │
  ├──────────────┼───────────────┼────────────┼────────────┤
  │ 内存占用     │ ~50MB         │ ~100MB+    │ ~500MB+    │
  └──────────────┴───────────────┴────────────┴────────────┘

  NSQ 选型核心一句话：Go 写的、部署简单、单机够用、深度监控开箱即用。在 K3s
  这种资源受限的环境中，省掉 JVM/Erlang VM 的开销是实实在在的收益。
  ## 4 多级缓存防穿透、防击穿、防雪崩，在你的系统里是怎么落地的？Redis 具体的使用策略？
  ---
  防穿透（查不存在的数据）
  
  穿透指大量请求查一个数据库中也不存在的 ID，每次打到 DB，严重时能拖垮数据库。

  两层防御：

  Bloom Filter（问题不存在，直接挡在 Redis 前面）

  if !cache.MightHaveProblem(id) {
      c.JSON(404, ...)
      return
  }

  12KB 的 bit 数组存了所有 problem ID。说不存在就一定不存在——根本不进入 Redis 和
   DB。这是第一道门，也是开销最小的，一次位运算 + 几次哈希。

  Null Marker（ID 曾经不存在，DB 已确认过）

  if cache.Get("problem:null:"+id, &nullMarker) {
      c.JSON(404, ...)
      return
  }

  如果 Bloom Filter 说"可能存在"，去 DB 查了确实不存在，就写一个 5 分钟 TTL 的
  problem:null:{id} = 1 进 Redis。后续对这个 ID 的请求，不查 Bloom Filter、不查
  DB，直接返回 404。

  注意两个机制的分工：

  ┌──────────┬──────────────────────────┬────────────────────────────────┐
  │          │       Bloom Filter       │          Null Marker           │
  ├──────────┼──────────────────────────┼────────────────────────────────┤
  │ 覆盖范围 │ 所有已存在的 ID          │ 查过且不存在的 ID              │
  ├──────────┼──────────────────────────┼────────────────────────────────┤
  │ 存储位置 │ 内存（12KB）             │ Redis                          │
  ├──────────┼──────────────────────────┼────────────────────────────────┤
  │ 淘汰方式 │ 每 10 分钟全量重建       │ 5 分钟 TTL 自动过期            │
  ├──────────┼──────────────────────────┼────────────────────────────────┤
  │ 防什么   │ 大量不存在 ID 的突发攻击 │ 某个 ID 确认不存在后被反复查询 │
  └──────────┴──────────────────────────┴────────────────────────────────┘

  ---
  防击穿（热点 key 过期瞬间的高并发）
  
  击穿指一个热点 key 在缓存过期的瞬间，大量并发请求同时打到 DB。


  假设 100 个人同时请求同一道题，缓存刚好过期：
  - 没有 Singleflight：100 个 goroutine 同时查 DB，DB 连接池被打满
  - 有 Singleflight：第 1 个 goroutine 拿到锁，去 DB 查，回写缓存。剩下 99 个在
  c.wg.Wait() 上 park（goroutine 阻塞，不占 CPU），等第 1 个完成后，直接拿到结果
  
  保证同一时刻对同一个 key 只有一次 DB 查询。

  ---
  防雪崩（大量 key 同时过期/Redis 整体不可用）
  
  雪崩和击穿的区别：击穿是单个热点 key 过期，雪崩是大批 key 同时过期或 Redis 
  整体宕机。

  这个系统的策略：

  1. TTL 分散

  problem:{id} 统一 TTL = 10 分钟。批量加载时所有 key
  同时创建意味着同时过期。但系统里每个 problem 的缓存是在被访问到时按需创建的（
  访问时间分散在不同时刻），所以过期时间自然分散，不会出现集体雪崩。

  2. Bloom Filter 不依赖 Redis

  Bloom Filter 在进程内存中，即使 Redis 挂了，它依然能拦截不存在的
  ID，避免穿透到 DB。

  3. Redis 降级到内存缓存

  if rdb == nil {
      mu.Lock()
      mem[key] = memItem{...}  // 内存 map 兜底
      mu.Unlock()
  }

  cache.Init() 时如果 Redis 连不上，自动切到内存 map[string]memItem，后台 memGC
  每 30 秒清理过期数据。功能不中断，只是缓存从分布式降级为单机。

  Redis 具体使用策略
  
  不只是 KV 缓存，Redis 在系统里承担了四种角色：

  ┌──────────────────────────────────────────────────┐
  │                    Redis                          │
  │                                                   │
  │  KV 缓存               判题队列 (Streams)         │
  │  ├── problem:{id}       └── judgex:stream:        │
  │  ├── problem:null:{id}       judge_tasks          │
  │  └── submission:{id}                              │
  │                                                   │
  │  实时排行 (ZSet)         实时推送 (Pub/Sub)       │
  │  └── contest_rank:{id}    └── submission:{id}     │
  │                                                   │
  │  限流器                   比赛状态 (Hash)          │
  │  └── ratelimit:*          └── contest_user:*      │
  └──────────────────────────────────────────────────┘

  KV 缓存：Set/Get/Del，JSON 序列化后存
  string。所有请求在读题目、查列表、看提交详情时走这里。AC 后主动 Del。

  ZSet 实时排行榜：比赛排名用 ZAdd/ZRevRangeWithScores，分数计算公式 solved * 
  1,000,000 − penalty，O(log N) 插入 + O(log N + M) 范围查询，比 MySQL ORDER BY
  快两个数量级。

  Hash 比赛状态：每道题每个用户的错误提交次数、AC 时间，用 HIncrBy 原子更新，ACM
   罚时计算直接从这里读。

  Pub/Sub + SSE：判题完成后 worker Publish("submission:{id}", result)，前端的
  SSE 端点通过 Subscribe 接收后直接推给浏览器，零轮询。

  Streams 判题队列：当 QUEUE_BACKEND=redis 时替代 NSQ，用 XAdd/XReadGroup/XAck
  实现 at-least-once 的消息投递。

  限流器（IncrWithTTL）：ratelimit:ip:{ip} 60
  次/分钟，ratelimit:submission:{userID} 10 次/分钟。第一次写时设 TTL，后续只
  Incr 不延长 TTL，到期自动归零。

  所有 Redis 操作统一 2 秒超时，失败不影响主流程：

  ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
  defer cancel()
  rdb.Get(ctx, key)
## 5 结合 KEDA 实现 K8s 控制器自动扩缩判题 Worker，讲下 KEDA 的工作原理，以及你基于队列深度做扩缩容的配置逻辑？

### 5.1 追问：扩缩容的阈值是怎么设定的？有没有出现过频繁扩缩容（抖动）的问题，如何解决？

 KEDA 工作原理

  KEDA 是 Kubernetes 的 Event-Driven Autoscaler，本质是一个自定义控制器，工作在
  HPA（HorizontalPodAutoscaler）上层：

  Kubernetes 集群
  │
  ├── KEDA Operator
  │     └── 监听 ScaledObject CRD
  │           │
  │           ├── 根据 trigger 指标计算目标副本数
  │           │     └── 创建/更新 HPA 对象
  │           │           └── HPA 调整 Deployment 的 replicas
  │           │
  │           └── pollingInterval（15秒）轮询一次指标
  │               cooldownPeriod（120秒）缩容冷静期

  KEDA 不直接操作 Pod，而是代理给 K8s 原生的 HPA：

  用户定义 ScaledObject → KEDA 生成 HPA → HPA 调整 Deployment 副本数

  这样 KEDA 的扩缩容行为（min/max、冷却时间、轮询间隔）最终通过 HPA
  的标准机制生效，兼容 K8s 的任何发行版。

  指标链

  NSQ (队列深度)
    ↓ HTTP /stat 接口
  API Server (queue.NSQDepth())
    ↓ 每 5 秒写入 atomic 变量
  /metrics 端点 (judgex_queue_depth gauge)
    ↓ Prometheus 抓取
  Prometheus Server
    ↓ (KEDA Prometheus trigger)
  KEDA Operator → HPA → Deployment

  
  整个链路从 NSQ 队列深度到 Prometheus 指标，约 5-10 
  秒的延迟，对于判题场景（一个任务运行 1-10 秒）来说足够实时。

  ---
  追问：阈值设定和抖动处理
  
  阈值的依据

  CPU 60% 和队列深度阈值不是拍脑门的：

  参数: CPU 阈值
  值: 60%
  依据: 单个 worker 的 CPU limit = 1 核。60% 意味着 600m 被持续使用，说明 worker
  
    在密集判题。保留 40% 余量（400m）避免 Worker 被压到 100% 后响应探针失败、K8s

    误杀 Pod。
  ────────────────────────────────────────
  参数: minReplicas
  值: 1
  依据: 无任务时缩到 1 个，不缩到 0 是因为 Worker 的 NSQ 消费者需要保持连接。
  ────────────────────────────────────────
  参数: maxReplicas
  值: 10
  依据: 10 个 Worker × 1 核 = 10 核，匹配集群 Worker Node 的 CPU 总量上限。
  ────────────────────────────────────────
  参数: cooldownPeriod
  值: 120s
  依据: 防抖动核心参数：缩容前必须等待 2 分钟，指标持续低于阈值才缩。
  ────────────────────────────────────────
  参数: pollingInterval
  值: 15s
  依据: 判题平均耗时 1-5 秒，15 秒检查一次能在 2-3 个判题周期内感知负载变化。

  抖动的产生和解决

  什么叫抖动：一波提交高峰来了，Worker 扩容到 5 个，高峰过去 30
  秒队列清空了，KEDA 缩到 1 个。结果 1 分钟后新的一波提交又来——Worker
  数不够了，队列瞬间积压，KEDA 再扩上去。频繁扩缩容导致 Pod 
  反复创建/销毁，每次启动还要连接 NSQ、重建缓存，浪费资源。

  三个措施：

  1. cooldownPeriod: 120

  从指标低于阈值到真正执行缩容，必须等够 120 秒。判题系统的负载特点是"上课提交 →
   5 分钟高峰 → 回落"，120
  秒的冷却时间能覆盖大部分短期波动，避免缩下去立刻又要扩。

  2. 队列深度告警的 for: 5m

  - alert: JudgeQueueDepthHigh
    expr: judgex_queue_depth > 20
    for: 5m         # 持续 5 分钟高于 20 才告警

  队列深度超过 20 持续 5 分钟才触发告警。避免偶尔的瞬时积压（比如同时来了 5 个
  Java 任务）引发不必要的告警噪声。

  3. minReplicas = 1，不缩到 0

  缩到 0 意味着下一个任务来时要冷启动 Worker Pod —— 拉镜像、初始化 NSQ
  消费者、重建 Bloom Filter。这些操作本身就要几秒到十几秒，导致任务的 P99 延迟被
   Pod 启动时间拖高。保留 1 个常驻 Pod 确保"随时有人干活"。

  目前的设计局限

  当前 ScaledObject 使用的是 CPU trigger，而非 Prometheus 队列深度
  trigger。原因很直接——Prometheus Server 和 Prometheus Adapter 不是 K3s
  的默认组件，部署它们本身就是一个大工程。CPU trigger 是 KEDA
  内置的，不需要额外组件：


  但 CPU 作为代理指标并不完美——队列深度能直接反映"积压了多少任务"，而 CPU
  是间接的（Worker 在判题时 CPU 高，但等待任务时接近 0）。如果有 Prometheus
  基础设施，最合理的配置应该是双触发条件：


  队列深度 > 10 就扩容，CPU > 60%
  也扩——谁先触发都生效。缩容时两个条件都低于阈值才缩，配合 120
  秒冷却，能更精确地匹配判题系统的负载特征。
## 6 整个服务的监控体系：Prometheus + OpenTelemetry 是怎么接入的？自定义了哪些核心指标？如何通过指标定位性能瓶颈？
接入架构

  两个观测系统是独立接入、互补工作的：

  请求进入
    │
    ├─► OpenTelemetry (tracing middleware)
    │     ├── 每个 HTTP 请求创建一个 Span
    │     ├── 属性：method, url, route, status_code
    │     ├── TraceContext 注入到 NSQ 消息（traceparent header）
    │     │     └── Worker 消费时提取，创建子 Span
    │     └── 每 5 秒 / 256 条批量导出到 Jaeger
    │
    ├─► Prometheus (metrics middleware)
    │     ├── IncAPIRequest() + ObserveLatency() 每个请求
    │     ├── IncSubmission(status) 判题完成
    │     ├── SetQueueDepth() 每 5 秒
    │     └── GET /metrics 端点给 Prometheus Server 拉取
    │
    └─► 业务处理


  自定义指标
  
  指标分四组：

  1. 业务指标（判题核心）

  指标名: judgex_submissions_total
  类型: Counter
  来源: IncSubmission()
  用途: 总提交量
  ────────────────────────────────────────
  指标名: judgex_submissions_accepted
  类型: Counter
  来源: AC 时 +1
  用途: 通过数、AC 率（曲线）
  ────────────────────────────────────────
  指标名: judgex_submissions_wrong_answer
  类型: Counter
  来源: WA 时 +1
  用途: 错误分布
  ────────────────────────────────────────
  指标名: judgex_submissions_tle
  类型: Counter
  来源: TLE 时 +1
  用途: 需关注的趋势
  ────────────────────────────────────────
  指标名: judgex_submissions_runtime_error
  类型: Counter
  来源: RE/CE 时 +1
  用途: 潜在 bug
  ────────────────────────────────────────
  指标名: judgex_active_judgements
  类型: Gauge
  来源: Inc/DecActiveJudge
  用途: 当前并发数
  ────────────────────────────────────────
  指标名: judgex_queue_depth
  类型: Gauge
  来源: SetQueueDepth() 每 5s
  用途: 队列积压（扩缩容依据）

  2. API 指标（接口性能）

  指标名: judgex_api_requests_total
  类型: Counter
  用途: 总请求量
  ────────────────────────────────────────
  指标名: judgex_api_errors_total
  类型: Counter
  用途: 4xx/5xx 率
  ────────────────────────────────────────
  指标名: judgex_api_latency_total
  类型: Counter
  用途: 延迟采样总数
  ────────────────────────────────────────
  指标名: judgex_api_latency_sum_ms
  类型: Counter
  用途: 延迟总和（avg = sum / total）
  ────────────────────────────────────────
  指标名: judgex_api_latency_bucket{le="..."}
  类型: Histogram
  用途: P50/P90/P99 计算

  延迟桶分布（毫秒）：[5, 10, 25, 50, 100, 250, 500, 1000, 2500, 5000, 10000]

  3. 系统指标（基础设施）

  指标名: judgex_go_goroutines
  类型: Gauge
  用途: 协程泄漏检测（>5000 告警）
  ────────────────────────────────────────
  指标名: judgex_go_mem_alloc_bytes
  类型: Gauge
  用途: 内存泄漏检测
  ────────────────────────────────────────
  指标名: judgex_disk_free_percent
  类型: Gauge
  用途: 磁盘满告警（<20% warning，<10% critical）
  ────────────────────────────────────────
  指标名: judgex_uptime_seconds
  类型: Gauge
  用途: 进程异常重启检测
  ────────────────────────────────────────
  指标名: 数据库连接池状态
  类型: PoolStats
  用途: MaxOpen/Open/InUse/Idle

  4. 系统快照（diagnostics 包）

  Collect() 聚合一份 JSON 快照，包含近 1 小时提交统计（AC
  率、状态分布）、错误按题目聚合（什么题什么错）、沙箱模式、eBPF 网络指标。用于
  SRE 面板展示而非 Prometheus 时序。

  ---
  通过指标定位性能瓶颈
  
  场景 1：用户反馈"判题变慢了"

  时间轴：

  queue_depth 开始上升  ─┐
                         ├── 队列在积压，但 Worker 还在干活
  active_judgements 上升 ─┘
                         ↓
  api_latency P99 上升   ─── 先看是接口慢还是判题慢

  - judgex_queue_depth 持续上升 → Worker 处理速度 < 提交速度
  - judgex_api_latency_bucket{le="5000"} 的 rate[5m] 看 P99 →
  用户端延迟主要受队列积压影响
  - 下一步看 Prometheus 告警规则：队列 > 20 持续 5 分钟 →
  JudgeQueueDepthHigh。如果触发了，说明 KEDA 扩缩没跟上或 maxReplicas 不够

  场景 2：单个判题耗时异常

  OpenTelemetry 追踪链：

  Trace: POST /api/submissions/123
    ├── submit_handler: 15ms    ← API 接收
    ├── queue_publish: 2ms      ← 入队
    │
    (跨进程：traceparent 传递到 worker)
    │
    └── judge_task:
          ├── compile: 8.2s     ← 编译时间（右上 Python？）
          ├── test_1: 2.1s      ← 沙箱执行
          ├── test_2: 2.3s
          └── test_10: 1.9s
             total: 23s         ← P99 异常

  在 Jaeger UI 中按 trace id 搜索，看 Span 耗时分布：

  - compile 耗时异常高 → 换语言或者编译器超时设置太宽松
  - 所有 test case 都接近 timeLimit → 用户代码效率问题，不是系统瓶颈
  - 沙箱启动耗时（<1ms 正常）> 100ms → cgroup 创建或 chroot 目录构建异常
  - 总时间 ≈ timeLimit × 测试点数 → C++ 秒级任务被 Python
  慢队列拖了（话题分离没问题，但 worker 并发处理能力不够）

  场景 3：数据库瓶颈

  - judgex_go_mem_alloc_bytes 持续上升 + GC 频繁 → 某条查询扫描了大量数据
  - 数据库连接池 InUse ≈ MaxOpen 且 API 延迟上升 → 连接池打满，请求在排队等连接
  - 配合 OpenTelemetry 看 DB Span 的耗时分布 → 找出慢查询（比如 JOIN 没索引）
  - 回溯到 Bloom Filter → 看 MightHaveProblem
  是否误判率高导致大量穿透（正常应该是 0 穿透）

  场景 4：队列深度波动异常

  queue_depth:
    正常: 0-5 波动
    异常: 每隔 10 分钟从 0 跳到 50 又回落

  这种周期性积压通常是上课时间节点的行为——整点收作业，几百人同时提交。解决方案：

  - 查看 active_judgements 是否 maxed out（判断是否需要调整 maxReplicas）
  - 查看 submissions_tle 是否同步上升（队列积压导致判题延迟，但 CPU
  时间仍然从提交时开始算，导致更多 TLE？）
  - 如果 TLE 没变化 → 只是用户等待时间变长，系统没崩溃
  - 如果 TLE 同步上升 → 需要扩容或增加 Worker 并发数

  五条告警规则构成防线

   prometheus-rules.yml
  JudgeQueueDepthHigh:     queue > 20 for 5m       → 扩容滞后
  HighSubmissionErrorRate: 错误率 > 20% for 5m     → 沙箱/编译器问题
  HighGoroutineCount:      goroutine > 5000 for 5m → 协程泄漏
  HighAPILatency:          平均延迟 > 5s for 5m    → DB 或缓存问题
  DiskSpaceLow:            磁盘 < 20% for 5m        → 测试数据膨胀

  每个告警指向一个明确的排查方向，不需要看几十个图表再去找根因。
  


  已有的应对层级
## 7 如果 NSQ 队列堆积严重，Worker 处理不过来，你的系统会怎么应对？
  第一层：KEDA 自动扩容（秒级响应）

  keda:
    minReplicaCount: 2
    maxReplicaCount: 20
    cooldownPeriod: 300
    threshold: "20"
    query: judgex_queue_depth

  队列深度 > 20，KEDA 发现 Prometheus 指标 judgex_queue_depth 上升，在 15 秒的
  pollingInterval 内触发扩容。Worker 从 2 个逐步增加到 20 个，理论上处理能力提升
   10 倍。

  第二层：双 Topic 分流（防止慢任务阻塞快任务）

  func topicForLanguage(lang string) string {
      switch lang {
      case "cpp", "c", "go", "rust":
          return TopicFast
      default:
          return TopicSlow
      }
  }

  如果积压的全是 Python/Java，Fast 队列的 Worker 不受影响。这是队列层面的 Task
  隔离。

  第三层：提交限流（控制入口流量）

  用户侧：

  // middleware/auth.go
  // 每个用户每分钟最多 10 次提交
  // 每个 IP 每分钟最多 60 次 API 请求

  去重缓存 dedup:{sha256} 3 秒 TTL——同一份代码 3
  秒内重复提交不进队列，直接返回缓存结果。

  第四层：死信丢弃（防止坏任务循环重试）

  func Requeue(task JudgeTask) {
      task.RetryCount++
      if task.RetryCount > MaxRetries {   // 3 次上限
          return   // 丢弃，不重新入队
      }
      Publish(task)
  }

  一个任务最多重试 3 次，之后丢弃。防止某个有 bug 的任务（比如编译永远过不了但
  panic 了）无限循环填满队列。

  第五层：告警（人工介入）


## 8 假如大量恶意代码提交，耗尽服务器资源，你的沙箱机制能否拦截？还有哪些兜底方案？
沙箱能拦截什么

  先说沙箱对单个恶意代码的效果，再谈对大量提交的防御。

  单次恶意代码

  假设用户提交了一段恶意 C 代码，沙箱的四层防御逐一拦截：

  Fork Bomb

  while(1) fork();

  - cgroup pids.max = 16 → 第 17 个进程创建时被内核直接拒绝
  - 结果：Runtime Error 或 TLE，服务器不受影响

  Memory Bomb

  while(1) malloc(1024*1024);

  - cgroup memory.max + memory.high → 超过限制时 OOM Killer 终止进程
  - memory.peak 仍能读到峰值，判为 MLE
  - 结果：Memory Limit Exceeded

  CPU 死循环

  while(1);

  - cgroup cpu.max = "2000000 100000"（假设 timeLimit=2000ms）→ 每 100ms
  周期内最多给 2000ms CPU
  - 外加 context.WithTimeout 兜底（timeLimit + 5s）
  - 结果：Time Limit Exceeded

  文件系统逃逸

  chroot(".");  // 绕过沙箱的 chroot？

  - 不走 seccomp 白名单 → SIGSYS 杀死进程
  - 系统调用如 mount、pivot_root、unshare、ptrace、bpf 全部在白名单之外
  - 结果：Runtime Error (seccomp)

  网络攻击

  socket(AF_INET, SOCK_STREAM, 0);
  connect(sock, ...);

  - seccomp 白名单里虽然允许了 socket（某些运行时需要），但 unshare -n
  创建了独立的 net namespace → 虚拟网络栈，没有任何网络接口，connect 必然失败
  - 结果：Runtime Error

  编译阶段投毒

  // 包含一个巨大的头文件 10000 次
  #include "/dev/urandom"

  - 编译也受 30 秒 context.WithTimeout 保护 → 超时则 Compile Error
  - 编译进程没有 namespace 隔离，但 cgroup pids.max 和 memory.max 同样生效

  沙箱的四层深度防御

  恶意代码
    │
    ├─► cgroup v2: CPU/pids/memory 硬限制    ← 资源无法超标
    ├─► chroot: 只能看到 /tmp/jail-xxx/       ← 文件系统不可见
    ├─► namespace: 隔离网络/PID/mount          ← 无法逃逸
    └─► seccomp: 66 个白名单外全部 SIGSIS       ← 关键系统调用被封
         │
         ▼
    被杀死 → worker 捕获 SIGSYS → 判为 Runtime Error

  ---
  沙箱拦不住的：大量提交耗尽服务器资源
  
  沙箱管的是单次恶意代码，但对大量恶意提交无能为力——每个提交本身是合法的，但数量
  大到撑爆系统。攻击者可以用 1000 个账号，每个每分钟提交 10
  次，生成海量编译任务。

  攻击者 → 1000 个账号 × 10次/分钟 = 10000 req/min

  每个请求：
    1. 写源代码到磁盘（几十 KB）
    2. 编译器启动（g++ 吃 CPU）
    3. 沙箱创建 cgroup + chroot
    4. 运行、清理

  → 磁盘写满  ← 沙箱不管这个
  → CPU 打满  ← 每个编译只占一点，但量大了也扛不住
  → 队列积压  ← KEDA 最多扩到 20 个 Worker，还是不够

  ---
  现有的兜底方案
  
  1. 提交限流（入口层）

  // 每个用户每分钟最多 10 次
  func RateLimitSubmission() gin.HandlerFunc {
      count := cache.IncrWithTTL(key, 1*time.Minute)
      if count > 10 {
          c.AbortWithStatusJSON(429, ...)
          return
      }
  }

  但这是 per-user 限流。攻击者可以注册大量账号绕过——注册接口本身也限制了 20
  次/分钟/IP，但代理池可以绕过 IP 限制。

  2. 去重缓存

  hash := sha256("userID:problemID:language:code")
  if cache.Get("dedup:"+hash) {
      return cachedResult  // 不进队列，不加编译
  }

  如果攻击者反复提交同一段代码，每次都会被 dedup
  拦截，不进沙箱。但这只能防一模一样的代码。

  3. 编译超时

  ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)

  C++ 编译超时 30 秒，Rust 60 秒。编译器被打满时，超时的请求自动释放。

  4. 临时目录自动清理

  workDir, _ := os.MkdirTemp("", "judgex-*")
  defer os.RemoveAll(workDir)  // 函数退出时删除

  每个判题任务的临时目录在结束后删除。即使 panic 了，os.RemoveAll 也会在 defer
  中执行。但如果是 worker 进程被 kill -9，残留的临时目录需要额外的清理机制。

  5. KEDA 扩缩容 + maxReplicas 硬上限

  maxReplicaCount: 20  # 最多 20 个 Worker

  这是一道物理防线——不管队列多深，Worker 不会无限增长耗尽集群资源。
## 9 线上服务突然 Panic 崩溃，你的 Panic Recovery 机制能否保证服务不宕机？具体实现逻辑
 系统有两层 panic recovery，但覆盖范围有明确缺口。

  现有的两层 Recovery

  第一层：Gin Recovery（HTTP 请求层）

  // server/main.go:156
  r.Use(gin.Recovery())  // 内置中间件

  Gin 的 Recovery() 中间件拦截每个 HTTP handler goroutine 中的 panic。任何一个
  API 请求处理中 panic（比如空指针解引用、数组越界），Gin 捕获后写回 500
  响应，进程本身不挂。

  覆盖范围：所有 HTTP 请求处理器。

  第二层：processWithRecovery（判题任务层）

  func processWithRecovery(handler TaskHandler, task JudgeTask) {
      defer func() {
          if r := recover(); r != nil {
              log.Printf("[queue] #%d: panic recovered: %v, requeuing",
  task.SubmissionID, r)
              Requeue(task)  // 重试，最多 3 次
          }
      }()
      handler(task)
  }

  每一个判题任务（调用 worker.JudgeTask）都在 processWithRecovery
  中执行。所有四种消费者（NSQ、Redis Streams、Local Channel）都经过这个函数：

  // NSQ consumer
  consumer.AddHandler(nsq.HandlerFunc(func(msg *nsq.Message) error {
      processWithRecovery(handler, task)  // ← 有 recover
      return nil
  }))

  // Redis Streams consumer
  processWithRecovery(handler, task)  // ← 有 recover

  // Local worker
  processWithRecovery(handler, task)  // ← 有 recover

  覆盖范围：所有判题任务处理。
## 10 系统整体的调用流程是什么？从用户提交代码到返回判题结果，完整链路讲一下
现在有了完整链路。从用户点击提交到看到结果，全程如下：

TraceParent 是 W3C TraceContext
  标准中定义的追踪上下文传播头，一个简单的字符串，用来在服务间传递追踪信息。

  格式

  traceparent = "00-0af7651916cd43dd8448eb211c80319c-b7ad6b7169203331-01"
                  ^^ ^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^^ ^^^^^^^^^^^^^^^^^^^^^^^^
  ^^
                 版本          trace-id (16字节)          span-id (8字节)
  flags

  三段信息：

  - trace-id（全局唯一）：一次请求链路的全局 ID，所有参与的服务共享这个 ID
  - span-id（当前节点）：当前服务/操作的 ID
  - flags: 采样标记（01 = 需要采样）

  在 JudgeX 中的作用

  没有 TraceParent，API Server 的请求和 Worker 的判题在两个进程里，OpenTelemetry
   无法把它们关联起来。



  完整调用链路

  ┌─────────────────────────────────────────────────────────────────────────────
  ─────┐
  │  用户浏览器/Frontend
       │
  │  POST /api/submissions  {problem_id, language, code}
       │
  └─────────────────┬───────────────────────────────────────────────────────────
  ─────┘
                    │
                    ▼
  ┌─────────────────────────────────────────────────────────────────────────────
  ─────┐
  │  Step 1: API Server — 接收提交
        │
  │
       │
  │  handler/submission.go:87  Submit()
       │
  │    ├─ 1.1 验证参数 (problem_id, language, code 是否合法)
        │
  │    ├─ 1.2 查 problem 是否存在 (DB First, 走缓存)
       │
  │    ├─ 1.3 去重检测 (SHA256 → Redis "dedup:" key) ← 3秒内相同提交直接返回
        │
  │    ├─ 1.4 创建 DB 记录 (status = "pending")
       │
  │    ├─ 1.5 注入 TraceParent (W3C TraceContext)
       │
       
  │    └─ 1.6 queue.Publish()  → 发布到 NSQ topic
      │
  │          ├─ 语言判断 topicForLanguage() → "judge_tasks_fast" 或 "_slow"
        │
  │          └─ 返回 201 Created {submission_id, status: "pending"}
      │
  └─────────────────┬───────────────────────────────────────────────────────────
  ─────┘
                    │
                    ▼
  ┌─────────────────────────────────────────────────────────────────────────────
  ─────┐
  │  Step 2: 客户端同时建立 SSE 连接
          │
  │
       │
  │  GET /api/submissions/{id}/events (SSE)
       │
  │    ├─ 立即推送当前状态 {status: "pending"}
        │
  │    ├─ 订阅 Redis Pub/Sub "submission:{id}" 频道
       │
  │    └─ select{} 阻塞等待 → 新消息 → flush 给浏览器
       │
  └─────────────────┬───────────────────────────────────────────────────────────
  ─────┘
                    │
                    ▼
  ┌─────────────────────────────────────────────────────────────────────────────
  ─────┐
  │  Step 3: NSQ 消息路由
        │
  │
       │
  │  queue/queue.go:350  Publish()
       │
  │    └─ topicForLanguage(language)
       │
  │          ├─ cpp / c / go / rust  → TopicFast  "judge_tasks_fast"
       │
  │          └─ python / java / js  → TopicSlow  "judge_tasks_slow"
       │
  └─────────────────┬───────────────────────────────────────────────────────────
  ─────┘
                    │
                    ▼
  ┌─────────────────────────────────────────────────────────────────────────────
  ─────┐
  │  Step 4: Judge Worker — 消费消息
        │
  │
       │
  │  queue/queue.go:315  nsq.HandlerFunc
       │
  │    └─ processWithRecovery(handler, task)  ← 有 panic recover
       │
  │          └─ worker/worker.go:74  JudgeTask()
       │
  │
       │
  │  4.1 恢复 TraceContext (traceparent → 子 Span)
      │
  │  4.2 加载测试数据
        │
  │      ├─ loadTestCasesFromDisk(problemID)
       │
  │      │     ├─ 查 Redis "tcversion:{id}" ↔ test_case_version
       │
  │      │     ├─ 匹配 → 读磁盘 /data/testcases/{problemID}/*.in/*.out
       │
  │      │     └─ 版本不匹配 → DB 查 version 后更新缓存
        │
  │      └─ 失败 → 降级 loadTestCasesFromMySQL()
       │
  │
       │
  │  4.3 检测比赛规则 (ACM / IOI)
       │
  │
       │
  │  4.4 逐条运行测试用例
        │
  │
  ┌──────────────────────────────────────────────────────────────────────┐    │
  │      │ for each test case:
  │    │
  │      │   ┌───────────────────────────────────────────────────────────────┐
  │    │
  │      │   │ judge.Run(lang, code, input, timeLimit, memoryLimit)          │
  │    │
  │      │   │   ├─ workDir = os.MkdirTemp("", "judgex-*")                   │
  │    │
  │      │   │   ├─ 写入源代码到临时文件
  │  │    │
  │      │   │   ├─ 编译（C++ 30s超时, Rust 60s, Python跳过）                 │
   │    │
  │      │   │   ├─ sandbox.Run(cfg) → 进入沙箱                              │
  │    │
  │      │   │   │     ├─ createCgroup() (cpu.max / memory.max / pids.max)   │
  │    │
  │      │   │   │     ├─ /proc/self/exe (重新执行自己)                       │
   │    │
  │      │   │   │     │    ├─ unshare -U -r -p -m -n -i (命名空间隔离)       │
   │    │
  │      │   │   │     │    ├─ 写 PID 到 cgroup.procs (加入 cgroup)          │
  │    │
  │      │   │   │     │    ├─ setupChrootJail() (tmpfs + bind mount)        │
  │    │
  │      │   │   │     │    ├─ syscall.Chroot(".") (切根)                    │
  │    │
  │      │   │   │     │    ├─ applySeccomp() (BPF 白名单)                   │
  │    │
  │      │   │   │     │    └─ syscall.Exec(用户程序) (运行)                 │
  │    │
  │      │   │   │     ├─ 读 memory.peak (cgroup v2 峰值)                   │  │
      │
  │      │   │   │     ├─ 分析退出状态 (SIGKILL→TLE, SIGSYS→seccomp)        │  │
      │
  │      │   │   │     └─ 清理 cgroup 目录                                    │
   │    │
  │      │   │   └─ 比较输出 (CompareOutput, 标准化去空白差异)               │
  │    │
  │      │   └───────────────────────────────────────────────────────────────┘
  │    │
  │      │
  │    │
  │      │   结果判定:
  │    │
  │      │     ACM 模式: 遇到第一个失败测点立即返回
   │    │
  │      │     IOI 模式: 跑完所有测点, 算部分分
  │    │
  │
  └──────────────────────────────────────────────────────────────────────┘    │
  └─────────────────┬───────────────────────────────────────────────────────────
  ─────┘
                    │
                    ▼
  ┌─────────────────────────────────────────────────────────────────────────────
  ─────┐
  │  Step 5: 写回结果
         │
  │
       │
  │  5.1 DB 事务 (Transaction)
      │
  │      ├─ UPDATE submissions SET status, time_used, memory_used, ...
      │
  │      └─ IF status == Accepted AND 用户首次 AC → UPDATE
  problems.accepted_count+1 │
  │
       │
  │  5.2 Redis Pub/Sub → "submission:{id}" 频道
      │
  │      └─ {status, time_used, memory_used, passed_count, total_cases}
      │
  │
       │
  │  5.3 cache.Del("problem:{id}")  ← 清除题目缓存（accepted_count 更新）
      │
  │
       │
  │  5.4 cache.Set("dedup:{sha256}", status, 3s)  ← 去重缓存
     │
  │
       │
  │  5.5 比赛排名更新 (Redis ZAdd)  ← 如果是比赛提交
       │
  │
       │
  │  5.6 metrics.IncSubmission(status)  ← 记录指标
      │
  └─────────────────┬───────────────────────────────────────────────────────────
  ─────┘
                    │
                    ▼
  ┌─────────────────────────────────────────────────────────────────────────────
  ─────┐
  │  Step 6: SSE 推送 → 前端
          │
  │
       │
  │  handler/submission.go:456  StreamEvents 的 select{}
       │
  │    └─ 收到 Redis Pub/Sub 消息
       │
  │         └─ writeEvent({status: "Accepted", time_used: 42, ...})
      │
  │              └─ flusher.Flush() → 浏览器收到 SSE 事件
        │
  │                    └─ 前端更新UI: 显示判题结果
        │
  └─────────────────────────────────────────────────────────────────────────────
  ─────┘

  关键路径耗时分布

  用户提交 → 返回 pending    ~50ms     (DB insert + NSQ publish)
           → 队列等待        0-500ms   (取决于 worker 是否空闲)
           → 加载测试数据     ~2ms     (磁盘读取, LRU 缓存)
           → 编译          0.5-10s    (C++ 快, Rust 慢, Python 跳过)
           → 沙箱执行       1-10s     (逐测点, cpu.max 控制)
           → DB 写入 + SSE  ~5ms
           → 前端收到结果    < 1ms     (SSE 直推)

  总耗时 = 编译 + Σ(每个测试点执行时间) + 队列等待 + ~60ms 固定开销。P99 < 3s
  的目标意味着 99% 的提交（不含编译和队列等待）能在 3
  秒内从"提交"走到"前端展示结果"。
