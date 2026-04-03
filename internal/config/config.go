package config

import "runtime"

// Config 表示应用配置
type Config struct {
	// 服务监听地址
	ListenAddr string

	// WorkerPoolSize 表示业务协程池大小
	WorkerPoolSize int

	// 是否启用 Kafka
	EnableKafka bool

	// Kafka Brokers 列表
	KafkaBrokers []string

	// Kafka Topic 名称
	KafkaTopic string

	// Kafka 消费组 ID
	KafkaGroupID string
}

// Load 返回默认配置
func Load() *Config {
	return &Config{
		ListenAddr:     "tcp://0.0.0.0:9000",
		WorkerPoolSize: runtime.NumCPU() * 1024,

		// 本地开发默认关闭 Kafka
		EnableKafka: false,
		KafkaBrokers: []string{
			"127.0.0.1:9092",
		},
		KafkaTopic:   "barrage-broadcast",
		KafkaGroupID: "barrage-node-1",
	}
}
