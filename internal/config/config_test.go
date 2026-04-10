package config

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadConfig(t *testing.T) {
	// Create temporary config file
	content := `
server:
  http_addr: ":9080"
  ws_addr: ":9443"
  max_devices: 500

database:
  host: "testhost"
  port: 5432
  user: "testuser"
  password: "testpassword"
  dbname: "testdb"
  sslmode: "disable"

redis:
  addr: "localhost:6379"
  password: "testredis"

auth:
  jwt_secret: "test-secret-that-is-at-least-32-characters-long!!"
  jwt_expiry: 12h

log:
  level: "debug"
  format: "console"
`
	tmpFile, err := os.CreateTemp("", "config-*.yaml")
	require.NoError(t, err)
	defer os.Remove(tmpFile.Name())

	_, err = tmpFile.WriteString(content)
	require.NoError(t, err)
	tmpFile.Close()

	cfg, err := Load(tmpFile.Name())
	require.NoError(t, err)

	assert.Equal(t, ":9080", cfg.Server.HTTPAddr)
	assert.Equal(t, ":9443", cfg.Server.WSAddr)
	assert.Equal(t, 500, cfg.Server.MaxDevices)
	assert.Equal(t, "testhost", cfg.Database.Host)
	assert.Equal(t, "testuser", cfg.Database.User)
	assert.Equal(t, "debug", cfg.Log.Level)

	// Test DSN generation
	dsn := cfg.Database.DSN()
	assert.Contains(t, dsn, "testhost")
	assert.Contains(t, dsn, "testuser")
	assert.Contains(t, dsn, "testdb")
}

func TestValidateConfig(t *testing.T) {
	tests := []struct {
		name    string
		modify  func(*Config)
		wantErr string
	}{
		{
			name:    "valid config",
			modify:  func(c *Config) {},
			wantErr: "",
		},
		{
			name: "missing db host",
			modify: func(c *Config) {
				c.Database.Host = ""
			},
			wantErr: "database.host is required",
		},
		{
			name: "short jwt secret",
			modify: func(c *Config) {
				c.Auth.JWTSecret = "short"
			},
			wantErr: "auth.jwt_secret must be at least 32 characters",
		},
		{
			name: "zero max devices",
			modify: func(c *Config) {
				c.Server.MaxDevices = 0
			},
			wantErr: "server.max_devices must be at least 1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
			tt.modify(cfg)
			err := cfg.Validate()
			if tt.wantErr == "" {
				assert.NoError(t, err)
			} else {
				assert.ErrorContains(t, err, tt.wantErr)
			}
		})
	}
}

func validConfig() *Config {
	return &Config{
		Server: ServerConfig{
			HTTPAddr:   ":8080",
			MaxDevices: 1000,
		},
		Database: DatabaseConfig{
			Host:     "localhost",
			Port:     5432,
			User:     "user",
			Password: "password",
			DBName:   "testdb",
		},
		Auth: AuthConfig{
			JWTSecret: "a-very-long-secret-that-is-at-least-32-characters",
		},
	}
}