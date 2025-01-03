package utils

import (
	"sync"
	"time"
)

// RateLimiter 令牌桶限流器
type RateLimiter struct {
	rate       float64    // 每秒生成的令牌数
	capacity   float64    // 桶的容量
	tokens     float64    // 当前令牌数
	lastUpdate time.Time  // 上次更新时间
	mu         sync.Mutex // 互斥锁
}

// NewRateLimiter 创建一个新的限流器
func NewRateLimiter(rate int, capacity int) *RateLimiter {
	return &RateLimiter{
		rate:       float64(rate),
		capacity:   float64(capacity),
		tokens:     float64(capacity),
		lastUpdate: time.Now(),
	}
}

// Allow 是否允许请求
func (l *RateLimiter) Allow() bool {
	l.mu.Lock()
	defer l.mu.Unlock()

	now := time.Now()
	// 计算从上次更新到现在生成的令牌数
	elapsed := now.Sub(l.lastUpdate).Seconds()
	l.tokens = min(l.capacity, l.tokens+elapsed*l.rate)
	l.lastUpdate = now

	if l.tokens >= 1 {
		l.tokens--
		return true
	}
	return false
}

// Wait 等待直到获取到令牌
func (l *RateLimiter) Wait() {
	for !l.Allow() {
		time.Sleep(time.Millisecond * 10)
	}
}

func min(a, b float64) float64 {
	if a < b {
		return a
	}
	return b
}
