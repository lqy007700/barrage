package gateway

import (
	"sync"
	"sync/atomic"
	"time"

	"barrage/internal/common"
)

// HotDetector 热点检测器
// 统计每个房间的消息速率，判断热点级别
type HotDetector struct {
	mu sync.RWMutex

	// roomId → *RoomHotInfo
	rooms map[int64]*RoomHotInfo

	// 清理间隔
	cleanupInterval time.Duration

	// 热点阈值配置
	hotThreshold   int32 // 热点阈值 (消息数/秒)
	warmThreshold  int32 // 温热阈值
	superThreshold int32 // 超级热点阈值

	stopCh chan struct{}
}

// RoomHotInfo 房间热点信息
type RoomHotInfo struct {
	RoomId       int64
	MsgCount     int64  // 时间窗口内消息数
	WindowStart  int64  // 窗口开始时间 (Unix毫秒)
	Level        common.HotLevel // 当前热点级别
	SampleRate   float32 // 当前采样率
	UpdateAt     int64   // 更新时间
}

// NewHotDetector 创建热点检测器
func NewHotDetector() *HotDetector {
	h := &HotDetector{
		rooms:           make(map[int64]*RoomHotInfo),
		cleanupInterval: 5 * time.Minute,
		hotThreshold:    500,   // 500 msg/sec
		warmThreshold:   100,   // 100 msg/sec
		superThreshold:  2000,  // 2000 msg/sec
		stopCh:         make(chan struct{}),
	}
	go h.cleanupLoop()
	return h
}

// cleanupLoop 定期清理过期的房间热点信息
func (h *HotDetector) cleanupLoop() {
	ticker := time.NewTicker(h.cleanupInterval)
	defer ticker.Stop()
	for {
		select {
		case <-h.stopCh:
			return
		case <-ticker.C:
			h.cleanup()
		}
	}
}

// cleanup 清理过期数据
func (h *HotDetector) cleanup() {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := time.Now().UnixMilli()
	for roomId, info := range h.rooms {
		// 超过10分钟没有更新则删除
		if now-info.UpdateAt > 10*60*1000 {
			delete(h.rooms, roomId)
		}
	}
}

// RecordMessage 记录一条消息
func (h *HotDetector) RecordMessage(roomId int64) {
	info := h.getOrCreate(roomId)
	atomic.AddInt64(&info.MsgCount, 1)
	info.UpdateAt = time.Now().UnixMilli()
}

// RecordMessages 批量记录消息
func (h *HotDetector) RecordMessages(roomId int64, count int) {
	info := h.getOrCreate(roomId)
	atomic.AddInt64(&info.MsgCount, int64(count))
	info.UpdateAt = time.Now().UnixMilli()
}

// GetHotLevel 获取房间热点级别
func (h *HotDetector) GetHotLevel(roomId int64) common.HotLevel {
	info := h.getRoomInfo(roomId)
	if info == nil {
		return common.HotLevelNormal
	}
	h.updateLevel(info)
	return info.Level
}

// GetSampleRate 获取房间采样率
func (h *HotDetector) GetSampleRate(roomId int64) float32 {
	info := h.getRoomInfo(roomId)
	if info == nil {
		return 1.0
	}
	h.updateLevel(info)
	return info.SampleRate
}

// ShouldSample 判断消息是否应该采样丢弃
func (h *HotDetector) ShouldSample(roomId int64) bool {
	level := h.GetHotLevel(roomId)
	if level == common.HotLevelNormal {
		return false
	}
	// 随机采样
	cfg := common.GetHotConfig(int32(level))
	return float32(time.Now().UnixNano()%100)/100.0 > cfg.SampleRate
}

// GetRoomInfo 获取房间热点信息
func (h *HotDetector) getRoomInfo(roomId int64) *RoomHotInfo {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.rooms[roomId]
}

// getOrCreate 获取或创建房间热点信息
func (h *HotDetector) getOrCreate(roomId int64) *RoomHotInfo {
	// 先快速查询
	h.mu.RLock()
	info, ok := h.rooms[roomId]
	h.mu.RUnlock()
	if ok {
		return info
	}

	// 创建新的
	h.mu.Lock()
	defer h.mu.Unlock()
	// 双重检查
	if info, ok := h.rooms[roomId]; ok {
		return info
	}
	info = &RoomHotInfo{
		RoomId:      roomId,
		MsgCount:    0,
		WindowStart: time.Now().UnixMilli(),
		Level:       common.HotLevelNormal,
		SampleRate:  1.0,
		UpdateAt:    time.Now().UnixMilli(),
	}
	h.rooms[roomId] = info
	return info
}

// updateLevel 更新热点级别
func (h *HotDetector) updateLevel(info *RoomHotInfo) {
	now := time.Now().UnixMilli()
	windowMs := now - info.WindowStart

	// 每秒计算
	if windowMs >= 1000 {
		msgPerSec := float64(atomic.LoadInt64(&info.MsgCount)) * 1000 / float64(windowMs)

		// 根据消息速率判断级别
		switch {
		case int32(msgPerSec) >= h.superThreshold:
			info.Level = common.HotLevelSuper
			info.SampleRate = common.SamplerRateHotLevelSuper
		case int32(msgPerSec) >= h.hotThreshold:
			info.Level = common.HotLevelHot
			info.SampleRate = common.SamplerRateHotLevelHot
		case int32(msgPerSec) >= h.warmThreshold:
			info.Level = common.HotLevelWarm
			info.SampleRate = common.SamplerRateHotLevelWarm
		default:
			info.Level = common.HotLevelNormal
			info.SampleRate = common.SamplerRateNormal
		}

		// 重置窗口
		atomic.StoreInt64(&info.MsgCount, 0)
		info.WindowStart = now
	}
}

// Stop 停止检测器
func (h *HotDetector) Stop() {
	close(h.stopCh)
}

// GetStats 获取统计数据
func (h *HotDetector) GetStats() (hotCount, warmCount, normalCount int) {
	h.mu.RLock()
	defer h.mu.RUnlock()
	for _, info := range h.rooms {
		switch info.Level {
		case common.HotLevelSuper, common.HotLevelHot:
			hotCount++
		case common.HotLevelWarm:
			warmCount++
		default:
			normalCount++
		}
	}
	return
}
