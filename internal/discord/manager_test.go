package discord

import (
	"testing"
)

func TestNewGuildManager(t *testing.T) {
	t.Run("with nil logger", func(t *testing.T) {
		manager := NewGuildManager(nil)
		if manager == nil {
			t.Fatal("manager should not be nil")
		}
		if manager.logger == nil {
			t.Error("logger should default to slog.Default()")
		}
		if manager.clients == nil {
			t.Error("clients map should be initialized")
		}
	})
}

func TestGuildManager_GuildIDs_Empty(t *testing.T) {
	manager := NewGuildManager(nil)
	ids := manager.GuildIDs()
	if len(ids) != 0 {
		t.Errorf("GuildIDs() = %v, want empty slice", ids)
	}
}

func TestGuildManager_Client_NotFound(t *testing.T) {
	manager := NewGuildManager(nil)
	_, ok := manager.Client("nonexistent")
	if ok {
		t.Error("Client() should return false for non-existent guild")
	}
}

func TestGuildManager_ForEach_Empty(t *testing.T) {
	manager := NewGuildManager(nil)
	count := 0
	manager.ForEach(func(_ string, _ *Client) {
		count++
	})
	if count != 0 {
		t.Errorf("ForEach called %d times, want 0", count)
	}
}
