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

// Broadcaster 表示广播器
type Broadcaster struct {
	// 房间管理器
	Rooms *room.Manager

	// 按 loop 分发任务的函数
	LoopExec LoopExecutor

	batchMu       sync.Mutex
	pending       map[int64][]*mq.BroadcastEnvelope
	batchInterval time.Duration
}

type LoopExecutor interface {
	Dispatch(loopIdx int, task func()) error
}

// New 创建广播器
// batchInterval: 如果大于 0 则启用 Micro-batching 异步合并微批发送，如果等于 0 则退化为同步即时逐帧发送。
func New(rooms *room.Manager, loopExec LoopExecutor, batchInterval time.Duration) *Broadcaster {
	if loopExec == nil {
		panic("请指定 loop 分发任务函数")
	}

	b := &Broadcaster{
		Rooms:         rooms,
		LoopExec:      loopExec,
		pending:       make(map[int64][]*mq.BroadcastEnvelope),
		batchInterval: batchInterval,
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

	for range ticker.C {
		b.batchMu.Lock()
		flushMap := b.pending
		// 指针交换快速清空
		b.pending = make(map[int64][]*mq.BroadcastEnvelope)
		b.batchMu.Unlock()

		if len(flushMap) == 0 {
			continue
		}

		for roomID, envelopes := range flushMap {
			b.dispatchBatch(roomID, envelopes)
		}
	}
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

	// 否则加入积压蓄水池
	b.batchMu.Lock()
	b.pending[msg.RoomId] = append(b.pending[msg.RoomId], msg)
	b.batchMu.Unlock()

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
