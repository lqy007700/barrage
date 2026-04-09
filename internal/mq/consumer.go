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

	// 最大重试次数，防止 retryCount 溢出
	maxRetryCount int

	// 优雅关闭信号
	stopCh chan struct{}

	// 是否正在关闭
	closing bool
}

const (
	// 最大重试次数上限，防止 retryCount 溢出
	defaultMaxRetryCount = 31
	// 读取超时，防止一直阻塞无法响应取消信号
	readTimeout = 5 * time.Second
)

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
		handle:         handler,
		maxRetryCount:  defaultMaxRetryCount,
	}
}

// Start 启动消费循环
func (c *Consumer) Start(ctx context.Context) {
	if c == nil || c.reader == nil {
		return
	}

	c.stopCh = make(chan struct{})
	defer close(c.stopCh)

	for {
		// 使用带超时的读取，允许定期检查 context 取消信号
		readCtx, cancel := context.WithTimeout(ctx, readTimeout)
		msg, err := c.reader.ReadMessage(readCtx)
		cancel()

		// 检查是否收到了关闭信号
		select {
		case <-c.stopCh:
			// 收到关闭信号，等待当前消息处理完成后退出
			if msg.Value != nil {
				c.processMessage(msg.Value)
			}
			log.Printf("Kafka 消费者优雅关闭完成")
			return
		default:
		}

		// context 被取消，且没有读取到消息
		if ctx.Err() != nil && msg.Value == nil {
			log.Printf("Kafka 消费者 context 已取消，正常退出")
			return
		}

		// 读取错误（可能是超时）
		if err != nil {
			metrics.KafkaConsumeErrCount.Add(1)
			c.retryCount++
			if c.retryCount > c.maxRetryCount {
				c.retryCount = c.maxRetryCount
			}
			// 指数退避：1s -> 2s -> 4s -> 8s -> 16s -> 30s (上限)
			backoff := time.Duration(math.Min(float64(30*time.Second), float64(time.Second)*math.Pow(2, float64(c.retryCount-1))))
			log.Printf("读取 Kafka 消息失败 (第%d次重试)，%.1f秒后重试: %v", c.retryCount, backoff.Seconds(), err)
			time.Sleep(backoff)
			continue
		}

		// 成功读取，重置重试计数
		c.retryCount = 0

		// 处理消息
		c.processMessage(msg.Value)
	}
}

// processMessage 处理单条消息
func (c *Consumer) processMessage(data []byte) {
	envelope, err := DecodeBroadcastEnvelope(data)
	if err != nil {
		log.Printf("解析 Kafka 消息失败: %v", err)
		return
	}

	if c.handle != nil {
		if err := c.handle(envelope); err != nil {
			log.Printf("处理 Kafka 消息失败: %v", err)
		}
	}
}

// Close 优雅关闭消费者
// 会等待当前正在处理的消息完成后再关闭
func (c *Consumer) Close() error {
	if c == nil || c.reader == nil {
		return nil
	}

	// 发送关闭信号，让消费循环处理完当前消息
	if c.stopCh != nil {
		close(c.stopCh)
	}

	return c.reader.Close()
}
