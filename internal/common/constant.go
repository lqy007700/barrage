package common

// 弹幕系统常量

const (
	// Kafka Topic
	TopicDanmaku = "barrage-danmaku" // 弹幕消息Topic
	TopicVideoTS = "barrage-video"    // 视频时间戳Topic

	// 采样率配置
	SamplerRateHotLevelSuper = 0.05  // 超级热点: 5% 保留
	SamplerRateHotLevelHot   = 0.10  // 热点: 10% 保留
	SamplerRateHotLevelWarm  = 0.30  // 温热: 30% 保留
	SamplerRateNormal        = 1.00  // 正常: 100% 保留

	// 采样阈值 (按房间连接数)
	HotThresholdSuper = 20000 // 超级热点阈值
	HotThresholdHot   = 5000  // 热点阈值
	HotThresholdWarm  = 1000  // 温热阈值

	// 视频同步配置
	DefaultVideoDelayMs    = 3000  // 默认视频延迟 3秒
	MaxVideoDelayMs        = 10000 // 最大视频延迟
	VideoSyncIntervalMs    = 50    // 视频同步检查间隔 50ms
	MaxBufferPerSession    = 200   // 单用户最大弹幕缓冲数
	BufferOverflowDropOld  = true  // 缓冲区溢出时丢弃最旧的

	// 慢连接降级阈值 (字节)
	SlowConnForceClose = 4 * 1024 * 1024    // 4MB 强制断开
	SlowConnVIPSkip    = 1536 * 1024        // 1.5MB VIP跳过
	SlowConnSkip       = 512 * 1024         // 512KB 普通跳过
	SlowConnSampleSkip = 256 * 1024         // 256KB 抽样跳过

	// Micro-batching 配置
	DefaultBatchIntervalMs = 50   // 默认批次间隔 50ms
	MaxEnvelopesPerBatch   = 15  // 每批次最大消息数 (用于采样)
	MaxPendingPerRoom      = 1000 // 每房间最大pending队列

	// Kafka配置
	KafkaWriteTimeoutSec = 5     // Kafka写入超时秒
	KafkaMinPartitions    = 100   // 最小Partition数
	KafkaReplicationFactor = 3    // 副本数

	// Edge节点配置
	MaxConnPerEdge    = 10000 // 单Edge最大连接数
	MaxRoomsPerEdge   = 500   // 单Edge最大房间数

	// Gateway配置
	GatewayMaxConn = 50000 // Gateway最大连接数 (用于用户接入)
)

// HotConfig 热点采样配置
type HotConfig struct {
	Level       HotLevel
	SampleRate  float32
	Threshold   int32
}

var HotConfigs = []HotConfig{
	{Level: HotLevelSuper, SampleRate: SamplerRateHotLevelSuper, Threshold: HotThresholdSuper},
	{Level: HotLevelHot, SampleRate: SamplerRateHotLevelHot, Threshold: HotThresholdHot},
	{Level: HotLevelWarm, SampleRate: SamplerRateHotLevelWarm, Threshold: HotThresholdWarm},
	{Level: HotLevelNormal, SampleRate: SamplerRateNormal, Threshold: 0},
}

// GetHotConfig 获取热点配置
func GetHotConfig(count int32) HotConfig {
	for _, c := range HotConfigs {
		if count >= c.Threshold {
			return c
		}
	}
	return HotConfigs[len(HotConfigs)-1]
}
