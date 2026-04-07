package app

import (
	"context"
	"errors"
	"fmt"
	"github.com/panjf2000/gnet/v2"
	"sync"
	"sync/atomic"
)

var (
	// ErrDispatcherNil 实例为空
	ErrDispatcherNil = errors.New("loop dispatcher is nil")
	
	// ErrTaskNil 任务回调为空
	ErrTaskNil = errors.New("loop task is nil")
	
	// ErrLoopNotBound 目标 event-loop 未绑定或已丢失
	ErrLoopNotBound = errors.New("target event-loop is not bound")
	
	// ErrLoopExecute 目标 event-loop 抛出投递异常（如队列满等）
	ErrLoopExecute = errors.New("event-loop execute failed")
)

// DispatchMetrics 纪律派送指标
type DispatchMetrics struct {
	TotalDispatches  atomic.Int64
	SuccessCount     atomic.Int64
	ErrNotBoundCount atomic.Int64
	ErrExecuteCount  atomic.Int64
}

// LoopDispatcher 用于把任务派发回指定的 event-loop 执行
type LoopDispatcher struct {
	mu sync.RWMutex

	// 逻辑 loop 下标到 event-loop 的映射
	loops map[int]gnet.EventLoop

	// DispatchMetrics 暴露派送与故障指标
	Metrics DispatchMetrics
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
		return ErrDispatcherNil
	}

	if task == nil {
		return ErrTaskNil
	}

	// 指标上报打点：投递总数
	d.Metrics.TotalDispatches.Add(1)

	d.mu.RLock()
	loop, ok := d.loops[loopIdx]
	d.mu.RUnlock()
	if !ok {
		// 指标上报打点：Loop未绑定导致丢弃
		d.Metrics.ErrNotBoundCount.Add(1)
		return ErrLoopNotBound
	}
	
	err := loop.Execute(
		context.Background(),
		gnet.RunnableFunc(func(ctx context.Context) error {
			task()
			return nil
		}),
	)

	if err != nil {
		// 指标上报打点：Loop队列满或阻塞报错
		d.Metrics.ErrExecuteCount.Add(1)
		return fmt.Errorf("%w: %v", ErrLoopExecute, err)
	}

	// 指标上报打点：分发成功
	d.Metrics.SuccessCount.Add(1)
	return nil
}
