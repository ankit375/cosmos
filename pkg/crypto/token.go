package crypto

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// GenerateToken creates a cryptographically secure random token of the given byte length.
// Returns the hex-encoded token string.
func GenerateToken(byteLength int) (string, error) {
	if byteLength < 16 {
		return "", fmt.Errorf("token must be at least 16 bytes")
	}

	bytes := make([]byte, byteLength)
	if _, err := rand.Read(bytes); err != nil {
		return "", fmt.Errorf("generate random bytes: %w", err)
	}

	return hex.EncodeToString(bytes), nil
}

// HashToken computes the SHA-256 hash of a token for safe storage.
func HashToken(token string) string {
	hash := sha256.Sum256([]byte(token))
	return hex.EncodeToString(hash[:])
}

// GenerateAPIKey generates an API key with a prefix for identification.
// Format: "ccap_" + 48 random hex chars = 53 char total
func GenerateAPIKey() (string, error) {
	token, err := GenerateToken(24)
	if err != nil {
		return "", err
	}
	return "ccap_" + token, nil
}
