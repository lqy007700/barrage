package broadcast

import (
	"barrage/internal/connctx"
	"barrage/internal/metrics"
	"barrage/internal/mq"
	"barrage/internal/protocol/ws"
	"barrage/internal/room"
	"log"

	"github.com/panjf2000/gnet/v2"
)

// Broadcaster 表示广播器
type Broadcaster struct {
	// 房间管理器
	Rooms *room.Manager

	// 按 loop 分发任务的函数
	LoopExec LoopExecutor
}

type LoopExecutor interface {
	Dispatch(loopIdx int, task func()) error
}

// New 创建广播器
func New(rooms *room.Manager, loopExec LoopExecutor) *Broadcaster {
	if loopExec == nil {
		panic("请指定 loop 分发任务函数")
	}

	return &Broadcaster{
		Rooms:    rooms,
		LoopExec: loopExec,
	}
}

// BroadcastLocal 在当前机器内广播消息
func (b *Broadcaster) BroadcastLocal(msg *mq.BroadcastEnvelope) error {
	if b == nil || b.Rooms == nil || msg == nil {
		return nil
	}
	loopIndexes := b.Rooms.ActiveLoopIndexes(msg.RoomId)
	if len(loopIndexes) == 0 {
		return nil
	}

	for _, loopIdx := range loopIndexes {
		currentLoopIdx := loopIdx

		err := b.LoopExec.Dispatch(currentLoopIdx, func() {
			b.broadcastInLoop(currentLoopIdx, msg)
		})

		metrics.BroadcastDispatchCount.Add(1)

		if err != nil {
			metrics.BroadcastDispatchErrCount.Add(1)
			log.Printf("派发广播任务失败: roomID=%d loopIdx=%d err=%v", msg.RoomId, currentLoopIdx, err)
			continue
		}
	}

	return nil
}

// broadcastInLoop 在指定 loop 内广播消息
// 该方法应当只在目标 event-loop 中执行
func (b *Broadcaster) broadcastInLoop(loopIdx int, msg *mq.BroadcastEnvelope) {
	if b == nil || b.Rooms == nil || msg == nil {
		return
	}

	frame := ws.BuildBinaryFrame(msg.Payload)

	b.Rooms.RangeShardInLoop(msg.RoomId, loopIdx, func(connID string, c gnet.Conn) {
		if c == nil {
			return
		}

		ctx, ok := c.Context().(*connctx.ConnContext)
		if !ok || ctx == nil {
			return
		}

		// 发送连接本身不回显自己的消息
		if msg.SenderConnId != "" && ctx.ConnID == msg.SenderConnId {
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
			// 2. VIP 用户重度降级策略 (缓冲超过 1.5MB)：跳过非必要发包
			if outbound > 1536*1024 {
				metrics.SlowConnSkipCount.Add(1)
				return
			}
		} else {
			// 3. 普通用户重度降级策略 (缓冲超过 512KB)：全部跳过
			if outbound > 512*1024 {
				metrics.SlowConnSkipCount.Add(1)
				return
			}

			// 4. 普通用户轻度降级策略 (缓冲超过 256KB)：通过消息毫秒戳做无锁的 50% 概率抛弃
			if outbound > 256*1024 {
				if msg.Timestamp%2 == 1 {
					metrics.SlowConnSkipCount.Add(1)
					return
				}
			}
		}

		_ = c.AsyncWrite(frame, nil)
	})
}
