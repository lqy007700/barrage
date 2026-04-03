package mq

import "encoding/json"

// BroadcastEnvelope 表示广播链路中的统一消息结构
type BroadcastEnvelope struct {
	// 房间 ID
	RoomID int64 `json:"room_id"`

	// 发送者用户 ID
	UserID int64 `json:"user_id"`

	// 发送者连接 ID
	SenderConnID string `json:"sender_conn_id"`

	// 是否为付费用户
	IsPremium bool `json:"is_premium"`

	// 业务负载
	Payload []byte `json:"payload"`

	// 毫秒时间戳
	Timestamp int64 `json:"timestamp"`
}

// EncodeBroadcastEnvelope 将广播消息序列化
func EncodeBroadcastEnvelope(msg *BroadcastEnvelope) ([]byte, error) {
	return json.Marshal(msg)
}

// DecodeBroadcastEnvelope 将广播消息反序列化
func DecodeBroadcastEnvelope(data []byte) (*BroadcastEnvelope, error) {
	var msg BroadcastEnvelope
	if err := json.Unmarshal(data, &msg); err != nil {
		return nil, err
	}
	return &msg, nil
}
