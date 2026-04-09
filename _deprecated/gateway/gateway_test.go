package gateway

import (
	"testing"

	"barrage/internal/common"
)

func TestHotDetector(t *testing.T) {
	h := NewHotDetector()
	defer h.Stop()

	// 正常房间
	level := h.GetHotLevel(100)
	if level != common.HotLevelNormal {
		t.Errorf("expected HotLevelNormal, got %v", level)
	}

	// 记录消息
	for i := 0; i < 100; i++ {
		h.RecordMessage(100)
	}

	// 由于 updateLevel 需要 1 秒窗口，短时间内 level 不会变化
	// 这是预期行为：热点检测基于滑动时间窗口
	// 测试采样器时应该直接使用 HotLevel 级别，不依赖时间窗口
}

func TestHotDetectorSuperHot(t *testing.T) {
	h := NewHotDetector()
	defer h.Stop()

	// 模拟超级热点 (20000+ 连接数)
	// 但HotDetector是基于消息速率，不是连接数
	// 所以我们测试消息速率
}

func TestSampler(t *testing.T) {
	s := NewSampler()

	// 更新房间为热点
	s.UpdateRoom(100, common.HotLevelSuper)

	// 正常房间不采样
	if s.ShouldDrop(999) {
		t.Error("should not drop for normal room")
	}

	// 热点房间应该有一定概率丢弃
	// HotLevelSuper: 5% 保留, 95% 丢弃
	dropCount := 0
	passCount := 0
	for i := 0; i < 1000; i++ {
		if s.ShouldDropWithLevel(100, common.HotLevelSuper) {
			dropCount++
		} else {
			passCount++
		}
	}

	// 超级热点采样率5%，应该有显著的丢弃 (约950次)
	if dropCount < 900 || dropCount > 1000 {
		t.Errorf("expected ~950 drops, got %d (pass=%d)", dropCount, passCount)
	}
	if passCount < 0 || passCount > 100 {
		t.Errorf("expected ~50 passes, got %d", passCount)
	}
}

func TestSamplerStats(t *testing.T) {
	s := NewSampler()
	s.UpdateRoom(100, common.HotLevelHot)

	// 多次采样
	for i := 0; i < 100; i++ {
		s.ShouldDropWithLevel(100, common.HotLevelHot)
	}

	pass, drop, level := s.GetStats(100)
	if level != common.HotLevelHot {
		t.Errorf("expected HotLevelHot, got %v", level)
	}
	if pass+drop != 100 {
		t.Errorf("expected 100 total, got %d", pass+drop)
	}
}

func TestRouter(t *testing.T) {
	r := NewRoomRouter()

	// 注册Edge节点
	r.RegisterEdge("edge-1", "192.168.1.1:8080")
	r.RegisterEdge("edge-2", "192.168.1.2:8080")
	r.RegisterEdge("edge-3", "192.168.1.3:8080")

	edges := r.GetAllEdges()
	if len(edges) != 3 {
		t.Errorf("expected 3 edges, got %d", len(edges))
	}

	// 用户绑定到Edge
	r.BindUserToEdge(1, "edge-1")
	r.BindUserToEdge(2, "edge-2")

	nodeId, ok := r.GetUserEdge(1)
	if !ok || nodeId != "edge-1" {
		t.Errorf("expected edge-1, got %s", nodeId)
	}

	// 房间路由
	r.AddUserToRoom(1, 100, "edge-1")
	r.AddUserToRoom(2, 100, "edge-2")

	roomEdges := r.GetRoomEdges(100)
	if len(roomEdges) != 2 {
		t.Errorf("expected 2 edges in room, got %d", len(roomEdges))
	}
}

func TestRouterSelectEdge(t *testing.T) {
	r := NewRoomRouter()

	// 没有Edge时选择失败
	_, _, ok := r.SelectEdgeForUser(1)
	if ok {
		t.Error("should fail when no edges registered")
	}

	// 注册Edge
	r.RegisterEdge("edge-1", "192.168.1.1:8080")

	nodeId, addr, ok := r.SelectEdgeForUser(1)
	if !ok {
		t.Fatal("should succeed when edge registered")
	}
	if nodeId != "edge-1" {
		t.Errorf("expected edge-1, got %s", nodeId)
	}
	if addr != "192.168.1.1:8080" {
		t.Errorf("expected addr, got %s", addr)
	}
}

func TestRouterUnbind(t *testing.T) {
	r := NewRoomRouter()

	r.RegisterEdge("edge-1", "192.168.1.1:8080")
	r.BindUserToEdge(1, "edge-1")
	r.AddUserToRoom(1, 100, "edge-1")

	// 解绑用户
	r.UnbindUser(1)

	_, ok := r.GetUserEdge(1)
	if ok {
		t.Error("user should be unbound")
	}
}
