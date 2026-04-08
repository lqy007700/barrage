package main

import (
	"flag"
	"log"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"barrage/internal/gateway"
)

func main() {
	// 命令行参数
	addr := flag.String("addr", "0.0.0.0", "Gateway bind address")
	port := flag.Int("port", 8080, "Gateway HTTP port")
	kafkaBrokers := flag.String("kafka", "localhost:9092", "Kafka brokers (comma separated)")
	filterPath := flag.String("filter", "", "Sensitive words filter file path")
	flag.Parse()

	// 创建Gateway
	gw, err := gateway.NewGateway(&gateway.Config{
		Addr:        *addr,
		Port:        *port,
		KafkaBrokers: parseBrokers(*kafkaBrokers),
		FilterPath:  *filterPath,
	})
	if err != nil {
		log.Fatalf("Create gateway error: %v", err)
	}

	log.Printf("Gateway started")

	// 等待信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	log.Printf("Shutting down...")
	gw.Close()
	log.Printf("Gateway stopped")
}

func parseBrokers(brokers string) []string {
	if brokers == "" {
		return []string{}
	}
	var result []string
	for _, b := range strings.Split(brokers, ",") {
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
