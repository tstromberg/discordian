package usermapping

import (
	"context"
	"testing"
	"time"
)

type mockConfigLookup struct {
	users map[string]string
}

func (m *mockConfigLookup) DiscordUserID(_, githubUsername string) string {
	if m.users == nil {
		return ""
	}
	return m.users[githubUsername]
}

type mockDiscordLookup struct {
	users map[string]string
}

func (m *mockDiscordLookup) LookupUserByUsername(_ context.Context, username string) string {
	if m.users == nil {
		return ""
	}
	return m.users[username]
}

func TestMapper_DiscordID(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockConfigLookup{
		users: map[string]string{
			"alice": "111111111111111111",
		},
	}

	discordLookup := &mockDiscordLookup{
		users: map[string]string{
			"bob":     "222222222222222222",
			"charlie": "333333333333333333",
		},
	}

	mapper := New("testorg", configLookup, discordLookup)

	tests := []struct {
		name           string
		githubUsername string
		want           string
		tier           string
	}{
		{
			name:           "config mapping (tier 1)",
			githubUsername: "alice",
			want:           "111111111111111111",
			tier:           "config",
		},
		{
			name:           "discord lookup (tier 2)",
			githubUsername: "bob",
			want:           "222222222222222222",
			tier:           "discord",
		},
		{
			name:           "no mapping (tier 3)",
			githubUsername: "unknown",
			want:           "",
			tier:           "none",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapper.DiscordID(ctx, tt.githubUsername)
			if got != tt.want {
				t.Errorf("DiscordID(%q) = %q, want %q", tt.githubUsername, got, tt.want)
			}
		})
	}
}

func TestMapper_DiscordID_ConfigOverridesDiscord(t *testing.T) {
	ctx := context.Background()

	// Same user in both config and Discord with different IDs
	configLookup := &mockConfigLookup{
		users: map[string]string{
			"alice": "111111111111111111",
		},
	}

	discordLookup := &mockDiscordLookup{
		users: map[string]string{
			"alice": "999999999999999999", // Different ID
		},
	}

	mapper := New("testorg", configLookup, discordLookup)

	// Config should take priority
	got := mapper.DiscordID(ctx, "alice")
	if got != "111111111111111111" {
		t.Errorf("DiscordID(alice) = %q, want config ID 111111111111111111", got)
	}
}

func TestMapper_DiscordID_Caching(t *testing.T) {
	ctx := context.Background()

	discordLookup := &mockDiscordLookup{
		users: map[string]string{
			"bob": "222222222222222222",
		},
	}

	mapper := New("testorg", nil, discordLookup)

	// First call - should hit Discord lookup
	id1 := mapper.DiscordID(ctx, "bob")
	if id1 != "222222222222222222" {
		t.Errorf("First DiscordID(bob) = %q, want 222222222222222222", id1)
	}

	// Change the underlying data
	discordLookup.users["bob"] = "999999999999999999"

	// Second call - should be cached (still returns old value)
	id2 := mapper.DiscordID(ctx, "bob")
	if id2 != id1 {
		t.Errorf("Second DiscordID(bob) = %q, want cached %q", id2, id1)
	}
}

func TestMapper_Mention(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockConfigLookup{
		users: map[string]string{
			"alice": "111111111111111111",
		},
	}

	mapper := New("testorg", configLookup, nil)

	tests := []struct {
		name           string
		githubUsername string
		want           string
	}{
		{
			name:           "mapped user gets mention",
			githubUsername: "alice",
			want:           "<@111111111111111111>",
		},
		{
			name:           "unmapped user gets plain text",
			githubUsername: "unknown",
			want:           "unknown",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := mapper.Mention(ctx, tt.githubUsername)
			if got != tt.want {
				t.Errorf("Mention(%q) = %q, want %q", tt.githubUsername, got, tt.want)
			}
		})
	}
}

func TestMapper_ClearCache(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockConfigLookup{
		users: map[string]string{
			"alice": "111111111111111111",
		},
	}

	mapper := New("testorg", configLookup, nil)

	// Populate cache
	mapper.DiscordID(ctx, "alice")

	// Change the underlying data
	configLookup.users["alice"] = "999999999999999999"

	// Still returns cached value
	if got := mapper.DiscordID(ctx, "alice"); got != "111111111111111111" {
		t.Errorf("Before ClearCache: DiscordID(alice) = %q, want cached 111111111111111111", got)
	}

	// Clear cache
	mapper.ClearCache()

	// Now returns new value
	if got := mapper.DiscordID(ctx, "alice"); got != "999999999999999999" {
		t.Errorf("After ClearCache: DiscordID(alice) = %q, want new 999999999999999999", got)
	}
}

func TestMapper_NilLookups(t *testing.T) {
	ctx := context.Background()

	// Both lookups nil
	mapper := New("testorg", nil, nil)

	got := mapper.DiscordID(ctx, "anyone")
	if got != "" {
		t.Errorf("DiscordID with nil lookups = %q, want empty", got)
	}

	mention := mapper.Mention(ctx, "anyone")
	if mention != "anyone" {
		t.Errorf("Mention with nil lookups = %q, want plain username", mention)
	}
}

func TestMapper_DiscordID_CacheTTL(t *testing.T) {
	ctx := context.Background()

	discordLookup := &mockDiscordLookup{
		users: map[string]string{
			"bob": "222222222222222222",
		},
	}

	mapper := New("testorg", nil, discordLookup)

	// First call - populates cache
	id1 := mapper.DiscordID(ctx, "bob")
	if id1 != "222222222222222222" {
		t.Errorf("First DiscordID(bob) = %q, want 222222222222222222", id1)
	}

	// Manually expire the cache entry by setting cachedAt to past TTL
	mapper.mu.Lock()
	if entry, ok := mapper.cache["bob"]; ok {
		entry.cachedAt = time.Now().Add(-25 * time.Hour) // Older than 24h TTL
		mapper.cache["bob"] = entry
	}
	mapper.mu.Unlock()

	// Change underlying data
	discordLookup.users["bob"] = "999999999999999999"

	// After TTL expiry, should get fresh value
	id2 := mapper.DiscordID(ctx, "bob")
	if id2 != "999999999999999999" {
		t.Errorf("After TTL expiry, DiscordID(bob) = %q, want fresh 999999999999999999", id2)
	}
}

func TestMapper_ConfigUsernameResolution(t *testing.T) {
	ctx := context.Background()

	tests := []struct {
		name           string
		configValue    string
		discordUsers   map[string]string
		githubUsername string
		wantID         string
		description    string
	}{
		{
			name:           "config with numeric ID uses it directly",
			configValue:    "111111111111111111",
			discordUsers:   map[string]string{},
			githubUsername: "alice",
			wantID:         "111111111111111111",
			description:    "Numeric ID in config should be used without Discord lookup",
		},
		{
			name:        "config with Discord username resolves it",
			configValue: "alice_discord",
			discordUsers: map[string]string{
				"alice_discord": "222222222222222222",
			},
			githubUsername: "alice",
			wantID:         "222222222222222222",
			description:    "Discord username in config should be resolved to numeric ID",
		},
		{
			name:           "config with Discord username not found falls back",
			configValue:    "nonexistent",
			discordUsers:   map[string]string{},
			githubUsername: "alice",
			wantID:         "",
			description:    "Discord username not found should fall back (no ID)",
		},
		{
			name:        "config maps GitHub user to different Discord username",
			configValue: "bob_discord",
			discordUsers: map[string]string{
				"bob_discord": "333333333333333333",
			},
			githubUsername: "bob_github",
			wantID:         "333333333333333333",
			description:    "GitHub username mapped to different Discord username",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			configLookup := &mockConfigLookup{
				users: map[string]string{
					tt.githubUsername: tt.configValue,
				},
			}

			discordLookup := &mockDiscordLookup{
				users: tt.discordUsers,
			}

			mapper := New("testorg", configLookup, discordLookup)

			got := mapper.DiscordID(ctx, tt.githubUsername)
			if got != tt.wantID {
				t.Errorf("%s: DiscordID(%q) = %q, want %q",
					tt.description, tt.githubUsername, got, tt.wantID)
			}
		})
	}
}

func TestMapper_ConfigUsernameResolution_Mention(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockConfigLookup{
		users: map[string]string{
			"alice": "alice_discord", // Discord username (not numeric ID)
		},
	}

	discordLookup := &mockDiscordLookup{
		users: map[string]string{
			"alice_discord": "111111111111111111",
		},
	}

	mapper := New("testorg", configLookup, discordLookup)

	got := mapper.Mention(ctx, "alice")
	want := "<@111111111111111111>"
	if got != want {
		t.Errorf("Mention with username in config = %q, want %q", got, want)
	}
}
