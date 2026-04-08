package edge

import (
	"log"
	"sync"
	"time"

	"barrage/internal/common"
	"barrage/internal/metrics"
	"barrage/internal/protocol/ws"
)

const shardCount = 64

// EdgeBroadcaster Edge节点的广播器
// 负责将弹幕本地广播给连接的用户
type EdgeBroadcaster struct {
	// 组件
	sessionManager *SessionManager
	videoSync     *VideoSyncEngine

	// 分片 map，按 roomId % shardCount 分桶，减少锁竞争
	shards [shardCount]struct {
		mu      sync.Mutex
		pending map[int64][]*common.Danmaku
	}

	// 配置
	batchInterval   time.Duration
	maxPendingPerRoom int

	// 控制
	stopCh chan struct{}
}

// NewEdgeBroadcaster 创建Edge广播器
func NewEdgeBroadcaster(sessionManager *SessionManager, videoSync *VideoSyncEngine, batchInterval time.Duration, maxPendingPerRoom int) *EdgeBroadcaster {
	if maxPendingPerRoom <= 0 {
		maxPendingPerRoom = common.MaxPendingPerRoom
	}

	b := &EdgeBroadcaster{
		sessionManager:   sessionManager,
		videoSync:       videoSync,
		batchInterval:   batchInterval,
		maxPendingPerRoom: maxPendingPerRoom,
		stopCh:         make(chan struct{}),
	}

	// 初始化分片
	for i := range b.shards {
		b.shards[i].pending = make(map[int64][]*common.Danmaku)
	}

	return b
}

// BroadcastLocal 本地广播弹幕
func (b *EdgeBroadcaster) BroadcastLocal(danmaku *common.Danmaku) error {
	if danmaku == nil {
		return nil
	}

	// 0 延迟退化为直接同步派发策略
	if b.batchInterval <= 0 {
		b.broadcastNow(danmaku)
		return nil
	}

	// 加入积压蓄水池
	shardIdx := danmaku.RoomId % shardCount
	shard := &b.shards[shardIdx]

	shard.mu.Lock()
	shard.pending[danmaku.RoomId] = append(shard.pending[danmaku.RoomId], danmaku)
	if len(shard.pending[danmaku.RoomId]) > b.maxPendingPerRoom {
		dropped := len(shard.pending[danmaku.RoomId]) - b.maxPendingPerRoom
		shard.pending[danmaku.RoomId] = shard.pending[danmaku.RoomId][dropped:]
		metrics.SlowConnSkipCount.Add(int64(dropped))
		log.Printf("pending 队列超限，丢弃 %d 条最旧消息: roomID=%d", dropped, danmaku.RoomId)
	}
	shard.mu.Unlock()

	return nil
}

// broadcastNow 立即广播
func (b *EdgeBroadcaster) broadcastNow(danmaku *common.Danmaku) {
	roomId := danmaku.RoomId
	sessions := b.sessionManager.GetRoomSessions(roomId)

	for _, session := range sessions {
		// 防回显
		if danmaku.SenderConnId != "" && danmaku.SenderConnId == session.ConnId {
			continue
		}

		// TODO: 实际下发到连接
		// 需要通过 gnet.Conn 的 AsyncWrite 发送
	}
}

// StartBatchTicker 启动批次定时器
func (b *EdgeBroadcaster) StartBatchTicker() {
	if b.batchInterval <= 0 {
		return
	}

	go func() {
		ticker := time.NewTicker(b.batchInterval)
		defer ticker.Stop()

		for {
			select {
			case <-b.stopCh:
				b.flushAll()
				log.Printf("EdgeBroadcaster stopped")
				return
			case <-ticker.C:
				b.flushAll()
			}
		}
	}()
}

// flushAll 刷新所有分片
func (b *EdgeBroadcaster) flushAll() {
	for i := range b.shards {
		shard := &b.shards[i]
		shard.mu.Lock()
		flushMap := shard.pending
		shard.pending = make(map[int64][]*common.Danmaku)
		shard.mu.Unlock()

		for roomId, danmakus := range flushMap {
			b.broadcastBatch(roomId, danmakus)
		}
	}
}

// broadcastBatch 批量广播
func (b *EdgeBroadcaster) broadcastBatch(roomId int64, danmakus []*common.Danmaku) {
	if len(danmakus) == 0 {
		return
	}

	sessions := b.sessionManager.GetRoomSessions(roomId)
	if len(sessions) == 0 {
		return
	}

	// 构建 MegaFrame
	var megaFrame []byte
	for _, d := range danmakus {
		megaFrame = append(megaFrame, ws.BuildBinaryFrame(d.Payload)...)
	}

	// 广播给每个会话
	for _, session := range sessions {
		b.sendToSession(session, danmakus, megaFrame)
	}
}

// sendToSession 发送给单个会话
func (b *EdgeBroadcaster) sendToSession(session *common.UserSession, danmakus []*common.Danmaku, megaFrame []byte) {
	// TODO: 获取 gnet.Conn 并发送
	// 这里需要通过 LoopDispatcher 发送到对应的 event loop

	// 防回显检查
	hasEcho := false
	for _, d := range danmakus {
		if d.SenderConnId != "" && d.SenderConnId == session.ConnId {
			hasEcho = true
			break
		}
	}

	if !hasEcho {
		// 直接发送 megaFrame
		// _ = conn.AsyncWrite(megaFrame, nil)
	} else {
		// 需要过滤自身消息
		var personalFrame []byte
		for _, d := range danmakus {
			if d.SenderConnId != "" && d.SenderConnId == session.ConnId {
				continue
			}
			personalFrame = append(personalFrame, ws.BuildBinaryFrame(d.Payload)...)
		}
		if len(personalFrame) > 0 {
			// _ = conn.AsyncWrite(personalFrame, nil)
		}
	}
}

// Stop 停止广播器
func (b *EdgeBroadcaster) Stop() {
	close(b.stopCh)
}
