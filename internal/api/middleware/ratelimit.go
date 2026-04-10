package middleware

import (
	"fmt"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/yourorg/cloudctrl/internal/api/response"
)

type rateLimiterEntry struct {
	tokens    float64
	lastCheck time.Time
}

// RateLimiter implements a token bucket rate limiter.
type RateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimiterEntry
	rate    float64
	burst   int
	cleanup time.Duration
}

// NewRateLimiter creates a new rate limiter.
func NewRateLimiter(rate float64, burst int) *RateLimiter {
	rl := &RateLimiter{
		entries: make(map[string]*rateLimiterEntry),
		rate:    rate,
		burst:   burst,
		cleanup: 10 * time.Minute,
	}
	go rl.cleanupLoop()
	return rl
}

func (rl *RateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rl.cleanup)
	defer ticker.Stop()
	for range ticker.C {
		rl.mu.Lock()
		now := time.Now()
		for key, entry := range rl.entries {
			if now.Sub(entry.lastCheck) > rl.cleanup {
				delete(rl.entries, key)
			}
		}
		rl.mu.Unlock()
	}
}

// Allow checks if a request from the given key is allowed.
func (rl *RateLimiter) Allow(key string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	entry, exists := rl.entries[key]

	if !exists {
		rl.entries[key] = &rateLimiterEntry{
			tokens:    float64(rl.burst) - 1,
			lastCheck: now,
		}
		return true
	}

	elapsed := now.Sub(entry.lastCheck).Seconds()
	entry.tokens += elapsed * rl.rate
	if entry.tokens > float64(rl.burst) {
		entry.tokens = float64(rl.burst)
	}
	entry.lastCheck = now

	if entry.tokens < 1 {
		return false
	}

	entry.tokens--
	return true
}

// PerIPRateLimit creates per-IP rate limiting middleware.
func PerIPRateLimit(maxPerMinute int) gin.HandlerFunc {
	limiter := NewRateLimiter(float64(maxPerMinute)/60.0, maxPerMinute)
	return func(c *gin.Context) {
		if !limiter.Allow(c.ClientIP()) {
			response.RespondRateLimit(c)
			c.Abort()
			return
		}
		c.Next()
	}
}

// PerUserRateLimit creates per-user rate limiting middleware.
// Must be placed AFTER auth middleware.
func PerUserRateLimit(maxPerMinute int) gin.HandlerFunc {
	limiter := NewRateLimiter(float64(maxPerMinute)/60.0, maxPerMinute)
	return func(c *gin.Context) {
		userID := GetUserID(c)
		key := userID.String()
		if key == "00000000-0000-0000-0000-000000000000" {
			key = "ip:" + c.ClientIP()
		}

		if !limiter.Allow(key) {
			c.Header("Retry-After", "60")
			response.RespondRateLimit(c)
			c.Abort()
			return
		}

		c.Header("X-RateLimit-Limit", fmt.Sprintf("%d", maxPerMinute))
		c.Next()
	}
}
