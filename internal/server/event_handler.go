package server

import (
	"barrage/internal/app"
	"barrage/internal/connctx"
	"barrage/internal/dispatcher"
	"barrage/internal/protocol/ws"
	"bytes"
	"encoding/binary"
	"time"

	"github.com/panjf2000/gnet/v2"
)

// EventHandler 是 gnet 事件处理器
type EventHandler struct {
	// 嵌入内置事件引擎，避免必须实现全部接口
	gnet.BuiltinEventEngine

	// 应用总对象
	App *app.App
}

// OnShutdown 在服务停机关闭时触发
func (h *EventHandler) OnShutdown(eng gnet.Engine) {
	if h.App != nil {
		h.App.Close()
	}
}

// OnBoot 在服务启动完成后触发
func (h *EventHandler) OnBoot(eng gnet.Engine) gnet.Action {
	// 启动时把 engine 注入到应用对象中
	// 后续广播时需要借助 engine 将任务派发回指定 loop
	if h.App != nil {
		h.App.BindEngine(eng)
	}

	return gnet.None
}

// OnOpen 在连接建立时触发
func (h *EventHandler) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	// 连接建立时立即生成唯一连接 ID
	// 后续加入房间、广播排除发送方、断连清理都会复用这个标识
	connID := ""
	if h.App != nil {
		connID = app.NextConnID()
	}

	ctx := &connctx.ConnContext{
		ConnID:         connID,
	}
	ctx.LastActiveTime.Store(time.Now().Unix())
	c.SetContext(ctx)

	if h.App != nil {
		h.App.AllConns.Store(connID, c)
	}

	return nil, gnet.None
}

// OnTraffic 在连接收到数据时触发
func (h *EventHandler) OnTraffic(c gnet.Conn) gnet.Action {
	ctx, ok := c.Context().(*connctx.ConnContext)
	if !ok || ctx == nil {
		return gnet.Close
	}

	// 更新最后活跃时间
	ctx.LastActiveTime.Store(time.Now().Unix())

	// 如果还没有完成 websocket 握手，则先处理 HTTP Upgrade
	if !ctx.HandshakeDone {
		return h.handleHandshake(c, ctx)
	}

	// 握手完成后，按 websocket 帧协议解析
	return h.handleWebSocketTraffic(c, ctx)
}

// OnClose 在连接关闭时触发
func (h *EventHandler) OnClose(c gnet.Conn, err error) gnet.Action {
	ctx, ok := c.Context().(*connctx.ConnContext)
	if !ok || ctx == nil {
		return gnet.None
	}

	if h.App != nil {
		h.App.AllConns.Delete(ctx.ConnID)
	}

	// 如果连接之前已经进入房间，则在关闭时移除房间索引
	// 当前先保留这一步，后面如果你要进一步严格约束 loop 内调用，再单独优化
	if h.App != nil && h.App.RoomManager != nil && ctx.RoomID > 0 && ctx.UserID > 0 {
		h.App.RoomManager.RemoveConnInLoop(ctx.RoomID, ctx.LoopIdx, ctx.ConnID)
	}

	return gnet.None
}

// handleHandshake 处理 websocket 握手
func (h *EventHandler) handleHandshake(c gnet.Conn, ctx *connctx.ConnContext) gnet.Action {
	data, err := c.Next(-1)
	if err != nil {
		return gnet.Close
	}

	ctx.HandshakeBuffer = append(ctx.HandshakeBuffer, data...)

	// 安全防御：防止恶意客户端建立连接后不断以慢速发送字节但不补全 \r\n\r\n 凑算 OOM
	if len(ctx.HandshakeBuffer) > 4096 {
		return gnet.Close
	}

	// HTTP 请求头必须以 \r\n\r\n 结束
	if !bytes.Contains(ctx.HandshakeBuffer, []byte("\r\n\r\n")) {
		return gnet.None
	}

	resp, err := BuildWebSocketHandshakeResponse(ctx.HandshakeBuffer)
	if err != nil {
		return gnet.Close
	}

	ctx.HandshakeDone = true
	ctx.HandshakeBuffer = nil

	_ = c.AsyncWrite(resp, nil)
	return gnet.None
}

// handleWebSocketTraffic 处理 websocket 数据帧
func (h *EventHandler) handleWebSocketTraffic(c gnet.Conn, ctx *connctx.ConnContext) gnet.Action {
	data, err := c.Next(-1)
	if err != nil {
		return gnet.Close
	}

	// 追加到连接级读缓冲，用于处理半包和粘包
	ctx.ReadBuffer = append(ctx.ReadBuffer, data...)

	// 安全防御：限定单次待解包的字节池绝对不超过界限，防御恶意攻击发送的未闭合超巨量半包或死循环空载包
	if len(ctx.ReadBuffer) > 130*1024 {
		return gnet.Close
	}

	frames, consumed, err := DecodeWebSocketFrames(ctx.ReadBuffer)
	if err != nil {
		return gnet.Close
	}

	// 已消费的数据从缓冲区移除，保留未完成半包
	if consumed > 0 {
		remaining := append([]byte(nil), ctx.ReadBuffer[consumed:]...)
		ctx.ReadBuffer = remaining
	}

	for _, frame := range frames {
		switch frame.Opcode {
		case ws.OpPing:
			_ = c.AsyncWrite(BuildPongFrame(frame.Payload), nil)

		case ws.OpClose:
			var closeCode uint16 = 1000 // Normal Closure 默认标准关闭码
			if len(frame.Payload) >= 2 {
				closeCode = binary.BigEndian.Uint16(frame.Payload[:2])
			}

			// 依规范应向发起者原样回写该状态码或附带确认的 Close 载荷
			respPayload := make([]byte, 2)
			binary.BigEndian.PutUint16(respPayload, closeCode)
			_ = c.AsyncWrite(BuildCloseFrame(respPayload), nil)
			return gnet.Close

		case ws.OpBinary:
			if h.App == nil || h.App.WorkerPool == nil {
				continue
			}

			task := &dispatcher.InboundTask{
				Conn: c,
				Ctx:  ctx,
				Data: frame.Payload,
			}

			err = h.App.WorkerPool.Submit(func() {
				h.App.HandleInbound(task)
			})
			if err != nil {
				continue
			}
		}
	}

	return gnet.None
}
