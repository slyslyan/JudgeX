package queue

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/nsqio/go-nsq"

	"judgex/internal/cache"
)

// ============================================================================
// 判题任务队列
// ============================================================================
//
// JudgeX 支持三种队列后端，通过环境变量 QUEUE_BACKEND 切换：
//
// 1. NSQ（默认）：基于 NSQ 的分布式消息队列
//    - 快速主题（judge_tasks_fast）：C/C++/Go/Rust（编译型，运行快）
//    - 慢速主题（judge_tasks_slow）：Python/Java（解释型/JVM，运行慢）
//    - 独立运行 nsqd 服务，通过 HTTP API 监控深度
//
// 2. Redis Streams：基于 Redis 的持久化消息队列
//    - 支持消费组（consumer groups），自动负载均衡
//    - 消息持久化到 Redis RDB/AOF，重启不丢失
//    - 适合已有 Redis 基础设施的场景
//
// 3. Local Channel（回退）：Go channel 内存队列
//    - 无外部依赖，适合开发测试
//    - 进程重启后队列丢失
//    - 最大 1024 个缓冲槽
//
// 架构说明：
//
//	┌─────────┐   Publish    ┌──────────────┐  consume  ┌──────────┐
//	│  API    │ ──────────→  │   Queue      │ ────────→ │  Judge   │
//	│ Server  │              │ (NSQ/Redis/  │           │  Worker  │
//	│         │              │  Local)      │           │          │
//	│ Submit  │              │              │           │ Sandbox  │
//	└─────────┘              └──────────────┘           └──────────┘
//	                              │
//	                         Max 3 retries
//	                         + dead letter

const (
	MaxRetries = 3                  // 最大重试次数（超过后丢弃任务）
	TopicFast  = "judge_tasks_fast" // 快速主题：C/C++/Go/Rust
	TopicSlow  = "judge_tasks_slow" // 慢速主题：Python/Java
)

// JudgeTask 是判题任务的序列化结构，通过 JSON 传输。
// 包含判题所需的所有信息，由 submission handler 创建。
type JudgeTask struct {
	SubmissionID int64  `json:"submission_id"`
	ProblemID    uint   `json:"problem_id"`
	UserID       uint   `json:"user_id"`
	ContestID    *uint  `json:"contest_id,omitempty"` // 比赛提交时有此字段
	Language     string `json:"language"`
	Code         string `json:"code"`
	TimeLimit    int    `json:"time_limit"`
	MemoryLimit  int    `json:"memory_limit"`
	RetryCount   int    `json:"retry_count"`            // 当前重试次数
	TraceParent  string `json:"trace_parent,omitempty"` // W3C TraceContext 追踪
}

// TaskHandler 是判题任务处理函数类型。
// worker.JudgeTask 实现了此接口，负责编译、运行、比对。
type TaskHandler func(task JudgeTask)

var (
	producer     *nsq.Producer
	localChannel chan JudgeTask
	nsqReady     bool
	queueBackend string // "nsq" 或 "redis" 或 "local"
)

// ============================================================================
// 队列状态和监控
// ============================================================================

// Stats 返回队列当前的统计信息。
// 用于 SRE dashboard 监控和 diagnostics 系统快照。
func Stats() (backend string, bufLen int, nsqOK bool, workerCount int) {
	backend = queueBackend
	if nsqReady {
		nsqOK = true
	} else if queueBackend == "redis" {
		nsqOK = false
		backend = "redis"
	}
	if localChannel != nil {
		bufLen = len(localChannel)
	}
	workerCount = 4 // 当前硬编码为 4 个 worker
	return
}

// NSQDepth 返回当前队列的深度（待处理任务数）。
// 用于 Prometheus metrics（KEDA 根据此指标自动伸缩 worker）
func NSQDepth() int64 {
	switch queueBackend {
	case "redis":
		return cache.XLen(cache.StreamJudgeTasks)
	case "nsq":
		return nsqDepth()
	default:
		if localChannel != nil {
			return int64(len(localChannel))
		}
		return 0
	}
}

// nsqDepth 通过 NSQ HTTP API 获取队列深度。
// NSQ 的 HTTP 管理端口是 TCP 端口 -1（例如 4150 → 4151）
func nsqDepth() int64 {
	if !nsqReady {
		return 0
	}
	addr := os.Getenv("NSQD_ADDR")
	if addr == "" {
		addr = "127.0.0.1:4150"
	}
	// NSQ HTTP 管理接口在 4151（nsqd）或 4161（nsqlookupd）
	host := addr
	if len(host) > 5 && host[len(host)-5:] == ":4150" {
		host = host[:len(host)-5] + ":4151"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://" + host + "/stats?format=json")
	if err != nil {
		return 0
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 65536))
	var stats struct {
		Topics []struct {
			TopicName string `json:"topic_name"`
			Depth     int64  `json:"depth"`
		} `json:"topics"`
	}
	if json.Unmarshal(body, &stats) != nil {
		return 0
	}
	var total int64
	for _, t := range stats.Topics {
		if t.TopicName == TopicFast || t.TopicName == TopicSlow {
			total += t.Depth
		}
	}
	return total
}

// topicForLanguage 根据编程语言返回对应的 NSQ 主题。
// 编译型语言走快速队列，解释型语言走慢速队列。
func topicForLanguage(lang string) string {
	switch lang {
	case "cpp", "c", "go", "rust":
		return TopicFast
	default:
		return TopicSlow
	}
}

// ============================================================================
// 初始化
// ============================================================================

// Init 初始化消息队列。
// 参数 handler 是判题任务处理函数，为 nil 时只初始化生产者（API 服务器模式）。
//
// 后端选择流程：
//
//	QUEUE_BACKEND=redis → Redis Streams（需 Redis 可用）
//	QUEUE_BACKEND=nsq / 未设置 → NSQ（需 nsqd 可用）
//	其他 / 连接失败 → 本地内存通道（进程内通信）
func Init(handler TaskHandler) {
	backend := strings.ToLower(os.Getenv("QUEUE_BACKEND"))

	switch backend {
	case "redis":
		if cache.Ping() {
			queueBackend = "redis"
			// 创建消费组（幂等操作）
			cache.XGroupCreate(cache.StreamJudgeTasks, cache.StreamJudgeGroup)
			log.Println("[queue] Redis Streams backend")
			if handler != nil {
				startRedisConsumer(handler)
			} else {
				log.Println("[queue] API server mode — consumer not started")
			}
			return
		}
		log.Println("[queue] Redis not available, falling back")
		fallthrough
	case "", "nsq":
		initNSQ(handler)
	default:
		log.Printf("[queue] unknown backend %q, using local channel", backend)
		queueBackend = "local"
		if handler != nil {
			startLocalWorkers(handler)
		}
	}
}

// initNSQ 初始化 NSQ 生产者，并可选启动消费者。
func initNSQ(handler TaskHandler) {
	nsqdAddr := os.Getenv("NSQD_ADDR")
	if nsqdAddr == "" {
		nsqdAddr = "127.0.0.1:4150"
	}
	var err error
	producer, err = nsq.NewProducer(nsqdAddr, nsq.NewConfig())
	if err != nil {
		log.Printf("[queue] NSQ not available, using local channel")
		queueBackend = "local"
		if handler != nil {
			startLocalWorkers(handler)
		}
		return
	}
	if err := producer.Ping(); err != nil {
		log.Printf("[queue] NSQ ping failed, using local channel")
		producer = nil
		queueBackend = "local"
		if handler != nil {
			startLocalWorkers(handler)
		}
		return
	}
	queueBackend = "nsq"
	nsqReady = true
	log.Println("[queue] NSQ connected")
	if handler != nil {
		startConsumer(TopicFast, handler)
		startConsumer(TopicSlow, handler)
	} else {
		log.Println("[queue] API server mode — consumer not started")
	}
}

// ============================================================================
// 消费者实现（三种后端）
// ============================================================================

// startLocalWorkers 启动基于 Go channel 的本地 worker。
// 创建 1024 容量的缓冲通道，启动 4 个 goroutine 并发处理任务。
func startLocalWorkers(handler TaskHandler) {
	localChannel = make(chan JudgeTask, 1024)
	numWorkers := 4
	for i := 0; i < numWorkers; i++ {
		go func() {
			for task := range localChannel {
				processWithRecovery(handler, task)
			}
		}()
	}
	log.Printf("[queue] Using local channel fallback (%d workers)", numWorkers)
}

// startRedisConsumer 启动 Redis Streams 消费者。
// 使用 XReadGroup 从消费组读取消息，处理后通过 XAck 确认。
// 每个 worker 有不同的消费者名称以实现分布式消费。
func startRedisConsumer(handler TaskHandler) {
	numWorkers := 4
	for i := 0; i < numWorkers; i++ {
		go func(workerID int) {
			consumer := fmt.Sprintf("%s-%d", cache.StreamJudgeConsumer, workerID)
			for {
				msgs, err := cache.XReadGroup(cache.StreamJudgeTasks, cache.StreamJudgeGroup, consumer, 1)
				if err != nil || len(msgs) == 0 {
					time.Sleep(100 * time.Millisecond)
					continue
				}
				for _, msg := range msgs {
					payload, ok := msg.Values["payload"]
					if !ok {
						cache.XAck(cache.StreamJudgeTasks, cache.StreamJudgeGroup, msg.ID)
						continue
					}
					var task JudgeTask
					if err := json.Unmarshal([]byte(payload.(string)), &task); err != nil {
						cache.XAck(cache.StreamJudgeTasks, cache.StreamJudgeGroup, msg.ID)
						continue
					}
					processWithRecovery(handler, task)
					cache.XAck(cache.StreamJudgeTasks, cache.StreamJudgeGroup, msg.ID)
				}
			}
		}(i)
	}
	log.Printf("[queue] Redis Streams consumer started (%d workers)", numWorkers)
}

// startConsumer 启动 NSQ 消费者。
// 为指定主题创建消费者，注册消息处理函数。
func startConsumer(topic string, handler TaskHandler) {
	addr := os.Getenv("NSQD_ADDR")
	if addr == "" {
		addr = "127.0.0.1:4150"
	}
	consumer, err := nsq.NewConsumer(topic, "judge-worker", nsq.NewConfig())
	if err != nil {
		log.Printf("[queue] consumer create failed for %s: %v", topic, err)
		fallbackToLocal(handler)
		return
	}
	consumer.AddHandler(nsq.HandlerFunc(func(msg *nsq.Message) error {
		var task JudgeTask
		if err := json.Unmarshal(msg.Body, &task); err != nil {
			log.Printf("[queue] failed to parse task: %v", err)
			return nil
		}
		processWithRecovery(handler, task)
		return nil
	}))
	if err := consumer.ConnectToNSQD(addr); err != nil {
		log.Printf("[queue] consumer connect failed for %s: %v", topic, err)
		fallbackToLocal(handler)
	}
}

// fallbackToLocal 在 NSQ 连接失败时回退到本地内存队列。
func fallbackToLocal(handler TaskHandler) {
	nsqReady = false
	queueBackend = "local"
	if localChannel == nil {
		localChannel = make(chan JudgeTask, 1024)
		go func() {
			for task := range localChannel {
				processWithRecovery(handler, task)
			}
		}()
	}
}

// ============================================================================
// 发布与重试
// ============================================================================

// Publish 发布判题任务到队列。
// 根据队列后端选择不同的发布方式。
func Publish(task JudgeTask) error {
	topic := topicForLanguage(task.Language)

	switch queueBackend {
	case "redis":
		return publishRedis(task)
	case "nsq":
		return publishNSQ(task, topic)
	default:
		if localChannel == nil {
			return fmt.Errorf("queue: no backend available")
		}
		localChannel <- task
		return nil
	}
}

func publishRedis(task JudgeTask) error {
	data, _ := json.Marshal(task)
	return cache.XAdd(cache.StreamJudgeTasks, map[string]interface{}{
		"payload": string(data),
		"topic":   topicForLanguage(task.Language),
	})
}

func publishNSQ(task JudgeTask, topic string) error {
	if nsqReady && producer != nil {
		data, err := json.Marshal(task)
		if err != nil {
			return err
		}
		return producer.Publish(topic, data)
	}
	if localChannel == nil {
		return fmt.Errorf("queue: no backend available")
	}
	localChannel <- task
	return nil
}

// Requeue 重新入队判题任务（带重试计数）。
// 如果重试次数超过 MaxRetries，丢弃任务（防止死循环）。
func Requeue(task JudgeTask) {
	task.RetryCount++
	if task.RetryCount > MaxRetries {
		log.Printf("[queue] #%d: max retries exceeded (%d), dropping task",
			task.SubmissionID, task.RetryCount)
		return
	}
	log.Printf("[queue] #%d: requeuing (attempt %d/%d)",
		task.SubmissionID, task.RetryCount+1, MaxRetries)
	Publish(task)
}

// processWithRecovery 包装任务处理函数，捕获 panic 并自动重试。
// 防止单个任务的 panic 导致 worker 进程崩溃。
func processWithRecovery(handler TaskHandler, task JudgeTask) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[queue] #%d: panic recovered: %v, requeuing", task.SubmissionID, r)
			Requeue(task)
		}
	}()
	handler(task)
}

// Stop 停止队列（关闭 NSQ 生产者连接）。
func Stop() {
	if producer != nil {
		producer.Stop()
	}
}
