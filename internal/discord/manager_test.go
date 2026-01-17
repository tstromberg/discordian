package discord

import (
	"testing"

	"github.com/bwmarrin/discordgo"
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

func TestGuildManager_RegisterClient(t *testing.T) {
	manager := NewGuildManager(nil)
	client := &Client{guildID: "test-guild"}

	manager.RegisterClient("test-guild", client)

	// Verify client was registered
	got, ok := manager.Client("test-guild")
	if !ok {
		t.Error("Client() should return true for registered guild")
	}
	if got != client {
		t.Error("Client() should return the same client")
	}

	// Verify guild ID is in the list
	ids := manager.GuildIDs()
	if len(ids) != 1 {
		t.Errorf("GuildIDs() = %v, want 1 guild", ids)
	}
	if ids[0] != "test-guild" {
		t.Errorf("GuildIDs() = %v, want [test-guild]", ids)
	}
}

func TestGuildManager_RemoveClient(t *testing.T) {
	manager := NewGuildManager(nil)
	// Create a session (it won't connect in tests)
	session := &discordgo.Session{}
	client := &Client{guildID: "test-guild", session: session}

	// Register a client
	manager.RegisterClient("test-guild", client)

	// Remove the client
	manager.RemoveClient("test-guild")

	// Verify client was removed
	_, ok := manager.Client("test-guild")
	if ok {
		t.Error("Client() should return false for removed guild")
	}

	// Verify guild ID is not in the list
	ids := manager.GuildIDs()
	if len(ids) != 0 {
		t.Errorf("GuildIDs() = %v, want empty slice", ids)
	}
}

func TestGuildManager_ForEach_WithClients(t *testing.T) {
	manager := NewGuildManager(nil)
	client1 := &Client{guildID: "guild1"}
	client2 := &Client{guildID: "guild2"}

	manager.RegisterClient("guild1", client1)
	manager.RegisterClient("guild2", client2)

	visited := make(map[string]bool)
	manager.ForEach(func(guildID string, client *Client) {
		visited[guildID] = true
	})

	if len(visited) != 2 {
		t.Errorf("ForEach visited %d guilds, want 2", len(visited))
	}
	if !visited["guild1"] || !visited["guild2"] {
		t.Errorf("ForEach didn't visit all guilds: %v", visited)
	}
}

func TestGuildManager_Close(t *testing.T) {
	manager := NewGuildManager(nil)

	// Close should not panic even with no clients
	err := manager.Close()
	if err != nil {
		t.Errorf("Close() error = %v, want nil", err)
	}
}
