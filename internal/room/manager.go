package room

import (
	"context"
	"hash/fnv"
	"math/bits"
	"sync"
	"sync/atomic"
	"time"

	"github.com/panjf2000/gnet/v2"
)

// Room 表示一个房间在当前机器上的连接集合
type Room struct {
	// 房间 ID
	ID int64

	// 每个 sub-reactor 对应一个连接分片
	// key 是 connID，value 是 gnet.Conn
	Shards []map[string]gnet.Conn

	// 每个分片当前连接数
	ShardConnCount []atomic.Int32

	// 本机房间总连接数
	LocalConnCount atomic.Int64

	// 房间连接数归零的最后时间 (用于延迟回收)
	LastEmptyTime atomic.Int64

	// 活跃 loop 位图
	// 第 i 位为 1 表示该房间在 loop i 上当前至少有一个连接
	ActiveLoops atomic.Uint64
}

// bucket 表示房间管理器中的一个物理分桶
type bucket struct {
	mu sync.RWMutex

	rooms map[int64]*Room
}

// Manager 表示房间管理器
type Manager struct {
	buckets   []*bucket
	bucketNum int
	loopCount int
}

// NewManager 创建房间管理器
func NewManager(loopCount int) *Manager {
	if loopCount <= 0 {
		loopCount = 1
	}

	if loopCount > 64 {
		panic("loopCount 超过 64，当前 ActiveLoops 位图实现不支持")
	}

	bucketNum := loopCount * 4
	if bucketNum < 16 {
		bucketNum = 16
	}

	m := &Manager{
		buckets:   make([]*bucket, bucketNum),
		bucketNum: bucketNum,
		loopCount: loopCount,
	}

	for i := 0; i < bucketNum; i++ {
		m.buckets[i] = &bucket{
			rooms: make(map[int64]*Room),
		}
	}

	return m
}

// LoopCount 返回 sub-reactor 数量
func (m *Manager) LoopCount() int {
	return m.loopCount
}

// BucketNum 返回物理分桶数量
func (m *Manager) BucketNum() int {
	return m.bucketNum
}

// bucketIndex 根据 roomID 计算所属桶
func (m *Manager) bucketIndex(roomID int64) int {
	h := fnv.New32a()

	var b [8]byte
	for i := 0; i < 8; i++ {
		b[i] = byte(roomID >> (8 * i))
	}

	_, _ = h.Write(b[:])
	return int(h.Sum32() % uint32(m.bucketNum))
}

// Get 获取房间对象
func (m *Manager) Get(roomID int64) *Room {
	idx := m.bucketIndex(roomID)
	b := m.buckets[idx]

	b.mu.RLock()
	defer b.mu.RUnlock()

	return b.rooms[roomID]
}

// GetOrCreate 获取或创建房间对象
func (m *Manager) GetOrCreate(roomID int64) *Room {
	idx := m.bucketIndex(roomID)
	b := m.buckets[idx]

	b.mu.Lock()
	defer b.mu.Unlock()

	if r, ok := b.rooms[roomID]; ok {
		return r
	}

	r := &Room{
		ID:             roomID,
		Shards:         make([]map[string]gnet.Conn, m.loopCount),
		ShardConnCount: make([]atomic.Int32, m.loopCount),
	}

	for i := 0; i < m.loopCount; i++ {
		r.Shards[i] = make(map[string]gnet.Conn)
	}

	b.rooms[roomID] = r
	return r
}

// AddConnInLoop 将连接加入指定房间和指定分片
// 调用约束：
// 1. 该方法应当只在目标 loop 对应的 event-loop 线程中调用。
// 2. 该方法不会为分片 map 提供额外互斥保护，依赖 event-loop 串行语义保证安全。
func (m *Manager) AddConnInLoop(roomID int64, loopIdx int, connID string, c gnet.Conn) {
	if roomID <= 0 || connID == "" || c == nil {
		return
	}

	if loopIdx < 0 || loopIdx >= m.loopCount {
		return
	}

	r := m.GetOrCreate(roomID)

	if _, exists := r.Shards[loopIdx][connID]; exists {
		r.Shards[loopIdx][connID] = c
		return
	}

	r.Shards[loopIdx][connID] = c
	r.LocalConnCount.Add(1)

	newCount := r.ShardConnCount[loopIdx].Add(1)
	if newCount == 1 {
		mask := uint64(1) << uint(loopIdx)
		r.ActiveLoops.Or(mask)
	}

	// 只要有新连接进房，清除空闲标记
	r.LastEmptyTime.Store(0)
}

// RemoveConnInLoop 将连接从指定房间和指定分片移除
// 调用约束：
// 1. 该方法应当只在目标 loop 对应的 event-loop 线程中调用。
// 2. 该方法不会为分片 map 提供额外互斥保护，依赖 event-loop 串行语义保证安全。
func (m *Manager) RemoveConnInLoop(roomID int64, loopIdx int, connID string) {
	if roomID <= 0 || connID == "" {
		return
	}

	if loopIdx < 0 || loopIdx >= m.loopCount {
		return
	}

	idx := m.bucketIndex(roomID)
	b := m.buckets[idx]

	b.mu.RLock()
	r, ok := b.rooms[roomID]
	b.mu.RUnlock()
	if !ok {
		return
	}

	if _, exists := r.Shards[loopIdx][connID]; !exists {
		return
	}

	delete(r.Shards[loopIdx], connID)
	r.LocalConnCount.Add(-1)

	newCount := r.ShardConnCount[loopIdx].Add(-1)
	if newCount == 0 {
		mask := ^(uint64(1) << uint(loopIdx))
		r.ActiveLoops.And(mask)
	}

	// 记录退房后的空闲状态，交由异步 GC 处理
	if r.LocalConnCount.Load() == 0 {
		r.LastEmptyTime.Store(time.Now().Unix())
	}
}

// HasLocalRoom 判断当前机器是否存在该房间连接
func (m *Manager) HasLocalRoom(roomID int64) bool {
	r := m.Get(roomID)
	if r == nil {
		return false
	}

	return r.LocalConnCount.Load() > 0
}

// ConnCount 返回某个房间在本机的总连接数
func (m *Manager) ConnCount(roomID int64) int64 {
	r := m.Get(roomID)
	if r == nil {
		return 0
	}

	return r.LocalConnCount.Load()
}

// ActiveLoopBitmap 返回房间当前活跃 loop 位图
func (m *Manager) ActiveLoopBitmap(roomID int64) uint64 {
	r := m.Get(roomID)
	if r == nil {
		return 0
	}

	return r.ActiveLoops.Load()
}

// ActiveLoopIndexes 返回房间当前活跃的 loop 下标列表
func (m *Manager) ActiveLoopIndexes(roomID int64) []int {
	r := m.Get(roomID)
	if r == nil {
		return nil
	}

	bitmap := r.ActiveLoops.Load()
	if bitmap == 0 {
		return nil
	}

	indexes := make([]int, 0, bits.OnesCount64(bitmap))
	for bitmap != 0 {
		loopIdx := bits.TrailingZeros64(bitmap)
		indexes = append(indexes, loopIdx)
		bitmap &= bitmap - 1
	}

	return indexes
}

// RangeShardInLoop 遍历指定房间在某个 loop 分片内的连接
// 调用约束：
// 1. 该方法应当只在目标 loop 对应的 event-loop 线程中调用。
// 2. 遍历期间如果同时有其他 goroutine 修改同一分片 map，会破坏并发安全。
func (m *Manager) RangeShardInLoop(roomID int64, loopIdx int, fn func(connID string, c gnet.Conn)) {
	if loopIdx < 0 || loopIdx >= m.loopCount {
		return
	}

	r := m.Get(roomID)
	if r == nil {
		return
	}

	shard := r.Shards[loopIdx]
	for connID, c := range shard {
		fn(connID, c)
	}
}

// StartRoomCleaner 开启后台定期清理空闲房间
// idleSeconds: 空闲多少秒后认为可以安全销毁
func (m *Manager) StartRoomCleaner(ctx context.Context, idleSeconds int64) {
	go func() {
		// 检查间隔一般为空闲阈值的一半
		checkInterval := time.Duration(idleSeconds/2) * time.Second
		if checkInterval < 10*time.Second {
			checkInterval = 10 * time.Second
		}

		ticker := time.NewTicker(checkInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now().Unix()
				// 遍历分桶进行锁清理
				for i := 0; i < m.bucketNum; i++ {
					m.cleanBucket(i, now, idleSeconds)
				}
			}
		}
	}()
}

// cleanBucket 清理具体的分桶
func (m *Manager) cleanBucket(idx int, now int64, idleSeconds int64) {
	b := m.buckets[idx]
	
	// 在清理时必须要拿到桶的写锁
	b.mu.Lock()
	defer b.mu.Unlock()

	for roomID, r := range b.rooms {
		// 如果当前连接数为0，并且空闲时间超过阈值，则淘汰
		if r.LocalConnCount.Load() == 0 {
			emptyTime := r.LastEmptyTime.Load()
			if emptyTime > 0 && now-emptyTime > idleSeconds {
				delete(b.rooms, roomID)
			}
		}
	}
}
