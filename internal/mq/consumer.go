package mq

import (
	"context"
	"log"
	"math"
	"time"

	"barrage/internal/metrics"

	"github.com/segmentio/kafka-go"
)

// Consumer 表示 Kafka 消费者
type Consumer struct {
	// Kafka Reader
	reader *kafka.Reader

	// 消息处理回调
	handle func(*BroadcastEnvelope) error

	// 重试计数器，用于指数退避
	retryCount int
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

			metrics.KafkaConsumeErrCount.Add(1)
			c.retryCount++
			// 指数退避：1s -> 2s -> 4s -> 8s -> 16s -> 30s (上限)
			backoff := time.Duration(math.Min(float64(30*time.Second), float64(time.Second)*math.Pow(2, float64(c.retryCount-1))))
			log.Printf("读取 Kafka 消息失败 (第%d次重试)，%.1f秒后重试: %v", c.retryCount, backoff.Seconds(), err)
			time.Sleep(backoff)
			continue
		} else {
			// 成功读取，重置重试计数
			c.retryCount = 0
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
