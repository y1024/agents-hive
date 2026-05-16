package tools

import (
	"fmt"
	"sync"
	"time"

	"github.com/chef-guo/agents-hive/internal/errs"
)

// ReadTracker 跟踪文件的最后读取时间，防止基于过期内容的编辑
type ReadTracker struct {
	reads      map[string]time.Time
	mu         sync.RWMutex
	staleAfter time.Duration
}

// NewReadTracker 创建新的 ReadTracker
func NewReadTracker(staleAfter time.Duration) *ReadTracker {
	if staleAfter == 0 {
		staleAfter = 5 * time.Minute // 默认 5 分钟后过期
	}
	return &ReadTracker{
		reads:      make(map[string]time.Time),
		staleAfter: staleAfter,
	}
}

// RecordRead 记录文件读取
func (t *ReadTracker) RecordRead(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reads[path] = time.Now()
}

// CheckRead 检查文件是否最近读取过
func (t *ReadTracker) CheckRead(path string) error {
	t.mu.RLock()
	defer t.mu.RUnlock()

	readTime, ok := t.reads[path]
	if !ok {
		return errs.New(errs.CodeInvalidInput, fmt.Sprintf("file %q has not been read - please read it first before editing", path))
	}

	// 检查是否过期
	if time.Since(readTime) > t.staleAfter {
		return errs.New(errs.CodeInvalidInput, fmt.Sprintf("file %q read is stale (>%v ago) - please re-read before editing", path, t.staleAfter))
	}

	return nil
}

// Clear 清除所有读取记录
func (t *ReadTracker) Clear() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.reads = make(map[string]time.Time)
}

// GetReads 返回所有读取记录的副本
func (t *ReadTracker) GetReads() map[string]time.Time {
	t.mu.RLock()
	defer t.mu.RUnlock()
	result := make(map[string]time.Time, len(t.reads))
	for k, v := range t.reads {
		result[k] = v
	}
	return result
}

// RemoveRead 删除特定文件的读取记录
func (t *ReadTracker) RemoveRead(path string) {
	t.mu.Lock()
	defer t.mu.Unlock()
	delete(t.reads, path)
}

// RemoveReadTrackerPath 删除全局读记录；用于上下文压缩丢弃 read 结果后强制后续写入重新读取。
func RemoveReadTrackerPath(path string) {
	if globalReadTracker == nil {
		return
	}
	globalReadTracker.RemoveRead(path)
}
