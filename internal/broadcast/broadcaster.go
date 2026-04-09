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
}

// New 创建广播器
func New(rooms *room.Manager) *Broadcaster {
	if rooms == nil {
		panic("rooms 不能为空")
	}
	return &Broadcaster{
		Rooms: rooms,
	}
}

// BroadcastLocal 在当前机器内广播消息
// 直接遍历连接调用 AsyncWrite，不经过 event-loop 队列
func (b *Broadcaster) BroadcastLocal(msg *mq.BroadcastEnvelope) error {
	if b == nil || b.Rooms == nil || msg == nil {
		return nil
	}

	room := b.Rooms.Get(msg.RoomId)
	if room == nil {
		return nil
	}

	// 构建 MegaFrame（TCP Write Coalescing）
	megaFrame := ws.BuildBinaryFrame(msg.Payload)

	// 遍历所有有连接的 loop，直接 AsyncWrite
	for loopIdx := 0; loopIdx < b.Rooms.LoopCount(); loopIdx++ {
		room.RangeLoopConns(loopIdx, func(c gnet.Conn) {
			b.sendToConn(c, msg, megaFrame)
		})
	}

	metrics.BroadcastDispatchCount.Add(1)
	return nil
}

// sendToConn 发送消息到单个连接
// 包含慢连接降级和防回显逻辑
func (b *Broadcaster) sendToConn(c gnet.Conn, msg *mq.BroadcastEnvelope, megaFrame []byte) {
	if c == nil {
		return
	}

	ctx, ok := c.Context().(*connctx.ConnContext)
	if !ok || ctx == nil {
		return
	}

	outbound := c.OutboundBuffered()

	// 1. 极端慢连接判定 (缓冲超过 4MB)：强制断开
	if outbound > 4*1024*1024 {
		log.Printf("连接下发缓冲超过 4MB，强制关闭: connID=%s", ctx.ConnID)
		c.Close()
		return
	}

	// 2. VIP 用户重度降级策略 (缓冲超过 1.5MB)：跳过
	if ctx.IsPremium && outbound > 1536*1024 {
		metrics.SlowConnSkipCount.Add(1)
		return
	}

	// 3. 普通用户重度降级策略 (缓冲超过 512KB)：跳过
	if outbound > 512*1024 {
		metrics.SlowConnSkipCount.Add(1)
		return
	}

	// 4. 普通用户轻度降级策略 (缓冲超过 256KB)：抽样跳过
	if outbound > 256*1024 {
		if msg.Timestamp%2 == 1 {
			metrics.SlowConnSkipCount.Add(1)
			return
		}
	}

	// 防回显：检查发送者是否是自身
	if msg.SenderConnId != "" && msg.SenderConnId == ctx.ConnID {
		return
	}

	// 发送消息
	_ = c.AsyncWrite(megaFrame, nil)
}
