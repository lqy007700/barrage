package edge

import (
	"testing"
	"time"

	"barrage/internal/common"
)

func TestSessionManager(t *testing.T) {
	m := NewSessionManager()

	// 创建会话
	session := m.CreateSession(1, 100, "conn-1", 0, false)
	if session == nil {
		t.Fatal("CreateSession returned nil")
	}
	if session.UserId != 1 {
		t.Errorf("expected UserId 1, got %d", session.UserId)
	}
	if session.RoomId != 100 {
		t.Errorf("expected RoomId 100, got %d", session.RoomId)
	}

	// 获取会话
	s, ok := m.GetSession(1)
	if !ok {
		t.Fatal("GetSession failed")
	}
	if s.UserId != 1 {
		t.Errorf("expected UserId 1, got %d", s.UserId)
	}

	// 房间会话
	count := m.GetRoomSessionCount(100)
	if count != 1 {
		t.Errorf("expected room session count 1, got %d", count)
	}

	// 移除会话
	m.RemoveSession(1)
	_, ok = m.GetSession(1)
	if ok {
		t.Fatal("session should be removed")
	}
}

func TestSessionManagerMultipleSessions(t *testing.T) {
	m := NewSessionManager()

	// 多个用户加入同一房间
	for i := int64(1); i <= 10; i++ {
		m.CreateSession(i, 100, "conn-"+string(rune('0'+i)), 0, false)
	}

	count := m.GetRoomSessionCount(100)
	if count != 10 {
		t.Errorf("expected 10 sessions, got %d", count)
	}

	// 移除一个
	m.RemoveSession(5)
	count = m.GetRoomSessionCount(100)
	if count != 9 {
		t.Errorf("expected 9 sessions, got %d", count)
	}
}

func TestRoomMap(t *testing.T) {
	m := NewRoomMap()

	// 获取或创建房间
	room := m.GetOrCreate(100)
	if room == nil {
		t.Fatal("GetOrCreate returned nil")
	}
	if room.RoomId != 100 {
		t.Errorf("expected RoomId 100, got %d", room.RoomId)
	}

	// 重复获取返回同一房间
	room2 := m.GetOrCreate(100)
	if room != room2 {
		t.Error("GetOrCreate should return same room")
	}

	// 连接数
	count := m.GetConnCount(100)
	if count != 0 {
		t.Errorf("expected 0 connections, got %d", count)
	}

	// 热点级别
	level := m.GetHotLevel(100)
	if level != common.HotLevelNormal {
		t.Errorf("expected HotLevelNormal, got %v", level)
	}
}

func TestRoomMapVideoTS(t *testing.T) {
	m := NewRoomMap()
	m.GetOrCreate(100)

	// 更新视频时间戳
	m.UpdateVideoTS(100, 5000)
	ts := m.GetVideoTS(100)
	if ts != 5000 {
		t.Errorf("expected video TS 5000, got %d", ts)
	}

	// 更新
	m.UpdateVideoTS(100, 6000)
	ts = m.GetVideoTS(100)
	if ts != 6000 {
		t.Errorf("expected video TS 6000, got %d", ts)
	}
}

func TestUserSessionBuffer(t *testing.T) {
	session := common.NewUserSession(1, 100, "conn-1", 0, false)

	// 添加弹幕
	d1 := common.NewDanmaku(100, 1, "conn-1", 1000, []byte("hello"), false)
	d2 := common.NewDanmaku(100, 2, "conn-2", 1001, []byte("world"), false)

	session.OnDanmaku(d1)
	session.OnDanmaku(d2)

	if session.BufferLen() != 2 {
		t.Errorf("expected buffer len 2, got %d", session.BufferLen())
	}

	// 刷新到期的弹幕
	due := session.FlushDueDanmaku(1000)
	if len(due) != 1 {
		t.Errorf("expected 1 due danmaku, got %d", len(due))
	}
	if session.BufferLen() != 1 {
		t.Errorf("expected buffer len 1 after flush, got %d", session.BufferLen())
	}

	// 刷新剩余的
	due = session.FlushDueDanmaku(2000)
	if len(due) != 1 {
		t.Errorf("expected 1 due danmaku, got %d", len(due))
	}
	if session.BufferLen() != 0 {
		t.Errorf("expected buffer len 0, got %d", session.BufferLen())
	}
}

func TestUserSessionDropOverflow(t *testing.T) {
	session := common.NewUserSession(1, 100, "conn-1", 0, false)

	// 添加超过限制的弹幕
	for i := 0; i < 300; i++ {
		d := common.NewDanmaku(100, 1, "conn-1", int64(i), []byte("msg"), false)
		session.OnDanmaku(d)
	}

	// 缓冲区应该被限制
	maxSize := 200
	dropped := session.DropOverflow(maxSize)
	if dropped != 100 {
		t.Errorf("expected dropped 100, got %d", dropped)
	}
	if session.BufferLen() != 200 {
		t.Errorf("expected buffer len 200, got %d", session.BufferLen())
	}
}

func TestVideoSyncEngine(t *testing.T) {
	m := NewSessionManager()
	roomMap := NewRoomMap()
	engine := NewVideoSyncEngine(roomMap, m)

	// 创建房间和会话
	roomMap.GetOrCreate(100)
	session := m.CreateSession(1, 100, "conn-1", 0, false)

	// 更新视频时间戳
	engine.UpdateVideoTS(100, 3000)

	// 添加弹幕
	d := common.NewDanmaku(100, 1, "conn-1", 2900, []byte("test"), false)
	session.OnDanmaku(d)

	// 启动引擎并等待同步
	engine.Start()
	time.Sleep(100 * time.Millisecond)
	engine.Stop()
}
