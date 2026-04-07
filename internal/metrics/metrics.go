package metrics

import (
	"sync/atomic"
)

// ---------------------------------------------------------
// 内存埋点：计数器（Counter）类型
// ---------------------------------------------------------

var (
	// BroadcastDispatchCount 广播派发总次数
	BroadcastDispatchCount atomic.Int64

	// BroadcastDispatchErrCount 广播派发失败总数
	BroadcastDispatchErrCount atomic.Int64

	// FilterRejectCount 敏感词触发驳回总数
	FilterRejectCount atomic.Int64

	// KafkaPublishErrCount Kafka 开发生产（发送）失败总数
	KafkaPublishErrCount atomic.Int64

	// KafkaConsumeErrCount Kafka 消费读取失败总数
	KafkaConsumeErrCount atomic.Int64

	// SlowConnSkipCount 慢连接（外发缓冲区超载）被跳过下发的总次数
	SlowConnSkipCount atomic.Int64
)

// ---------------------------------------------------------
// 设计思路：仪表盘（Gauge）类型
// ---------------------------------------------------------
// Gauge 类型（如：每房间连接数、每 loop 连接数、worker 忙碌度）通常是瞬时状态。
// 我们不在内存中进行高频的 Add() 和 Sub() 维护，而是提倡以“拉取(Pull)”为主。
// 后续如果接入 Prometheus，我们可以直接调用现成的系统接口或利用现成管理器。
//
// 1. 每房间连接数：可直接调用 app.RoomManager.ConnCount(roomID) 获取。
// 2. Worker Pool 忙碌度：可直接调用 app.WorkerPool.Running() 和 app.WorkerPool.Cap()。
// 3. 全局总连接数/每 Loop 连接数：可借用 app.AllConns 动态遍历，或者通过 gnet 引擎的内建连接查询。

// RegisterPrometheus (Mock) 表示后续如何快速接入
// func RegisterPrometheus() {
//      // 例如针对计数器：
//      prometheus.MustRegister(prometheus.NewCounterFunc(
//          prometheus.CounterOpts{Name: "barrage_dispatch_total"},
//          func() float64 { return float64(BroadcastDispatchCount.Load()) },
//      ))
// }
