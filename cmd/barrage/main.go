package main

import (
	"barrage/internal/app"
	"barrage/internal/config"
	"barrage/internal/server"
	"context"
	"github.com/panjf2000/gnet/v2"
	"log"
)

func main() {
	cfg := config.Load()

	application, err := app.New(cfg)
	if err != nil {
		log.Fatalf("创建应用失败: %v", err)
	}

	application.StartTextFilterReload(context.Background())

	// 启动 Kafka 消费者
	application.StartConsumer(context.Background())

	handler := &server.EventHandler{
		App: application,
	}

	log.Printf("弹幕服务启动中，监听地址: %s", cfg.ListenAddr)

	err = gnet.Run(
		handler,
		cfg.ListenAddr,
		gnet.WithMulticore(true),
		gnet.WithReusePort(true),
	)
	if err != nil {
		log.Fatalf("启动 gnet 服务失败: %v", err)
	}
}
