package mq

import (
	"context"
	"strconv"

	"github.com/segmentio/kafka-go"
)

// Producer 表示 Kafka 生产者
type Producer struct {
	// Kafka Writer
	writer *kafka.Writer

	// Topic 名称
	topic string
}

// NewProducer 创建 Kafka 生产者
func NewProducer(brokers []string, topic string) *Producer {
	return &Producer{
		topic: topic,
		writer: &kafka.Writer{
			Addr:                   kafka.TCP(brokers...),
			Topic:                  topic,
			Balancer:               &kafka.Hash{},
			AllowAutoTopicCreation: false,
			RequiredAcks:           kafka.RequireOne,
			Async:                  false,
		},
	}
}

// Publish 发送广播消息到 Kafka
// 这里使用 RoomID 作为 Key，确保同房间消息落到同一 partition，保证房间内有序
func (p *Producer) Publish(msg *BroadcastEnvelope) error {
	if p == nil || msg == nil {
		return nil
	}

	value, err := EncodeBroadcastEnvelope(msg)
	if err != nil {
		return err
	}

	return p.writer.WriteMessages(context.Background(), kafka.Message{
		Key:   []byte(strconv.FormatInt(msg.RoomID, 10)),
		Value: value,
	})
}

// Close 关闭生产者
func (p *Producer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}
