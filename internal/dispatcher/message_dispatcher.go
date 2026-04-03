package dispatcher

import (
	"barrage/internal/connctx"

	"github.com/panjf2000/gnet/v2"
)

// InboundTask 表示一个入站消息处理任务
type InboundTask struct {
	// 当前连接对象
	Conn gnet.Conn

	// 当前连接上下文
	// 这里包含 ConnID、UserID、RoomID、LoopIdx 等关键信息
	Ctx *connctx.ConnContext

	// 原始 protobuf 消息字节
	Data []byte
}
