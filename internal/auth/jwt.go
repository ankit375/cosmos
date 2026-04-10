package auth

import (
	"fmt"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/yourorg/cloudctrl/internal/config"
	"github.com/yourorg/cloudctrl/internal/model"
)

// Claims represents the JWT claims for user authentication.
type Claims struct {
	jwt.RegisteredClaims
	TenantID uuid.UUID      `json:"tenant_id"`
	Email    string         `json:"email"`
	Role     model.UserRole `json:"role"`
}

// JWTService handles JWT token generation and validation.
type JWTService struct {
	secret        []byte
	accessExpiry  time.Duration
	refreshExpiry time.Duration
}

// NewJWTService creates a new JWT service.
func NewJWTService(cfg config.AuthConfig) *JWTService {
	return &JWTService{
		secret:        []byte(cfg.JWTSecret),
		accessExpiry:  cfg.JWTExpiry,
		refreshExpiry: cfg.RefreshExpiry,
	}
}

// GenerateAccessToken creates a new JWT access token for the given user.
func (s *JWTService) GenerateAccessToken(user *model.User) (string, error) {
	now := time.Now()
	claims := Claims{
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   user.ID.String(),
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(s.accessExpiry)),
			Issuer:    "cloudctrl",
		},
		TenantID: user.TenantID,
		Email:    user.Email,
		Role:     user.Role,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign access token: %w", err)
	}
	return signed, nil
}

// GenerateRefreshToken creates a new JWT refresh token.
// Refresh tokens carry minimal claims — just subject and expiry.
func (s *JWTService) GenerateRefreshToken(userID uuid.UUID) (string, error) {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		Subject:   userID.String(),
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(s.refreshExpiry)),
		Issuer:    "cloudctrl",
		ID:        uuid.New().String(), // Unique token ID for revocation
	}

	token := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	signed, err := token.SignedString(s.secret)
	if err != nil {
		return "", fmt.Errorf("sign refresh token: %w", err)
	}
	return signed, nil
}

// ValidateAccessToken parses and validates an access token, returning claims.
func (s *JWTService) ValidateAccessToken(tokenString string) (*Claims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &Claims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse access token: %w", err)
	}

	claims, ok := token.Claims.(*Claims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid access token claims")
	}

	return claims, nil
}

// ValidateRefreshToken parses and validates a refresh token, returning the standard claims.
func (s *JWTService) ValidateRefreshToken(tokenString string) (*jwt.RegisteredClaims, error) {
	token, err := jwt.ParseWithClaims(tokenString, &jwt.RegisteredClaims{}, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}
		return s.secret, nil
	})
	if err != nil {
		return nil, fmt.Errorf("parse refresh token: %w", err)
	}

	claims, ok := token.Claims.(*jwt.RegisteredClaims)
	if !ok || !token.Valid {
		return nil, fmt.Errorf("invalid refresh token claims")
	}

	return claims, nil
}

// AccessExpiry returns the access token TTL in seconds.
func (s *JWTService) AccessExpiry() int64 {
	return int64(s.accessExpiry.Seconds())
}

// RefreshExpiry returns the refresh token TTL.
func (s *JWTService) RefreshExpiry() time.Duration {
	return s.refreshExpiry
}
