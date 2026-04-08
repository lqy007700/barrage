package broadcast

import (
	"barrage/internal/connctx"
	"barrage/internal/metrics"
	"barrage/internal/mq"
	"barrage/internal/protocol/ws"
	"barrage/internal/room"
	"log"
	"sync"
	"time"

	"github.com/panjf2000/gnet/v2"
)

const shardCount = 64

// Broadcaster 表示广播器
type Broadcaster struct {
	// 房间管理器
	Rooms *room.Manager

	// 按 loop 分发任务的函数
	LoopExec LoopExecutor

	// 分片 map，按 roomId % shardCount 分桶，减少锁竞争
	shards [shardCount]struct {
		mu      sync.Mutex
		pending map[int64][]*mq.BroadcastEnvelope
	}
	batchInterval time.Duration

	// 每房间 pending 队列最大长度，防止 OOM
	maxPendingPerRoom int

	// 优雅关闭信号
	stopCh chan struct{}
}

type LoopExecutor interface {
	Dispatch(loopIdx int, task func()) error
}

// New 创建广播器
// batchInterval: 如果大于 0 则启用 Micro-batching 异步合并微批发送，如果等于 0 则退化为同步即时逐帧发送。
// maxPendingPerRoom: 每房间 pending 队列最大长度，防止高流量时 OOM，默认 1000
func New(rooms *room.Manager, loopExec LoopExecutor, batchInterval time.Duration, maxPendingPerRoom int) *Broadcaster {
	if loopExec == nil {
		panic("请指定 loop 分发任务函数")
	}
	if maxPendingPerRoom <= 0 {
		maxPendingPerRoom = 1000
	}

	b := &Broadcaster{
		Rooms:             rooms,
		LoopExec:          loopExec,
		batchInterval:     batchInterval,
		maxPendingPerRoom: maxPendingPerRoom,
		stopCh:            make(chan struct{}),
	}

	// 初始化分片
	for i := range b.shards {
		b.shards[i].pending = make(map[int64][]*mq.BroadcastEnvelope)
	}

	// 启用后台轮询微批积压打包
	if batchInterval > 0 {
		go b.startBatchTicker()
	}

	return b
}

// startBatchTicker 周期性收割挤压的消息进行 TCP Coalescing 下发
func (b *Broadcaster) startBatchTicker() {
	ticker := time.NewTicker(b.batchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-b.stopCh:
			// 优雅关闭：处理完剩余消息再退出
			for i := range b.shards {
				shard := &b.shards[i]
				shard.mu.Lock()
				flushMap := shard.pending
				shard.pending = make(map[int64][]*mq.BroadcastEnvelope)
				shard.mu.Unlock()
				for roomID, envelopes := range flushMap {
					b.dispatchBatch(roomID, envelopes)
				}
			}
			log.Printf("Broadcaster 已优雅关闭")
			return
		case <-ticker.C:
			// 遍历所有分片收集待发送消息
			for i := range b.shards {
				shard := &b.shards[i]
				shard.mu.Lock()
				flushMap := shard.pending
				// 指针交换快速清空
				shard.pending = make(map[int64][]*mq.BroadcastEnvelope)
				shard.mu.Unlock()

				if len(flushMap) == 0 {
					continue
				}

				for roomID, envelopes := range flushMap {
					// 【自适应本地消息密度抽样器 (Adaptive Message Density Sampling)】
					// 假设 50ms 为一个批次，如果这 50ms 堆积的消息超过 15 条（相当于单秒 300 条极强对端下发密度）
					// 触发降维打击：无条件放行 VIP/特权/高代价弹幕，对剩下来的普通闲聊弹幕执行滑动丢弃，确保系统永远不可能被击穿。
					const MaxEnvelopesPerBatch = 15
					if len(envelopes) > MaxEnvelopesPerBatch {
						var premium []*mq.BroadcastEnvelope
						var normal []*mq.BroadcastEnvelope

						for _, env := range envelopes {
							if env.IsPremium {
								premium = append(premium, env)
							} else {
								normal = append(normal, env)
							}
						}

						sampled := premium
						slotsLeft := MaxEnvelopesPerBatch - len(premium)
						if slotsLeft > 0 && len(normal) > 0 {
							// 使用均匀间隔步长算法（匀速抽帧），避免只截断队尾带来的时序偏见
							step := float64(len(normal)) / float64(slotsLeft)
							for idx := 0; idx < slotsLeft; idx++ {
								envIdx := int(float64(idx) * step)
								if envIdx < len(normal) {
									sampled = append(sampled, normal[envIdx])
								}
							}
						}

						// 替换为处理后的抽样子集
						envelopes = sampled

						// 记录触发了高能熔断抽样的次数，这里只是做简单的降维统计
						metrics.SlowConnSkipCount.Add(1)
					}

					b.dispatchBatch(roomID, envelopes)
				}
			}
		}
	}
}

// Stop 优雅关闭广播器
func (b *Broadcaster) Stop() {
	if b == nil {
		return
	}
	close(b.stopCh)
}

// BroadcastLocal 在当前机器内广播消息
func (b *Broadcaster) BroadcastLocal(msg *mq.BroadcastEnvelope) error {
	if b == nil || b.Rooms == nil || msg == nil {
		return nil
	}

	// 0 延迟退化为直接同步派发策略
	if b.batchInterval <= 0 {
		b.dispatchBatch(msg.RoomId, []*mq.BroadcastEnvelope{msg})
		return nil
	}

	// 根据 roomId 选择对应分片，减少锁竞争
	shardIdx := msg.RoomId % shardCount
	shard := &b.shards[shardIdx]

	// 否则加入积压蓄水池
	shard.mu.Lock()
	shard.pending[msg.RoomId] = append(shard.pending[msg.RoomId], msg)
	// 如果积压超过上限，丢弃最旧的消息防止 OOM
	if len(shard.pending[msg.RoomId]) > b.maxPendingPerRoom {
		dropped := len(shard.pending[msg.RoomId]) - b.maxPendingPerRoom
		shard.pending[msg.RoomId] = shard.pending[msg.RoomId][dropped:]
		metrics.SlowConnSkipCount.Add(int64(dropped))
		log.Printf("pending 队列超限，丢弃 %d 条最旧消息: roomID=%d", dropped, msg.RoomId)
	}
	shard.mu.Unlock()

	return nil
}

// dispatchBatch 根据一批连贯数据在各个拥有连接的 loop 之间进行物理调度绑定派发
func (b *Broadcaster) dispatchBatch(roomID int64, envelopes []*mq.BroadcastEnvelope) {
	loopIndexes := b.Rooms.ActiveLoopIndexes(roomID)
	if len(loopIndexes) == 0 {
		return
	}

	for _, loopIdx := range loopIndexes {
		currentLoopIdx := loopIdx

		// 将合并好的列表整合成一次 loop 线程调度执行
		err := b.LoopExec.Dispatch(currentLoopIdx, func() {
			b.broadcastBatchInLoop(roomID, currentLoopIdx, envelopes)
		})

		metrics.BroadcastDispatchCount.Add(1)

		if err != nil {
			metrics.BroadcastDispatchErrCount.Add(1)
			log.Printf("派发广播微批任务失败: roomID=%d loopIdx=%d err=%v", roomID, currentLoopIdx, err)
			continue
		}
	}
}

// broadcastBatchInLoop 在指定 loop 内分发整个集合
// 该方法应当只在目标 event-loop 中执行
func (b *Broadcaster) broadcastBatchInLoop(roomID int64, loopIdx int, envelopes []*mq.BroadcastEnvelope) {
	if b == nil || b.Rooms == nil || len(envelopes) == 0 {
		return
	}

	// 【核弹防爆核心机制】：Micro-batching (TCP Write Coalescing)
	// 无论这 50ms 积压了多少帧弹幕，我们进行 O(1) 级的预先合并包装形成单一 `MegaFrame`，极大削减 Syscalls!
	var megaFrame []byte
	for _, env := range envelopes {
		megaFrame = append(megaFrame, ws.BuildBinaryFrame(env.Payload)...)
	}

	b.Rooms.RangeShardInLoop(roomID, loopIdx, func(connID string, c gnet.Conn) {
		if c == nil {
			return
		}

		ctx, ok := c.Context().(*connctx.ConnContext)
		if !ok || ctx == nil {
			return
		}

		outbound := c.OutboundBuffered()

		// 1. 极端慢连接判定 (缓冲超过 4MB)：无视是否 VIP，强制断开此连接以防 OOM 雪崩
		if outbound > 4*1024*1024 {
			log.Printf("连接下发缓冲超过 4MB，触发极慢连接阻断保护: connID=%s", ctx.ConnID)
			c.Close()
			return
		}

		if ctx.IsPremium {
			// 2. VIP 用户重度降级策略 (缓冲超过 1.5MB)：跳过
			if outbound > 1536*1024 {
				metrics.SlowConnSkipCount.Add(int64(len(envelopes)))
				return
			}
		} else {
			// 3. 普通用户重度降级策略 (缓冲超过 512KB)：全部跳过
			if outbound > 512*1024 {
				metrics.SlowConnSkipCount.Add(int64(len(envelopes)))
				return
			}

			// 4. 普通用户轻度降级策略 (缓冲超过 256KB)：通过包特征做无锁抽样抛弃
			if outbound > 256*1024 {
				if envelopes[0].Timestamp%2 == 1 {
					metrics.SlowConnSkipCount.Add(int64(len(envelopes)))
					return
				}
			}
		}

		// 鉴别是否有发送者自弹幕以实现防回显
		hasEcho := false
		for _, env := range envelopes {
			if env.SenderConnId != "" && env.SenderConnId == ctx.ConnID {
				hasEcho = true
				break
			}
		}

		if !hasEcho {
			// 绝大多数情况命中：该合并包里完全没有自身产生的弹幕，直接暴力全下发，零拷贝！
			_ = c.AsyncWrite(megaFrame, nil)
		} else {
			// Sender 的慢速小包专属特推构造
			var personalMegaFrame []byte
			for _, env := range envelopes {
				if env.SenderConnId != "" && env.SenderConnId == ctx.ConnID {
					continue
				}
				personalMegaFrame = append(personalMegaFrame, ws.BuildBinaryFrame(env.Payload)...)
			}
			if len(personalMegaFrame) > 0 {
				_ = c.AsyncWrite(personalMegaFrame, nil)
			}
		}
	})
}
