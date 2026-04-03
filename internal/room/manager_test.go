package room

import "testing"

// TestActiveLoopIndexes 用于验证房间活跃 loop 位图的基础行为
func TestActiveLoopIndexes(t *testing.T) {
	m := NewManager(8)
	roomID := int64(1001)

	room := m.GetOrCreate(roomID)
	room.ActiveLoops.Store((1 << 1) | (1 << 3) | (1 << 5))

	indexes := m.ActiveLoopIndexes(roomID)
	expected := []int{1, 3, 5}

	if len(indexes) != len(expected) {
		t.Fatalf("活跃 loop 数量不符合预期，got=%v want=%v", indexes, expected)
	}

	for i := range expected {
		if indexes[i] != expected[i] {
			t.Fatalf("活跃 loop 下标不符合预期，got=%v want=%v", indexes, expected)
		}
	}
}

// TestHasLocalRoom 用于验证房间本机连接判断逻辑
func TestHasLocalRoom(t *testing.T) {
	m := NewManager(4)
	roomID := int64(2001)

	if m.HasLocalRoom(roomID) {
		t.Fatalf("空房间不应被判断为本机有连接")
	}

	room := m.GetOrCreate(roomID)
	room.LocalConnCount.Store(2)

	if !m.HasLocalRoom(roomID) {
		t.Fatalf("房间连接数大于 0 时应判断为本机有连接")
	}
}
