package handler

import (
	"net/http"
	"runtime"
	"time"

	"github.com/gin-gonic/gin"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	"go.uber.org/zap"
)

var startTime = time.Now()

// HealthHandler handles health check endpoints.
type HealthHandler struct {
	pg     *pgstore.Store
	redis  *redisstore.Store
	logger *zap.Logger
}

// NewHealthHandler creates a new HealthHandler.
func NewHealthHandler(pg *pgstore.Store, redis *redisstore.Store, logger *zap.Logger) *HealthHandler {
	return &HealthHandler{
		pg:     pg,
		redis:  redis,
		logger: logger,
	}
}

// Health is a simple liveness check.
func (h *HealthHandler) Health(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status": "ok",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// Ready is a readiness check — verifies all dependencies.
func (h *HealthHandler) Ready(c *gin.Context) {
	ctx := c.Request.Context()

	// Check PostgreSQL
	if err := h.pg.Health(ctx); err != nil {
		h.logger.Warn("readiness check failed: database", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"error":  "database unavailable",
		})
		return
	}

	// Check Redis
	if err := h.redis.Health(ctx); err != nil {
		h.logger.Warn("readiness check failed: redis", zap.Error(err))
		c.JSON(http.StatusServiceUnavailable, gin.H{
			"status": "not ready",
			"error":  "redis unavailable",
		})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"status": "ready",
		"time":   time.Now().UTC().Format(time.RFC3339),
	})
}

// DetailedHealth returns detailed health information.
func (h *HealthHandler) DetailedHealth(c *gin.Context) {
	ctx := c.Request.Context()

	overallStatus := "healthy"
	checks := make(map[string]interface{})

	// Database check
	dbStart := time.Now()
	dbErr := h.pg.Health(ctx)
	dbLatency := time.Since(dbStart)

	dbCheck := gin.H{
		"status":     "healthy",
		"latency_ms": dbLatency.Milliseconds(),
	}
	if dbErr != nil {
		dbCheck["status"] = "unhealthy"
		dbCheck["error"] = dbErr.Error()
		overallStatus = "unhealthy"
	}

	// Add pool stats
	dbStats := h.pg.Stats()
	dbCheck["pool"] = gin.H{
		"total_conns":       dbStats.TotalConns(),
		"idle_conns":        dbStats.IdleConns(),
		"acquired_conns":    dbStats.AcquiredConns(),
		"constructing_conns": dbStats.ConstructingConns(),
		"max_conns":         dbStats.MaxConns(),
	}
	checks["database"] = dbCheck

	// Redis check
	redisStart := time.Now()
	redisErr := h.redis.Health(ctx)
	redisLatency := time.Since(redisStart)

	redisCheck := gin.H{
		"status":     "healthy",
		"latency_ms": redisLatency.Milliseconds(),
	}
	if redisErr != nil {
		redisCheck["status"] = "unhealthy"
		redisCheck["error"] = redisErr.Error()
		overallStatus = "unhealthy"
	}
	checks["redis"] = redisCheck

	// Runtime info
	var memStats runtime.MemStats
	runtime.ReadMemStats(&memStats)

	c.JSON(http.StatusOK, gin.H{
		"status": overallStatus,
		"uptime": time.Since(startTime).String(),
		"checks": checks,
		"runtime": gin.H{
			"goroutines":     runtime.NumGoroutine(),
			"memory_alloc":   memStats.Alloc,
			"memory_sys":     memStats.Sys,
			"gc_cycles":      memStats.NumGC,
			"go_version":     runtime.Version(),
			"num_cpu":        runtime.NumCPU(),
		},
	})
}

// Info returns version and build information.
func (h *HealthHandler) Info(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"name":       "cloudctrl",
		"version":    "0.1.0",
		"go_version": runtime.Version(),
		"os":         runtime.GOOS,
		"arch":       runtime.GOARCH,
		"uptime":     time.Since(startTime).String(),
		"started_at": startTime.UTC().Format(time.RFC3339),
	})
}