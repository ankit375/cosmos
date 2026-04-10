package websocket

import (
	"sync"
	"time"
)

// RateLimiter implements a sliding-window rate limiter using a token bucket.
type RateLimiter struct {
	mu       sync.Mutex
	tokens   float64
	maxTokens float64
	refillRate float64 // tokens per second
	lastRefill time.Time
}

// NewRateLimiter creates a rate limiter with the given max tokens and refill rate.
func NewRateLimiter(maxTokens float64, refillPerSecond float64) *RateLimiter {
	return &RateLimiter{
		tokens:     maxTokens,
		maxTokens:  maxTokens,
		refillRate: refillPerSecond,
		lastRefill: time.Now(),
	}
}

// Allow returns true if a token is available (and consumes it).
func (rl *RateLimiter) Allow() bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	elapsed := now.Sub(rl.lastRefill).Seconds()
	rl.tokens += elapsed * rl.refillRate
	if rl.tokens > rl.maxTokens {
		rl.tokens = rl.maxTokens
	}
	rl.lastRefill = now

	if rl.tokens >= 1 {
		rl.tokens--
		return true
	}
	return false
}

// ConnectionRateLimiter manages per-IP and global connection rate limits.
type ConnectionRateLimiter struct {
	mu            sync.Mutex
	global        *RateLimiter
	perIP         map[string]*RateLimiter
	maxPerIP      float64
	perIPRefill   float64
	cleanupTicker *time.Ticker
	done          chan struct{}
}

// NewConnectionRateLimiter creates a connection rate limiter.
// globalPerSec: max new connections per second globally.
// perIPPerMin: max connections per minute per IP.
func NewConnectionRateLimiter(globalPerSec int, perIPPerMin int) *ConnectionRateLimiter {
	crl := &ConnectionRateLimiter{
		global:      NewRateLimiter(float64(globalPerSec), float64(globalPerSec)),
		perIP:       make(map[string]*RateLimiter),
		maxPerIP:    float64(perIPPerMin),
		perIPRefill: float64(perIPPerMin) / 60.0,
		done:        make(chan struct{}),
	}

	// Periodically clean up stale per-IP limiters
	crl.cleanupTicker = time.NewTicker(5 * time.Minute)
	go func() {
		for {
			select {
			case <-crl.cleanupTicker.C:
				crl.cleanup()
			case <-crl.done:
				return
			}
		}
	}()

	return crl
}

// Allow checks both global and per-IP rate limits.
func (crl *ConnectionRateLimiter) Allow(ip string) bool {
	if !crl.global.Allow() {
		wsRateLimitRejects.WithLabelValues("global_connection").Inc()
		return false
	}

	crl.mu.Lock()
	limiter, ok := crl.perIP[ip]
	if !ok {
		limiter = NewRateLimiter(crl.maxPerIP, crl.perIPRefill)
		crl.perIP[ip] = limiter
	}
	crl.mu.Unlock()

	if !limiter.Allow() {
		wsRateLimitRejects.WithLabelValues("per_ip_connection").Inc()
		return false
	}

	return true
}

func (crl *ConnectionRateLimiter) cleanup() {
	crl.mu.Lock()
	defer crl.mu.Unlock()
	// Simple cleanup: just reset the map if it gets too large
	if len(crl.perIP) > 10000 {
		crl.perIP = make(map[string]*RateLimiter)
	}
}

// Stop stops the background cleanup goroutine.
func (crl *ConnectionRateLimiter) Stop() {
	crl.cleanupTicker.Stop()
	close(crl.done)
}

// MessageRateLimiter manages per-channel message rate limits for a single connection.
type MessageRateLimiter struct {
	control   *RateLimiter
	telemetry *RateLimiter
	bulk      *RateLimiter
}

// NewMessageRateLimiter creates per-channel rate limiters.
func NewMessageRateLimiter(controlPerSec, telemetryPerSec, bulkPerSec int) *MessageRateLimiter {
	return &MessageRateLimiter{
		control:   NewRateLimiter(float64(controlPerSec)*2, float64(controlPerSec)),
		telemetry: NewRateLimiter(float64(telemetryPerSec)*2, float64(telemetryPerSec)),
		bulk:      NewRateLimiter(float64(bulkPerSec)*2, float64(bulkPerSec)),
	}
}

// Allow checks the rate limit for the given channel.
func (mrl *MessageRateLimiter) Allow(channel uint8) bool {
	switch channel {
	case 0x00: // Control
		return mrl.control.Allow()
	case 0x01: // Telemetry
		return mrl.telemetry.Allow()
	case 0x02: // Bulk
		return mrl.bulk.Allow()
	default:
		return false
	}
}
