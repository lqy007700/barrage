package app

import (
	"context"
	"errors"
	"github.com/panjf2000/gnet/v2"
	"sync"
)

// LoopDispatcher 用于把任务派发回指定的 event-loop 执行
type LoopDispatcher struct {
	mu sync.RWMutex

	// 逻辑 loop 下标到 event-loop 的映射
	loops map[int]gnet.EventLoop
}

// NewLoopDispatcher 创建 loop 分发器
func NewLoopDispatcher() *LoopDispatcher {
	return &LoopDispatcher{
		loops: make(map[int]gnet.EventLoop),
	}
}

// Bind 绑定逻辑 loop 下标和 event-loop
func (d *LoopDispatcher) Bind(loopIdx int, loop gnet.EventLoop) {
	if d == nil || loop == nil || loopIdx < 0 {
		return
	}

	d.mu.Lock()
	defer d.mu.Unlock()

	d.loops[loopIdx] = loop
}

// Dispatch 将任务派发到指定 loop 执行
func (d *LoopDispatcher) Dispatch(loopIdx int, task func()) error {
	if d == nil {
		return errors.New("loop dispatcher is nil")
	}

	if task == nil {
		return errors.New("loop task is nil")
	}

	d.mu.RLock()
	loop, ok := d.loops[loopIdx]
	d.mu.RUnlock()
	if !ok {
		return errors.New("target event-loop is not bound")
	}
	return loop.Execute(
		context.Background(),
		gnet.RunnableFunc(func(ctx context.Context) error {
			task()
			return nil
		}),
	)
}
