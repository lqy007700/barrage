package app

import (
	"fmt"
	"io"
	"log"
	"net/http"
	"time"

	"barrage/internal/metrics"
)

// MetricsServer HTTP 服务，用于暴露 Prometheus 监控指标
type MetricsServer struct {
	addr   string
	server *http.Server
}

// NewMetricsServer 创建 metrics HTTP 服务器
func NewMetricsServer(addr string) *MetricsServer {
	return &MetricsServer{
		addr: addr,
	}
}

// Start 启动 HTTP 服务器
func (s *MetricsServer) Start() {
	mux := http.NewServeMux()
	mux.HandleFunc("/metrics", s.handleMetrics)
	mux.HandleFunc("/health", s.handleHealth)

	s.server = &http.Server{
		Addr:         s.addr,
		Handler:      mux,
		ReadTimeout:  5 * time.Second,
		WriteTimeout: 10 * time.Second,
	}

	go func() {
		log.Printf("Metrics HTTP 服务启动: %s", s.addr)
		if err := s.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Printf("Metrics HTTP 服务错误: %v", err)
		}
	}()
}

// Stop 停止 HTTP 服务器
func (s *MetricsServer) Stop() {
	if s.server != nil {
		_ = s.server.Close()
	}
}

// handleMetrics 处理 Prometheus metrics 请求
func (s *MetricsServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")

	// 输出 Counter 类型指标
	outputCounter(w, "barrage_broadcast_dispatch_total", "广播派发总次数", metrics.BroadcastDispatchCount.Load())
	outputCounter(w, "barrage_broadcast_dispatch_errors_total", "广播派发失败总数", metrics.BroadcastDispatchErrCount.Load())
	outputCounter(w, "barrage_filter_reject_total", "敏感词触发驳回总数", metrics.FilterRejectCount.Load())
	outputCounter(w, "barrage_kafka_publish_errors_total", "Kafka生产失败总数", metrics.KafkaPublishErrCount.Load())
	outputCounter(w, "barrage_kafka_consume_errors_total", "Kafka消费失败总数", metrics.KafkaConsumeErrCount.Load())
	outputCounter(w, "barrage_slow_conn_skip_total", "慢连接跳过下发总数", metrics.SlowConnSkipCount.Load())
	outputCounter(w, "barrage_rate_limit_reject_total", "频率限制驳回总数", metrics.RateLimitRejectCount.Load())
}

// outputCounter 输出 Prometheus Counter 格式
func outputCounter(w io.Writer, name, help string, value int64) {
	fmt.Fprintf(w, "# HELP %s %s\n", name, help)
	fmt.Fprintf(w, "# TYPE %s counter\n", name)
	fmt.Fprintf(w, "%s %d\n\n", name, value)
}

// handleHealth 处理健康检查请求
func (s *MetricsServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.Write([]byte(`{"status":"ok"}`))
}
