package config

import (
	"fmt"
	"math/rand"
	"os"
	"runtime"
	"time"
)

// Config 表示应用配置
type Config struct {
	// 服务监听地址
	ListenAddr string

	// WorkerPoolSize 表示业务协程池大小
	WorkerPoolSize int

	// SensitiveWordsPath 表示敏感词词库文件路径
	SensitiveWordsPath string

	// SensitiveWordsReloadInterval 表示敏感词词库热更新检查间隔
	SensitiveWordsReloadInterval time.Duration

	// 是否启用 Kafka
	EnableKafka bool

	// Kafka Brokers 列表
	KafkaBrokers []string

	// Kafka Topic 名称
	KafkaTopic string

	// Kafka 消费组 ID
	KafkaGroupID string

	// AuthToken 认证 Token，为空则不校验
	AuthToken string

	// BannedUsers 封禁用户 ID 列表
	BannedUsers []int64

	// Registry 注册中心配置
	RegistryType string // "static", "consul", "etcd"
	RegistryAddr string // 注册中心地址
	NodeId       string // 节点唯一ID
	ServiceName  string // 服务名称

	// Metrics HTTP 服务地址
	MetricsAddr string // 默认 :9090

	// 连接限制配置
	MaxConns         int // 节点最大连接数，默认 10万
	MaxConnsPerUser  int // 单用户最大连接数，默认 3

	// 消息频率限制
	MsgRatePerUser   int // 单用户每秒允许消息数，默认 10
	MsgBurstCapacity int // 消息突发容量，默认 20
}

// Load 返回默认配置
func Load() *Config {
	// 【核心逻辑】：保证在横向扩容（多机/多 Pod 部署）时，每一台单机都拥有独立的 Kafka GroupID！
	// 这样 Kafka 才会把每一条广播分别送达每一台网关节点，从而实现"级联放大广播"，绝不能写死。
	// 使用 hostname + 随机后缀 确保唯一性，避免 PID 重用导致消息重放
	hostname, _ := os.Hostname()
	randSuffix := rand.Int63()
	uniqueGroupID := fmt.Sprintf("barrage-%s-%d", hostname, randSuffix)

	// 生成唯一节点ID
	nodeId := fmt.Sprintf("barrage-%s-%d", hostname, os.Getpid())

	// 协程池大小计算：CPU核数 * 4，限制最大 2048，避免创建过多协程
	workerPoolSize := runtime.NumCPU() * 4
	if workerPoolSize > 2048 {
		workerPoolSize = 2048
	}

	return &Config{
		ListenAddr:                   "tcp://0.0.0.0:9000",
		WorkerPoolSize:               workerPoolSize,
		SensitiveWordsPath:           "configs/sensitive_words.txt",
		SensitiveWordsReloadInterval: 2 * time.Second,

		// 本地开发默认关闭 Kafka
		EnableKafka: false,
		KafkaBrokers: []string{
			"127.0.0.1:9092",
		},
		KafkaTopic:   "barrage-broadcast",
		KafkaGroupID: uniqueGroupID,

		// Registry 配置
		RegistryType: "static",
		RegistryAddr: "",
		NodeId:       nodeId,
		ServiceName:  "barrage",

		// Metrics HTTP 服务
		MetricsAddr: ":9090",

		// 连接限制配置
		MaxConns:        100000, // 10万连接
		MaxConnsPerUser: 3,      // 单用户最多3个连接

		// 消息频率限制
		MsgRatePerUser:   10, // 每秒10条
		MsgBurstCapacity: 20, // 突发容量20条
	}
}
