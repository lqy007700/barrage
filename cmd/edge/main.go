package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"

	"barrage/internal/edge"
)

func main() {
	// 命令行参数
	nodeId := flag.String("node-id", "", "Edge node ID (required)")
	addr := flag.String("addr", "0.0.0.0", "Bind address")
	port := flag.Int("port", 8081, "HTTP port")
	kafkaBrokers := flag.String("kafka", "localhost:9092", "Kafka brokers (comma separated)")
	kafkaTopic := flag.String("topic", "barrage-danmaku", "Kafka topic")
	wsAddr := flag.String("ws-addr", "0.0.0.0:8080", "WebSocket listen address")
	flag.Parse()

	if *nodeId == "" {
		fmt.Println("node-id is required")
		os.Exit(1)
	}

	// 解析 Kafka brokers
	brokers := parseBrokers(*kafkaBrokers)

	// 创建 Edge 节点组件
	sessionManager := edge.NewSessionManager()
	roomMap := edge.NewRoomMap()
	videoSync := edge.NewVideoSyncEngine(roomMap, sessionManager)
	consumer := edge.NewConsumer(*nodeId, brokers, *kafkaTopic, videoSync, sessionManager)
	broadcaster := edge.NewEdgeBroadcaster(sessionManager, videoSync, 50*time.Millisecond, 1000)

	// 启动组件
	videoSync.Start()
	broadcaster.StartBatchTicker()

	ctx, cancel := context.WithCancel(context.Background())
	consumer.Start(ctx)

	log.Printf("Edge node %s started", *nodeId)
	log.Printf("  - Kafka: %v", brokers)
	log.Printf("  - Topic: %s", *kafkaTopic)
	log.Printf("  - HTTP: %s:%d", *addr, *port)
	log.Printf("  - WebSocket: %s", *wsAddr)

	// 等待信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Printf("Shutting down...")
	cancel()

	consumer.Stop()
	videoSync.Stop()
	broadcaster.Stop()

	log.Printf("Edge node %s stopped", *nodeId)
}

func parseBrokers(brokers string) []string {
	if brokers == "" {
		return []string{}
	}
	var result []string
	for _, b := range split(brokers, ",") {
		if b := trim(b); b != "" {
			result = append(result, b)
		}
	}
	return result
}

func split(s string, sep string) []string {
	var result []string
	start := 0
	for i := 0; i <= len(s)-len(sep); i++ {
		if s[i:i+len(sep)] == sep {
			result = append(result, s[start:i])
			start = i + len(sep)
			i += len(sep) - 1
		}
	}
	result = append(result, s[start:])
	return result
}

func trim(s string) string {
	start := 0
	end := len(s)
	for start < end && (s[start] == ' ' || s[start] == '\t') {
		start++
	}
	for end > start && (s[end-1] == ' ' || s[end-1] == '\t') {
		end--
	}
	return s[start:end]
}
