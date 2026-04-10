package crypto

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHashPassword(t *testing.T) {
	t.Run("hashes successfully", func(t *testing.T) {
		hash, err := HashPassword("securepassword123")
		require.NoError(t, err)
		assert.NotEmpty(t, hash)
		assert.NotEqual(t, "securepassword123", hash)
	})

	t.Run("rejects short password", func(t *testing.T) {
		_, err := HashPassword("short")
		assert.Error(t, err)
	})

	t.Run("same password different hashes", func(t *testing.T) {
		h1, _ := HashPassword("securepassword123")
		h2, _ := HashPassword("securepassword123")
		assert.NotEqual(t, h1, h2) // bcrypt uses random salt
	})
}

func TestVerifyPassword(t *testing.T) {
	hash, err := HashPassword("mypassword123")
	require.NoError(t, err)

	assert.True(t, VerifyPassword("mypassword123", hash))
	assert.False(t, VerifyPassword("wrongpassword", hash))
	assert.False(t, VerifyPassword("", hash))
}
