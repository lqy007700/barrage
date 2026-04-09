# Barrage 弹幕系统学习指南

## 项目概述

分布式弹幕实时广播系统，支撑 2000+ 直播间、400万用户规模。

**技术栈**：Go / gnet / Kafka / Consul / Etcd / Prometheus

**架构特点**：
- 多节点对等部署 + Kafka 解耦
- gnet 事件驱动 (Epoll)
- 50ms Micro-batching 批处理
- 64分片 Mutex 降低锁竞争
- 自适应采样 + 慢连接降级

---

## 学习路径

### 第一阶段：基础概念 (1-2天)

#### 1.1 弹幕系统简介
- 弹幕是什么：滚动字幕/评论叠加在视频上
- 弹幕 vs 普通聊天：消息量大、实时性高、允许丢失
- 弹幕同步问题：视频时间戳对齐

#### 1.2 WebSocket 协议
- 握手流程 (HTTP Upgrade)
- 帧格式：Opcode, Mask, Payload
- Protobuf vs JSON 序列化

**学习资源**：
- RFC 6455 (WebSocket 协议)
- Protobuf 官方文档

**验证**：
```bash
# 启动服务后，用浏览器控制台连接
ws = new WebSocket("ws://localhost:9000")
ws.onopen = () => console.log("connected")
```

---

### 第二阶段：网络模型 (2-3天)

#### 2.1 阻塞 IO vs 非阻塞 IO

| 模型 | 特点 | 代表 |
|------|------|------|
| 阻塞 IO | 每连接一个线程/协程 | Go net |
| IO 多路复用 | 单线程监听多连接 | epoll/kqueue |
| 事件驱动 | 回调函数处理事件 | gnet |

**关键概念**：
- epoll_wait() - 等待 IO 就绪
- 阻塞 IO 的 goroutine 开销
- 事件循环的工作方式

#### 2.2 gnet 框架

**核心接口**：
```go
type EventHandler struct {
    gnet.BuiltinEventEngine
    App *App
}

func (h *EventHandler) OnBoot(eng gnet.Engine) gnet.Action
func (h *EventHandler) OnOpen(c gnet.Conn) ([]byte, gnet.Action)
func (h *EventHandler) OnTraffic(c gnet.Conn) gnet.Action
func (h *EventHandler) OnClose(c gnet.Conn, err error) gnet.Action
func (h *EventHandler) OnTick() (time.Duration, gnet.Action)
```

**gnet 注意事项**：
1. OnTick 不能做耗时操作（会阻塞事件循环）
2. 多线程安全：回调可能在任意线程执行
3. 连接上下文通过 c.SetContext() / c.Context() 管理

**验证**：
```bash
# 查看代码
cat internal/server/event_handler.go
# 理解 OnOpen → OnTraffic → OnClose 的流程
```

---

### 第三阶段：房间管理 (1-2天)

#### 3.1 多层分片架构

```
Manager (bucket级分片)
    ↓ 按 roomId Hash
Room (loop级分片)
    ↓
Shard[loopIdx] (map[connID]Conn)
```

**核心代码**：
```go
// internal/room/manager.go
type Room struct {
    Shards []map[string]gnet.Conn  // 按 loop 分片
    ActiveLoops atomic.Uint64        // 位图：哪些 loop 有连接
}

// 位图快速获取活跃 loop
func (r *Room) ActiveLoopIndexes() []int {
    bitmap := r.ActiveLoops.Load()
    // bits.TrailingZeros64 找出最低位的 1
}
```

#### 3.2 位图索引

用 uint64 位图标记哪些 loop 有该房间的连接：
- 第 i 位 = 1 表示 loop i 有连接
- O(1) 判断是否有活跃连接
- O(k) 获取所有活跃节点 (k = 1的个数)

**学习要点**：
- atomic.Uint64 的使用
- 位运算操作

---

### 第四阶段：广播系统 (2-3天)

#### 4.1 50ms Micro-batching

**问题**：每条消息单独发送，syscall 太多

**解决**：
```go
// 消息先入 pending 队列
shard.pending[roomId] = append(shard.pending[roomId], msg)

// 50ms 后一次性处理所有消息
ticker := time.NewTicker(50 * time.Millisecond)
for {
    <-ticker.C
    flushMap := shard.pending
    shard.pending = make(map[int64][]*mq.BroadcastEnvelope)
    // 处理 flushMap...
}
```

#### 4.2 MegaFrame 合并

**问题**：每条消息一次 TCP 写入效率低

**解决**：
```go
var megaFrame []byte
for _, env := range envelopes {
    megaFrame = append(megaFrame, ws.BuildBinaryFrame(env.Payload)...)
}
conn.AsyncWrite(megaFrame, nil)  // 一次发送所有消息
```

#### 4.3 64分片 Mutex

**问题**：单 mutex 高并发成为瓶颈

**解决**：
```go
const shardCount = 64
shardIdx := roomId % shardCount  // 按房间选择分片
shard := &b.shards[shardIdx]
shard.mu.Lock()
```

#### 4.4 自适应采样

**问题**：超级房间 (20万用户) 带宽爆炸

**解决**：
```go
if len(envelopes) > MaxEnvelopesPerBatch {
    // VIP 全部保留
    // 普通消息匀速抽样
    // 只发 15 条
}
```

**学习要点**：
- 消息密度 = 50ms 内收到多少条
- 采样率动态调整

---

### 第五阶段：Kafka 消息队列 (1-2天)

#### 5.1 Kafka 核心概念

| 概念 | 说明 |
|------|------|
| Topic | 消息主题 |
| Partition | 物理分区，顺序保证 |
| Consumer Group | 消费组，每条消息只被一个组员消费 |
| Offset | 消费进度 |

**消息流向**：
```
Producer → Kafka Topic → Consumer Group (各节点独立消费)
```

#### 5.2 消息分类与幂等

```go
// 普通弹幕：不重试，失败丢弃
if msg.MsgType == MsgTypeDanmaku {
    // 无重试
}

// 礼物/道具：3次幂等重试
if msg.MsgType > MsgTypeDanmaku {
    // 使用 idempotency_key 作为 Kafka Message Key
    // Kafka 自动去重
}
```

#### 5.3 Consumer Group 负载均衡

每节点独立的 Consumer Group，Kafka 自动分配分区：
- 消息被均衡分发到各节点
- 节点间无重复消费

---

### 第六阶段：服务治理 (1-2天)

#### 6.1 服务注册发现

```go
// internal/registry/service.go
type Registry interface {
    Register(ctx context.Context, info *ServiceInfo) error
    GetServices(ctx context.Context, serviceName string) ([]*ServiceInfo, error)
}
```

**支持类型**：
- StaticRegistry - 测试用
- ConsulRegistry - Consul 实现
- EtcdRegistry - Etcd 实现

#### 6.2 优雅关闭

```go
// Consumer 优雅关闭
func (c *Consumer) Close() {
    close(c.stopCh)  // 通知消费循环
    // 等待当前消息处理完成
}
```

---

### 第七阶段：连接治理 (1-2天)

#### 7.1 三层限制

| 限制 | 配置 | 作用 |
|------|------|------|
| 节点总连接数 | MaxConns = 10万 | 防单节点过载 |
| 单用户连接数 | MaxConnsPerUser = 3 | 防多挂 |
| 发消息频率 | MsgRatePerUser = 10条/秒 | 防刷屏 |

#### 7.2 Token Bucket 限速

```go
type RateLimiter struct {
    users    map[int64]*userBucket
    rate     int   // 每秒 token 数
    capacity int   // 桶容量
}

type userBucket struct {
    tokens    float64  // 剩余 token
    lastUpdate int64   // 上次更新时间
}

// Allow: token >= 1 才允许发送
```

---

## 调试技巧

### 1. 日志断点

```go
log.Printf("用户加入房间成功: roomID=%d userID=%d", roomID, userID)
```

### 2. pprof 性能分析

```bash
# 启用 pprof
import _ "net/http/pprof"
go http.ListenAndServe(":6060", nil)

# 查看 goroutine
curl http://localhost:6060/debug/pprof/goroutine?debug=1
```

### 3. Kafka 消息验证

```bash
# 查看消费进度
kafka-consumer-groups.sh --bootstrap-server localhost:9092 --group barrage-* --describe
```

---

## 推荐阅读

### 核心技术

| 文章 | 链接 |
|------|------|
| gnet 官方文档 | https://github.com/panjf2000/gnet |
| Kafka Go 客户端 | https://github.com/segmentio/kafka-go |
| Epoll 原理 | 《Linux 高性能服务器编程》 |

### 项目代码顺序

```
1. cmd/barrage/main.go          → 入口，了解启动流程
2. internal/server/event_handler.go → 网络事件处理
3. internal/app/app.go         → 核心调度
4. internal/room/manager.go    → 房间管理
5. internal/broadcast/broadcaster.go → 广播系统
6. internal/mq/producer.go / consumer.go → Kafka
7. internal/registry/service.go → 服务注册
```

---

## 学习检查清单

- [ ] 理解 Epoll 与阻塞 IO 的区别
- [ ] 能画出声单体和分布式的架构图
- [ ] 理解 50ms Micro-batching 和 MegaFrame 的关系
- [ ] 理解 64 分片 Mutex 的作用
- [ ] 理解自适应采样的逻辑
- [ ] 理解 Token Bucket 限速原理
- [ ] 能启动服务并测试 WebSocket 连接
- [ ] 能追踪一条消息的完整生命周期
