package connctx

// ConnContext 用于保存连接级别的上下文信息
type ConnContext struct {
	// 连接唯一标识
	ConnID string

	// 用户 ID
	UserID int64

	// 房间 ID
	RoomID int64

	// 当前连接所属的 sub-reactor 下标
	LoopIdx int

	// 是否为付费用户
	IsPremium bool

	// 是否已经完成 WebSocket 握手
	HandshakeDone bool

	// 握手阶段读缓冲
	HandshakeBuffer []byte

	// WebSocket 数据阶段读缓冲
	ReadBuffer []byte
}
