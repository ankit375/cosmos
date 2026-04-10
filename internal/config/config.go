package config

import (
	"fmt"
	"strings"
	"time"

	"github.com/spf13/viper"
)

type Config struct {
	Server     ServerConfig     `mapstructure:"server"`
	Database   DatabaseConfig   `mapstructure:"database"`
	Redis      RedisConfig      `mapstructure:"redis"`
	Auth       AuthConfig       `mapstructure:"auth"`
	TLS        TLSConfig        `mapstructure:"tls"`
	Firmware   FirmwareConfig   `mapstructure:"firmware"`
	Metrics    MetricsConfig    `mapstructure:"metrics"`
	Monitoring MonitoringConfig `mapstructure:"monitoring"`
	Log        LogConfig        `mapstructure:"log"`
	Dev        DevConfig        `mapstructure:"dev"`
	WebSocket  WebSocketConfig  `mapstructure:"websocket"`
}

type ServerConfig struct {
	HTTPAddr      string        `mapstructure:"http_addr"`
	WSAddr        string        `mapstructure:"ws_addr"`
	FirmwareAddr  string        `mapstructure:"firmware_addr"`
	ReadTimeout   time.Duration `mapstructure:"read_timeout"`
	WriteTimeout  time.Duration `mapstructure:"write_timeout"`
	MaxDevices    int           `mapstructure:"max_devices"`
	ShutdownGrace time.Duration `mapstructure:"shutdown_grace"`
}

type DatabaseConfig struct {
	Host            string        `mapstructure:"host"`
	Port            int           `mapstructure:"port"`
	User            string        `mapstructure:"user"`
	Password        string        `mapstructure:"password"`
	DBName          string        `mapstructure:"dbname"`
	SSLMode         string        `mapstructure:"sslmode"`
	MaxOpenConns    int           `mapstructure:"max_open_conns"`
	MaxIdleConns    int           `mapstructure:"max_idle_conns"`
	ConnMaxLifetime time.Duration `mapstructure:"conn_max_lifetime"`
}

// New config block — add after MetricsConfig
type WebSocketConfig struct {
	// Connection limits
	MaxConnections       int           `mapstructure:"max_connections"`
	MaxConnectionsPerIP  int           `mapstructure:"max_connections_per_ip"`
	ConnectionRateLimit  int           `mapstructure:"connection_rate_limit"`  // per second

	// Timeouts
	WriteWait            time.Duration `mapstructure:"write_wait"`
	PongWait             time.Duration `mapstructure:"pong_wait"`
	PingInterval         time.Duration `mapstructure:"ping_interval"`
	AuthTimeout          time.Duration `mapstructure:"auth_timeout"`

	// Buffers
	ReadBufferSize       int           `mapstructure:"read_buffer_size"`
	WriteBufferSize      int           `mapstructure:"write_buffer_size"`
	SendChannelSize      int           `mapstructure:"send_channel_size"`

	// Message rate limits (per second)
	ControlRateLimit     int           `mapstructure:"control_rate_limit"`
	TelemetryRateLimit   int           `mapstructure:"telemetry_rate_limit"`
	BulkRateLimit        int           `mapstructure:"bulk_rate_limit"`

	// Workers
	StatePersistInterval time.Duration `mapstructure:"state_persist_interval"`
	OfflineCheckInterval time.Duration `mapstructure:"offline_check_interval"`
	HeartbeatTimeout     time.Duration `mapstructure:"heartbeat_timeout"`
}

func (d DatabaseConfig) DSN() string {
	return fmt.Sprintf(
		"postgres://%s:%s@%s:%d/%s?sslmode=%s",
		d.User, d.Password, d.Host, d.Port, d.DBName, d.SSLMode,
	)
}

type RedisConfig struct {
	Addr     string `mapstructure:"addr"`
	Password string `mapstructure:"password"`
	DB       int    `mapstructure:"db"`
}

type AuthConfig struct {
	JWTSecret         string        `mapstructure:"jwt_secret"`
	JWTExpiry         time.Duration `mapstructure:"jwt_expiry"`
	RefreshExpiry     time.Duration `mapstructure:"refresh_expiry"`
	DeviceTokenLength int           `mapstructure:"device_token_length"`
}

type TLSConfig struct {
	CACert     string `mapstructure:"ca_cert"`
	ServerCert string `mapstructure:"server_cert"`
	ServerKey  string `mapstructure:"server_key"`
	Enabled    bool   `mapstructure:"enabled"`
}

type FirmwareConfig struct {
	StorageType    string `mapstructure:"storage_type"`
	MinioEndpoint  string `mapstructure:"minio_endpoint"`
	MinioAccessKey string `mapstructure:"minio_access_key"`
	MinioSecretKey string `mapstructure:"minio_secret_key"`
	MinioBucket    string `mapstructure:"minio_bucket"`
	MinioUseSSL    bool   `mapstructure:"minio_use_ssl"`
	LocalPath      string `mapstructure:"local_path"`
}

type MetricsConfig struct {
	RetentionDays         int           `mapstructure:"retention_days"`
	FlushInterval         time.Duration `mapstructure:"flush_interval"`
	BatchSize             int           `mapstructure:"batch_size"`
	SessionUpdateInterval time.Duration `mapstructure:"session_update_interval"`
	ClientSnapshotTTL     time.Duration `mapstructure:"client_snapshot_ttl"`
}

type MonitoringConfig struct {
	HeartbeatTimeout time.Duration `mapstructure:"heartbeat_timeout"`
	CheckInterval    time.Duration `mapstructure:"check_interval"`
	AlertWebhook     string        `mapstructure:"alert_webhook"`
}

type LogConfig struct {
	Level  string `mapstructure:"level"`
	Format string `mapstructure:"format"`
	Output string `mapstructure:"output"`
}

type DevConfig struct {
	SeedAdminEmail    string `mapstructure:"seed_admin_email"`
	SeedAdminPassword string `mapstructure:"seed_admin_password"`
	AutoAdopt         bool   `mapstructure:"auto_adopt"`
	CORSAllowAll      bool   `mapstructure:"cors_allow_all"`
}

func Load(path string) (*Config, error) {
	v := viper.New()
	v.SetConfigFile(path)
	v.SetConfigType("yaml")

	// Environment variable support
	v.SetEnvPrefix("CLOUDCTRL")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Defaults
	setDefaults(v)

	if err := v.ReadInConfig(); err != nil {
		return nil, fmt.Errorf("read config file: %w", err)
	}

	var cfg Config
	if err := v.Unmarshal(&cfg); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, fmt.Errorf("validate config: %w", err)
	}

	return &cfg, nil
}

func setDefaults(v *viper.Viper) {
	// Server
	v.SetDefault("server.http_addr", ":8080")
	v.SetDefault("server.ws_addr", ":8443")
	v.SetDefault("server.firmware_addr", ":8090")
	v.SetDefault("server.read_timeout", "30s")
	v.SetDefault("server.write_timeout", "30s")
	v.SetDefault("server.max_devices", 1000)
	v.SetDefault("server.shutdown_grace", "15s")

	// Database
	v.SetDefault("database.host", "localhost")
	v.SetDefault("database.port", 5432)
	v.SetDefault("database.sslmode", "disable")
	v.SetDefault("database.max_open_conns", 25)
	v.SetDefault("database.max_idle_conns", 5)
	v.SetDefault("database.conn_max_lifetime", "1h")

	// Redis
	v.SetDefault("redis.addr", "localhost:6379")
	v.SetDefault("redis.db", 0)

	// Auth
	v.SetDefault("auth.jwt_expiry", "24h")
	v.SetDefault("auth.refresh_expiry", "720h")
	v.SetDefault("auth.device_token_length", 64)

	// TLS
	v.SetDefault("tls.enabled", false)

	// Firmware
	v.SetDefault("firmware.storage_type", "minio")
	v.SetDefault("firmware.minio_bucket", "firmware")
	v.SetDefault("firmware.minio_use_ssl", false)

	// Metrics
	v.SetDefault("metrics.retention_days", 90)
	v.SetDefault("metrics.flush_interval", "10s")
	v.SetDefault("metrics.batch_size", 1000)

	// Monitoring
	v.SetDefault("monitoring.heartbeat_timeout", "90s")
	v.SetDefault("monitoring.check_interval", "30s")

	// Log
	v.SetDefault("log.level", "info")
	v.SetDefault("log.format", "console")
	v.SetDefault("log.output", "stdout")

	// WebSocket
	v.SetDefault("websocket.max_connections", 1000)
	v.SetDefault("websocket.max_connections_per_ip", 5)
	v.SetDefault("websocket.connection_rate_limit", 50)
	v.SetDefault("websocket.write_wait", "10s")
	v.SetDefault("websocket.pong_wait", "30s")
	v.SetDefault("websocket.ping_interval", "15s")
	v.SetDefault("websocket.auth_timeout", "10s")
	v.SetDefault("websocket.read_buffer_size", 4096)
	v.SetDefault("websocket.write_buffer_size", 4096)
	v.SetDefault("websocket.send_channel_size", 64)
	v.SetDefault("websocket.control_rate_limit", 10)
	v.SetDefault("websocket.telemetry_rate_limit", 5)
	v.SetDefault("websocket.bulk_rate_limit", 2)
	v.SetDefault("websocket.state_persist_interval", "30s")
	v.SetDefault("websocket.offline_check_interval", "30s")
	v.SetDefault("websocket.heartbeat_timeout", "90s")

	// Metrics (update existing defaults)
	v.SetDefault("metrics.retention_days", 7)
	v.SetDefault("metrics.flush_interval", "10s")
	v.SetDefault("metrics.batch_size", 2000)
	v.SetDefault("metrics.session_update_interval", "120s")
	v.SetDefault("metrics.client_snapshot_ttl", "5m")
}

func (c *Config) Validate() error {
	if c.Database.Host == "" {
		return fmt.Errorf("database.host is required")
	}
	if c.Database.User == "" {
		return fmt.Errorf("database.user is required")
	}
	if c.Database.Password == "" {
		return fmt.Errorf("database.password is required")
	}
	if c.Database.DBName == "" {
		return fmt.Errorf("database.dbname is required")
	}
	if c.Auth.JWTSecret == "" {
		return fmt.Errorf("auth.jwt_secret is required")
	}
	if len(c.Auth.JWTSecret) < 32 {
		return fmt.Errorf("auth.jwt_secret must be at least 32 characters")
	}
	if c.Server.MaxDevices < 1 {
		return fmt.Errorf("server.max_devices must be at least 1")
	}
	return nil
}
