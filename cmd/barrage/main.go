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

	// 启动服务注册 (Consul/Etcd)
	ctx := context.Background()
	if err := application.StartRegistry(ctx); err != nil {
		log.Fatalf("启动服务注册失败: %v", err)
	}

	application.StartTextFilterReload(ctx)

	// 启动 Kafka 消费者
	application.StartConsumer(ctx)

	// 启动心跳检测清理僵尸连接
	application.StartHeartbeatCheck(ctx)

	// 启动空闲房间定时回收（设置阀值为空闲180秒）
	application.RoomManager.StartRoomCleaner(ctx, 180)

	// 启动 Metrics HTTP 服务 (Prometheus 拉取指标)
	application.StartMetricsServer(cfg.MetricsAddr)

	handler := &server.EventHandler{
		App: application,
	}

	log.Printf("弹幕服务启动中，监听地址: %s", cfg.ListenAddr)
	log.Printf("节点ID: %s, Registry: %s://%s", cfg.NodeId, cfg.RegistryType, cfg.RegistryAddr)

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
