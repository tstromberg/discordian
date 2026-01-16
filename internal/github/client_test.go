package github

import (
	"testing"
)

// TestNewAppClient tests AppClient construction.
func TestNewAppClient(t *testing.T) {
	t.Run("invalid private key", func(t *testing.T) {
		_, err := NewAppClient("12345", "invalid-key", nil)
		if err == nil {
			t.Error("NewAppClient() error = nil, want error for invalid key")
		}
	})

	t.Run("empty private key", func(t *testing.T) {
		_, err := NewAppClient("12345", "", nil)
		if err == nil {
			t.Error("NewAppClient() error = nil, want error for empty key")
		}
	})
}
