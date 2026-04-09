package gateway

import (
	"context"
	"errors"
	"strconv"
	"time"

	"barrage/internal/common"
	"barrage/internal/pb"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
)

// Producer Kafka生产者
// 负责将弹幕消息写入Kafka
type Producer struct {
	writer *kafka.Writer
	topic  string
}

// NewProducer 创建Kafka生产者
func NewProducer(brokers []string, topic string) *Producer {
	return &Producer{
		topic: topic,
		writer: &kafka.Writer{
			Addr:                   kafka.TCP(brokers...),
			Topic:                  topic,
			Balancer:               &kafka.Hash{}, // 按key hash到partition
			AllowAutoTopicCreation: false,
			RequiredAcks:           kafka.RequireOne,
			Async:                  false,
			WriteTimeout:           common.KafkaWriteTimeoutSec * time.Second,
		},
	}
}

// PublishDanmaku 发布弹幕消息
// videoTS: 视频时间戳，用于弹幕和视频同步
func (p *Producer) PublishDanmaku(envelope *pb.BroadcastEnvelope) error {
	if p == nil || p.writer == nil {
		return errors.New("producer未初始化")
	}
	if envelope == nil {
		return errors.New("envelope为空")
	}

	data, err := proto.Marshal(envelope)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), common.KafkaWriteTimeoutSec*time.Second)
	defer cancel()

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(strconv.FormatInt(envelope.GetRoomId(), 10)),
		Value: data,
	})
}

// PublishVideoTS 发布视频时间戳更新
func (p *Producer) PublishVideoTS(update *pb.VideoTSUpdate) error {
	if p == nil || p.writer == nil {
		return errors.New("producer未初始化")
	}
	if update == nil {
		return errors.New("update为空")
	}

	data, err := proto.Marshal(update)
	if err != nil {
		return err
	}

	ctx, cancel := context.WithTimeout(context.Background(), common.KafkaWriteTimeoutSec*time.Second)
	defer cancel()

	return p.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(strconv.FormatInt(update.GetRoomId(), 10)),
		Value: data,
	})
}

// PublishBatch 批量发布
func (p *Producer) PublishBatch(envelopes []*pb.BroadcastEnvelope) error {
	if p == nil || p.writer == nil {
		return errors.New("producer未初始化")
	}

	messages := make([]kafka.Message, 0, len(envelopes))
	for _, env := range envelopes {
		data, err := proto.Marshal(env)
		if err != nil {
			continue
		}
		messages = append(messages, kafka.Message{
			Key:   []byte(strconv.FormatInt(env.GetRoomId(), 10)),
			Value: data,
		})
	}

	if len(messages) == 0 {
		return nil
	}

	ctx, cancel := context.WithTimeout(context.Background(), common.KafkaWriteTimeoutSec*time.Second)
	defer cancel()

	return p.writer.WriteMessages(ctx, messages...)
}

// Close 关闭生产者
func (p *Producer) Close() error {
	if p == nil || p.writer == nil {
		return nil
	}
	return p.writer.Close()
}
