package app

import (
	"barrage/internal/broadcast"
	"barrage/internal/config"
	"barrage/internal/connctx"
	"barrage/internal/dispatcher"
	"barrage/internal/filter"
	"barrage/internal/metrics"
	"barrage/internal/mq"
	"barrage/internal/pb"
	"barrage/internal/protocol/ws"
	"barrage/internal/registry"
	"barrage/internal/room"
	"barrage/internal/worker"
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
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

	// 服务注册中心
	Registry     registry.Registry
	Registrar    *registry.ServiceRegistrar
	ServiceInfo  *registry.ServiceInfo

	// 全局连接表，用于心跳检测等
	AllConns sync.Map

	// 当前节点总连接数
	CurrConnCount atomic.Int64

	// 单用户连接数限制
	userConnCount map[int64]int64
	userConnMu    sync.RWMutex

	// 消息频率限制器
	RateLimiter *RateLimiter

	// Metrics HTTP 服务器
	MetricsServer *MetricsServer
}

// RateLimiter 消息频率限制器 (Token Bucket)
type RateLimiter struct {
	mu       sync.RWMutex
	users    map[int64]*userBucket
	rate     int // 每秒允许的消息数
	capacity int // 桶容量（突发能力）
}

type userBucket struct {
	tokens    float64
	lastUpdate int64 // Unix秒
}

// NewRateLimiter 创建频率限制器
func NewRateLimiter(rate, capacity int) *RateLimiter {
	return &RateLimiter{
		users:    make(map[int64]*userBucket),
		rate:     rate,
		capacity: capacity,
	}
}

// Allow 检查是否允许发送
func (r *RateLimiter) Allow(userId int64) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	now := time.Now().Unix()
	bucket := r.users[userId]

	if bucket == nil {
		r.users[userId] = &userBucket{
			tokens:    float64(r.capacity - 1),
			lastUpdate: now,
		}
		return true
	}

	// 计算补充的token
	elapsed := now - bucket.lastUpdate
	bucket.tokens += float64(elapsed) * float64(r.rate)
	if bucket.tokens > float64(r.capacity) {
		bucket.tokens = float64(r.capacity)
	}
	bucket.lastUpdate = now

	if bucket.tokens >= 1 {
		bucket.tokens -= 1
		return true
	}
	return false
}

// addUserConn 增加用户连接计数
// 返回 true 表示允许，false 表示超过限制
func (a *App) addUserConn(userId int64) bool {
	a.userConnMu.Lock()
	defer a.userConnMu.Unlock()

	count := a.userConnCount[userId] + 1
	if count > int64(a.Config.MaxConnsPerUser) {
		return false
	}
	a.userConnCount[userId] = count
	return true
}

// removeUserConn 减少用户连接计数
func (a *App) RemoveUserConn(userId int64) {
	a.userConnMu.Lock()
	defer a.userConnMu.Unlock()

	if count := a.userConnCount[userId] - 1; count > 0 {
		a.userConnCount[userId] = count
	} else {
		delete(a.userConnCount, userId)
	}
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
		userConnCount: make(map[int64]int64),
		RateLimiter:   NewRateLimiter(cfg.MsgRatePerUser, cfg.MsgBurstCapacity),
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

	a.Broadcaster = broadcast.New(roomManager, a.LoopDispatcher, 50*time.Millisecond, 0)

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

	// 初始化 Registry
	if cfg.RegistryType != "static" && cfg.RegistryAddr != "" {
		a.Registry, err = registry.NewRegistry(cfg.RegistryType, cfg.RegistryAddr)
		if err != nil {
			return nil, fmt.Errorf("create registry: %w", err)
		}

		// 解析监听地址获取端口
		addr := cfg.ListenAddr
		port := 9000
		if len(addr) > 0 {
			// 格式: tcp://0.0.0.0:9000
			for i := len(addr) - 1; i >= 0; i-- {
				if addr[i] == ':' {
					portStr := addr[i+1:]
					fmt.Sscanf(portStr, "%d", &port)
					break
				}
			}
		}

		a.ServiceInfo = &registry.ServiceInfo{
			ID:   cfg.NodeId,
			Name: cfg.ServiceName,
			Addr: addr,
			Port: port,
			Tags: []string{},
			Meta: map[string]string{
				"hostname": hostname(),
			},
		}

		a.Registrar = registry.NewServiceRegistrar(a.Registry, a.ServiceInfo)
	}

	return a, nil
}

// hostname 获取主机名
func hostname() string {
	h, _ := os.Hostname()
	return h
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

// StartHeartbeatCheck 启动定时清理僵尸连接
func (a *App) StartHeartbeatCheck(ctx context.Context) {
	go func() {
		ticker := time.NewTicker(30 * time.Second)
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				now := time.Now().Unix()
				// 遍历所有连接
				a.AllConns.Range(func(key, value interface{}) bool {
					conn, ok := value.(gnet.Conn)
					if !ok {
						return true
					}

					cCtx, ok := conn.Context().(*connctx.ConnContext)
					if !ok {
						return true
					}

					// 如果超过 90 秒未活跃，则视为僵尸连接关闭
					if now-cCtx.LastActiveTime.Load() > 90 {
						log.Printf("连接心跳超时断开: connID=%s userID=%d", cCtx.ConnID, cCtx.UserID)
						// 从全局连接表中移除，防止内存泄漏
						a.AllConns.Delete(cCtx.ConnID)
						conn.Close()
					}
					return true
				})
			}
		}
	}()
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
	// 二层防护：防御来自底层代理或非 WS 协议引发的超大包体，限制单包解析至高在 64KB 内
	if task == nil || task.Conn == nil || task.Ctx == nil || len(task.Data) == 0 || len(task.Data) > 64*1024 {
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

	// 1. token 校验 (如果配置了 AuthToken)
	if a.Config.AuthToken != "" && req.Token != a.Config.AuthToken {
		a.sendError(task, 401, "鉴权失败: 无效的 token")
		return
	}

	// 2. 封禁用户校验
	if req.UserId <= 0 {
		a.sendError(task, 403, "准入失败: 无效的用户 ID")
		return
	}
	if a.isUserBanned(req.UserId) {
		a.sendError(task, 403, "准入失败: 该用户已被封禁")
		return
	}

	// 3. 房间准入校验 (必须大于 0)
	if req.RoomId <= 0 {
		a.sendError(task, 404, "准入失败: 房间不存在或已关闭")
		return
	}

	// 4. 单用户连接数限制检查
	if !a.addUserConn(req.UserId) {
		a.sendError(task, 429, "该账号连接数过多，请稍后重试")
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

	// 消息频率限制检查
	if a.RateLimiter != nil && !a.RateLimiter.Allow(task.Ctx.UserID) {
		metrics.RateLimitRejectCount.Add(1)
		a.sendError(task, 429, "发送频率过快，请稍后重试")
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
			metrics.FilterRejectCount.Add(1)
			log.Printf("消息因敏感词被驳回: roomID=%d userID=%d connID=%s words=%v",
				task.Ctx.RoomID,
				task.Ctx.UserID,
				task.Ctx.ConnID,
				filterResult.Words,
			)

			// 构造错误消息下发给发送者
			a.sendError(task, 400, "消息包含敏感词，发送失败")
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
			metrics.KafkaPublishErrCount.Add(1)
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

// isUserBanned 检查用户是否在封禁列表中
func (a *App) isUserBanned(userID int64) bool {
	if a == nil || a.Config == nil {
		return false
	}
	for _, banned := range a.Config.BannedUsers {
		if banned == userID {
			return true
		}
	}
	return false
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

// sendError 辅助方法：向客户端发送错误消息
func (a *App) sendError(task *dispatcher.InboundTask, code int32, message string) {
	errorMsg := &pb.ErrorMsg{
		Code:    code,
		Message: message,
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

	task.Conn.AsyncWrite(ws.BuildBinaryFrame(frameData), func(c gnet.Conn, err error) error {
		if err != nil {
			log.Printf("下发错误提示失败: connID=%s err=%v", task.Ctx.ConnID, err)
		}
		return nil
	})
}

// KickConn 主动断开指定连接
func (a *App) KickConn(connID string, reason string) {
	value, ok := a.AllConns.Load(connID)
	if !ok {
		return
	}

	conn, ok := value.(gnet.Conn)
	if !ok {
		return
	}

	cCtx, ok := conn.Context().(*connctx.ConnContext)
	if !ok {
		return
	}

	_ = a.LoopDispatcher.Dispatch(cCtx.LoopIdx, func() {
		errorMsg := &pb.ErrorMsg{
			Code:    403,
			Message: reason,
		}
		errPayload, _ := proto.Marshal(errorMsg)

		errorFrame := &pb.Frame{
			Op:        pb.OpType_OP_ERROR,
			RoomId:    cCtx.RoomID,
			UserId:    cCtx.UserID,
			Payload:   errPayload,
			Timestamp: time.Now().UnixMilli(),
		}

		frameData, _ := proto.Marshal(errorFrame)

		// 派发到对应 loop 内部，发送提示帧后回调执行关闭
		_ = conn.AsyncWrite(ws.BuildBinaryFrame(frameData), func(c gnet.Conn, err error) error {
			log.Printf("主动踢出连接: connID=%s userID=%d reason=%s", cCtx.ConnID, cCtx.UserID, reason)
			return c.Close()
		})
	})
}

// KickUser 断开指定 userID 的所有连接
func (a *App) KickUser(userID int64, reason string) {
	if userID <= 0 {
		return
	}

	// 遍历查找对应 userID
	a.AllConns.Range(func(key, value interface{}) bool {
		conn, ok := value.(gnet.Conn)
		if !ok {
			return true
		}

		cCtx, ok := conn.Context().(*connctx.ConnContext)
		if !ok || cCtx.UserID != userID {
			return true
		}

		// 复用 KickConn 的逻辑
		a.KickConn(cCtx.ConnID, reason)
		return true
	})
}

// BroadcastSystemMsg 主动向指定房间下发系统消息（如：房间通知、管理消息）
func (a *App) BroadcastSystemMsg(roomID int64, content string, msgType int32) {
	if roomID <= 0 {
		return
	}

	sysMsg := &pb.SystemMsg{
		Content: content,
		Type:    msgType,
	}
	payload, _ := proto.Marshal(sysMsg)

	outFrame := &pb.Frame{
		Op:        pb.OpType_OP_SYSTEM,
		RoomId:    roomID,
		UserId:    0, // 0 表示系统发出
		Payload:   payload,
		Timestamp: time.Now().UnixMilli(),
	}

	data, _ := proto.Marshal(outFrame)

	envelope := &mq.BroadcastEnvelope{
		RoomId:       roomID,
		UserId:       0,
		SenderConnId: "SYSTEM",
		IsPremium:    true,
		Payload:      data,
		Timestamp:    time.Now().UnixMilli(),
	}

	// 优先写入 Kafka（依靠各节点消费回来实现多机触达）
	if a.Producer != nil {
		if err := a.Producer.Publish(envelope); err != nil {
			metrics.KafkaPublishErrCount.Add(1)
			log.Printf("系统消息发送 Kafka 失败，降级到本机广播: roomID=%d err=%v", roomID, err)
			// Kafka 失败时降级到本机广播
		} else {
			return
		}
	}

	// 退化到单机广播
	if a.RoomManager.HasLocalRoom(roomID) && a.Broadcaster != nil {
		_ = a.Broadcaster.BroadcastLocal(envelope)
	}
}

// BroadcastGlobalSystemMsg 全局广播系统消息（系统公告）
// 直接作用于该节点下的所有连接，下发全局的系统公告。
func (a *App) BroadcastGlobalSystemMsg(content string, msgType int32) {
	sysMsg := &pb.SystemMsg{
		Content: content,
		Type:    msgType,
	}
	payload, _ := proto.Marshal(sysMsg)

	a.AllConns.Range(func(key, value interface{}) bool {
		conn, ok := value.(gnet.Conn)
		if !ok {
			return true
		}

		cCtx, ok := conn.Context().(*connctx.ConnContext)
		if !ok {
			return true
		}

		// 分发到对应 Loop 线程执行发送
		_ = a.LoopDispatcher.Dispatch(cCtx.LoopIdx, func() {
			outFrame := &pb.Frame{
				Op:        pb.OpType_OP_SYSTEM,
				RoomId:    cCtx.RoomID,
				UserId:    0,
				Payload:   payload,
				Timestamp: time.Now().UnixMilli(),
			}

			frameData, _ := proto.Marshal(outFrame)
		_ = conn.AsyncWrite(ws.BuildBinaryFrame(frameData), nil)
		})
		return true
	})
}

// Close 释放服务资源，主要用于优雅停机
func (a *App) Close() {
	log.Println("执行 App 资源释放和停机清理...")
	if a.Broadcaster != nil {
		a.Broadcaster.Stop()
	}
	if a.Producer != nil {
		_ = a.Producer.Close()
		log.Println("Kafka Producer 已关闭")
	}
	if a.Consumer != nil {
		_ = a.Consumer.Close()
		log.Println("Kafka Consumer 已关闭")
	}
	if a.Registrar != nil {
		a.Registrar.Stop()
		log.Println("服务注册已注销")
	}
	if a.MetricsServer != nil {
		a.MetricsServer.Stop()
		log.Println("Metrics HTTP 服务已关闭")
	}
}

// StartMetricsServer 启动 metrics HTTP 服务
func (a *App) StartMetricsServer(addr string) {
	if addr == "" {
		addr = ":9090" // 默认端口 9090
	}
	a.MetricsServer = NewMetricsServer(addr)
	a.MetricsServer.Start()
}

// StartRegistry 启动服务注册
func (a *App) StartRegistry(ctx context.Context) error {
	if a.Registrar == nil {
		return nil
	}
	if err := a.Registrar.Start(ctx); err != nil {
		return fmt.Errorf("start registry: %w", err)
	}
	log.Printf("服务注册成功: nodeId=%s addr=%s:%d", a.Config.NodeId, a.Config.ListenAddr, a.ServiceInfo.Port)
	return nil
}

// GetRegistryServices 获取注册中心中的服务列表
func (a *App) GetRegistryServices(ctx context.Context, serviceName string) ([]*registry.ServiceInfo, error) {
	if a.Registry == nil {
		return nil, nil
	}
	return a.Registry.GetServices(ctx, serviceName)
}


