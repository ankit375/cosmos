package logger

import (
	"os"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNew(t *testing.T) {
	t.Run("console format", func(t *testing.T) {
		log, err := New("debug", "console", "stdout")
		require.NoError(t, err)
		assert.NotNil(t, log)
		log.Info("test message")
	})

	t.Run("json format", func(t *testing.T) {
		log, err := New("info", "json", "stdout")
		require.NoError(t, err)
		assert.NotNil(t, log)
		log.Info("test message")
	})

	t.Run("file output", func(t *testing.T) {
		tmpFile, err := os.CreateTemp("", "log-*.log")
		require.NoError(t, err)
		defer os.Remove(tmpFile.Name())
		tmpFile.Close()

		log, err := New("debug", "json", tmpFile.Name())
		require.NoError(t, err)
		log.Info("test file message")
		log.Sync()

		content, err := os.ReadFile(tmpFile.Name())
		require.NoError(t, err)
		assert.Contains(t, string(content), "test file message")
	})

	t.Run("invalid level defaults to info", func(t *testing.T) {
		log, err := New("invalid", "console", "stdout")
		require.NoError(t, err)
		assert.NotNil(t, log)
	})
}