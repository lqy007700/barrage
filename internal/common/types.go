package common

import (
	"sync"
	"sync/atomic"
	"time"
)

// Danmaku 弹幕消息 (用于 Edge 节点内部传递)
type Danmaku struct {
	RoomId         int64  // 房间ID
	UserId         int64  // 发送者用户ID
	SenderConnId   string // 发送者连接ID (用于防回显)
	VideoTS        int64  // 视频时间戳 (毫秒) - 弹幕对应的视频时刻
	SendTS         int64  // 发送时间戳 (毫秒Unix时间)
	Payload        []byte // 实际下发的数据
	IsPremium      bool   // 是否VIP
}

// NewDanmaku 创建弹幕
func NewDanmaku(roomId, userId int64, senderConnId string, videoTS int64, payload []byte, isPremium bool) *Danmaku {
	return &Danmaku{
		RoomId:       roomId,
		UserId:       userId,
		SenderConnId: senderConnId,
		VideoTS:      videoTS,
		SendTS:       time.Now().UnixMilli(),
		Payload:      payload,
		IsPremium:    isPremium,
	}
}

// VideoTSUpdate 视频时间戳更新 (用于视频同步)
type VideoTSUpdate struct {
	RoomId   int64 // 房间ID
	VideoTS  int64 // 当前视频时间戳 (毫秒)
	SentAt   int64 // 墙钟发送时间 (毫秒Unix时间)
}

// UserSession 用户会话 (Edge节点)
type UserSession struct {
	UserId     int64
	RoomId     int64
	ConnId     string
	LoopIdx    int
	IsPremium  bool
	VideoTS    int64  // 用户当前播放的视频时间戳
	DelayDelta int64  // 用户延迟差 = 用户视频延迟 - 标准延迟 (可能为负)
	JoinedAt   int64  // 加入时间

	mu      sync.Mutex
	buffer  []*Danmaku // 待展示弹幕缓冲区
}

// NewUserSession 创建用户会话
func NewUserSession(userId, roomId int64, connId string, loopIdx int, isPremium bool) *UserSession {
	return &UserSession{
		UserId:     userId,
		RoomId:     roomId,
		ConnId:     connId,
		LoopIdx:    loopIdx,
		IsPremium:  isPremium,
		VideoTS:    0,
		DelayDelta: 0,
		JoinedAt:   time.Now().UnixMilli(),
		buffer:     make([]*Danmaku, 0, 64),
	}
}

// OnDanmaku 收到弹幕，加入缓冲区
func (s *UserSession) OnDanmaku(d *Danmaku) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.buffer = append(s.buffer, d)
}

// FlushDueDanmaku 释放到期的弹幕
// visibleTS: 用户当前可见的视频时间戳
// 返回可以下发的弹幕列表
func (s *UserSession) FlushDueDanmaku(visibleTS int64) []*Danmaku {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.buffer) == 0 {
		return nil
	}

	var due []*Danmaku
	var remaining []*Danmaku

	for _, d := range s.buffer {
		// 弹幕对应的视频时刻 <= 用户可见的视频时刻，说明视频已过
		if d.VideoTS <= visibleTS {
			due = append(due, d)
		} else {
			remaining = append(remaining, d)
		}
	}

	s.buffer = remaining
	return due
}

// BufferLen 返回缓冲区长度
func (s *UserSession) BufferLen() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.buffer)
}

// RoomInfo 房间信息 (Edge节点维护)
type RoomInfo struct {
	RoomId       int64
	CurrentVideoTS int64  // 当前视频时间戳
	LastUpdateAt  int64    // 最后更新时间
	ConnCount    int32     // 连接数 (原子)
}

// IncConnCount 增加连接数
func (r *RoomInfo) IncConnCount() {
	atomic.AddInt32(&r.ConnCount, 1)
}

// DecConnCount 减少连接数
func (r *RoomInfo) DecConnCount() {
	atomic.AddInt32(&r.ConnCount, -1)
}

// GetConnCount 获取连接数
func (r *RoomInfo) GetConnCount() int32 {
	return atomic.LoadInt32(&r.ConnCount)
}

// EdgeInfo Edge节点信息 (注册中心使用)
type EdgeInfo struct {
	NodeId    string    // 节点ID (如 "edge-01")
	Addr      string    // 连接地址
	ConnCount int32     // 当前连接数
	UpdatedAt int64     // 更新时间
}

// IsHot 判断房间是否为热点
type HotLevel int

const (
	HotLevelNormal HotLevel = iota
	HotLevelWarm              // 温热 1000-5000用户
	HotLevelHot               // 热点 5000-20000用户
	HotLevelSuper             // 超级热点 20000+用户
)

// HotLevelFromCount 根据连接数判断热点级别
func HotLevelFromCount(count int32) HotLevel {
	switch {
	case count >= 20000:
		return HotLevelSuper
	case count >= 5000:
		return HotLevelHot
	case count >= 1000:
		return HotLevelWarm
	default:
		return HotLevelNormal
	}
}
