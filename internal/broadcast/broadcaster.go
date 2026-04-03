package broadcast

import (
	"barrage/internal/connctx"
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
		return nil
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

		if err != nil {
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

		// 当发送缓冲超过 512KB 时，非付费用户跳过本次发送
		if c.OutboundBuffered() > 512*1024 && !ctx.IsPremium {
			return
		}

		_ = c.AsyncWrite(frame, nil)
	})
}
