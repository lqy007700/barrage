package gateway

import (
	"barrage/internal/filter"
	"barrage/internal/pb"
	"errors"
	"time"

	"google.golang.org/protobuf/proto"
)

// DanmakuProc 弹幕处理器
// 负责敏感词过滤和消息校验
type DanmakuProc struct {
	filter filter.Filter
}

// NewDanmakuProc 创建弹幕处理器
func NewDanmakuProc(filter filter.Filter) *DanmakuProc {
	return &DanmakuProc{
		filter: filter,
	}
}

// ProcessResult 处理结果
type ProcessResult struct {
	Valid   bool
	Reason  string
	Payload []byte
}

// Process 处理弹幕消息
// 1. 校验格式
// 2. 敏感词过滤
// 3. 构建Payload
func (p *DanmakuProc) Process(msg *pb.ChatMsg, userId int64, roomId int64) *ProcessResult {
	if msg == nil {
		return &ProcessResult{Valid: false, Reason: "消息为空"}
	}

	// 1. 校验内容
	content := msg.GetContent()
	if content == "" {
		return &ProcessResult{Valid: false, Reason: "内容为空"}
	}

	if len(content) > 200 {
		return &ProcessResult{Valid: false, Reason: "内容过长"}
	}

	// 2. 敏感词过滤
	if p.filter != nil && p.filter.Check(content).Hit {
		return &ProcessResult{Valid: false, Reason: "包含敏感词"}
	}

	// 3. 构建广播Payload
	// 使用 BroadcastMsg 作为 Payload
	broadcastMsg := &pb.BroadcastMsg{
		RoomId:    roomId,
		UserId:    userId,
		Body:      []byte(content),
		Timestamp: time.Now().UnixMilli(),
	}

	payload, err := proto.Marshal(broadcastMsg)
	if err != nil {
		return &ProcessResult{Valid: false, Reason: "序列化失败"}
	}

	return &ProcessResult{
		Valid:   true,
		Payload: payload,
	}
}

// ValidateJoinRoom 校验加入房间请求
func (p *DanmakuProc) ValidateJoinRoom(req *pb.JoinRoomReq) error {
	if req == nil {
		return errors.New("请求为空")
	}
	if req.GetRoomId() <= 0 {
		return errors.New("房间ID无效")
	}
	if req.GetUserId() <= 0 {
		return errors.New("用户ID无效")
	}
	// Token验证可以在这里扩展
	return nil
}

// DanmakuProcError 错误类型
type DanmakuProcError struct {
	Code    int32
	Message string
}

func (e *DanmakuProcError) Error() string {
	return e.Message
}

// NewError 创建错误
func NewProcError(code int32, msg string) *DanmakuProcError {
	return &DanmakuProcError{Code: code, Message: msg}
}
