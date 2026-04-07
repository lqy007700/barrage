package app

import (
	"context"
	"errors"
	"sync/atomic"
	"testing"

	"github.com/panjf2000/gnet/v2"
)

// mockEventLoop 实现部分 gnet.EventLoop
type mockEventLoop struct {
	gnet.EventLoop // 嵌入防止必须完全实现所有未暴露接口
	execCount      atomic.Int32
}

func (m *mockEventLoop) Execute(ctx context.Context, fn gnet.Runnable) error {
	m.execCount.Add(1)
	_ = fn.Run(ctx)
	return nil
}

func TestLoopDispatcher_DispatchAndMetrics(t *testing.T) {
	dispatcher := NewLoopDispatcher()
	var mockLoop mockEventLoop

	// 测试 1：未绑定直接派发抛出异常 (ErrLoopNotBound)
	err := dispatcher.Dispatch(0, func() {})
	if !errors.Is(err, ErrLoopNotBound) {
		t.Fatalf("预期返回 ErrLoopNotBound, 实际得到: %v", err)
	}
	if dispatcher.Metrics.ErrNotBoundCount.Load() != 1 {
		t.Fatalf("指标 ErrNotBoundCount 应为 1, 实际: %d", dispatcher.Metrics.ErrNotBoundCount.Load())
	}
	if dispatcher.Metrics.TotalDispatches.Load() != 1 {
		t.Fatalf("全量指标不匹配")
	}

	// 测试 2：绑定正确的 Loop 后应该派送成功
	dispatcher.Bind(0, &mockLoop)

	var taskExecuted bool
	err = dispatcher.Dispatch(0, func() {
		taskExecuted = true
	})

	if err != nil {
		t.Fatalf("派送应该成功，实际报错: %v", err)
	}
	if !taskExecuted {
		t.Fatalf("关联的 task func 未被成功调用执行")
	}
	if mockLoop.execCount.Load() != 1 {
		t.Fatalf("EventLoop Execute 应该被调用 1 次")
	}

	// 确认最终指标递增成功
	if dispatcher.Metrics.SuccessCount.Load() != 1 {
		t.Fatalf("成功次数未自增")
	}
}
