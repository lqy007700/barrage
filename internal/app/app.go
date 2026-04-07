package app

import (
	"barrage/internal/broadcast"
	"barrage/internal/config"
	"barrage/internal/dispatcher"
	"barrage/internal/filter"
	"barrage/internal/mq"
	"barrage/internal/pb"
	"barrage/internal/protocol/ws"
	"barrage/internal/room"
	"barrage/internal/worker"
	"context"
	"log"
	"runtime"
	"time"

	"github.com/panjf2000/gnet/v2"
	"google.golang.org/protobuf/proto"
)

// App 是应用总装配对象
type App struct {
	// 全局配置
	Config *config.Config

	// loop 注册表
	LoopRegistry *LoopRegistry

	// loop 任务分发器
	LoopDispatcher *LoopDispatcher

	// 敏感词过滤
	TextFilter filter.Filter

	// gnet 引擎对象
	Engine gnet.Engine

	// sub-reactor 数量
	LoopCount int

	// 房间管理器
	RoomManager *room.Manager

	// ants 协程池
	WorkerPool *worker.Pool

	// 广播器
	Broadcaster *broadcast.Broadcaster

	// Kafka 生产者
	Producer *mq.Producer

	// Kafka 消费者
	Consumer *mq.Consumer
}

// New 创建应用对象
func New(cfg *config.Config) (*App, error) {
	loopCount := runtime.NumCPU()
	if loopCount <= 0 {
		loopCount = 1
	}

	pool, err := worker.NewPool(cfg.WorkerPoolSize)
	if err != nil {
		return nil, err
	}

	roomManager := room.NewManager(loopCount)

	a := &App{
		Config:         cfg,
		LoopCount:      loopCount,
		RoomManager:    roomManager,
		WorkerPool:     pool,
		LoopRegistry:   NewLoopRegistry(),
		LoopDispatcher: NewLoopDispatcher(),
	}

	defaultSensitiveWords := []string{
		"傻逼",
		"他妈的",
		"垃圾",
	}

	reloadableFilter := filter.NewReloadableFilter(cfg.SensitiveWordsPath, defaultSensitiveWords)
	if cfg.SensitiveWordsPath != "" {
		if err := reloadableFilter.LoadNow(); err != nil {
			log.Printf("加载敏感词词库失败，继续使用内置默认词表: path=%s err=%v", cfg.SensitiveWordsPath, err)
		}
	}
	a.TextFilter = reloadableFilter

	a.Broadcaster = broadcast.New(roomManager, a.LoopDispatcher)

	// 只有显式启用 Kafka 时才初始化
	if cfg.EnableKafka && len(cfg.KafkaBrokers) > 0 && cfg.KafkaTopic != "" {
		a.Producer = mq.NewProducer(cfg.KafkaBrokers, cfg.KafkaTopic)
		a.Consumer = mq.NewConsumer(
			cfg.KafkaBrokers,
			cfg.KafkaTopic,
			cfg.KafkaGroupID,
			a.HandleMQBroadcast,
		)
	}

	return a, nil
}

// StartTextFilterReload 启动敏感词热更新。
func (a *App) StartTextFilterReload(ctx context.Context) {
	if a == nil || a.Config == nil {
		return
	}

	reloadableFilter, ok := a.TextFilter.(*filter.ReloadableFilter)
	if !ok || reloadableFilter == nil {
		return
	}

	reloadableFilter.StartAutoReload(ctx, a.Config.SensitiveWordsReloadInterval)
}

// BindEngine 绑定 gnet 引擎
func (a *App) BindEngine(eng gnet.Engine) {
	a.Engine = eng
}

// StartConsumer 启动 Kafka 消费者
func (a *App) StartConsumer(ctx context.Context) {
	if a == nil || a.Consumer == nil {
		log.Printf("当前未启用 Kafka，服务以单机广播模式运行")
		return
	}

	log.Printf("Kafka 消费者启动成功: topic=%s groupID=%s", a.Config.KafkaTopic, a.Config.KafkaGroupID)
	go a.Consumer.Start(ctx)
}

// HandleMQBroadcast 处理 Kafka 广播消息
func (a *App) HandleMQBroadcast(msg *mq.BroadcastEnvelope) error {
	if a == nil || a.Broadcaster == nil || msg == nil {
		return nil
	}

	// Kafka 消费后的消息，只在本机有该房间连接时才进行广播
	if !a.RoomManager.HasLocalRoom(msg.RoomId) {
		return nil
	}

	return a.Broadcaster.BroadcastLocal(msg)
}

// HandleInbound 处理入站消息
func (a *App) HandleInbound(task *dispatcher.InboundTask) {
	if task == nil || task.Conn == nil || task.Ctx == nil || len(task.Data) == 0 {
		return
	}

	frame := &pb.Frame{}
	if err := proto.Unmarshal(task.Data, frame); err != nil {
		log.Printf("解析 protobuf Frame 失败: %v", err)
		return
	}

	switch frame.Op {
	case pb.OpType_OP_JOIN_ROOM:
		a.handleJoinRoom(task, frame)

	case pb.OpType_OP_CHAT:
		a.handleChat(task, frame)

	case pb.OpType_OP_HEARTBEAT:
		a.handleHeartbeat(task, frame)

	default:
		log.Printf("收到未知操作类型: op=%v", frame.Op)
	}
}

// handleJoinRoom 处理加入房间请求
func (a *App) handleJoinRoom(task *dispatcher.InboundTask, frame *pb.Frame) {
	req := &pb.JoinRoomReq{}
	if err := proto.Unmarshal(frame.Payload, req); err != nil {
		log.Printf("解析 JoinRoomReq 失败: %v", err)
		return
	}

	task.Ctx.UserID = req.UserId
	task.Ctx.RoomID = req.RoomId
	task.Ctx.IsPremium = req.IsPremium
	// 根据连接所属的 event-loop 获取稳定的逻辑 loop 下标
	task.Ctx.LoopIdx = a.ResolveLoopIdx(task.Conn)

	a.RoomManager.AddConnInLoop(task.Ctx.RoomID, task.Ctx.LoopIdx, task.Ctx.ConnID, task.Conn)

	log.Printf("用户加入房间成功: roomID=%d userID=%d premium=%v",
		task.Ctx.RoomID,
		task.Ctx.UserID,
		task.Ctx.IsPremium,
	)
}

// handleChat 处理聊天消息
func (a *App) handleChat(task *dispatcher.InboundTask, frame *pb.Frame) {
	if task.Ctx.RoomID <= 0 || task.Ctx.UserID <= 0 {
		log.Printf("收到未入房用户消息，忽略: userID=%d roomID=%d", task.Ctx.UserID, task.Ctx.RoomID)
		return
	}

	msg := &pb.ChatMsg{}
	if err := proto.Unmarshal(frame.Payload, msg); err != nil {
		log.Printf("解析 ChatMsg 失败: %v", err)
		return
	}

	// 对聊天内容做敏感词检测
	// 命中后直接驳回，不再向房间广播
	if a.TextFilter != nil {
		filterResult := a.TextFilter.Check(msg.Content)
		if filterResult.Hit {
			log.Printf("消息因敏感词被驳回: roomID=%d userID=%d connID=%s words=%v",
				task.Ctx.RoomID,
				task.Ctx.UserID,
				task.Ctx.ConnID,
				filterResult.Words,
			)

			// 构造错误消息下发给发送者
			errorMsg := &pb.ErrorMsg{
				Code:    400,
				Message: "消息包含敏感词，发送失败",
			}
			errPayload, _ := proto.Marshal(errorMsg)

			errorFrame := &pb.Frame{
				Op:        pb.OpType_OP_ERROR,
				RoomId:    task.Ctx.RoomID,
				UserId:    task.Ctx.UserID,
				Payload:   errPayload,
				Timestamp: time.Now().UnixMilli(),
			}

			frameData, _ := proto.Marshal(errorFrame)
			
			// 通过 AsyncWrite 回写给对应的 websocket 连接
			task.Conn.AsyncWrite(ws.BuildBinaryFrame(frameData), func(c gnet.Conn, err error) error {
				if err != nil {
					log.Printf("下发敏感词拦截提示失败: connID=%s err=%v", task.Ctx.ConnID, err)
				}
				return nil
			})
			return
		}
	}

	outFrame := &pb.Frame{
		Op:        pb.OpType_OP_BROADCAST,
		RoomId:    task.Ctx.RoomID,
		UserId:    task.Ctx.UserID,
		Payload:   frame.Payload,
		Timestamp: time.Now().UnixMilli(),
	}

	data, err := proto.Marshal(outFrame)
	if err != nil {
		log.Printf("序列化广播 Frame 失败: %v", err)
		return
	}

	envelope := &mq.BroadcastEnvelope{
		RoomId:       task.Ctx.RoomID,
		UserId:       task.Ctx.UserID,
		SenderConnId: task.Ctx.ConnID,
		IsPremium:    task.Ctx.IsPremium,
		Payload:      data,
		Timestamp:    time.Now().UnixMilli(),
	}

	// 当前版本改为统一写 Kafka
	// 这样各机器都可以通过各自独立 GroupID 消费后广播本机连接
	if a.Producer != nil {
		if err := a.Producer.Publish(envelope); err != nil {
			log.Printf("发送 Kafka 消息失败: roomID=%d userID=%d err=%v",
				envelope.RoomId,
				envelope.UserId,
				err,
			)
			return
		}

		log.Printf("聊天消息已发送到 Kafka: roomID=%d userID=%d content=%s payloadSize=%d",
			envelope.RoomId,
			envelope.UserId,
			msg.Content,
			len(envelope.Payload),
		)
		return
	}

	// 如果当前未配置 Kafka，则退化为单机本地广播
	if a.RoomManager.HasLocalRoom(envelope.RoomId) && a.Broadcaster != nil {
		if err := a.Broadcaster.BroadcastLocal(envelope); err != nil {
			log.Printf("本机广播失败: roomID=%d userID=%d err=%v",
				envelope.RoomId,
				envelope.UserId,
				err,
			)
			return
		}
	}

	log.Printf("收到聊天消息并完成本机广播: roomID=%d userID=%d content=%s payloadSize=%d",
		envelope.RoomId,
		envelope.UserId,
		msg.Content,
		len(envelope.Payload),
	)
}

// handleHeartbeat 处理心跳消息
func (a *App) handleHeartbeat(task *dispatcher.InboundTask, frame *pb.Frame) {
	heartbeat := &pb.Heartbeat{}
	if err := proto.Unmarshal(frame.Payload, heartbeat); err != nil {
		log.Printf("解析 Heartbeat 失败: %v", err)
		return
	}

	log.Printf("收到心跳: userID=%d roomID=%d clientTS=%d",
		task.Ctx.UserID,
		task.Ctx.RoomID,
		heartbeat.ClientTs,
	)
}

// ResolveLoopIdx 根据连接所属 event-loop 解析逻辑下标
func (a *App) ResolveLoopIdx(c gnet.Conn) int {
	if a == nil || c == nil || a.LoopRegistry == nil {
		return 0
	}

	loop := c.EventLoop()
	loopIdx := a.LoopRegistry.GetOrAssign(loop)

	// 在首次解析出 loopIdx 时，把 loop 也登记到分发器
	if a.LoopDispatcher != nil {
		a.LoopDispatcher.Bind(loopIdx, loop)
	}

	return loopIdx
}
