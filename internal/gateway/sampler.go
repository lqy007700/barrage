package gateway

import (
	"math/rand"
	"sync"
	"sync/atomic"

	"barrage/internal/common"
)

// Sampler 采样器
// 根据房间热点级别决定消息是否采样丢弃
type Sampler struct {
	mu sync.RWMutex

	// roomId → *RoomSampler
	rooms map[int64]*RoomSampler

	// 默认采样率 (当没有热点信息时)
	defaultRate float32
}

// RoomSampler 房间采样器
type RoomSampler struct {
	RoomId     int64
	SampleRate float32  // 当前采样率
	Level      common.HotLevel
	DropCount  int64    // 丢弃计数
	PassCount  int64    // 通过计数
}

// NewSampler 创建采样器
func NewSampler() *Sampler {
	return &Sampler{
		rooms:       make(map[int64]*RoomSampler),
		defaultRate: 1.0,
	}
}

// UpdateRoom 更新房间采样配置
func (s *Sampler) UpdateRoom(roomId int64, level common.HotLevel) {
	s.mu.Lock()
	defer s.mu.Unlock()

	cfg := common.GetHotConfig(int32(level))
	if s.rooms[roomId] == nil {
		s.rooms[roomId] = &RoomSampler{
			RoomId:     roomId,
			SampleRate: cfg.SampleRate,
			Level:      level,
		}
	} else {
		s.rooms[roomId].SampleRate = cfg.SampleRate
		s.rooms[roomId].Level = level
	}
}

// ShouldDrop 判断消息是否应该丢弃
func (s *Sampler) ShouldDrop(roomId int64) bool {
	s.mu.RLock()
	room, ok := s.rooms[roomId]
	s.mu.RUnlock()

	if !ok {
		return false // 默认放行
	}

	// 普通房间不采样
	if room.Level == common.HotLevelNormal {
		return false
	}

	// 基于概率采样
	drop := float32(rand.Intn(100))/100.0 > room.SampleRate

	if drop {
		atomic.AddInt64(&room.DropCount, 1)
	} else {
		atomic.AddInt64(&room.PassCount, 1)
	}

	return drop
}

// ShouldDropWithLevel 根据热点级别判断是否丢弃
func (s *Sampler) ShouldDropWithLevel(roomId int64, level common.HotLevel) bool {
	if level == common.HotLevelNormal {
		return false
	}

	cfg := common.GetHotConfig(int32(level))
	drop := float32(rand.Intn(100))/100.0 > cfg.SampleRate

	s.mu.RLock()
	room, ok := s.rooms[roomId]
	s.mu.RUnlock()

	if ok {
		if drop {
			atomic.AddInt64(&room.DropCount, 1)
		} else {
			atomic.AddInt64(&room.PassCount, 1)
		}
	}

	return drop
}

// GetStats 获取采样统计
func (s *Sampler) GetStats(roomId int64) (pass, drop int64, level common.HotLevel) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if room, ok := s.rooms[roomId]; ok {
		pass = atomic.LoadInt64(&room.PassCount)
		drop = atomic.LoadInt64(&room.DropCount)
		level = room.Level
	}
	return
}

// GetDropRate 获取丢弃率
func (s *Sampler) GetDropRate(roomId int64) float32 {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if room, ok := s.rooms[roomId]; ok {
		total := atomic.LoadInt64(&room.PassCount) + atomic.LoadInt64(&room.DropCount)
		if total > 0 {
			return float32(atomic.LoadInt64(&room.DropCount)) / float32(total)
		}
	}
	return 0
}

// RemoveRoom 移除房间采样器
func (s *Sampler) RemoveRoom(roomId int64) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.rooms, roomId)
}

// GetAllStats 获取所有房间统计
func (s *Sampler) GetAllStats() map[int64]*RoomSampler {
	s.mu.RLock()
	defer s.mu.RUnlock()
	result := make(map[int64]*RoomSampler, len(s.rooms))
	for id, room := range s.rooms {
		result[id] = &RoomSampler{
			RoomId:     room.RoomId,
			SampleRate: room.SampleRate,
			Level:      room.Level,
			DropCount:  atomic.LoadInt64(&room.DropCount),
			PassCount:  atomic.LoadInt64(&room.PassCount),
		}
	}
	return result
}
