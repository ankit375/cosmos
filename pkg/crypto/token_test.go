package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestGenerateToken(t *testing.T) {
	t.Run("generates correct length", func(t *testing.T) {
		token, err := GenerateToken(32)
		require.NoError(t, err)
		assert.Equal(t, 64, len(token)) // 32 bytes = 64 hex chars
	})

	t.Run("generates unique tokens", func(t *testing.T) {
		t1, _ := GenerateToken(32)
		t2, _ := GenerateToken(32)
		assert.NotEqual(t, t1, t2)
	})

	t.Run("rejects too short", func(t *testing.T) {
		_, err := GenerateToken(8)
		assert.Error(t, err)
	})
}

func TestHashToken(t *testing.T) {
	hash1 := HashToken("test-token-123")
	hash2 := HashToken("test-token-123")
	hash3 := HashToken("different-token")

	assert.Equal(t, hash1, hash2)
	assert.NotEqual(t, hash1, hash3)
	assert.Equal(t, 64, len(hash1)) // SHA-256 = 32 bytes = 64 hex chars
}

func TestGenerateAPIKey(t *testing.T) {
	key, err := GenerateAPIKey()
	require.NoError(t, err)
	assert.True(t, len(key) > 5)
	assert.Equal(t, "ccap_", key[:5])
}
