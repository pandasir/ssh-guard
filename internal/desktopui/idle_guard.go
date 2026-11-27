package desktopui

import (
	"sync"
	"time"
)

const (
	DefaultIdleTimeout = 30 * time.Minute
	DefaultMaxLifetime = 8 * time.Hour
	defaultIdleTick    = 5 * time.Second
)

// IdleGuard enforces desktop vault auto-lock on inactivity or max session lifetime,
// matching the 30-minute idle / 8-hour max-lifetime security policy of internal/auth.
type IdleGuard struct {
	idleTimeout time.Duration
	maxLifetime time.Duration
	tickEvery   time.Duration
	nowFn       func() time.Time

	mu           sync.Mutex
	active       bool
	unlockedAt   time.Time
	lastActiveAt time.Time
	stopCh       chan struct{}
}

func NewIdleGuard(idleTimeout, maxLifetime time.Duration) *IdleGuard {
	if idleTimeout <= 0 {
		idleTimeout = DefaultIdleTimeout
	}
	if maxLifetime <= 0 {
		maxLifetime = DefaultMaxLifetime
	}
	return &IdleGuard{
		idleTimeout: idleTimeout,
		maxLifetime: maxLifetime,
		tickEvery:   defaultIdleTick,
		nowFn:       time.Now,
	}
}

func (g *IdleGuard) now() time.Time {
	if g.nowFn != nil {
		return g.nowFn()
	}
	return time.Now()
}

// Start begins monitoring inactivity and max lifetime from now.
// When expired, onExpire is invoked once with a human-readable reason.
func (g *IdleGuard) Start(onExpire func(reason string)) {
	g.mu.Lock()
	if g.stopCh != nil {
		close(g.stopCh)
	}
	now := g.now()
	g.active = true
	g.unlockedAt = now
	g.lastActiveAt = now
	stopCh := make(chan struct{})
	g.stopCh = stopCh
	tickEvery := g.tickEvery
	g.mu.Unlock()

	if tickEvery <= 0 {
		tickEvery = defaultIdleTick
	}
	go func() {
		ticker := time.NewTicker(tickEvery)
		defer ticker.Stop()
		for {
			select {
			case <-stopCh:
				return
			case <-ticker.C:
				if expired, reason := g.CheckNow(); expired {
					if onExpire != nil {
						onExpire(reason)
					}
					return
				}
			}
		}
	}()
}

// Touch refreshes the last-active timestamp while unlocked.
func (g *IdleGuard) Touch() {
	g.mu.Lock()
	if g.active {
		g.lastActiveAt = g.now()
	}
	g.mu.Unlock()
}

// CheckNow evaluates whether either idle timeout or max lifetime has elapsed.
// When expired, it marks the guard inactive and stops its background ticker.
func (g *IdleGuard) CheckNow() (bool, string) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.active {
		return false, ""
	}
	now := g.now()
	if now.Sub(g.lastActiveAt) >= g.idleTimeout {
		g.stopLocked()
		return true, "空闲超过 30 分钟，已自动锁定凭证库"
	}
	if now.Sub(g.unlockedAt) >= g.maxLifetime {
		g.stopLocked()
		return true, "会话达到 8 小时最长有效期，已自动锁定凭证库"
	}
	return false, ""
}

// Stop halts monitoring immediately.
func (g *IdleGuard) Stop() {
	g.mu.Lock()
	g.stopLocked()
	g.mu.Unlock()
}

func (g *IdleGuard) stopLocked() {
	g.active = false
	if g.stopCh != nil {
		close(g.stopCh)
		g.stopCh = nil
	}
}
