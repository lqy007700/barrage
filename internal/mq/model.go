package mq

import (
	"barrage/internal/pb"

	"google.golang.org/protobuf/proto"
)

// BroadcastEnvelope 是对 protobuf 生成类型的业务别名
// 这样 mq 层可以直接复用 pb 中的消息定义，避免在 Kafka 链路外再包一层 JSON。
type BroadcastEnvelope = pb.BroadcastEnvelope

// EncodeBroadcastEnvelope 将广播消息序列化为 protobuf 二进制
func EncodeBroadcastEnvelope(msg *BroadcastEnvelope) ([]byte, error) {
	return proto.Marshal(msg)
}

// DecodeBroadcastEnvelope 将广播消息从 protobuf 二进制反序列化
func DecodeBroadcastEnvelope(data []byte) (*BroadcastEnvelope, error) {
	msg := &pb.BroadcastEnvelope{}
	if err := proto.Unmarshal(data, msg); err != nil {
		return nil, err
	}
	return msg, nil
}
