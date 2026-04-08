package edge

import (
	"sync"
	"sync/atomic"
	"time"

	"barrage/internal/common"
)

// RoomMap 房间用户映射 (Edge节点本地)
// 管理房间到用户的映射关系，用于快速定位房间内所有用户
type RoomMap struct {
	mu sync.RWMutex

	// roomId → *RoomInfo
	rooms map[int64]*RoomInfo

	// 统计
	stats struct {
		totalRooms int64
		totalConns int64
	}
}

// RoomInfo 房间信息
type RoomInfo struct {
	RoomId         int64
	CurrentVideoTS int64                 // 当前视频时间戳
	LastUpdateAt   int64                 // 最后更新时间
	connCount      int32                 // 连接数 (原子)
	mu             sync.RWMutex          // 保护 sessions
	sessions       map[int64]*common.UserSession // userId → session
}

// NewRoomMap 创建房间映射
func NewRoomMap() *RoomMap {
	return &RoomMap{
		rooms: make(map[int64]*RoomInfo),
	}
}

// GetOrCreate 获取或创建房间
func (m *RoomMap) GetOrCreate(roomId int64) *RoomInfo {
	m.mu.Lock()
	defer m.mu.Unlock()

	if room, ok := m.rooms[roomId]; ok {
		return room
	}

	room := &RoomInfo{
		RoomId:   roomId,
		sessions: make(map[int64]*common.UserSession),
	}
	m.rooms[roomId] = room
	atomic.AddInt64(&m.stats.totalRooms, 1)

	return room
}

// AddSession 添加会话到房间
func (m *RoomMap) AddSession(roomId int64, session *common.UserSession) {
	room := m.GetOrCreate(roomId)

	room.mu.Lock()
	defer room.mu.Unlock()

	room.sessions[session.UserId] = session
	room.LastUpdateAt = session.JoinedAt
	atomic.AddInt32(&room.connCount, 1)
	atomic.AddInt64(&m.stats.totalConns, 1)
}

// RemoveSession 从房间移除会话
func (m *RoomMap) RemoveSession(roomId int64, userId int64) {
	m.mu.RLock()
	room, ok := m.rooms[roomId]
	m.mu.RUnlock()

	if !ok {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()

	if _, exists := room.sessions[userId]; exists {
		delete(room.sessions, userId)
		atomic.AddInt32(&room.connCount, -1)
		atomic.AddInt64(&m.stats.totalConns, -1)
	}
}

// GetSessions 获取房间所有会话
func (m *RoomMap) GetSessions(roomId int64) []*common.UserSession {
	m.mu.RLock()
	room, ok := m.rooms[roomId]
	m.mu.RUnlock()

	if !ok {
		return nil
	}

	room.mu.RLock()
	defer room.mu.RUnlock()

	result := make([]*common.UserSession, 0, len(room.sessions))
	for _, session := range room.sessions {
		result = append(result, session)
	}
	return result
}

// GetConnCount 获取房间连接数
func (m *RoomMap) GetConnCount(roomId int64) int32 {
	m.mu.RLock()
	room, ok := m.rooms[roomId]
	m.mu.RUnlock()

	if !ok {
		return 0
	}

	return atomic.LoadInt32(&room.connCount)
}

// GetHotLevel 获取房间热点级别
func (m *RoomMap) GetHotLevel(roomId int64) common.HotLevel {
	return common.HotLevelFromCount(m.GetConnCount(roomId))
}

// UpdateVideoTS 更新房间视频时间戳
func (m *RoomMap) UpdateVideoTS(roomId int64, videoTS int64) {
	m.mu.RLock()
	room, ok := m.rooms[roomId]
	m.mu.RUnlock()

	if !ok {
		return
	}

	room.mu.Lock()
	defer room.mu.Unlock()
	room.CurrentVideoTS = videoTS
	room.LastUpdateAt = time.Now().UnixMilli()
}

// GetVideoTS 获取房间视频时间戳
func (m *RoomMap) GetVideoTS(roomId int64) int64 {
	m.mu.RLock()
	room, ok := m.rooms[roomId]
	m.mu.RUnlock()

	if !ok {
		return 0
	}

	return atomic.LoadInt64(&room.CurrentVideoTS)
}

// GetTotalRooms 获取房间总数
func (m *RoomMap) GetTotalRooms() int64 {
	return atomic.LoadInt64(&m.stats.totalRooms)
}

// GetTotalConns 获取连接总数
func (m *RoomMap) GetTotalConns() int64 {
	return atomic.LoadInt64(&m.stats.totalConns)
}

// GetAllRooms 获取所有房间
func (m *RoomMap) GetAllRooms() []*RoomInfo {
	m.mu.RLock()
	defer m.mu.RUnlock()

	result := make([]*RoomInfo, 0, len(m.rooms))
	for _, room := range m.rooms {
		result = append(result, room)
	}
	return result
}
