package edge

import (
	"log"
	"sync"
	"time"

	"barrage/internal/common"
)

// VideoSyncEngine 视频时间戳同步引擎
// 负责：1) 接收视频时间戳更新 2) 定期检查用户缓冲区，释放到期弹幕
type VideoSyncEngine struct {
	// 配置
	videoDelayMs    int64  // 视频延迟毫秒
	syncIntervalMs  int64  // 同步检查间隔毫秒
	maxBufferSize   int    // 单用户最大缓冲数

	// 依赖
	roomMap         *RoomMap
	sessionManager  *SessionManager

	// 内部
	mu              sync.RWMutex
	stopCh          chan struct{}
	wg              sync.WaitGroup
}

// NewVideoSyncEngine 创建视频同步引擎
func NewVideoSyncEngine(roomMap *RoomMap, sessionManager *SessionManager) *VideoSyncEngine {
	return &VideoSyncEngine{
		videoDelayMs:    common.DefaultVideoDelayMs,
		syncIntervalMs:  common.VideoSyncIntervalMs,
		maxBufferSize:   common.MaxBufferPerSession,
		roomMap:         roomMap,
		sessionManager:  sessionManager,
		stopCh:          make(chan struct{}),
	}
}

// Start 启动引擎
func (e *VideoSyncEngine) Start() {
	e.wg.Add(1)
	go e.run()
	log.Printf("VideoSyncEngine started (delay=%dms, interval=%dms)", e.videoDelayMs, e.syncIntervalMs)
}

// run 主循环
func (e *VideoSyncEngine) run() {
	defer e.wg.Done()

	ticker := time.NewTicker(time.Duration(e.syncIntervalMs) * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-e.stopCh:
			log.Printf("VideoSyncEngine stopped")
			return
		case <-ticker.C:
			e.sync()
		}
	}
}

// sync 同步检查
func (e *VideoSyncEngine) sync() {
	rooms := e.roomMap.GetAllRooms()

	for _, room := range rooms {
		roomVideoTS := room.CurrentVideoTS
		if roomVideoTS <= 0 {
			continue
		}

		// 获取房间所有会话
		sessions := e.roomMap.GetSessions(room.RoomId)

		for _, session := range sessions {
			// 计算用户可见的视频时间戳
			// 用户可见TS = 房间视频TS - 视频延迟 + 用户延迟差
			userVisibleTS := roomVideoTS - e.videoDelayMs + session.DelayDelta

			// 释放到期的弹幕
			dueDanmakus := session.FlushDueDanmaku(userVisibleTS)
			if len(dueDanmakus) > 0 {
				// TODO: 调用 broadcaster 下发这些弹幕
				e.onDueDanmakus(session, dueDanmakus)
			}

			// 检查缓冲区溢出
			if session.BufferLen() > e.maxBufferSize {
				// 丢弃最旧的弹幕
				e.dropOverflow(session)
			}
		}
	}
}

// onDueDanmakus 处理到期的弹幕
func (e *VideoSyncEngine) onDueDanmakus(session *common.UserSession, danmakus []*common.Danmaku) {
	// 这里会调用 Broadcaster 实际下发
	// 具体实现需要在 Edge 节点的主结构中注入 broadcaster
}

// UpdateVideoTS 更新视频时间戳
func (e *VideoSyncEngine) UpdateVideoTS(roomId int64, videoTS int64) {
	e.roomMap.UpdateVideoTS(roomId, videoTS)
}

// SetVideoDelay 设置视频延迟
func (e *VideoSyncEngine) SetVideoDelay(delayMs int64) {
	if delayMs > 0 && delayMs <= common.MaxVideoDelayMs {
		e.videoDelayMs = delayMs
	}
}

// SetUserDelayDelta 设置用户延迟差
func (e *VideoSyncEngine) SetUserDelayDelta(userId int64, deltaMs int64) {
	session, ok := e.sessionManager.GetSession(userId)
	if !ok {
		return
	}
	session.DelayDelta = deltaMs
}

// dropOverflow 丢弃缓冲区溢出的最旧弹幕
func (e *VideoSyncEngine) dropOverflow(session *common.UserSession) {
	dropped := session.DropOverflow(e.maxBufferSize)
	if dropped > 0 {
		log.Printf("Dropped %d overflow danmakus for user %d", dropped, session.UserId)
	}
}

// Stop 停止引擎
func (e *VideoSyncEngine) Stop() {
	close(e.stopCh)
	e.wg.Wait()
}
