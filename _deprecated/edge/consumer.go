package edge

import (
	"context"
	"fmt"
	"log"
	"time"

	"barrage/internal/common"
	"barrage/internal/pb"

	"github.com/segmentio/kafka-go"
	"google.golang.org/protobuf/proto"
)

// Consumer Kafka消费者
// 消费弹幕消息和视频时间戳更新
type Consumer struct {
	// 配置
	nodeId       string
	kafkaBrokers []string
	topic        string
	groupId      string // Consumer Group ID

	// 依赖
	videoSync *VideoSyncEngine
	sessionManager *SessionManager

	// Kafka消费者
	reader *kafka.Reader

	// 控制
	ctx    context.Context
	cancel context.CancelFunc
}

// NewConsumer 创建Kafka消费者
func NewConsumer(nodeId string, kafkaBrokers []string, topic string, videoSync *VideoSyncEngine, sessionManager *SessionManager) *Consumer {
	// 每个Edge节点使用独立的Consumer Group，确保每条消息被所有节点消费
	groupId := fmt.Sprintf("edge-%s", nodeId)

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        kafkaBrokers,
		Topic:          topic,
		GroupID:        groupId,
		MinBytes:       1e3,  // 1KB
		MaxBytes:       10e6, // 10MB
		MaxWait:        500 * time.Millisecond,
		StartOffset:    kafka.LastOffset,
		CommitInterval: time.Second,
	})

	return &Consumer{
		nodeId:        nodeId,
		kafkaBrokers:  kafkaBrokers,
		topic:         topic,
		groupId:       groupId,
		videoSync:     videoSync,
		sessionManager: sessionManager,
		reader:        reader,
	}
}

// Start 启动消费者
func (c *Consumer) Start(ctx context.Context) {
	c.ctx, c.cancel = context.WithCancel(ctx)

	go c.consumeLoop()
	log.Printf("Kafka consumer started (node=%s, group=%s, topic=%s)", c.nodeId, c.groupId, c.topic)
}

// consumeLoop 消费循环
func (c *Consumer) consumeLoop() {
	for {
		select {
		case <-c.ctx.Done():
			log.Printf("Kafka consumer stopped")
			return
		default:
			msg, err := c.reader.FetchMessage(c.ctx)
			if err != nil {
				if c.ctx.Err() != nil {
					return // 已取消
				}
				log.Printf("Fetch message error: %v", err)
				time.Sleep(time.Second) // 避免频繁重试
				continue
			}

			if err := c.handleMessage(msg); err != nil {
				log.Printf("Handle message error: %v", err)
			}

			// 提交offset
			if err := c.reader.CommitMessages(c.ctx, msg); err != nil {
				log.Printf("Commit error: %v", err)
			}
		}
	}
}

// handleMessage 处理消息
func (c *Consumer) handleMessage(msg kafka.Message) error {
	// 尝试解析为 BroadcastEnvelope
	envelope := &pb.BroadcastEnvelope{}
	if err := proto.Unmarshal(msg.Value, envelope); err == nil {
		return c.handleDanmaku(envelope)
	}

	// 尝试解析为 VideoTSUpdate
	videoTS := &pb.VideoTSUpdate{}
	if err := proto.Unmarshal(msg.Value, videoTS); err == nil {
		return c.handleVideoTS(videoTS)
	}

	log.Printf("Unknown message type, size=%d", len(msg.Value))
	return nil
}

// handleDanmaku 处理弹幕消息
func (c *Consumer) handleDanmaku(envelope *pb.BroadcastEnvelope) error {
	roomId := envelope.GetRoomId()
	userId := envelope.GetUserId()

	// 获取房间所有会话
	sessions := c.sessionManager.GetRoomSessions(roomId)
	if len(sessions) == 0 {
		return nil
	}

	// 构建 Danmaku
	danmaku := &common.Danmaku{
		RoomId:       roomId,
		UserId:       userId,
		SenderConnId: envelope.GetSenderConnId(),
		VideoTS:      envelope.GetVideoTs(),
		SendTS:       envelope.GetTimestamp(),
		Payload:      envelope.GetPayload(),
		IsPremium:    envelope.GetIsPremium(),
	}

	// 放入每个会话的缓冲区
	for _, session := range sessions {
		session.OnDanmaku(danmaku)
	}

	return nil
}

// handleVideoTS 处理视频时间戳更新
func (c *Consumer) handleVideoTS(update *pb.VideoTSUpdate) error {
	roomId := update.GetRoomId()
	videoTS := update.GetVideoTs()

	c.videoSync.UpdateVideoTS(roomId, videoTS)

	return nil
}

// Stop 停止消费者
func (c *Consumer) Stop() error {
	if c.cancel != nil {
		c.cancel()
	}

	if c.reader != nil {
		return c.reader.Close()
	}

	return nil
}
