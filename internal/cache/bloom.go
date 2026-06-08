package cache

import (
	"hash/fnv"
	"log"
	"math"
	"strconv"
	"sync"
	"time"

	"judgex/internal/database"
)

// ============================================================================
// Bloom Filter — 缓存穿透防护（防遍历不存在 ID）
// ============================================================================
//
// 布隆过滤器是一种概率性数据结构，用于判断一个元素是否"可能在集合中"。
// 它有两个特点：
//   - 说"不存在" → 一定不存在（绝不漏报）
//   - 说"存在" → 可能存在，也可能不存在（有误报率）
//
// 在本题缓存场景中的用法：
//   在查 Redis 缓存之前先查 Bloom Filter，如果 Bloom Filter 说 ID 不存在，
//   直接返回 404，完全避免对 Redis 和数据库的访问。
//
// 注意：Bloom Filter 只能添加元素，不能删除。删除操作会导致误报率升高，
// 解决方式是定期重建（见 RefreshLoop）。

const (
	bloomExpectedN = 10000  // 预期最大元素数（题目数上限）
	bloomFP        = 0.01   // 目标误报率 1%
	bloomRefresh   = 10 * time.Minute // 重建间隔
)

var (
	bf   *BloomFilter
	bfMu sync.RWMutex
)

// BloomFilter 是布隆过滤器的实现。
// 使用 k 个哈希函数映射到 m 位的 bit 数组。
type BloomFilter struct {
	m    uint64   // bit 数组大小
	k    int      // 哈希函数个数
	bits []uint64 // 用 uint64 切片模拟 bit 数组
	n    int      // 已添加元素数（用于监控）
}

// NewBloomFilter 创建一个布隆过滤器。
//   expectedN: 预期元素数量
//   fp: 目标误报率（0~1）
func NewBloomFilter(expectedN int, fp float64) *BloomFilter {
	if expectedN <= 0 {
		expectedN = 1
	}
	if fp <= 0 {
		fp = bloomFP
	}
	// m = -n * ln(p) / (ln(2))^2
	m := math.Ceil(float64(expectedN) * math.Abs(math.Log(fp)) / math.Pow(math.Log(2), 2))
	// k = (m/n) * ln(2)
	k := int(math.Ceil(m / float64(expectedN) * math.Log(2)))

	// 向上取整到 64 的倍数
	m = math.Ceil(m/64) * 64
	return &BloomFilter{
		m:    uint64(m),
		k:    k,
		bits: make([]uint64, int(m/64)),
	}
}

// Add 向过滤器添加一个元素。
func (b *BloomFilter) Add(data []byte) {
	for i := 0; i < b.k; i++ {
		pos := b.hash(data, i) % b.m
		b.bits[pos/64] |= 1 << (pos % 64)
	}
	b.n++
}

// MightContain 判断元素是否可能在集合中。
// false = 一定不在；true = 可能在（有误报）。
func (b *BloomFilter) MightContain(data []byte) bool {
	for i := 0; i < b.k; i++ {
		pos := b.hash(data, i) % b.m
		if b.bits[pos/64]&(1<<(pos%64)) == 0 {
			return false
		}
	}
	return true
}

// hash 生成第 i 个哈希值。
// 使用 FNV-1a + 不同种子，得到 k 个独立哈希。
func (b *BloomFilter) hash(data []byte, seed int) uint64 {
	h := fnv.New64a()
	h.Write([]byte{byte(seed), byte(seed >> 8), byte(seed >> 16), byte(seed >> 24)})
	h.Write(data)
	return h.Sum64()
}

// Reset 清空过滤器。
func (b *BloomFilter) Reset() {
	b.bits = make([]uint64, len(b.bits))
	b.n = 0
}

// ============================================================================
// 全局接口
// ============================================================================

// MightHaveProblem 判断题目 ID 是否可能存在。
// 如果返回 false，调用方可以直接返回 404。
// 注意：在 Bloom Filter 重建窗口内，此检查会被跳过（返回 true），
// 不会影响正常服务。
func MightHaveProblem(problemID uint64) bool {
	bfMu.RLock()
	f := bf
	bfMu.RUnlock()
	if f == nil {
		return true // Bloom Filter 未就绪，放行
	}
	return f.MightContain([]byte(strconv.FormatUint(problemID, 10)))
}

// AddProblemID 向 Bloom Filter 添加一个已知的题目 ID。
// 在创建题目时调用。
func AddProblemID(problemID uint64) {
	bfMu.RLock()
	f := bf
	bfMu.RUnlock()
	if f != nil {
		f.Add([]byte(strconv.FormatUint(problemID, 10)))
	}
}

// InitBloomFilter 初始化布隆过滤器，并从数据库加载所有已有的题目 ID。
// 返回后即可使用 MightHaveProblem 进行判断。
// 启动后台 goroutine 定期重建（补偿 Bloom Filter 不能删除的缺陷）。
func InitBloomFilter() {
	rebuild()

	go func() {
		for {
			time.Sleep(bloomRefresh)
			rebuild()
		}
	}()

	log.Printf("[bloom] Bloom filter initialized: m=%d bits (%.1f KB), k=%d hashes, fp=%.1f%%",
		bf.m, float64(bf.m)/8/1024, bf.k, bloomFP*100)
}

// rebuild 从数据库加载所有题目 ID 重建 Bloom Filter。
func rebuild() {
	rows, err := database.DB.Table("problems").Select("id").Rows()
	if err != nil {
		log.Printf("[bloom] failed to load problem IDs: %v", err)
		return
	}
	defer rows.Close()

	f := NewBloomFilter(bloomExpectedN, bloomFP)
	var count int
	for rows.Next() {
		var id uint64
		if err := rows.Scan(&id); err != nil {
			continue
		}
		f.Add([]byte(strconv.FormatUint(id, 10)))
		count++
	}

	bfMu.Lock()
	bf = f
	bfMu.Unlock()

	log.Printf("[bloom] rebuilt: %d problems loaded, %d bits used",
		count, f.m)
}
