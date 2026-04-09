package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"barrage/internal/common"
	"barrage/internal/filter"
	"barrage/internal/pb"

	"google.golang.org/protobuf/proto"
)

// Gateway HTTP API网关
// 接收弹幕消息，过滤后写入Kafka
// 注意: WebSocket连接由Edge节点(现有barrage server)处理
type Gateway struct {
	// 配置
	addr string
	port int

	// 组件
	router    *RoomRouter
	hot       *HotDetector
	sampler   *Sampler
	danmaku   *DanmakuProc
	producer  *Producer
	filter    filter.Filter

	// HTTP服务器
	httpServer *http.Server

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc
}

// Config 网关配置
type Config struct {
	Addr        string
	Port        int
	KafkaBrokers []string
	FilterPath  string
}

// NewGateway 创建Gateway
func NewGateway(cfg *Config) (*Gateway, error) {
	ctx, cancel := context.WithCancel(context.Background())

	// 初始化组件
	router := NewRoomRouter()
	hot := NewHotDetector()
	sampler := NewSampler()

	// 初始化敏感词过滤器
	var f filter.Filter
	if cfg.FilterPath != "" {
		f = filter.NewReloadableFilter(cfg.FilterPath, []string{"傻逼", "垃圾"})
	} else {
		f = filter.NewSimpleFilter([]string{})
	}

	danmaku := NewDanmakuProc(f)
	producer := NewProducer(cfg.KafkaBrokers, common.TopicDanmaku)

	g := &Gateway{
		addr:     cfg.Addr,
		port:     cfg.Port,
		router:   router,
		hot:      hot,
		sampler:  sampler,
		danmaku:  danmaku,
		producer: producer,
		filter:   f,
		ctx:      ctx,
		cancel:   cancel,
	}

	// 启动HTTP服务器
	g.startHTTPServer()

	return g, nil
}

// serveHTTP 主HTTP处理函数
func (g *Gateway) serveHTTP(w http.ResponseWriter, r *http.Request) {
	// 路由
	path := strings.TrimSuffix(r.URL.Path, "/")

	switch {
	case path == "/health" && r.Method == http.MethodGet:
		w.Write([]byte("OK"))

	case path == "/stats" && r.Method == http.MethodGet:
		g.handleStats(w, r)

	case path == "/edge/register" && r.Method == http.MethodPost:
		g.handleEdgeRegister(w, r)

	case path == "/edge/unregister" && r.Method == http.MethodPost:
		g.handleEdgeUnregister(w, r)

	case path == "/api/danmaku/publish" && r.Method == http.MethodPost:
		g.handlePublishDanmaku(w, r)

	case path == "/api/video/ts" && r.Method == http.MethodPost:
		g.handleVideoTS(w, r)

	default:
		http.NotFound(w, r)
	}
}

// startHTTPServer 启动HTTP服务器
func (g *Gateway) startHTTPServer() {
	addr := fmt.Sprintf("%s:%d", g.addr, g.port)
	g.httpServer = &http.Server{
		Addr:         addr,
		Handler:      http.HandlerFunc(g.serveHTTP),
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Gateway HTTP server starting on %s", addr)
		if err := g.httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("HTTP Server error: %v", err)
		}
	}()
}

// handleStats 统计信息
func (g *Gateway) handleStats(w http.ResponseWriter, r *http.Request) {
	stats := struct {
		HotRooms    int `json:"hot_rooms"`
		WarmRooms   int `json:"warm_rooms"`
		NormalRooms int `json:"normal_rooms"`
	}{}

	hot, warm, normal := g.hot.GetStats()
	stats.HotRooms = hot
	stats.WarmRooms = warm
	stats.NormalRooms = normal

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(stats)
}

// handleEdgeRegister Edge节点注册
func (g *Gateway) handleEdgeRegister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeId string `json:"node_id"`
		Addr   string `json:"addr"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	g.router.RegisterEdge(req.NodeId, req.Addr)
	w.Write([]byte("OK"))
}

// handleEdgeUnregister Edge节点注销
func (g *Gateway) handleEdgeUnregister(w http.ResponseWriter, r *http.Request) {
	var req struct {
		NodeId string `json:"node_id"`
	}

	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	g.router.UnregisterEdge(req.NodeId)
	w.Write([]byte("OK"))
}

// PublishDanmakuRequest 发布弹幕请求
type PublishDanmakuRequest struct {
	RoomId    int64  `json:"room_id"`
	UserId    int64  `json:"user_id"`
	ConnId    string `json:"conn_id"`
	Content   string `json:"content"`
	VideoTS   int64  `json:"video_ts"`
	IsPremium bool   `json:"is_premium"`
}

// handlePublishDanmaku 处理弹幕发布
func (g *Gateway) handlePublishDanmaku(w http.ResponseWriter, r *http.Request) {
	var req PublishDanmakuRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	// 校验
	if req.RoomId <= 0 {
		http.Error(w, "Invalid room_id", http.StatusBadRequest)
		return
	}
	if req.UserId <= 0 {
		http.Error(w, "Invalid user_id", http.StatusBadRequest)
		return
	}
	if req.Content == "" {
		http.Error(w, "Content empty", http.StatusBadRequest)
		return
	}

	// 敏感词过滤
	if g.filter.Check(req.Content).Hit {
		http.Error(w, "Content filtered", http.StatusForbidden)
		return
	}

	// 更新热点统计
	g.hot.RecordMessage(req.RoomId)

	// 采样判断
	if g.sampler.ShouldDrop(req.RoomId) {
		// 返回成功但实际丢弃
		w.Write([]byte(`{"success":true,"dropped":true}`))
		return
	}

	// 构建Payload
	chatMsg := &pb.ChatMsg{Content: req.Content}
	chatData, err := proto.Marshal(chatMsg)
	if err != nil {
		http.Error(w, "Marshal error", http.StatusInternalServerError)
		return
	}

	// 构建Envelope
	envelope := &pb.BroadcastEnvelope{
		RoomId:       req.RoomId,
		UserId:       req.UserId,
		SenderConnId: req.ConnId,
		IsPremium:    req.IsPremium,
		Payload:      chatData,
		Timestamp:    time.Now().UnixMilli(),
		VideoTs:      req.VideoTS,
	}

	// 写入Kafka
	if err := g.producer.PublishDanmaku(envelope); err != nil {
		log.Printf("Publish error: %v", err)
		http.Error(w, "Publish error", http.StatusInternalServerError)
		return
	}

	w.Write([]byte(`{"success":true}`))
}

// VideoTSRequest 视频时间戳请求
type VideoTSRequest struct {
	RoomId  int64 `json:"room_id"`
	VideoTS int64 `json:"video_ts"`
	SentAt  int64 `json:"sent_at"`
}

// handleVideoTS 处理视频时间戳更新
func (g *Gateway) handleVideoTS(w http.ResponseWriter, r *http.Request) {
	var req VideoTSRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid request", http.StatusBadRequest)
		return
	}

	if req.RoomId <= 0 {
		http.Error(w, "Invalid room_id", http.StatusBadRequest)
		return
	}

	// 视频时间戳通过专门的生产者写入另一个topic
	// 这里简化处理，直接写入日志
	log.Printf("VideoTS update: room=%d ts=%d", req.RoomId, req.VideoTS)

	w.Write([]byte(`{"success":true}`))
}

// Close 关闭Gateway
func (g *Gateway) Close() error {
	g.cancel()

	if g.httpServer != nil {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		g.httpServer.Shutdown(shutdownCtx)
	}

	if g.producer != nil {
		g.producer.Close()
	}

	g.hot.Stop()

	return nil
}
