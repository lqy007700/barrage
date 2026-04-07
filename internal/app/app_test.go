package app

import (
	"testing"
	"time"

	"barrage/internal/connctx"
	"barrage/internal/config"
	"barrage/internal/dispatcher"
	"barrage/internal/filter"
	"barrage/internal/metrics"
	"barrage/internal/pb"
	"github.com/panjf2000/gnet/v2"
	"google.golang.org/protobuf/proto"
)

type mockPushConn struct {
	gnet.Conn // 匿名嵌入兼容外部校验逻辑

	lastOpCode     pb.OpType
	closed         bool
}

func (m *mockPushConn) AsyncWrite(buf []byte, cb gnet.AsyncCallback) error {
	// 我们简单解析这个 buf，证明确实发下去了错误反馈即可
	if len(buf) > 2 {
		// WebSocket 提取出里面的 proto 负载（这里只是个简单的逆推验证，我们知道 0x82 是 Bin 帧起手）
		// 反序列化确保他确实发送了 pb.Frame 返回的 Err
		var errorFrame pb.Frame
		_ = proto.Unmarshal(buf[2:], &errorFrame)
		m.lastOpCode = errorFrame.Op
	}
	if cb != nil {
		_ = cb(m, nil)
	}
	return nil
}
func (m *mockPushConn) Close() error {
	m.closed = true
	return nil
}
func (m *mockPushConn) EventLoop() gnet.EventLoop {
	return nil
}


func TestApp_HandleChat_SensitiveCheck(t *testing.T) {
	// 创建基本 Mock 应用框架
	cfg := &config.Config{WorkerPoolSize: 1}
	app, _ := New(cfg)

	// 初始化一个敏感词检测字典
	simpleFilter := filter.NewReloadableFilter("", []string{"坏词"})
	_ = simpleFilter.LoadNow()
	app.TextFilter = simpleFilter

	// 重置打点计数
	metrics.FilterRejectCount.Store(0)

	// 构造 Mock 客户状态与 TCP 端
	mc := &mockPushConn{}
	ctx := &connctx.ConnContext{
		ConnID: "m1",
		RoomID: 100,
		UserID: 1234,
	}

	chatMsg := &pb.ChatMsg{
		Content: "你发的是个坏词语句",
	}
	chatPayload, _ := proto.Marshal(chatMsg)

	chatFrame := &pb.Frame{
		Op:        pb.OpType_OP_CHAT,
		RoomId:    100,
		UserId:    1234,
		Payload:   chatPayload,
		Timestamp: time.Now().UnixMilli(),
	}

	requestData, _ := proto.Marshal(chatFrame)

	task := &dispatcher.InboundTask{
		Conn: mc,
		Ctx:  ctx,
		Data: requestData,
	}

	// 执行实际的分发解析逻辑
	app.HandleInbound(task)

	// 断言判断：因为发了坏词，计数器理应飙升
	if metrics.FilterRejectCount.Load() != 1 {
		t.Fatalf("期待增加敏感词触发反制数据未达标。当前计数: %d", metrics.FilterRejectCount.Load())
	}

	// 并且我们应该发送了一张 pb.OpType_OP_ERROR 的 Frame 错误封包对客户端进行了反馈
	if mc.lastOpCode != pb.OpType_OP_ERROR {
		t.Fatalf("没能正确下压 Error 服务封包，反倒压了: %v", mc.lastOpCode)
	}
}
