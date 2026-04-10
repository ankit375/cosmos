package api

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/yourorg/cloudctrl/internal/api/handler"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/auth"
	"github.com/yourorg/cloudctrl/internal/command"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/internal/configmgr"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	ws "github.com/yourorg/cloudctrl/internal/websocket"
	"go.uber.org/zap"
	"github.com/yourorg/cloudctrl/internal/telemetry"
)

// App is the main application container.
type App struct {
	cfg        *config.Config
	logger     *zap.Logger
	httpServer *http.Server
	wsServer   *http.Server
	pgStore    *pgstore.Store
	redisStore *redisstore.Store
	jwtService *auth.JWTService
	hub        *ws.Hub
	configMgr  *configmgr.Manager
	commandMgr *command.Manager
	telemetryEngine *telemetry.Engine
}

// NewApp creates and wires up the entire application.
func NewApp(ctx context.Context, cfg *config.Config, logger *zap.Logger) (*App, error) {
	app := &App{
		cfg:    cfg,
		logger: logger,
	}

	// Initialize PostgreSQL
	logger.Info("connecting to PostgreSQL...")
	pg, err := pgstore.New(ctx, cfg.Database, logger.Named("postgres"))
	if err != nil {
		return nil, fmt.Errorf("init postgres: %w", err)
	}
	app.pgStore = pg

	// Initialize Redis
	logger.Info("connecting to Redis...")
	rds, err := redisstore.New(ctx, cfg.Redis, logger.Named("redis"))
	if err != nil {
		pg.Close()
		return nil, fmt.Errorf("init redis: %w", err)
	}
	app.redisStore = rds

	// Initialize JWT service
	app.jwtService = auth.NewJWTService(cfg.Auth)

	// Initialize WebSocket Hub
	app.hub = ws.NewHub(cfg.WebSocket, pg, logger.Named("websocket"))

	// Load device state from DB
	if err := app.hub.LoadStateFromDB(ctx); err != nil {
		logger.Warn("failed to load device state from DB (non-fatal)", zap.Error(err))
	}

	// Build HTTP router
	router := app.buildRouter()

	// Create HTTP server
	app.httpServer = &http.Server{
		Addr:         cfg.Server.HTTPAddr,
		Handler:      router,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: cfg.Server.WriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// Create WebSocket server (TLS)
	wsRouter := app.buildWSRouter()
	app.wsServer = &http.Server{
		Addr:         cfg.Server.WSAddr,
		Handler:      wsRouter,
		ReadTimeout:  cfg.Server.ReadTimeout,
		WriteTimeout: 0, // No write timeout for WebSocket
		IdleTimeout:  0, // No idle timeout for WebSocket
	}

	// Initialize Config Manager
	cmCfg := configmgr.DefaultManagerConfig()
	app.configMgr = configmgr.NewManager(pg, app.hub, cmCfg, logger.Named("configmgr"))

	// Wire config manager into hub
	app.hub.SetConfigManager(app.configMgr)

	// Initialize Command Manager
	cmdCfg := command.DefaultManagerConfig()
	app.commandMgr = command.NewManager(pg, app.hub, cmdCfg, logger.Named("commandmgr"))

	// Wire command manager into hub
	app.hub.SetCommandManager(app.commandMgr)

	// Initialize Telemetry Engine (Phase 7)
	teCfg := telemetry.EngineConfig{
		FlushInterval:         cfg.Metrics.FlushInterval,
		BufferCapacity:        cfg.Metrics.BatchSize,
		SessionUpdateInterval: 120 * time.Second,
		ClientSnapshotTTL:     5 * time.Minute,
	}
	if teCfg.FlushInterval == 0 {
		teCfg = telemetry.DefaultEngineConfig()
	}
	app.telemetryEngine = telemetry.NewEngine(teCfg, pg, rds, logger.Named("telemetry"))

	// Wire telemetry engine into hub
	app.hub.SetTelemetryEngine(app.telemetryEngine)

	// Wire state updater so telemetry can update in-memory device state
	app.telemetryEngine.SetStateUpdateFn(func(deviceID uuid.UUID, cpu float64, memUsed uint64, memTotal uint64, clientCount int, lastMetrics time.Time) {
		app.hub.StateStore().Update(deviceID, func(state *ws.DeviceState) {
			state.CPUUsage = cpu
			state.MemoryUsed = memUsed
			state.MemoryTotal = memTotal
			state.ClientCount = clientCount
			state.LastMetrics = lastMetrics
			state.Dirty = true
		})
	})

	if cfg.TLS.Enabled {
		tlsConfig := &tls.Config{
			MinVersion: tls.VersionTLS12,
			CipherSuites: []uint16{
				tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256,
				tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384,
				tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256,
			},
		}
		app.wsServer.TLSConfig = tlsConfig
	}

	return app, nil
}

// buildRouter creates the main HTTP API router.
func (a *App) buildRouter() *gin.Engine {
	if a.cfg.Log.Level != "debug" {
		gin.SetMode(gin.ReleaseMode)
	}

	router := gin.New()

	// Global middleware
	router.Use(middleware.RequestID())
	router.Use(middleware.Logger(a.logger))
	router.Use(middleware.Recovery(a.logger))
	router.Use(middleware.CORS(a.cfg.Dev.CORSAllowAll))
	router.Use(middleware.Metrics())
	router.Use(middleware.PerIPRateLimit(200))

	// ── Handlers ─────────────────────────────────────────────
	healthHandler := handler.NewHealthHandler(a.pgStore, a.redisStore, a.logger)
	authHandler := handler.NewAuthHandler(a.pgStore, a.redisStore, a.jwtService, a.logger)
	userHandler := handler.NewUserHandler(a.pgStore, a.redisStore, a.logger)
	tenantHandler := handler.NewTenantHandler(a.pgStore, a.redisStore, a.logger)
	siteHandler := handler.NewSiteHandler(a.pgStore, a.redisStore, a.logger)
	auditHandler := handler.NewAuditHandler(a.pgStore, a.logger)
	deviceHandler := handler.NewDeviceHandler(a.pgStore, a.hub, a.logger)
	configHandler := handler.NewConfigHandler(a.pgStore, a.configMgr, a.logger)
	commandHandler := handler.NewCommandHandler(a.pgStore, a.commandMgr, a.logger)
	metricsHandler := handler.NewMetricsHandler(a.pgStore, a.telemetryEngine, a.logger)

	// ── Public endpoints (no auth) ───────────────────────────
	router.GET("/health", healthHandler.Health)
	router.GET("/ready", healthHandler.Ready)
	router.GET("/metrics", gin.WrapH(promhttp.Handler()))

	// ── API v1 ───────────────────────────────────────────────
	v1 := router.Group("/api/v1")
	{
		// System (no auth)
		system := v1.Group("/system")
		{
			system.GET("/health", healthHandler.DetailedHealth)
			system.GET("/info", healthHandler.Info)
		}

		// Auth (no auth required for login/refresh)
		authGroup := v1.Group("/auth")
		{
			authGroup.POST("/login", authHandler.Login)
			authGroup.POST("/refresh", authHandler.Refresh)
		}

		// ── Authenticated routes ─────────────────────────────
		authenticated := v1.Group("")
		authenticated.Use(middleware.AuthOrAPIKey(a.jwtService, a.pgStore, a.redisStore, a.logger))
		authenticated.Use(middleware.PerUserRateLimit(100))
		authenticated.Use(middleware.Audit(a.pgStore, a.logger))
		{
			// Auth (authenticated)
			authProtected := authenticated.Group("/auth")
			{
				authProtected.POST("/logout", authHandler.Logout)
				authProtected.GET("/me", authHandler.Me)
			}

			// Users (admin only for create/update/delete)
			users := authenticated.Group("/users")
			{
				users.GET("", middleware.RequireViewer(), userHandler.List)
				users.GET("/:id", middleware.RequireViewer(), userHandler.Get)
				users.POST("", middleware.RequireAdmin(), userHandler.Create)
				users.PUT("/:id", middleware.RequireAdmin(), userHandler.Update)
				users.DELETE("/:id", middleware.RequireAdmin(), userHandler.Delete)
				users.PUT("/:id/password", userHandler.ChangePassword)
				users.POST("/:id/api-key", middleware.RequireAdmin(), userHandler.GenerateAPIKey)
			}

			// Tenants (super_admin only)
			tenants := authenticated.Group("/tenants")
			tenants.Use(middleware.RequireSuperAdmin())
			{
				tenants.GET("", tenantHandler.List)
				tenants.POST("", tenantHandler.Create)
				tenants.GET("/:id", tenantHandler.Get)
				tenants.PUT("/:id", tenantHandler.Update)
				tenants.DELETE("/:id", tenantHandler.Delete)
				tenants.GET("/:id/limits", tenantHandler.GetLimits)
			}

			// Sites
			sites := authenticated.Group("/sites")
			{
				sites.GET("", middleware.RequireViewer(), siteHandler.List)
				sites.POST("", middleware.RequireAdmin(), siteHandler.Create)
				sites.GET("/:id", middleware.RequireViewer(), siteHandler.Get)
				sites.PUT("/:id", middleware.RequireOperator(), siteHandler.Update)
				sites.DELETE("/:id", middleware.RequireAdmin(), siteHandler.Delete)
				sites.GET("/:id/stats", middleware.RequireViewer(), siteHandler.Stats)

				// Site Config
				sites.GET("/:id/config", middleware.RequireViewer(), configHandler.GetSiteConfig)
				sites.PUT("/:id/config", middleware.RequireOperator(), configHandler.UpdateSiteConfig)
				sites.GET("/:id/config/history", middleware.RequireViewer(), configHandler.GetSiteConfigHistory)
				sites.POST("/:id/config/rollback", middleware.RequireOperator(), configHandler.RollbackSiteConfig)
				sites.POST("/:id/config/validate", middleware.RequireOperator(), configHandler.ValidateSiteConfig)
				
				// Site Metrics (Phase 7)
				sites.GET("/:id/metrics", middleware.RequireViewer(), metricsHandler.GetSiteMetrics)
				sites.GET("/:id/clients", middleware.RequireViewer(), metricsHandler.GetSiteClients)
			}

			// Devices
			devices := authenticated.Group("/devices")
			{
				devices.GET("", middleware.RequireViewer(), deviceHandler.List)
				devices.GET("/pending", middleware.RequireViewer(), deviceHandler.ListPending)
				devices.GET("/stats", middleware.RequireViewer(), deviceHandler.Stats)
				devices.GET("/:id", middleware.RequireViewer(), deviceHandler.Get)
				devices.PUT("/:id", middleware.RequireOperator(), deviceHandler.Update)
				devices.DELETE("/:id", middleware.RequireAdmin(), deviceHandler.Delete)
				devices.POST("/:id/adopt", middleware.RequireOperator(), deviceHandler.Adopt)
				devices.POST("/:id/move", middleware.RequireOperator(), deviceHandler.Move)
				devices.GET("/:id/status", middleware.RequireViewer(), deviceHandler.LiveStatus)

				// Device Config
				devices.GET("/:id/config", middleware.RequireViewer(), configHandler.GetDeviceConfig)
				devices.GET("/:id/config/overrides", middleware.RequireViewer(), configHandler.GetDeviceOverrides)
				devices.PUT("/:id/config/overrides", middleware.RequireOperator(), configHandler.UpdateDeviceOverrides)
				devices.DELETE("/:id/config/overrides", middleware.RequireOperator(), configHandler.DeleteDeviceOverrides)
				devices.GET("/:id/config/history", middleware.RequireViewer(), configHandler.GetDeviceConfigHistory)
				devices.POST("/:id/config/rollback", middleware.RequireOperator(), configHandler.RollbackDeviceConfig)
				devices.POST("/:id/config/push", middleware.RequireOperator(), configHandler.ForcePushDeviceConfig)

				// Device Commands (Phase 6)
				devices.POST("/:id/reboot", middleware.RequireOperator(), commandHandler.Reboot)
				devices.POST("/:id/locate", middleware.RequireOperator(), commandHandler.Locate)
				devices.POST("/:id/kick-client", middleware.RequireOperator(), commandHandler.KickClient)
				devices.POST("/:id/scan", middleware.RequireOperator(), commandHandler.Scan)
				devices.GET("/:id/commands", middleware.RequireViewer(), commandHandler.ListCommands)

				// Device Metrics (Phase 7)
				devices.GET("/:id/metrics", middleware.RequireViewer(), metricsHandler.GetDeviceMetrics)
				devices.GET("/:id/metrics/radio", middleware.RequireViewer(), metricsHandler.GetDeviceRadioMetrics)
				devices.GET("/:id/clients", middleware.RequireViewer(), metricsHandler.GetDeviceClients)
				devices.GET("/:id/clients/history", middleware.RequireViewer(), metricsHandler.GetDeviceClientHistory)
			}

			// Audit
			audit := authenticated.Group("/audit")
			{
				audit.GET("", middleware.RequireAdmin(), auditHandler.List)
			}
		}
	}

	return router
}

// buildWSRouter creates the WebSocket server router.
func (a *App) buildWSRouter() *gin.Engine {
	gin.SetMode(gin.ReleaseMode)
	router := gin.New()
	router.Use(middleware.Recovery(a.logger))

	router.GET("/ws/device", a.hub.HandleWebSocket)

	return router
}

// Start starts all servers.
func (a *App) Start() error {
	// Start the hub
	a.hub.Run()

	// Start the config manager
	a.configMgr.Start()

	// Recover command queues from DB
	if err := a.commandMgr.RecoverOnStartup(context.Background()); err != nil {
		a.logger.Warn("failed to recover command queues (non-fatal)", zap.Error(err))
	}

	// Start the command manager
	a.commandMgr.Start()

	// Start the telemetry engine
	a.telemetryEngine.Start()

	go func() {
		a.logger.Info("starting HTTP API server", zap.String("addr", a.cfg.Server.HTTPAddr))
		if err := a.httpServer.ListenAndServe(); err != http.ErrServerClosed {
			a.logger.Fatal("HTTP server error", zap.Error(err))
		}
	}()

	go func() {
		if a.cfg.TLS.Enabled {
			a.logger.Info("starting WebSocket server (TLS)",
				zap.String("addr", a.cfg.Server.WSAddr),
				zap.String("cert", a.cfg.TLS.ServerCert),
			)
			if err := a.wsServer.ListenAndServeTLS(
				a.cfg.TLS.ServerCert, a.cfg.TLS.ServerKey,
			); err != http.ErrServerClosed {
				a.logger.Fatal("WebSocket server error", zap.Error(err))
			}
		} else {
			a.logger.Info("starting WebSocket server (no TLS)",
				zap.String("addr", a.cfg.Server.WSAddr),
			)
			if err := a.wsServer.ListenAndServe(); err != http.ErrServerClosed {
				a.logger.Fatal("WebSocket server error", zap.Error(err))
			}
		}
	}()

	return nil
}

// Stop gracefully shuts down all servers and connections.
func (a *App) Stop(ctx context.Context) error {
	a.logger.Info("shutting down servers...")

	// Stop accepting new HTTP requests
	if err := a.httpServer.Shutdown(ctx); err != nil {
		a.logger.Error("HTTP server shutdown error", zap.Error(err))
	}

	// Stop accepting new WebSocket connections
	if err := a.wsServer.Shutdown(ctx); err != nil {
		a.logger.Error("WebSocket server shutdown error", zap.Error(err))
	}

	// Stop command manager
	a.commandMgr.Stop()

	// Stop config manager
	a.configMgr.Stop()

	// Stop telemetry engine (flushes remaining data)
	a.telemetryEngine.Stop()

	// Drain WebSocket connections and stop hub
	a.hub.Stop(ctx)

	// Close stores
	if err := a.redisStore.Close(); err != nil {
		a.logger.Error("Redis close error", zap.Error(err))
	}

	a.pgStore.Close()

	a.logger.Info("all servers stopped")
	return nil
}

// Hub returns the WebSocket hub (for testing or external access).
func (a *App) Hub() *ws.Hub {
	return a.hub
}
