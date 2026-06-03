package cache

import (
	"context"
	"encoding/json"
	"log"
	"os"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/redis/go-redis/v9"
)

// ============================================================================
// 缓存层 — Redis + 内存双后端
// ============================================================================
//
// cache 包是 JudgeX 的缓存层抽象，提供统一的缓存接口。
// 同时支持两种后端：
//
//   1. Redis（主后端）— 生产环境使用，支持分布式、Pub/Sub、Streams、ZSet
//   2. 内存 Map（降级后端）— Redis 不可用时自动降级，保证功能不中断
//
// 判断逻辑：Init() 时尝试连接 Redis，失败则使用 sync.RWMutex 保护的内存 map，
// 并启动后台 goroutine (memGC) 每 30 秒清理过期数据。
//
// 功能分类：
//   - KV 缓存：Get / Set / Del / SetNX（用于缓存题目、列表等）
//   - 计数：IncrWithTTL（用于限流）
//   - Hash：HIncrBy / HGet / HGetAll（用于比赛排名计数）
//   - ZSet：ZAdd / ZRevRangeWithScores（用于比赛排行榜）
//   - Pub/Sub：Publish / Subscribe（用于 SSE 实时推送判题结果）
//   - Streams：XAdd / XReadGroup / XAck / XGroupCreate（用于判题队列）
//   - 工具：Ping / Do（singleflight 防缓存击穿）

var (
	rdb *redis.Client              // Redis 客户端（为 nil 时使用内存降级）
	mu  sync.RWMutex               // 保护内存 map 的读写锁
	mem = make(map[string]memItem) // 内存缓存存储（key → value+过期时间）
	// singleflight 机制：防止缓存击穿（并发请求同时查 DB）
	sfMu    sync.Mutex
	sfCalls = make(map[string]*sfCall)
)

// sfCall 代表一个进行中的 singleflight 调用。
// 多个 goroutine 同时请求同一个 key 时，只有一个会执行 fn()，
// 其他的通过 wg.Wait() 等待结果。
type sfCall struct {
	wg  sync.WaitGroup
	val interface{}
	err error
}

// memItem 是内存缓存中的一个条目，包含值和过期时间。
type memItem struct {
	val    string    // JSON 序列化后的值
	expire time.Time // 过期时间
}

// Init 初始化缓存层，尝试连接 Redis。
// 如果 Redis 不可用（如未启动），自动降级到内存缓存。
func Init() {
	addr := os.Getenv("REDIS_ADDR")
	if addr == "" {
		addr = "127.0.0.1:6379"
	}
	rdb = redis.NewClient(&redis.Options{Addr: addr})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := rdb.Ping(ctx).Err(); err != nil {
		rdb = nil
		// Redis 不可用，降级到内存缓存
		log.Println("[cache] Redis unavailable, using in-memory fallback")
		go memGC()
	}
}

// ============================================================================
// KV 缓存操作
// ============================================================================

// Set 设置一个缓存键值对。
//
//	key: 缓存键（自动加 "judgex:" 前缀避免 Redis 命名冲突）
//	val: 任意可 JSON 序列化的值
//	ttl: 过期时间
//
// Redis 后端：用 JSON 序列化后存储
// 内存后端：存储 JSON 字符串 + 过期时间
func Set(key string, val interface{}, ttl time.Duration) {
	if rdb != nil {
		data, _ := json.Marshal(val)
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rdb.Set(ctx, "judgex:"+key, data, ttl)
		return
	}
	// 内存降级
	mu.Lock()
	defer mu.Unlock()
	data, _ := json.Marshal(val)
	mem[key] = memItem{val: string(data), expire: time.Now().Add(ttl)}
}

// Get 获取一个缓存的值，反序列化到 dest。
// 返回 true 表示命中，false 表示未命中或已过期。
func Get(key string, dest interface{}) bool {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		data, err := rdb.Get(ctx, "judgex:"+key).Bytes()
		if err != nil {
			return false
		}
		json.Unmarshal(data, dest)
		return true
	}
	// 内存降级
	mu.RLock()
	defer mu.RUnlock()
	item, ok := mem[key]
	if !ok || time.Now().After(item.expire) {
		return false
	}
	json.Unmarshal([]byte(item.val), dest)
	return true
}

// Del 删除一个缓存键。
func Del(key string) {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rdb.Del(ctx, "judgex:"+key)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	delete(mem, key)
}

// IncrWithTTL 自增一个计数器，并在首次创建时设置 TTL。
// 用于限流器：第一次访问时初始化 TTL，后续只自增不延长 TTL。
// 返回自增后的值。
func IncrWithTTL(key string, ttl time.Duration) int64 {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		fullKey := "judgex:" + key
		val := rdb.Incr(ctx, fullKey).Val()
		if val == 1 {
			rdb.Expire(ctx, fullKey, ttl)
		}
		return val
	}
	mu.Lock()
	defer mu.Unlock()
	fullKey := "judgex:" + key
	item, ok := mem[fullKey]
	if !ok || time.Now().After(item.expire) {
		mem[fullKey] = memItem{val: "1", expire: time.Now().Add(ttl)}
		return 1
	}
	var count int64
	json.Unmarshal([]byte(item.val), &count)
	count++
	data, _ := json.Marshal(count)
	item.val = string(data)
	mem[fullKey] = item
	return count
}

// ============================================================================
// Hash 操作（用于比赛排名中的计数）
// ============================================================================

// HIncrBy 对 hash 中的某个字段自增。
// 用于记录用户在某道题上的错误提交次数（比赛排名中算罚时用）。
func HIncrBy(key, field string, incr int64) int64 {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return rdb.HIncrBy(ctx, "judgex:"+key, field, incr).Val()
	}
	mu.Lock()
	defer mu.Unlock()
	fullKey := "judgex:" + key
	item, ok := mem[fullKey]
	var hash map[string]int64
	if ok && item.val != "" {
		json.Unmarshal([]byte(item.val), &hash)
	} else {
		hash = make(map[string]int64)
	}
	hash[field] += incr
	data, _ := json.Marshal(hash)
	mem[fullKey] = memItem{val: string(data), expire: time.Now().Add(24 * time.Hour)}
	return hash[field]
}

// HGet 读取 hash 中某个字段的值。
func HGet(key, field string) (string, bool) {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		val, err := rdb.HGet(ctx, "judgex:"+key, field).Result()
		if err != nil {
			return "", false
		}
		return val, true
	}
	mu.RLock()
	defer mu.RUnlock()
	fullKey := "judgex:" + key
	item, ok := mem[fullKey]
	if !ok || item.val == "" {
		return "", false
	}
	var hash map[string]int64
	json.Unmarshal([]byte(item.val), &hash)
	if v, exists := hash[field]; exists {
		return strconv.FormatInt(v, 10), true
	}
	return "", false
}

// HGetAll 读取整个 hash 的所有字段。
func HGetAll(key string) map[string]string {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		return rdb.HGetAll(ctx, "judgex:"+key).Val()
	}
	mu.RLock()
	defer mu.RUnlock()
	fullKey := "judgex:" + key
	item, ok := mem[fullKey]
	if !ok || item.val == "" {
		return nil
	}
	var hash map[string]int64
	json.Unmarshal([]byte(item.val), &hash)
	result := make(map[string]string, len(hash))
	for k, v := range hash {
		result[k] = strconv.FormatInt(v, 10)
	}
	return result
}

// ============================================================================
// ZSet 操作（用于比赛排行榜排序）
// ============================================================================
//
// ZSet（有序集合）是 Redis 提供的核心数据结构，用于排行榜场景。
// 每个成员关联一个 score（分数），Redis 自动按分数排序。
//
// 比赛排名算法：
//   ACM 模式：score = 解题数 × 1,000,000 − 罚时（毫秒）
//   IOI 模式：score = 总通过数 × 10,000,000 − 总耗时（毫秒）
//
// 这样设计确保解题数多的排名靠前（高权重），
// 同解题数下罚时少（减去）的排名靠前。

// ZAdd 向有序集合添加成员。
func ZAdd(key string, score float64, member string) {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rdb.ZAdd(ctx, "judgex:"+key, redis.Z{Score: score, Member: member})
		return
	}
	mu.Lock()
	defer mu.Unlock()
	fullKey := "judgex:" + key
	item, ok := mem[fullKey]
	var zset map[string]float64
	if ok && item.val != "" {
		json.Unmarshal([]byte(item.val), &zset)
	} else {
		zset = make(map[string]float64)
	}
	zset[member] = score
	data, _ := json.Marshal(zset)
	mem[fullKey] = memItem{val: string(data), expire: time.Now().Add(24 * time.Hour)}
}

// ZMember 是有序集合中的一个成员及其分数。
type ZMember struct {
	Member string
	Score  float64
}

// ZRevRangeWithScores 返回有序集合中指定范围内的成员（按分数从高到低）。
// 用于获取比赛排行榜的前 N 名。
func ZRevRangeWithScores(key string, start, stop int64) []ZMember {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		zs := rdb.ZRevRangeWithScores(ctx, "judgex:"+key, start, stop).Val()
		result := make([]ZMember, len(zs))
		for i, z := range zs {
			result[i] = ZMember{Member: z.Member.(string), Score: z.Score}
		}
		return result
	}
	mu.RLock()
	defer mu.RUnlock()
	fullKey := "judgex:" + key
	item, ok := mem[fullKey]
	if !ok || item.val == "" {
		return nil
	}
	var zset map[string]float64
	json.Unmarshal([]byte(item.val), &zset)
	type pair struct {
		member string
		score  float64
	}
	pairs := make([]pair, 0, len(zset))
	for m, s := range zset {
		pairs = append(pairs, pair{m, s})
	}
	sort.Slice(pairs, func(i, j int) bool { return pairs[i].score > pairs[j].score })
	if start < 0 {
		start = 0
	}
	if stop < 0 || int(stop) >= len(pairs) {
		stop = int64(len(pairs) - 1)
	}
	result := make([]ZMember, 0)
	for i := int(start); i <= int(stop); i++ {
		result = append(result, ZMember{Member: pairs[i].member, Score: pairs[i].score})
	}
	return result
}

// ============================================================================
// Pub/Sub（用于 SSE 实时推送）
// ============================================================================
//
// 判题完成后，judge worker 通过 Publish 将结果发到 "submission:{id}" 频道，
// 前端的 SSE 端点通过 Subscribe 订阅该频道，收到消息后推送给浏览器。
//
// 这种方式避免了轮询（polling），实现真正的实时推送。

// Publish 向指定频道发布一条消息。
func Publish(channel string, msg string) {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		rdb.Publish(ctx, "judgex:"+channel, msg)
		return
	}
	mu.Lock()
	defer mu.Unlock()
	key := "judgex:pubsub:" + channel
	item, ok := mem[key]
	if !ok {
		return
	}
	var subs []chan string
	json.Unmarshal([]byte(item.val), &subs)
	for _, ch := range subs {
		select {
		case ch <- msg:
		default:
		}
	}
}

// Subscribe 订阅一个频道，返回一个接收消息的 channel 和取消订阅的函数。
//
//	ch: 接收消息的 channel
//	unsub: 调用此函数取消订阅
func Subscribe(channel string) (chan string, func()) {
	if rdb != nil {
		ctx := context.Background()
		pubsub := rdb.Subscribe(ctx, "judgex:"+channel)
		ch := make(chan string, 8)
		go func() {
			for msg := range pubsub.Channel() {
				ch <- msg.Payload
			}
			close(ch)
		}()
		return ch, func() { pubsub.Close() }
	}
	// 内存降级
	mu.Lock()
	defer mu.Unlock()
	key := "judgex:pubsub:" + channel
	item, ok := mem[key]
	var subs []chan string
	if ok && item.val != "" {
		json.Unmarshal([]byte(item.val), &subs)
	}
	ch := make(chan string, 8)
	subs = append(subs, ch)
	data, _ := json.Marshal(subs)
	mem[key] = memItem{val: string(data), expire: time.Now().Add(24 * time.Hour)}
	unsub := func() {
		mu.Lock()
		defer mu.Unlock()
		item, ok := mem[key]
		if !ok {
			return
		}
		var cur []chan string
		json.Unmarshal([]byte(item.val), &cur)
		for i, c := range cur {
			if c == ch {
				cur = append(cur[:i], cur[i+1:]...)
				break
			}
		}
		data, _ := json.Marshal(cur)
		mem[key] = memItem{val: string(data), expire: time.Now().Add(24 * time.Hour)}
		close(ch)
	}
	return ch, unsub
}

// ============================================================================
// Singleflight — 防止缓存击穿
// ============================================================================
//
// 缓存击穿：当某个热点 key 在缓存过期的瞬间，大量并发请求同时打到 DB。
//  singleflight 保证同一时刻只有一个 goroutine 执行 DB 查询，
//  其他 goroutine 等待并复用第一个的结果。
//
// 使用场景：题目详情（Get Problem）接口。
// 当缓存过期时，可能有 100 个用户同时请求同一道题，
// singleflight 确保只有 1 个请求查 DB，其余 99 个等待结果。

// Do 确保同一 key 的 fn() 在同一时刻只执行一次。
// 多个调用者同时请求同一 key 时，只有第一个执行 fn()，
// 其余等待第一个完成并返回相同结果。
func Do(key string, fn func() (interface{}, error)) (interface{}, error) {
	sfMu.Lock()
	if c, ok := sfCalls[key]; ok {
		sfMu.Unlock()
		c.wg.Wait()
		return c.val, c.err
	}
	c := &sfCall{}
	c.wg.Add(1)
	sfCalls[key] = c
	sfMu.Unlock()

	c.val, c.err = fn()
	c.wg.Done()

	sfMu.Lock()
	delete(sfCalls, key)
	sfMu.Unlock()

	return c.val, c.err
}

// ============================================================================
// 分布式锁 & 健康检查
// ============================================================================

// SetNX 设置一个键值对，仅当键不存在时设置成功（原子操作）。
// 返回 true 表示设置成功（键之前不存在）。
// 可以用作分布式锁的基础。
func SetNX(key string, val interface{}, ttl time.Duration) bool {
	if rdb != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		data, _ := json.Marshal(val)
		ok, _ := rdb.SetNX(ctx, "judgex:"+key, data, ttl).Result()
		return ok
	}
	mu.Lock()
	defer mu.Unlock()
	if item, ok := mem[key]; ok && time.Now().Before(item.expire) {
		return false
	}
	data, _ := json.Marshal(val)
	mem[key] = memItem{val: string(data), expire: time.Now().Add(ttl)}
	return true
}

// Ping 检查 Redis 是否可达。
// 如果返回 false，说明当前使用的是内存降级后端。
func Ping() bool {
	if rdb == nil {
		return false
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return rdb.Ping(ctx).Err() == nil
}

// ============================================================================
// Redis Streams — 分布式判题队列
// ============================================================================
//
// Redis Streams 是 Redis 5.0 引入的消息队列模型，比 Pub/Sub 更可靠：
// - 消息持久化（RDB/AOF）
// - 消费者组（Consumer Groups）支持负载均衡
// - 消息确认（ACK）机制
// - 消息回溯（不会被消费后删除）
//
// 当配置 QUEUE_BACKEND=redis 时，判题任务通过 Streams 分发，
// 替代默认的 NSQ。

const (
	StreamJudgeTasks    = "judgex:stream:judge_tasks" // 判题任务 Stream 名称
	StreamJudgeGroup    = "judge-workers"             // 消费者组名称
	StreamJudgeConsumer = "worker"                    // 消费者名称（每个 worker 实例不同）
)

// XAdd 向 Redis Stream 追加一条消息。
func XAdd(stream string, fields map[string]interface{}) error {
	if rdb == nil {
		return nil // Redis 不可用，静默降级到 local channel
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return rdb.XAdd(ctx, &redis.XAddArgs{
		Stream: stream,
		Values: fields,
	}).Err()
}

// XReadGroup 从消费者组中读取消息（阻塞等待）。
// 使用 ">" 表示只读取未被当前组消费的消息。
func XReadGroup(stream, group, consumer string, count int64) ([]redis.XMessage, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	result, err := rdb.XReadGroup(ctx, &redis.XReadGroupArgs{
		Group:    group,
		Consumer: consumer,
		Streams:  []string{stream, ">"},
		Count:    count,
		Block:    2 * time.Second,
	}).Result()
	if err != nil {
		return nil, err
	}
	for _, s := range result {
		return s.Messages, nil
	}
	return nil, nil
}

// XAck 确认一条消息已被处理。
// 未 ACK 的消息会在 Pending 列表中，超时后会被重新投递。
func XAck(stream, group, id string) error {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return rdb.XAck(ctx, stream, group, id).Err()
}

// XGroupCreate 创建消费者组（幂等操作）。
// "$" 表示只消费创建后的新消息。
func XGroupCreate(stream, group string) {
	if rdb == nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	rdb.XGroupCreateMkStream(ctx, stream, group, "$")
}

// XLen 返回 Stream 的长度（当前未消费的消息数）。
// 用于 SRE 监控面板展示队列积压。
func XLen(stream string) int64 {
	if rdb == nil {
		return 0
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	return rdb.XLen(ctx, stream).Val()
}

// memGC 是内存缓存的后台垃圾回收器。
// 每 30 秒扫描一次，删除过期条目，防止内存泄漏。
func memGC() {
	for {
		time.Sleep(30 * time.Second)
		mu.Lock()
		now := time.Now()
		for k, v := range mem {
			if now.After(v.expire) {
				delete(mem, k)
			}
		}
		mu.Unlock()
	}
}
