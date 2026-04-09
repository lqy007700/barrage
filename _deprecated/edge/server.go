package edge

import (
	"bytes"
	"context"
	"encoding/binary"
	"fmt"
	"log"
	"sync"
	"time"

	"barrage/internal/common"
	"barrage/internal/connctx"
	"barrage/internal/dispatcher"
	"barrage/internal/pb"
	"barrage/internal/protocol/ws"
	"barrage/internal/server"
	"barrage/internal/worker"

	"github.com/panjf2000/gnet/v2"
	"google.golang.org/protobuf/proto"
)

// EdgeServer Edge节点的 WebSocket 服务器
// 复用现有 barrage server 的 gnet + WebSocket 架构
type EdgeServer struct {
	// 配置
	nodeId   string
	wsAddr   string
	httpAddr string

	// 核心组件
	sessionManager *SessionManager
	roomMap       *RoomMap
	videoSync     *VideoSyncEngine
	broadcaster   *EdgeBroadcaster
	consumer      *Consumer

	// gnet
	engine gnet.Engine

	// 连接管理
	mu      sync.RWMutex
	conns   map[string]gnet.Conn

	// Worker Pool
	pool *worker.Pool
}

// NewEdgeServer 创建 Edge 服务器
func NewEdgeServer(nodeId, wsAddr, httpAddr string, poolSize int) (*EdgeServer, error) {
	pool, err := worker.NewPool(poolSize)
	if err != nil {
		return nil, fmt.Errorf("create worker pool: %w", err)
	}

	return &EdgeServer{
		nodeId:   nodeId,
		wsAddr:   wsAddr,
		httpAddr: httpAddr,
		sessionManager: NewSessionManager(),
		roomMap:       NewRoomMap(),
		conns:         make(map[string]gnet.Conn),
		pool:          pool,
	}, nil
}

// SetComponents 设置核心组件
func (s *EdgeServer) SetComponents(videoSync *VideoSyncEngine, broadcaster *EdgeBroadcaster, consumer *Consumer) {
	s.videoSync = videoSync
	s.broadcaster = broadcaster
	s.consumer = consumer
}

// Start 启动服务器
func (s *EdgeServer) Start(ctx context.Context) error {
	// 初始化视频同步和广播器
	if s.videoSync == nil {
		s.videoSync = NewVideoSyncEngine(s.roomMap, s.sessionManager)
	}
	if s.broadcaster == nil {
		s.broadcaster = NewEdgeBroadcaster(s.sessionManager, s.videoSync, 50*time.Millisecond, 1000)
	}
	s.videoSync.Start()
	s.broadcaster.StartBatchTicker()

	log.Printf("EdgeServer %s starting on %s", s.nodeId, s.wsAddr)

	// 启动 gnet
	return gnet.Run(s, s.wsAddr,
		gnet.WithMulticore(true),
		gnet.WithReusePort(true),
	)
}

// Stop 停止服务器
func (s *EdgeServer) Stop() {
	if s.videoSync != nil {
		s.videoSync.Stop()
	}
	if s.broadcaster != nil {
		s.broadcaster.Stop()
	}
	if s.consumer != nil {
		s.consumer.Stop()
	}
}

// OnBoot 实现 gnet.EventHandler
func (s *EdgeServer) OnBoot(eng gnet.Engine) gnet.Action {
	s.engine = eng
	return gnet.None
}

// OnShutdown 实现 gnet.EventHandler
func (s *EdgeServer) OnShutdown(eng gnet.Engine) {
	s.Stop()
}

// OnOpen 实现 gnet.EventHandler
func (s *EdgeServer) OnOpen(c gnet.Conn) ([]byte, gnet.Action) {
	connID := fmt.Sprintf("%s-%d", s.nodeId, time.Now().UnixNano())
	ctx := &connctx.ConnContext{
		ConnID: connID,
	}
	ctx.LastActiveTime.Store(time.Now().Unix())
	c.SetContext(ctx)

	s.mu.Lock()
	s.conns[connID] = c
	s.mu.Unlock()

	return nil, gnet.None
}

// OnClose 实现 gnet.EventHandler
func (s *EdgeServer) OnClose(c gnet.Conn, err error) gnet.Action {
	ctx, ok := c.Context().(*connctx.ConnContext)
	if !ok || ctx == nil {
		return gnet.None
	}

	// 移除会话
	s.sessionManager.RemoveSession(ctx.UserID)
	if ctx.RoomID > 0 {
		s.roomMap.RemoveSession(ctx.RoomID, ctx.UserID)
	}

	s.mu.Lock()
	delete(s.conns, ctx.ConnID)
	s.mu.Unlock()

	return gnet.None
}

// OnTraffic 实现 gnet.EventHandler
func (s *EdgeServer) OnTraffic(c gnet.Conn) gnet.Action {
	ctx, ok := c.Context().(*connctx.ConnContext)
	if !ok || ctx == nil {
		return gnet.Close
	}

	ctx.LastActiveTime.Store(time.Now().Unix())

	// WebSocket 握手
	if !ctx.HandshakeDone {
		return s.handleHandshake(c, ctx)
	}

	return s.handleWebSocketTraffic(c, ctx)
}

// OnTick 实现 gnet.EventHandler (必须实现)
func (s *EdgeServer) OnTick() (time.Duration, gnet.Action) {
	return time.Second, gnet.None
}

// handleHandshake 处理 WebSocket 握手
func (s *EdgeServer) handleHandshake(c gnet.Conn, ctx *connctx.ConnContext) gnet.Action {
	data, err := c.Next(-1)
	if err != nil {
		return gnet.Close
	}

	ctx.HandshakeBuffer = append(ctx.HandshakeBuffer, data...)

	if len(ctx.HandshakeBuffer) > 4096 {
		return gnet.Close
	}

	if !bytes.Contains(ctx.HandshakeBuffer, []byte("\r\n\r\n")) {
		return gnet.None
	}

	resp, err := server.BuildWebSocketHandshakeResponse(ctx.HandshakeBuffer)
	if err != nil {
		return gnet.Close
	}

	ctx.HandshakeDone = true
	ctx.HandshakeBuffer = nil

	_ = c.AsyncWrite(resp, nil)
	return gnet.None
}

// handleWebSocketTraffic 处理 WebSocket 数据帧
func (s *EdgeServer) handleWebSocketTraffic(c gnet.Conn, ctx *connctx.ConnContext) gnet.Action {
	data, err := c.Next(-1)
	if err != nil {
		return gnet.Close
	}

	ctx.ReadBuffer = append(ctx.ReadBuffer, data...)

	if len(ctx.ReadBuffer) > 130*1024 {
		return gnet.Close
	}

	frames, consumed, err := server.DecodeWebSocketFrames(ctx.ReadBuffer)
	if err != nil {
		return gnet.Close
	}

	if consumed > 0 {
		remaining := append([]byte(nil), ctx.ReadBuffer[consumed:]...)
		ctx.ReadBuffer = remaining
	}

	for _, frame := range frames {
		switch frame.Opcode {
		case ws.OpPing:
			_ = c.AsyncWrite(server.BuildPongFrame(frame.Payload), nil)

		case ws.OpClose:
			var closeCode uint16 = 1000
			if len(frame.Payload) >= 2 {
				closeCode = binary.BigEndian.Uint16(frame.Payload[:2])
			}
			respPayload := make([]byte, 2)
			binary.BigEndian.PutUint16(respPayload, closeCode)
			_ = c.AsyncWrite(server.BuildCloseFrame(respPayload), nil)
			return gnet.Close

		case ws.OpBinary:
			task := &dispatcher.InboundTask{
				Conn: c,
				Ctx:  ctx,
				Data: frame.Payload,
			}

			err = s.pool.Submit(func() {
				s.handleInbound(task)
			})
			if err != nil {
				continue
			}
		}
	}

	return gnet.None
}

// handleInbound 处理入站消息
func (s *EdgeServer) handleInbound(task *dispatcher.InboundTask) {
	frame := &pb.Frame{}
	if err := proto.Unmarshal(task.Data, frame); err != nil {
		return
	}

	switch frame.GetOp() {
	case pb.OpType_OP_JOIN_ROOM:
		s.handleJoinRoom(task.Conn, task.Ctx, frame)
	case pb.OpType_OP_CHAT:
		s.handleChat(task.Conn, task.Ctx, frame)
	case pb.OpType_OP_HEARTBEAT:
		s.handleHeartbeat(task.Conn, task.Ctx, frame)
	}
}

// handleJoinRoom 处理加入房间
func (s *EdgeServer) handleJoinRoom(c gnet.Conn, ctx *connctx.ConnContext, frame *pb.Frame) {
	req := &pb.JoinRoomReq{}
	if err := proto.Unmarshal(frame.GetPayload(), req); err != nil {
		return
	}

	ctx.UserID = req.GetUserId()
	ctx.RoomID = req.GetRoomId()
	ctx.IsPremium = req.GetIsPremium()

	// 创建会话
	session := s.sessionManager.CreateSession(req.GetUserId(), req.GetRoomId(), ctx.ConnID, ctx.LoopIdx, req.GetIsPremium())

	// 添加到房间
	s.roomMap.AddSession(req.GetRoomId(), session)

	log.Printf("[EdgeServer] User %d joined room %d", req.GetUserId(), req.GetRoomId())
}

// handleChat 处理聊天/弹幕消息
func (s *EdgeServer) handleChat(c gnet.Conn, ctx *connctx.ConnContext, frame *pb.Frame) {
	if ctx.RoomID == 0 || ctx.UserID == 0 {
		return
	}

	chatMsg := &pb.ChatMsg{}
	if err := proto.Unmarshal(frame.GetPayload(), chatMsg); err != nil {
		return
	}

	// 构建弹幕
	videoTS := frame.GetTimestamp() // 客户端传入的视频时间戳
	d := &common.Danmaku{
		RoomId:       ctx.RoomID,
		UserId:       ctx.UserID,
		SenderConnId: ctx.ConnID,
		VideoTS:      videoTS,
		SendTS:       time.Now().UnixMilli(),
		Payload:      []byte(chatMsg.GetContent()),
		IsPremium:    ctx.IsPremium,
	}

	// 广播给房间内所有用户
	s.broadcaster.BroadcastLocal(d)
}

// handleHeartbeat 处理心跳
func (s *EdgeServer) handleHeartbeat(c gnet.Conn, ctx *connctx.ConnContext, frame *pb.Frame) {
	// 更新最后活跃时间
	ctx.LastActiveTime.Store(time.Now().Unix())
}

// SendToConn 发送消息到连接 (供外部调用)
func (s *EdgeServer) SendToConn(connId string, data []byte) error {
	s.mu.RLock()
	c, ok := s.conns[connId]
	s.mu.RUnlock()

	if !ok {
		return fmt.Errorf("connection not found: %s", connId)
	}

	return c.AsyncWrite(data, nil)
}

// BroadcastToRoom 在房间内广播 (供外部调用)
func (s *EdgeServer) BroadcastToRoom(roomId int64, data []byte) error {
	sessions := s.roomMap.GetSessions(roomId)
	for _, session := range sessions {
		if err := s.SendToConn(session.ConnId, data); err != nil {
			log.Printf("Send to conn %s failed: %v", session.ConnId, err)
		}
	}
	return nil
}

// GetStats 获取统计信息
func (s *EdgeServer) GetStats() map[string]interface{} {
	return map[string]interface{}{
		"node_id":         s.nodeId,
		"total_conns":    len(s.conns),
		"total_rooms":    s.roomMap.GetTotalRooms(),
		"total_sessions": s.sessionManager.GetTotalSessionCount(),
	}
}
