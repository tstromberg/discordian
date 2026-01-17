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

// TestMapper_ExportCache tests cache export functionality.
func TestMapper_ExportCache(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockConfigLookup{
		users: map[string]string{
			"alice": "111111111111111111",
			"bob":   "222222222222222222",
		},
	}

	mapper := New("testorg", configLookup, nil)

	// Populate cache
	mapper.DiscordID(ctx, "alice")
	mapper.DiscordID(ctx, "bob")

	// Export cache
	cache := mapper.ExportCache()

	if len(cache) != 2 {
		t.Errorf("ExportCache returned %d entries, want 2", len(cache))
	}

	if cache["alice"] != "111111111111111111" {
		t.Errorf("cache[alice] = %q, want 111111111111111111", cache["alice"])
	}

	if cache["bob"] != "222222222222222222" {
		t.Errorf("cache[bob] = %q, want 222222222222222222", cache["bob"])
	}
}

// mockOrgConfig implements OrgConfig for testing.
type mockOrgConfig struct {
	users map[string]string
}

func (m *mockOrgConfig) UserMappings() map[string]string {
	return m.users
}

// mockReverseConfigLookup implements ReverseConfigLookup for testing.
type mockReverseConfigLookup struct {
	orgs map[string]*mockOrgConfig
}

func (m *mockReverseConfigLookup) Config(org string) (OrgConfig, bool) {
	cfg, ok := m.orgs[org]
	return cfg, ok
}

// TestReverseMapper_GitHubUsername tests reverse mapping from Discord ID to GitHub username.
func TestReverseMapper_GitHubUsername(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockReverseConfigLookup{
		orgs: map[string]*mockOrgConfig{
			"org1": {
				users: map[string]string{
					"alice": "111111111111111111",
					"bob":   "222222222222222222",
				},
			},
			"org2": {
				users: map[string]string{
					"charlie": "333333333333333333",
				},
			},
		},
	}

	tests := []struct {
		name      string
		discordID string
		orgs      []string
		want      string
	}{
		{
			name:      "find user in first org",
			discordID: "111111111111111111",
			orgs:      []string{"org1", "org2"},
			want:      "alice",
		},
		{
			name:      "find user in second org",
			discordID: "333333333333333333",
			orgs:      []string{"org1", "org2"},
			want:      "charlie",
		},
		{
			name:      "user not found",
			discordID: "999999999999999999",
			orgs:      []string{"org1", "org2"},
			want:      "",
		},
		{
			name:      "empty orgs list",
			discordID: "444444444444444444",
			orgs:      []string{},
			want:      "",
		},
		{
			name:      "org not in config",
			discordID: "555555555555555555",
			orgs:      []string{"org3"},
			want:      "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Use fresh mapper for each test to avoid cache pollution
			mapper := NewReverseMapper()
			got := mapper.GitHubUsername(ctx, tt.discordID, configLookup, tt.orgs)
			if got != tt.want {
				t.Errorf("GitHubUsername(%q, %v) = %q, want %q", tt.discordID, tt.orgs, got, tt.want)
			}
		})
	}
}

// TestReverseMapper_GitHubUsername_Caching tests that reverse mapping results are cached.
func TestReverseMapper_GitHubUsername_Caching(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockReverseConfigLookup{
		orgs: map[string]*mockOrgConfig{
			"org1": {
				users: map[string]string{
					"alice": "111111111111111111",
				},
			},
		},
	}

	mapper := NewReverseMapper()

	// First call - should hit config
	username1 := mapper.GitHubUsername(ctx, "111111111111111111", configLookup, []string{"org1"})
	if username1 != "alice" {
		t.Errorf("First call: GitHubUsername = %q, want alice", username1)
	}

	// Change underlying config
	configLookup.orgs["org1"].users["alice"] = "999999999999999999"
	configLookup.orgs["org1"].users["bob"] = "111111111111111111"

	// Second call - should return cached value
	username2 := mapper.GitHubUsername(ctx, "111111111111111111", configLookup, []string{"org1"})
	if username2 != "alice" {
		t.Errorf("Cached call: GitHubUsername = %q, want cached alice", username2)
	}
}

// TestReverseMapper_GitHubUsername_NilConfigLookup tests handling of nil config lookup.
func TestReverseMapper_GitHubUsername_NilConfigLookup(t *testing.T) {
	ctx := context.Background()
	mapper := NewReverseMapper()

	got := mapper.GitHubUsername(ctx, "111111111111111111", nil, []string{"org1"})
	if got != "" {
		t.Errorf("GitHubUsername with nil lookup = %q, want empty", got)
	}
}

// TestReverseMapper_ClearCache tests clearing the reverse mapper cache.
func TestReverseMapper_ClearCache(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockReverseConfigLookup{
		orgs: map[string]*mockOrgConfig{
			"org1": {
				users: map[string]string{
					"alice": "111111111111111111",
				},
			},
		},
	}

	mapper := NewReverseMapper()

	// Populate cache
	mapper.GitHubUsername(ctx, "111111111111111111", configLookup, []string{"org1"})

	// Verify cached
	if len(mapper.cache) != 1 {
		t.Errorf("Cache size = %d, want 1", len(mapper.cache))
	}

	// Clear cache
	mapper.ClearCache()

	// Verify cleared
	if len(mapper.cache) != 0 {
		t.Errorf("After ClearCache, cache size = %d, want 0", len(mapper.cache))
	}
}

// TestReverseMapper_ExportCache tests exporting the reverse mapper cache.
func TestReverseMapper_ExportCache(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockReverseConfigLookup{
		orgs: map[string]*mockOrgConfig{
			"org1": {
				users: map[string]string{
					"alice": "111111111111111111",
					"bob":   "222222222222222222",
				},
			},
		},
	}

	mapper := NewReverseMapper()

	// Populate cache
	mapper.GitHubUsername(ctx, "111111111111111111", configLookup, []string{"org1"})
	mapper.GitHubUsername(ctx, "222222222222222222", configLookup, []string{"org1"})

	// Export cache
	cache := mapper.ExportCache()

	if len(cache) != 2 {
		t.Errorf("ExportCache returned %d entries, want 2", len(cache))
	}

	if cache["111111111111111111"] != "alice" {
		t.Errorf("cache[111111111111111111] = %q, want alice", cache["111111111111111111"])
	}

	if cache["222222222222222222"] != "bob" {
		t.Errorf("cache[222222222222222222] = %q, want bob", cache["222222222222222222"])
	}
}

// TestReverseMapper_CacheTTL tests that reverse mapper cache entries expire after TTL.
func TestReverseMapper_CacheTTL(t *testing.T) {
	ctx := context.Background()

	configLookup := &mockReverseConfigLookup{
		orgs: map[string]*mockOrgConfig{
			"org1": {
				users: map[string]string{
					"alice": "111111111111111111",
				},
			},
		},
	}

	mapper := NewReverseMapper()

	// First call - populate cache
	username1 := mapper.GitHubUsername(ctx, "111111111111111111", configLookup, []string{"org1"})
	if username1 != "alice" {
		t.Errorf("First call: GitHubUsername = %q, want alice", username1)
	}

	// Manually expire the cache entry
	mapper.cacheMu.Lock()
	if entry, ok := mapper.cache["111111111111111111"]; ok {
		entry.cachedAt = time.Now().Add(-25 * time.Hour) // Older than 24h TTL
		mapper.cache["111111111111111111"] = entry
	}
	mapper.cacheMu.Unlock()

	// Change underlying config
	configLookup.orgs["org1"].users["bob"] = "111111111111111111"
	delete(configLookup.orgs["org1"].users, "alice")

	// After TTL expiry, should get fresh value
	username2 := mapper.GitHubUsername(ctx, "111111111111111111", configLookup, []string{"org1"})
	if username2 != "bob" {
		t.Errorf("After TTL expiry: GitHubUsername = %q, want bob", username2)
	}
}
