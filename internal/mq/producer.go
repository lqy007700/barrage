package mq

import (
	"context"
	"errors"
	"strconv"
	"time"

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
// 这里使用 RoomId 作为 Key，确保同房间消息落到同一 partition，保证房间内有序
func (p *Producer) Publish(msg *BroadcastEnvelope) error {
	if p == nil {
		return nil
	}
	if msg == nil {
		return errors.New("广播消息为空")
	}

	value, err := EncodeBroadcastEnvelope(msg)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(strconv.FormatInt(msg.RoomId, 10)),
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
