package redis

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/redis/go-redis/v9"
	"github.com/yourorg/cloudctrl/internal/config"
	"go.uber.org/zap"
)

// Store holds the Redis client.
type Store struct {
	Client *redis.Client
	logger *zap.Logger
}

// New creates a new Redis store.
func New(ctx context.Context, cfg config.RedisConfig, logger *zap.Logger) (*Store, error) {
	client := redis.NewClient(&redis.Options{
		Addr:            cfg.Addr,
		Password:        cfg.Password,
		DB:              cfg.DB,
		MaxRetries:      3,
		MinRetryBackoff: 100 * time.Millisecond,
		MaxRetryBackoff: 3 * time.Second,
		DialTimeout:     5 * time.Second,
		ReadTimeout:     3 * time.Second,
		WriteTimeout:    3 * time.Second,
		PoolSize:        20,
		MinIdleConns:    5,
		PoolTimeout:     4 * time.Second,
	})

	// Verify connectivity
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("ping redis: %w", err)
	}

	// Log Redis info
	info, err := client.Info(ctx, "server").Result()
	if err == nil {
		logger.Info("connected to redis", zap.String("addr", cfg.Addr))
		_ = info // Could parse redis version from info
	}

	return &Store{
		Client: client,
		logger: logger,
	}, nil
}

// Close closes the Redis connection.
func (s *Store) Close() error {
	s.logger.Info("closing redis connection")
	return s.Client.Close()
}

// Health checks Redis connectivity.
func (s *Store) Health(ctx context.Context) error {
	ctx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()

	if err := s.Client.Ping(ctx).Err(); err != nil {
		return fmt.Errorf("redis ping failed: %w", err)
	}
	return nil
}
// ── Token Blacklist (for JWT logout) ─────────────────────────

const tokenBlacklistPrefix = "blacklist:token:"

// BlacklistToken adds a token to the blacklist with the given TTL.
// TTL should match the remaining token expiry time.
func (s *Store) BlacklistToken(ctx context.Context, token string, ttl time.Duration) error {
	key := tokenBlacklistPrefix + hashToken(token)
	if err := s.Client.Set(ctx, key, "1", ttl).Err(); err != nil {
		return fmt.Errorf("blacklist token: %w", err)
	}
	return nil
}

// IsTokenBlacklisted checks if a token has been blacklisted.
func (s *Store) IsTokenBlacklisted(ctx context.Context, token string) (bool, error) {
	key := tokenBlacklistPrefix + hashToken(token)
	result, err := s.Client.Exists(ctx, key).Result()
	if err != nil {
		return false, fmt.Errorf("check token blacklist: %w", err)
	}
	return result > 0, nil
}

// ── Refresh Token Storage ────────────────────────────────────

const refreshTokenPrefix = "refresh:"

// StoreRefreshToken stores a refresh token mapping (jti → userID).
func (s *Store) StoreRefreshToken(ctx context.Context, jti string, userID string, ttl time.Duration) error {
	key := refreshTokenPrefix + jti
	if err := s.Client.Set(ctx, key, userID, ttl).Err(); err != nil {
		return fmt.Errorf("store refresh token: %w", err)
	}
	return nil
}

// GetRefreshToken retrieves the user ID associated with a refresh token JTI.
func (s *Store) GetRefreshToken(ctx context.Context, jti string) (string, error) {
	key := refreshTokenPrefix + jti
	result, err := s.Client.Get(ctx, key).Result()
	if err == redis.Nil {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get refresh token: %w", err)
	}
	return result, nil
}

// RevokeRefreshToken removes a refresh token.
func (s *Store) RevokeRefreshToken(ctx context.Context, jti string) error {
	key := refreshTokenPrefix + jti
	if err := s.Client.Del(ctx, key).Err(); err != nil {
		return fmt.Errorf("revoke refresh token: %w", err)
	}
	return nil
}

// RevokeAllUserRefreshTokens removes all refresh tokens for a user.
// This is used when a user changes password or is deactivated.
func (s *Store) RevokeAllUserRefreshTokens(ctx context.Context, userID string) error {
	// Scan for all refresh tokens and check values
	// In production, consider maintaining a set of JTIs per user
	var cursor uint64
	for {
		keys, nextCursor, err := s.Client.Scan(ctx, cursor, refreshTokenPrefix+"*", 100).Result()
		if err != nil {
			return fmt.Errorf("scan refresh tokens: %w", err)
		}

		for _, key := range keys {
			val, err := s.Client.Get(ctx, key).Result()
			if err != nil {
				continue
			}
			if val == userID {
				s.Client.Del(ctx, key)
			}
		}

		cursor = nextCursor
		if cursor == 0 {
			break
		}
	}
	return nil
}

// hashToken creates a SHA256 hash for token storage keys.
// We don't store raw tokens in Redis keys.
func hashToken(token string) string {
	h := sha256.New()
	h.Write([]byte(token))
	return hex.EncodeToString(h.Sum(nil))
}
