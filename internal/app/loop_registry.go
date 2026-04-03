package app

import (
	"sync"
	"sync/atomic"

	"github.com/panjf2000/gnet/v2"
)

// LoopRegistry 用于把 gnet.EventLoop 映射成稳定的逻辑下标
type LoopRegistry struct {
	mu sync.Mutex

	// EventLoop 是接口类型，底层一般是可比较的指针实现
	indexByLoop map[gnet.EventLoop]int

	// 已分配下标计数
	next atomic.Int32
}

// NewLoopRegistry 创建 loop 注册表
func NewLoopRegistry() *LoopRegistry {
	return &LoopRegistry{
		indexByLoop: make(map[gnet.EventLoop]int),
	}
}

// GetOrAssign 为 event-loop 分配稳定下标
func (r *LoopRegistry) GetOrAssign(loop gnet.EventLoop) int {
	if loop == nil {
		return 0
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if idx, ok := r.indexByLoop[loop]; ok {
		return idx
	}

	idx := int(r.next.Load())
	r.indexByLoop[loop] = idx
	r.next.Add(1)
	return idx
}
