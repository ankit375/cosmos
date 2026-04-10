//go:build integration

package integration

import (
	"context"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/api/handler"
	"github.com/yourorg/cloudctrl/internal/api/middleware"
	"github.com/yourorg/cloudctrl/internal/auth"
	"github.com/yourorg/cloudctrl/internal/command"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/internal/configmgr"
	"github.com/yourorg/cloudctrl/internal/model"
	"github.com/yourorg/cloudctrl/internal/telemetry"
	pgstore "github.com/yourorg/cloudctrl/internal/store/postgres"
	redisstore "github.com/yourorg/cloudctrl/internal/store/redis"
	ws "github.com/yourorg/cloudctrl/internal/websocket"
	"github.com/yourorg/cloudctrl/pkg/logger"
	"go.uber.org/zap"
	"golang.org/x/crypto/bcrypt"
)

var (
	testPG         *pgstore.Store
	testRedis      *redisstore.Store
	testJWT        *auth.JWTService
	testLogger     *zap.Logger
	testRouter     *gin.Engine
	testCfg        *config.Config
	testHub        *ws.Hub
	testConfigMgr  *configmgr.Manager
)

// TestMain sets up and tears down the integration test environment.
func TestMain(m *testing.M) {
	ctx := context.Background()

	// Load test config
	cfg := testConfig()
	testCfg = cfg

	// Logger
	log, err := logger.New("debug", "console", "stdout")
	if err != nil {
		fmt.Fprintf(os.Stderr, "Failed to init logger: %v\n", err)
		os.Exit(1)
	}
	testLogger = log

	// PostgreSQL
	pg, err := pgstore.New(ctx, cfg.Database, log.Named("postgres"))
	if err != nil {
		log.Fatal("failed to connect to test database", zap.Error(err))
	}
	testPG = pg

	// Redis
	rds, err := redisstore.New(ctx, cfg.Redis, log.Named("redis"))
	if err != nil {
		pg.Close()
		log.Fatal("failed to connect to test redis", zap.Error(err))
	}
	testRedis = rds

	// JWT service
	testJWT = auth.NewJWTService(cfg.Auth)

	// WebSocket Hub
	testHub = ws.NewHub(cfg.WebSocket, pg, log.Named("websocket"))

	// Config Manager
	cmCfg := configmgr.ManagerConfig{
		SafeApplyTimeout:  10 * time.Second,
		StabilityDelay:    1 * time.Second,
		ReconcileInterval: 60 * time.Second,
	}
	testConfigMgr = configmgr.NewManager(pg, testHub, cmCfg, log.Named("configmgr"))
	testHub.SetConfigManager(testConfigMgr)

	// Build test router (includes all routes)
	testRouter = buildTestRouter(pg, rds, testJWT, testHub, testConfigMgr, log)

	// Run tests
	code := m.Run()

	// Cleanup
	rds.Close()
	pg.Close()

	os.Exit(code)
}

func testConfig() *config.Config {
	return &config.Config{
		Database: config.DatabaseConfig{
			Host:            envOrDefault("TEST_DB_HOST", "localhost"),
			Port:            5433,
			User:            envOrDefault("TEST_DB_USER", "cloudctrl"),
			Password:        envOrDefault("TEST_DB_PASSWORD", "cloudctrl"),
			DBName:          envOrDefault("TEST_DB_NAME", "cloudctrl_test"),
			SSLMode:         "disable",
			MaxOpenConns:    10,
			MaxIdleConns:    2,
			ConnMaxLifetime: 30 * time.Minute,
		},
		Redis: config.RedisConfig{
			Addr:     envOrDefault("TEST_REDIS_ADDR", "localhost:6379"),
			Password: "cloudctrl_redis_password",
			DB:       1,
		},
		Auth: config.AuthConfig{
			JWTSecret:         "integration-test-secret-key-minimum-32-characters!!",
			JWTExpiry:         15 * time.Minute,
			RefreshExpiry:     24 * time.Hour,
			DeviceTokenLength: 64,
		},
		Log: config.LogConfig{
			Level:  "debug",
			Format: "console",
			Output: "stdout",
		},
		Dev: config.DevConfig{
			CORSAllowAll: true,
		},
		WebSocket: config.WebSocketConfig{
			MaxConnections:       100,
			MaxConnectionsPerIP:  5,
			ConnectionRateLimit:  50,
			WriteWait:            10 * time.Second,
			PongWait:             30 * time.Second,
			PingInterval:         15 * time.Second,
			AuthTimeout:          10 * time.Second,
			ReadBufferSize:       4096,
			WriteBufferSize:      4096,
			SendChannelSize:      64,
			ControlRateLimit:     10,
			TelemetryRateLimit:   5,
			BulkRateLimit:        2,
			StatePersistInterval: 30 * time.Second,
			OfflineCheckInterval: 30 * time.Second,
			HeartbeatTimeout:     90 * time.Second,
		},
	}
}

func envOrDefault(key, defaultVal string) string {
	if val := os.Getenv(key); val != "" {
		return val
	}
	return defaultVal
}

func buildTestRouter(
	pg *pgstore.Store,
	rds *redisstore.Store,
	jwtSvc *auth.JWTService,
	hub *ws.Hub,
	cm *configmgr.Manager,
	log *zap.Logger,
) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	router.Use(middleware.RequestID())
	router.Use(middleware.Recovery(log))

	// Handlers
	healthHandler := handler.NewHealthHandler(pg, rds, log)
	authHandler := handler.NewAuthHandler(pg, rds, jwtSvc, log)
	userHandler := handler.NewUserHandler(pg, rds, log)
	tenantHandler := handler.NewTenantHandler(pg, rds, log)
	siteHandler := handler.NewSiteHandler(pg, rds, log)
	auditHandler := handler.NewAuditHandler(pg, log)
	deviceHandler := handler.NewDeviceHandler(pg, hub, log)
	configHandler := handler.NewConfigHandler(pg, cm, log)

	// Command manager + handler for tests
	cmdCfg := command.ManagerConfig{
		CommandTimeout:       5 * time.Second,
		TimeoutCheckInterval: 1 * time.Second,
		DefaultMaxRetries:    3,
		DefaultPriority:      5,
		DefaultTTL:           5 * time.Minute,
	}
	cmdMgr := command.NewManager(pg, hub, cmdCfg, log.Named("commandmgr"))
	hub.SetCommandManager(cmdMgr)
	commandHandler := handler.NewCommandHandler(pg, cmdMgr, log)

	// Telemetry engine for tests (Phase 7)
	teCfg := telemetry.EngineConfig{
		FlushInterval:         1 * time.Hour, // Don't auto-flush in tests
		BufferCapacity:        1000,
		SessionUpdateInterval: 1 * time.Hour,
		ClientSnapshotTTL:     5 * time.Minute,
	}
	testTelemetryEngine := telemetry.NewEngine(teCfg, pg, rds, log.Named("telemetry"))
	metricsHandler := handler.NewMetricsHandler(pg, testTelemetryEngine, log)

	// Public
	router.GET("/health", healthHandler.Health)

	v1 := router.Group("/api/v1")
	{
		// Auth (public)
		authGroup := v1.Group("/auth")
		{
			authGroup.POST("/login", authHandler.Login)
			authGroup.POST("/refresh", authHandler.Refresh)
		}

		// Authenticated
		authenticated := v1.Group("")
		authenticated.Use(middleware.AuthOrAPIKey(jwtSvc, pg, rds, log))
		authenticated.Use(middleware.Audit(pg, log))
		{
			authProtected := authenticated.Group("/auth")
			{
				authProtected.POST("/logout", authHandler.Logout)
				authProtected.GET("/me", authHandler.Me)
			}

			// Users
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

// ── Test Data Helpers ────────────────────────────────────────

// seedTenant creates a test tenant and returns it.
func seedTenant(t *testing.T, suffix string) *model.Tenant {
	t.Helper()
	ctx := context.Background()

	tenant := &model.Tenant{
		ID:           uuid.New(),
		Name:         fmt.Sprintf("Test Tenant %s", suffix),
		Slug:         fmt.Sprintf("test-%s-%s", suffix, uuid.New().String()[:8]),
		Subscription: "standard",
		MaxDevices:   100,
		MaxSites:     15,
		MaxUsers:     50,
		Active:       true,
		Settings:     map[string]interface{}{},
	}

	err := testPG.Tenants.Create(ctx, tenant)
	if err != nil {
		t.Fatalf("failed to seed tenant: %v", err)
	}
	return tenant
}

// seedTenantWithLimits creates a tenant with custom limits.
func seedTenantWithLimits(t *testing.T, suffix string, maxSites, maxDevices, maxUsers int) *model.Tenant {
	t.Helper()
	ctx := context.Background()

	tenant := &model.Tenant{
		ID:           uuid.New(),
		Name:         fmt.Sprintf("Test Tenant %s", suffix),
		Slug:         fmt.Sprintf("test-%s-%s", suffix, uuid.New().String()[:8]),
		Subscription: "standard",
		MaxDevices:   maxDevices,
		MaxSites:     maxSites,
		MaxUsers:     maxUsers,
		Active:       true,
		Settings:     map[string]interface{}{},
	}

	err := testPG.Tenants.Create(ctx, tenant)
	if err != nil {
		t.Fatalf("failed to seed tenant: %v", err)
	}
	return tenant
}

// Pre-computed bcrypt hash of "testpassword123" (MinCost for test speed)
var testPasswordHash string

func init() {
	hash, err := bcrypt.GenerateFromPassword([]byte("testpassword123"), bcrypt.MinCost)
	if err != nil {
		panic("failed to hash test password: " + err.Error())
	}
	testPasswordHash = string(hash)
}

// seedUser creates a test user and returns it along with the plaintext password.
func seedUser(t *testing.T, tenantID uuid.UUID, role model.UserRole) (*model.User, string) {
	t.Helper()
	ctx := context.Background()

	password := "testpassword123"

	user := &model.User{
		ID:           uuid.New(),
		TenantID:     tenantID,
		Email:        fmt.Sprintf("%s-%s@test.com", role, uuid.New().String()[:8]),
		PasswordHash: testPasswordHash,
		Name:         fmt.Sprintf("Test %s", role),
		Role:         role,
		Active:       true,
	}

	err := testPG.Users.Create(ctx, user)
	if err != nil {
		t.Fatalf("failed to seed user: %v", err)
	}
	return user, password
}

// seedSite creates a test site within a tenant.
func seedSite(t *testing.T, tenantID uuid.UUID, name string) *model.Site {
	t.Helper()
	ctx := context.Background()

	site := &model.Site{
		ID:          uuid.New(),
		TenantID:    tenantID,
		Name:        name,
		Description: "Test site",
		Timezone:    "UTC",
		CountryCode: "US",
		AutoAdopt:   false,
		AutoUpgrade: false,
		Settings:    map[string]interface{}{},
	}

	err := testPG.Sites.Create(ctx, site)
	if err != nil {
		t.Fatalf("failed to seed site: %v", err)
	}
	return site
}

// seedSiteWithAutoAdopt creates a test site with auto_adopt enabled.
func seedSiteWithAutoAdopt(t *testing.T, tenantID uuid.UUID, name string) *model.Site {
	t.Helper()
	ctx := context.Background()

	site := &model.Site{
		ID:          uuid.New(),
		TenantID:    tenantID,
		Name:        name,
		Description: "Auto-adopt test site",
		Timezone:    "UTC",
		CountryCode: "US",
		AutoAdopt:   true,
		AutoUpgrade: false,
		Settings:    map[string]interface{}{},
	}

	err := testPG.Sites.Create(ctx, site)
	if err != nil {
		t.Fatalf("failed to seed site: %v", err)
	}
	return site
}

// seedPendingDevice creates a pending_adopt device in the database.
func seedPendingDevice(t *testing.T, tenantID uuid.UUID) *model.Device {
	t.Helper()
	ctx := context.Background()

	deviceID := uuid.New()
	mac := fmt.Sprintf("AA:BB:CC:%02X:%02X:%02X", deviceID[0], deviceID[1], deviceID[2])

	_, err := testPG.Pool.Exec(ctx, `
		INSERT INTO devices (id, tenant_id, mac, serial, name, model, status, firmware_version, capabilities, system_info)
		VALUES ($1, $2, $3, $4, $5, $6, 'pending_adopt', '1.0.0', '{}', '{}')`,
		deviceID, tenantID, mac,
		"SN-"+deviceID.String()[:8],
		"AP-"+mac[len(mac)-5:],
		"AP-2400-AC",
	)
	if err != nil {
		t.Fatalf("failed to seed pending device: %v", err)
	}

	device, err := testPG.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		t.Fatalf("failed to retrieve seeded device: %v", err)
	}
	return device
}

// loginUser logs in a user and returns the access token.
func loginUser(t *testing.T, email, password string) *model.LoginResponse {
	t.Helper()

	body := fmt.Sprintf(`{"email":"%s","password":"%s"}`, email, password)
	w := performRequest(testRouter, "POST", "/api/v1/auth/login", body, "")

	if w.Code != 200 {
		t.Fatalf("login failed: status=%d body=%s", w.Code, w.Body.String())
	}

	var resp struct {
		Success bool                `json:"success"`
		Data    model.LoginResponse `json:"data"`
	}
	parseJSON(t, w, &resp)

	if !resp.Success {
		t.Fatal("login response success=false")
	}
	if resp.Data.AccessToken == "" {
		t.Fatal("login response missing access token")
	}

	return &resp.Data
}

// cleanupTenant removes all data for a tenant.
func cleanupTenant(t *testing.T, tenantID uuid.UUID) {
	t.Helper()
	ctx := context.Background()


	// Clean up metrics data (Phase 7)
	_, _ = testPG.Pool.Exec(ctx, "DELETE FROM device_metrics WHERE tenant_id = $1", tenantID)
	_, _ = testPG.Pool.Exec(ctx, "DELETE FROM radio_metrics WHERE tenant_id = $1", tenantID)
	_, _ = testPG.Pool.Exec(ctx, "DELETE FROM client_sessions WHERE tenant_id = $1", tenantID)

	// Delete command queue (FK to devices)
	_, _ = testPG.Pool.Exec(ctx, "DELETE FROM command_queue WHERE tenant_id = $1", tenantID)

	// Delete device configs (FK to devices)
	_, _ = testPG.Pool.Exec(ctx, "DELETE FROM device_configs WHERE tenant_id = $1", tenantID)

	// Delete device overrides (FK to devices)
	_, _ = testPG.Pool.Exec(ctx, "DELETE FROM device_overrides WHERE tenant_id = $1", tenantID)

	// Delete config templates (FK to sites)
	_, _ = testPG.Pool.Exec(ctx, "DELETE FROM config_templates WHERE tenant_id = $1", tenantID)

	// Delete devices (FK constraint)
	_, _ = testPG.Pool.Exec(ctx, "DELETE FROM devices WHERE tenant_id = $1", tenantID)

	// Delete sites
	sites, _ := testPG.Sites.List(ctx, tenantID)
	for _, s := range sites {
		_ = testPG.Sites.Delete(ctx, tenantID, s.ID)
	}

	// Delete users
	users, _ := testPG.Users.List(ctx, tenantID)
	for _, u := range users {
		_ = testPG.Users.Delete(ctx, tenantID, u.ID)
	}

	// Delete tenant
	_ = testPG.Tenants.Delete(ctx, tenantID)

	// Flush test Redis DB
	_ = testRedis.Client.FlushDB(ctx).Err()
}

// seedAdoptedDevice creates a device that's already adopted to a site.
func seedAdoptedDevice(t *testing.T, tenantID, siteID uuid.UUID, name string) *model.Device {
	t.Helper()
	ctx := context.Background()

	deviceID := uuid.New()
	mac := fmt.Sprintf("DD:EE:FF:%02X:%02X:%02X", deviceID[0], deviceID[1], deviceID[2])

	_, err := testPG.Pool.Exec(ctx, `
		INSERT INTO devices (id, tenant_id, site_id, mac, serial, name, model, status,
			firmware_version, capabilities, system_info, adopted_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, 'online', '1.0.0',
			'{"bands":["2g","5g"],"max_ssids":16,"wpa3":true,"vlan":true}', '{}', NOW())`,
		deviceID, tenantID, siteID, mac,
		"SN-"+deviceID.String()[:8],
		name,
		"AP-2400-AC",
	)
	if err != nil {
		t.Fatalf("failed to seed adopted device: %v", err)
	}

	device, err := testPG.Devices.GetByID(ctx, tenantID, deviceID)
	if err != nil || device == nil {
		t.Fatalf("failed to retrieve seeded adopted device: %v", err)
	}
	return device
}
