package mq

import (
	"context"
	"errors"
	"math"
	"strconv"
	"time"

	"github.com/segmentio/kafka-go"
)

// 消息类型定义
const (
	// MsgTypeDanmaku 普通弹幕，不重试
	MsgTypeDanmaku int32 = 0
	// MsgTypeGift 礼物消息，需要重试
	MsgTypeGift int32 = 1
	// MsgTypeProp 道具消息，需要重试
	MsgTypeProp int32 = 2
	// MsgTypeColorful 彩色弹幕，需要重试
	MsgTypeColorful int32 = 3
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

const (
	// maxRetryCount 最大重试次数
	maxRetryCount = 3
	// baseBackoffMs 基础退避时间（毫秒）
	baseBackoffMs = 100
)

// Publish 发送广播消息到 Kafka
//
// 普通弹幕 (msg_type=0): 使用 roomId 作为 Key，无重试，失败直接丢弃
// 特殊消息 (msg_type>0): 使用 idempotency_key 作为 Key，保证幂等，最多重试3次
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

	// 普通弹幕：不重试，失败直接丢弃
	if msg.MsgType == MsgTypeDanmaku {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		return p.writer.WriteMessages(ctx, kafka.Message{
			Key:   []byte(strconv.FormatInt(msg.RoomId, 10)),
			Value: value,
		})
	}

	// 特殊消息（礼物/道具/彩色弹幕）：使用幂等key + 重试
	return p.publishWithRetry(msg, value)
}

// publishWithRetry 带重试的发布（用于特殊消息）
func (p *Producer) publishWithRetry(msg *BroadcastEnvelope, value []byte) error {
	// 生成幂等 key（如果消息没有设置）
	idempotencyKey := msg.IdempotencyKey
	if idempotencyKey == "" {
		idempotencyKey = generateIdempotencyKey(msg)
	}

	var lastErr error
	for attempt := 0; attempt < maxRetryCount; attempt++ {
		if attempt > 0 {
			// 指数退避：100ms -> 200ms -> 400ms
			backoff := time.Duration(baseBackoffMs*int(math.Pow(2, float64(attempt-1)))) * time.Millisecond
			time.Sleep(backoff)
		}

		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err := p.writer.WriteMessages(ctx, kafka.Message{
			Key:   []byte(idempotencyKey), // 使用幂等key作为消息key，Kafka会进行去重
			Value: value,
		})
		cancel()

		if err == nil {
			return nil // 成功
		}
		lastErr = err
	}

	return lastErr // 所有重试都失败
}

// generateIdempotencyKey 生成幂等key
// 格式: {userId}_{roomId}_{timestamp}_{random}
func generateIdempotencyKey(msg *BroadcastEnvelope) string {
	return strconv.FormatInt(msg.UserId, 10) + "_" +
		strconv.FormatInt(msg.RoomId, 10) + "_" +
		strconv.FormatInt(msg.Timestamp, 10) + "_" +
		strconv.FormatInt(time.Now().UnixNano(), 10)
}

// Close 关闭生产者
func (p *Producer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}
