package worker

import (
	"container/list"
	"fmt"
	"sync"
	"time"

	"judgex/internal/database"
)

// ============================================================================
// 测试数据 LRU 缓存 — Worker 进程内缓存
// ============================================================================
//
// 这层缓存是 Worker 进程内的 LRU（Least Recently Used）缓存，
// 与 Redis 的 "tcversion" 版本缓存作用不同：
//
//   Redis 版本缓存（loadTestCasesFromDisk 中）：
//     - 缓存 test_case_version，避免每次判题都查数据库
//     - key = "tcversion:{problemID}"，value = 版本号
//     - 用于快速判断磁盘文件是否还新鲜
//
//   本文件的内存 LRU 缓存（loadTestCasesWithCache 中）：
//     - 缓存解析后的 []testCaseDisk 测试数据本体
//     - key = "{problemID}:{version}"，value = 测试用例数组
//     - 用于避免重复从磁盘读取和解析测试文件
//
// 两级缓存一起工作：
//   1. 先查 Redis 版本缓存（判断版本是否变更）
//   2. 将版本号拼接后查 LRU 内存缓存（命中则跳过磁盘 I/O）
//   3. 都未命中 → 读磁盘 → 写入两级缓存
//
// LRU 淘汰策略：
//   最多缓存 100 道题的测试数据。超过时淘汰最久未使用的条目。
//   每个条目 TTL 为 1 小时。

// testCaseCache 是全局的 LRU 测试数据缓存实例。
// key 格式："{problemID}:{test_case_version}" — 版本变化自动导致未命中。
var testCaseCache = newLRUCache(100) // 最多缓存 100 道题的测试数据

// cacheEntry 是 LRU 缓存中的一个条目。
type cacheEntry struct {
	key     string         // 缓存键（"{problemID}:{version}"）
	value   []testCaseDisk // 测试数据
	expires time.Time      // 过期时间
}

// lruCache 是线程安全的 LRU 缓存实现。
// 使用 container/list（双向链表）+ map 实现 O(1) 查找和更新。
type lruCache struct {
	mu      sync.Mutex
	maxSize int                      // 最大条目数
	ll      *list.List               // 双向链表（越靠近 Front 越最近使用）
	entries map[string]*list.Element // key → 链表节点的映射
}

func newLRUCache(maxSize int) *lruCache {
	return &lruCache{
		maxSize: maxSize,
		ll:      list.New(),
		entries: make(map[string]*list.Element),
	}
}

// Get 从缓存中获取值。
// 如果 key 不存在或已过期，返回 false。
// 如果命中，将该条目移动到链表前端（标记为最近使用）。
func (c *lruCache) Get(key string) ([]testCaseDisk, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	elem, ok := c.entries[key]
	if !ok {
		return nil, false
	}

	entry := elem.Value.(*cacheEntry)
	if time.Now().After(entry.expires) {
		// 已过期 → 删除
		c.ll.Remove(elem)
		delete(c.entries, key)
		return nil, false
	}

	// 移动到链表前端（最近使用）
	c.ll.MoveToFront(elem)
	return entry.value, true
}

// Set 向缓存中添加或更新一个值。
// 如果 key 已存在，更新值并移动到前端。
// 如果达到最大容量，淘汰最久未使用的条目（链表末尾）。
func (c *lruCache) Set(key string, value []testCaseDisk) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if elem, ok := c.entries[key]; ok {
		// 更新已有条目
		c.ll.MoveToFront(elem)
		elem.Value.(*cacheEntry).value = value
		elem.Value.(*cacheEntry).expires = time.Now().Add(1 * time.Hour)
		return
	}

	// 添加新条目
	entry := &cacheEntry{
		key:     key,
		value:   value,
		expires: time.Now().Add(1 * time.Hour),
	}
	elem := c.ll.PushFront(entry)
	c.entries[key] = elem

	// 超出容量 → 淘汰最久未使用的
	if c.ll.Len() > c.maxSize {
		c.evictOldest()
	}
}

// evictOldest 淘汰链表末尾的条目（最久未使用）。
func (c *lruCache) evictOldest() {
	elem := c.ll.Back()
	if elem == nil {
		return
	}
	entry := elem.Value.(*cacheEntry)
	delete(c.entries, entry.key)
	c.ll.Remove(elem)
}

// loadTestCasesWithCache 是带 LRU 缓存的测试数据加载函数。
// key 包含 test_case_version，因此版本升级会自动导致缓存未命中。
//
// 这是 loadTestCasesFromDisk 的替代入口，增加了进程内缓存层，
// 避免同一 Worker 在短时间内反复判同一题时重复磁盘 I/O。
func loadTestCasesWithCache(problemID uint) ([]testCaseDisk, error) {
	var problem struct {
		TestCaseVersion int
	}
	if err := database.DB.Select("test_case_version").First(&problem, problemID).Error; err != nil {
		return loadTestCasesFromDisk(problemID)
	}

	cacheKey := fmt.Sprintf("%d:%d", problemID, problem.TestCaseVersion)
	if cached, ok := testCaseCache.Get(cacheKey); ok {
		return cached, nil
	}

	tcs, err := loadTestCasesFromDisk(problemID)
	if err != nil {
		return nil, err
	}

	testCaseCache.Set(cacheKey, tcs)
	return tcs, nil
}
