package filter

import (
	"context"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// ReloadableFilter 表示支持从文件加载并热更新的敏感词过滤器。
type ReloadableFilter struct {
	path string

	current atomic.Pointer[SimpleFilter]

	mu           sync.Mutex
	lastModTime  time.Time
	lastFileSize int64
}

// NewReloadableFilter 创建可热更新的敏感词过滤器。
func NewReloadableFilter(path string, fallbackWords []string) *ReloadableFilter {
	rf := &ReloadableFilter{
		path: path,
	}
	rf.current.Store(NewSimpleFilter(fallbackWords))
	return rf
}

// Check 检查文本是否命中敏感词。
func (f *ReloadableFilter) Check(text string) Result {
	if f == nil {
		return Result{Original: text}
	}

	current := f.current.Load()
	if current == nil {
		return Result{Original: text}
	}

	return current.Check(text)
}

// LoadNow 立即从文件加载一次敏感词词库。
func (f *ReloadableFilter) LoadNow() error {
	if f == nil || f.path == "" {
		return nil
	}

	f.mu.Lock()
	defer f.mu.Unlock()

	return f.loadLocked(true)
}

// StartAutoReload 启动基于轮询的热更新。
func (f *ReloadableFilter) StartAutoReload(ctx context.Context, interval time.Duration) {
	if f == nil || f.path == "" || interval <= 0 {
		return
	}

	ticker := time.NewTicker(interval)
	go func() {
		defer ticker.Stop()

		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if err := f.reloadIfChanged(); err != nil {
					log.Printf("敏感词词库热更新失败: path=%s err=%v", f.path, err)
				}
			}
		}
	}()
}

func (f *ReloadableFilter) reloadIfChanged() error {
	f.mu.Lock()
	defer f.mu.Unlock()

	info, err := os.Stat(f.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}

	if info.ModTime().Equal(f.lastModTime) && info.Size() == f.lastFileSize {
		return nil
	}

	return f.loadLocked(false)
}

func (f *ReloadableFilter) loadLocked(force bool) error {
	info, err := os.Stat(f.path)
	if err != nil {
		return err
	}

	if !force && info.ModTime().Equal(f.lastModTime) && info.Size() == f.lastFileSize {
		return nil
	}

	words, err := LoadWordsFromFile(f.path)
	if err != nil {
		return err
	}

	f.current.Store(NewSimpleFilter(words))
	f.lastModTime = info.ModTime()
	f.lastFileSize = info.Size()

	log.Printf("敏感词词库加载成功: path=%s words=%d", f.path, len(words))
	return nil
}
