package mq

import (
	"context"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
)

// Consumer 表示 Kafka 消费者
type Consumer struct {
	// Kafka Reader
	reader *kafka.Reader

	// 消息处理回调
	handle func(*BroadcastEnvelope) error
}

// NewConsumer 创建 Kafka 消费者
func NewConsumer(brokers []string, topic, groupID string, handler func(*BroadcastEnvelope) error) *Consumer {
	return &Consumer{
		reader: kafka.NewReader(kafka.ReaderConfig{
			Brokers:  brokers,
			Topic:    topic,
			GroupID:  groupID,
			MinBytes: 1,
			MaxBytes: 10e6,
		}),
		handle: handler,
	}
}

// Start 启动消费循环
func (c *Consumer) Start(ctx context.Context) {
	if c == nil || c.reader == nil {
		return
	}

	for {
		msg, err := c.reader.ReadMessage(ctx)
		if err != nil {
			if ctx.Err() != nil {
				log.Printf("Kafka 消费者捕捉到关闭信号，正常退出")
				return
			}

			log.Printf("读取 Kafka 消息失败，将在 1 秒后重连重试: %v", err)
			time.Sleep(time.Second)
			continue
		}

		envelope, err := DecodeBroadcastEnvelope(msg.Value)
		if err != nil {
			log.Printf("解析 Kafka 消息失败: %v", err)
			continue
		}

		if c.handle != nil {
			if err := c.handle(envelope); err != nil {
				log.Printf("处理 Kafka 消息失败: %v", err)
			}
		}
	}
}

// Close 关闭消费者
func (c *Consumer) Close() error {
	if c == nil || c.reader == nil {
		return nil
	}
	return c.reader.Close()
}
