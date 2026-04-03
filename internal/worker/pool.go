package worker

import "github.com/panjf2000/ants/v2"

// Pool 是对 ants 协程池的简单封装
type Pool struct {
	// ants 协程池实例
	pool *ants.Pool
}

// NewPool 创建协程池
func NewPool(size int) (*Pool, error) {
	if size <= 0 {
		size = 1
	}

	p, err := ants.NewPool(
		size,
		ants.WithPreAlloc(true),
	)
	if err != nil {
		return nil, err
	}

	return &Pool{
		pool: p,
	}, nil
}

// Submit 提交任务到协程池
func (p *Pool) Submit(task func()) error {
	return p.pool.Submit(task)
}

// Running 返回当前正在运行的协程数量
func (p *Pool) Running() int {
	return p.pool.Running()
}

// Free 返回当前空闲协程数量
func (p *Pool) Free() int {
	return p.pool.Free()
}

// Cap 返回协程池容量
func (p *Pool) Cap() int {
	return p.pool.Cap()
}

// Release 释放协程池资源
func (p *Pool) Release() {
	p.pool.Release()
}
