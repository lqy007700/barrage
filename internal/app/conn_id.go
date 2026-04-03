package app

import (
	"strconv"
	"sync/atomic"
)

// connSeq 用于生成进程内递增连接序号
var connSeq atomic.Uint64

// NextConnID 生成连接唯一标识
// 当前版本使用进程内递增序号，便于本机连接生命周期管理
func NextConnID() string {
	id := connSeq.Add(1)
	return strconv.FormatUint(id, 10)
}
