package edge

import (
	"sync"
	"sync/atomic"

	"barrage/internal/common"
)

// SessionManager 用户会话管理器
type SessionManager struct {
	mu sync.RWMutex

	// userId → *common.UserSession
	sessions map[int64]*common.UserSession

	// roomId → []*common.UserSession (房间内所有会话)
	roomSessions map[int64][]*common.UserSession

	// 统计
	stats struct {
		totalSessions int64
		activeCount   int64
	}
}

// NewSessionManager 创建会话管理器
func NewSessionManager() *SessionManager {
	return &SessionManager{
		sessions:     make(map[int64]*common.UserSession),
		roomSessions: make(map[int64][]*common.UserSession),
	}
}

// CreateSession 创建会话
func (m *SessionManager) CreateSession(userId, roomId int64, connId string, loopIdx int, isPremium bool) *common.UserSession {
	m.mu.Lock()
	defer m.mu.Unlock()

	// 检查是否已存在
	if existing, ok := m.sessions[userId]; ok {
		return existing
	}

	session := common.NewUserSession(userId, roomId, connId, loopIdx, isPremium)

	m.sessions[userId] = session
	m.roomSessions[roomId] = append(m.roomSessions[roomId], session)
	atomic.AddInt64(&m.stats.totalSessions, 1)
	atomic.AddInt64(&m.stats.activeCount, 1)

	return session
}

// GetSession 获取会话
func (m *SessionManager) GetSession(userId int64) (*common.UserSession, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	session, ok := m.sessions[userId]
	return session, ok
}

// RemoveSession 移除会话
func (m *SessionManager) RemoveSession(userId int64) {
	m.mu.Lock()
	defer m.mu.Unlock()

	session, ok := m.sessions[userId]
	if !ok {
		return
	}

	delete(m.sessions, userId)

	// 从房间会话列表中移除
	if sessions, ok := m.roomSessions[session.RoomId]; ok {
		newSessions := make([]*common.UserSession, 0, len(sessions))
		for _, s := range sessions {
			if s.UserId != userId {
				newSessions = append(newSessions, s)
			}
		}
		if len(newSessions) == 0 {
			delete(m.roomSessions, session.RoomId)
		} else {
			m.roomSessions[session.RoomId] = newSessions
		}
	}

	atomic.AddInt64(&m.stats.activeCount, -1)
}

// GetRoomSessions 获取房间所有会话
func (m *SessionManager) GetRoomSessions(roomId int64) []*common.UserSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	sessions := m.roomSessions[roomId]
	result := make([]*common.UserSession, len(sessions))
	copy(result, sessions)
	return result
}

// GetRoomSessionCount 获取房间会话数
func (m *SessionManager) GetRoomSessionCount(roomId int64) int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.roomSessions[roomId])
}

// GetTotalSessionCount 获取总会话数
func (m *SessionManager) GetTotalSessionCount() int64 {
	return atomic.LoadInt64(&m.stats.totalSessions)
}

// GetActiveSessionCount 获取活跃会话数
func (m *SessionManager) GetActiveSessionCount() int64 {
	return atomic.LoadInt64(&m.stats.activeCount)
}

// UpdateVideoTS 更新用户视频时间戳
func (m *SessionManager) UpdateVideoTS(userId int64, videoTS int64) {
	session, ok := m.GetSession(userId)
	if !ok {
		return
	}
	session.VideoTS = videoTS
}

// GetAllSessions 获取所有会话
func (m *SessionManager) GetAllSessions() []*common.UserSession {
	m.mu.RLock()
	defer m.mu.RUnlock()
	result := make([]*common.UserSession, 0, len(m.sessions))
	for _, session := range m.sessions {
		result = append(result, session)
	}
	return result
}
