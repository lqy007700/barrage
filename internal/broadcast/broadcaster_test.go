package broadcast

import (
	"barrage/internal/connctx"
	"barrage/internal/mq"
	"barrage/internal/room"
	"testing"

	"github.com/panjf2000/gnet/v2"
)

// mockConn 极简连接代理
type mockConn struct {
	gnet.Conn // 屏蔽全部未用到接口

	written  int
	ctx      interface{}
	outbound int
}

func (m *mockConn) SetContext(ctx interface{}) { m.ctx = ctx }
func (m *mockConn) Context() interface{}       { return m.ctx }
func (m *mockConn) OutboundBuffered() int      { return m.outbound }
func (m *mockConn) AsyncWrite(buf []byte, cb gnet.AsyncCallback) error {
	m.written++
	return nil
}

func TestBroadcaster_NoEcho(t *testing.T) {
	manager := room.NewManager(4)
	b := New(manager)

	conn1 := &mockConn{}
	conn1.SetContext(&connctx.ConnContext{ConnID: "c1", RoomID: 100, UserID: 1001, IsPremium: false, LoopIdx: 1})
	manager.AddConnInLoop(100, 1, "c1", conn1)

	conn2 := &mockConn{}
	conn2.SetContext(&connctx.ConnContext{ConnID: "c2", RoomID: 100, UserID: 1002, IsPremium: false, LoopIdx: 3})
	manager.AddConnInLoop(100, 3, "c2", conn2)

	// 测试广播（c1 产生了一条全房广播消息）
	envelope := &mq.BroadcastEnvelope{
		RoomId:       100,
		SenderConnId: "c1", // 提供 SenderConnId 是触发不回显特性的关键
		Payload:      []byte("hello_packet"),
	}

	err := b.BroadcastLocal(envelope)
	if err != nil {
		t.Fatalf("局域广播失败: %v", err)
	}

	// 1. 验证防自我回显特性
	if conn1.written != 0 {
		t.Fatalf("发现异常：发送者 c1 收到了自我发送 Echo 弹幕反射")
	}
	if conn2.written != 1 {
		t.Fatalf("发现异常：接收者 c2 未能正常收取到应有的客端广播")
	}
}

func TestBroadcaster_SlowConnSkip(t *testing.T) {
	manager := room.NewManager(1)
	b := New(manager)

	// 构造一个超级拥堵连接
	conn3 := &mockConn{outbound: 600 * 1024} // 大于 512k 测试下线
	conn3.SetContext(&connctx.ConnContext{ConnID: "c3", RoomID: 200, UserID: 2001, IsPremium: false, LoopIdx: 0})
	manager.AddConnInLoop(200, 0, "c3", conn3)

	env := &mq.BroadcastEnvelope{
		RoomId:  200,
		Payload: []byte("slow_ignored_packet"),
	}

	_ = b.BroadcastLocal(env)

	// 由于其 outBuffer 大于容忍限度，理应跳过发送避免阻塞雪崩
	if conn3.written != 0 {
		t.Fatalf("本应当保护性跳过的高延迟积压客户端却依然下发了协议包")
	}
}
