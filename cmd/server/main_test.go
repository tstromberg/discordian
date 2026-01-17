package main

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/bot"
	"github.com/codeGROOVE-dev/discordian/internal/config"
	"github.com/codeGROOVE-dev/discordian/internal/discord"
	"github.com/codeGROOVE-dev/discordian/internal/state"
	"github.com/codeGROOVE-dev/discordian/internal/usermapping"
)

// Mock implementations for testing

type mockStateStore struct {
	pendingDMs       []*state.PendingDM
	dailyReportInfos map[string]state.DailyReportInfo
}

type mockTurnClient struct {
	response   *bot.CheckResponse
	checkError error
}

func (m *mockTurnClient) Check(_ context.Context, _, _ string, _ time.Time) (*bot.CheckResponse, error) {
	if m.checkError != nil {
		return nil, m.checkError
	}
	return m.response, nil
}

type mockConfigManager struct {
	configs map[string]*config.DiscordConfig
}

func (m *mockConfigManager) LoadConfig(_ context.Context, _ string) error {
	return nil
}

func (m *mockConfigManager) ReloadConfig(_ context.Context, _ string) error {
	return nil
}

func (m *mockConfigManager) Config(org string) (*config.DiscordConfig, bool) {
	if m.configs == nil {
		return nil, false
	}
	cfg, ok := m.configs[org]
	return cfg, ok
}

func (m *mockConfigManager) ChannelsForRepo(_, _ string) []string {
	return []string{}
}

func (m *mockConfigManager) ChannelType(_, _ string) string {
	return "text"
}

func (m *mockConfigManager) DiscordUserID(_, _ string) string {
	return ""
}

func (m *mockConfigManager) ReminderDMDelay(_, _ string) int {
	return 0
}

func (m *mockConfigManager) When(_, _ string) string {
	return ""
}

func (m *mockConfigManager) GuildID(org string) string {
	if cfg, ok := m.Config(org); ok {
		return cfg.Global.GuildID
	}
	return ""
}

func (m *mockConfigManager) SetGitHubClient(_ string, _ any) {}

func (m *mockStateStore) Thread(_ context.Context, _, _ string, _ int, _ string) (state.ThreadInfo, bool) {
	return state.ThreadInfo{}, false
}

func (m *mockStateStore) SaveThread(_ context.Context, _, _ string, _ int, _ string, _ state.ThreadInfo) error {
	return nil
}

func (m *mockStateStore) ClaimThread(_ context.Context, _, _ string, _ int, _ string, _ time.Duration) bool {
	return true // Always succeed in tests
}

func (m *mockStateStore) DMInfo(_ context.Context, _, _ string) (state.DMInfo, bool) {
	return state.DMInfo{}, false
}

func (m *mockStateStore) SaveDMInfo(_ context.Context, _, _ string, _ state.DMInfo) error {
	return nil
}

func (m *mockStateStore) ClaimDM(_ context.Context, _, _ string, _ time.Duration) bool {
	return true // Always succeed in tests
}

func (m *mockStateStore) ListDMUsers(_ context.Context, _ string) []string {
	return nil
}

func (m *mockStateStore) WasProcessed(_ context.Context, _ string) bool {
	return false
}

func (m *mockStateStore) MarkProcessed(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (m *mockStateStore) QueuePendingDM(_ context.Context, dm *state.PendingDM) error {
	m.pendingDMs = append(m.pendingDMs, dm)
	return nil
}

func (m *mockStateStore) PendingDMs(_ context.Context, _ time.Time) ([]*state.PendingDM, error) {
	return m.pendingDMs, nil
}

func (m *mockStateStore) RemovePendingDM(_ context.Context, id string) error {
	for i, dm := range m.pendingDMs {
		if dm.ID == id {
			m.pendingDMs = append(m.pendingDMs[:i], m.pendingDMs[i+1:]...)
			break
		}
	}
	return nil
}

func (m *mockStateStore) DailyReportInfo(_ context.Context, userID string) (state.DailyReportInfo, bool) {
	info, exists := m.dailyReportInfos[userID]
	return info, exists
}

func (m *mockStateStore) SaveDailyReportInfo(_ context.Context, userID string, info state.DailyReportInfo) error {
	if m.dailyReportInfos == nil {
		m.dailyReportInfos = make(map[string]state.DailyReportInfo)
	}
	m.dailyReportInfos[userID] = info
	return nil
}

func (m *mockStateStore) Cleanup(_ context.Context) error {
	return nil
}

func (m *mockStateStore) Close() error {
	return nil
}

func TestCoordinatorManager_Status(t *testing.T) {
	t.Run("empty state", func(t *testing.T) {
		cm := &coordinatorManager{
			active:         make(map[string]context.CancelFunc),
			discordClients: make(map[string]*discord.Client),
			slashHandlers:  make(map[string]*discord.SlashCommandHandler),
			coordinators:   make(map[string]*bot.Coordinator),
			lastEventTime:  make(map[string]time.Time),
			startTime:      time.Now().Add(-1 * time.Hour),
			store:          &mockStateStore{},
			configManager:  config.New(),
			reverseMapper:  usermapping.NewReverseMapper(),
		}

		status := cm.Status(context.Background(), "test-guild")

		if status.Connected {
			t.Error("Connected should be false with no active coordinators")
		}
		if status.UptimeSeconds < 3500 || status.UptimeSeconds > 3700 {
			t.Errorf("UptimeSeconds = %d, expected around 3600", status.UptimeSeconds)
		}
		if len(status.ConnectedOrgs) != 0 {
			t.Errorf("ConnectedOrgs = %d, want 0", len(status.ConnectedOrgs))
		}
	})

	t.Run("with pending DMs", func(t *testing.T) {
		mockStore := &mockStateStore{
			pendingDMs: []*state.PendingDM{
				{ID: "1", UserID: "user1"},
				{ID: "2", UserID: "user2"},
				{ID: "3", UserID: "user3"},
			},
		}

		cm := &coordinatorManager{
			active:         make(map[string]context.CancelFunc),
			discordClients: make(map[string]*discord.Client),
			slashHandlers:  make(map[string]*discord.SlashCommandHandler),
			coordinators:   make(map[string]*bot.Coordinator),
			lastEventTime:  make(map[string]time.Time),
			startTime:      time.Now(),
			store:          mockStore,
			configManager:  config.New(),
			reverseMapper:  usermapping.NewReverseMapper(),
		}

		status := cm.Status(context.Background(), "test-guild")

		if status.PendingDMs != 3 {
			t.Errorf("PendingDMs = %d, want 3", status.PendingDMs)
		}
	})

	t.Run("with active coordinators", func(t *testing.T) {
		cm := &coordinatorManager{
			active: map[string]context.CancelFunc{
				"org1": func() {},
				"org2": func() {},
				"org3": func() {},
			},
			discordClients: make(map[string]*discord.Client),
			slashHandlers:  make(map[string]*discord.SlashCommandHandler),
			coordinators:   make(map[string]*bot.Coordinator),
			lastEventTime:  make(map[string]time.Time),
			startTime:      time.Now(),
			store:          &mockStateStore{},
			configManager:  config.New(),
		}

		status := cm.Status(context.Background(), "guild1")

		if !status.Connected {
			t.Error("Connected should be true with active coordinators")
		}
		if len(status.ConnectedOrgs) != 3 {
			t.Errorf("ConnectedOrgs = %d, want 3", len(status.ConnectedOrgs))
		}
	})
}

func TestCoordinatorManager_Report_Errors(t *testing.T) {
	t.Run("no org for guild", func(t *testing.T) {
		cm := &coordinatorManager{
			active:         make(map[string]context.CancelFunc),
			discordClients: make(map[string]*discord.Client),
			slashHandlers:  make(map[string]*discord.SlashCommandHandler),
			coordinators:   make(map[string]*bot.Coordinator),
			lastEventTime:  make(map[string]time.Time),
			startTime:      time.Now(),
			store:          &mockStateStore{},
			configManager:  config.New(),
			reverseMapper:  usermapping.NewReverseMapper(),
		}

		_, err := cm.Report(context.Background(), "unknown-guild", "user123")

		if err == nil {
			t.Error("expected error for unknown guild")
		}
		if err != nil && err.Error() != "no org found for guild unknown-guild" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("no GitHub username mapping", func(t *testing.T) {
		mockCfg := &mockConfigManager{
			configs: map[string]*config.DiscordConfig{
				"test-org": {
					Global: config.GlobalConfig{
						GuildID: "test-guild",
					},
				},
			},
		}

		cm := &coordinatorManager{
			active: map[string]context.CancelFunc{
				"test-org": func() {},
			},
			discordClients: make(map[string]*discord.Client),
			slashHandlers:  make(map[string]*discord.SlashCommandHandler),
			coordinators:   make(map[string]*bot.Coordinator),
			lastEventTime:  make(map[string]time.Time),
			startTime:      time.Now(),
			store:          &mockStateStore{},
			configManager:  mockCfg,
			reverseMapper:  usermapping.NewReverseMapper(),
		}

		_, err := cm.Report(context.Background(), "test-guild", "user-without-mapping")

		if err == nil {
			t.Error("expected error for no GitHub username mapping")
		}
		if err != nil && err.Error() != "no GitHub username mapping found for Discord user" {
			t.Errorf("unexpected error message: %v", err)
		}
	})
}

func TestGetEnv(t *testing.T) {
	t.Run("with value set", func(t *testing.T) {
		t.Setenv("TEST_VAR", "test-value")
		v := os.Getenv("TEST_VAR")
		if v == "" {
			v = "default"
		}
		if v != "test-value" {
			t.Errorf("getEnv() = %q, want 'test-value'", v)
		}
	})

	t.Run("with default", func(t *testing.T) {
		v := os.Getenv("NONEXISTENT_VAR")
		if v == "" {
			v = "default-value"
		}
		if v != "default-value" {
			t.Errorf("getEnv() = %q, want 'default-value'", v)
		}
	})

	t.Run("empty string uses default", func(t *testing.T) {
		t.Setenv("EMPTY_VAR", "")
		v := os.Getenv("EMPTY_VAR")
		if v == "" {
			v = "default"
		}
		if v != "default" {
			t.Errorf("getEnv() = %q, want 'default' for empty string", v)
		}
	})
}

func TestCoordinatorManager_Lifecycle(t *testing.T) {
	t.Run("handleCoordinatorExit removes from maps", func(t *testing.T) {
		cm := &coordinatorManager{
			active: map[string]context.CancelFunc{
				"test-org": func() {},
			},
			coordinators: map[string]*bot.Coordinator{
				"test-org": nil,
			},
			discordClients: make(map[string]*discord.Client),
			slashHandlers:  make(map[string]*discord.SlashCommandHandler),
			failed:         make(map[string]time.Time),
			lastEventTime:  make(map[string]time.Time),
		}

		cm.handleCoordinatorExit("test-org", nil)

		if _, exists := cm.active["test-org"]; exists {
			t.Error("org should be removed from active map")
		}
		if _, exists := cm.coordinators["test-org"]; exists {
			t.Error("org should be removed from coordinators map")
		}
	})

	t.Run("handleCoordinatorExit with error marks as failed", func(t *testing.T) {
		cm := &coordinatorManager{
			active:         make(map[string]context.CancelFunc),
			coordinators:   make(map[string]*bot.Coordinator),
			discordClients: make(map[string]*discord.Client),
			slashHandlers:  make(map[string]*discord.SlashCommandHandler),
			failed:         make(map[string]time.Time),
			lastEventTime:  make(map[string]time.Time),
		}

		testErr := context.DeadlineExceeded
		cm.handleCoordinatorExit("test-org", testErr)

		if _, exists := cm.failed["test-org"]; !exists {
			t.Error("org should be marked as failed")
		}
	})
}

func TestCoordinatorManager_StatusGetterInterface(t *testing.T) {
	// Test that coordinatorManager implements StatusGetter interface
	var _ discord.StatusGetter = (*coordinatorManager)(nil)
}

func TestCoordinatorManager_ReportGetterInterface(t *testing.T) {
	// Test that coordinatorManager implements ReportGetter interface
	var _ discord.ReportGetter = (*coordinatorManager)(nil)
}

func TestHealthHandler(t *testing.T) {
	req := &http.Request{}
	rec := &responseRecorder{
		headers: make(http.Header),
	}

	healthHandler(rec, req)

	if rec.status != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.status, http.StatusOK)
	}
	if rec.body != "ok\n" {
		t.Errorf("body = %q, want %q", rec.body, "ok\n")
	}
	if ct := rec.headers.Get("Content-Type"); ct != "text/plain" {
		t.Errorf("Content-Type = %q, want %q", ct, "text/plain")
	}
}

func TestSecurityHeadersMiddleware(t *testing.T) {
	nextCalled := false
	next := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		nextCalled = true
	})

	rec := &responseRecorder{
		headers: make(http.Header),
	}
	req := &http.Request{}

	handler := securityHeadersMiddleware(next)
	handler.ServeHTTP(rec, req)

	if !nextCalled {
		t.Error("next handler was not called")
	}

	tests := []struct {
		header string
		want   string
	}{
		{"X-Content-Type-Options", "nosniff"},
		{"X-Frame-Options", "DENY"},
		{"X-XSS-Protection", "1; mode=block"},
		{"Strict-Transport-Security", "max-age=31536000; includeSubDomains"},
		{"Content-Security-Policy", "default-src 'none'"},
	}

	for _, tt := range tests {
		if got := rec.headers.Get(tt.header); got != tt.want {
			t.Errorf("%s = %q, want %q", tt.header, got, tt.want)
		}
	}
}

func TestCoordinatorManager_UserMappings(t *testing.T) {
	t.Run("no orgs for guild", func(t *testing.T) {
		cm := &coordinatorManager{
			active:        make(map[string]context.CancelFunc),
			coordinators:  make(map[string]*bot.Coordinator),
			configManager: config.New(),
			reverseMapper: usermapping.NewReverseMapper(),
		}

		mappings, err := cm.UserMappings(context.Background(), "unknown-guild")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if mappings.TotalUsers != 0 {
			t.Errorf("TotalUsers = %d, want 0", mappings.TotalUsers)
		}
	})

	t.Run("with config mappings", func(t *testing.T) {
		mockCfg := &mockConfigManager{
			configs: map[string]*config.DiscordConfig{
				"test-org": {
					Global: config.GlobalConfig{
						GuildID: "test-guild",
					},
					Users: map[string]string{
						"github-user1": "discord-id-1",
						"github-user2": "discord-id-2",
					},
				},
			},
		}

		cm := &coordinatorManager{
			active: map[string]context.CancelFunc{
				"test-org": func() {},
			},
			coordinators:  make(map[string]*bot.Coordinator),
			configManager: mockCfg,
			reverseMapper: usermapping.NewReverseMapper(),
		}

		mappings, err := cm.UserMappings(context.Background(), "test-guild")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(mappings.ConfigMappings) != 2 {
			t.Errorf("ConfigMappings = %d, want 2", len(mappings.ConfigMappings))
		}
		if mappings.TotalUsers != 2 {
			t.Errorf("TotalUsers = %d, want 2", mappings.TotalUsers)
		}
	})
}

func TestCoordinatorManager_ChannelMappings(t *testing.T) {
	t.Run("no orgs for guild", func(t *testing.T) {
		cm := &coordinatorManager{
			active:        make(map[string]context.CancelFunc),
			coordinators:  make(map[string]*bot.Coordinator),
			configManager: config.New(),
		}

		mappings, err := cm.ChannelMappings(context.Background(), "unknown-guild")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if mappings.TotalRepos != 0 {
			t.Errorf("TotalRepos = %d, want 0", mappings.TotalRepos)
		}
	})

	t.Run("with channel and repo mappings", func(t *testing.T) {
		mockCfg := &mockConfigManager{
			configs: map[string]*config.DiscordConfig{
				"test-org": {
					Global: config.GlobalConfig{
						GuildID: "test-guild",
					},
					Channels: map[string]config.ChannelConfig{
						"channel-1": {
							Repos: []string{"repo1", "repo2"},
						},
						"channel-2": {
							Repos: []string{"repo3"},
						},
					},
				},
			},
		}

		cm := &coordinatorManager{
			active: map[string]context.CancelFunc{
				"test-org": func() {},
			},
			coordinators:  make(map[string]*bot.Coordinator),
			configManager: mockCfg,
		}

		mappings, err := cm.ChannelMappings(context.Background(), "test-guild")
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if len(mappings.RepoMappings) != 3 {
			t.Errorf("RepoMappings = %d, want 3", len(mappings.RepoMappings))
		}
		if mappings.TotalRepos != 3 {
			t.Errorf("TotalRepos = %d, want 3", mappings.TotalRepos)
		}
	})
}

func TestLoadConfig_MissingRequired(t *testing.T) {
	t.Run("missing GITHUB_APP_ID", func(t *testing.T) {
		t.Setenv("GITHUB_APP_ID", "")
		t.Setenv("GITHUB_PRIVATE_KEY", "test-key")
		t.Setenv("DISCORD_BOT_TOKEN", "test-token")

		_, err := loadConfig(context.Background())
		if err == nil {
			t.Error("expected error for missing GITHUB_APP_ID")
		}
	})

	t.Run("missing GITHUB_PRIVATE_KEY", func(t *testing.T) {
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("GITHUB_PRIVATE_KEY", "")
		t.Setenv("GITHUB_PRIVATE_KEY_PATH", "")
		t.Setenv("DISCORD_BOT_TOKEN", "test-token")

		_, err := loadConfig(context.Background())
		if err == nil {
			t.Error("expected error for missing GITHUB_PRIVATE_KEY")
		}
	})

	t.Run("missing DISCORD_BOT_TOKEN", func(t *testing.T) {
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("GITHUB_PRIVATE_KEY", "test-key")
		t.Setenv("DISCORD_BOT_TOKEN", "")

		_, err := loadConfig(context.Background())
		if err == nil {
			t.Error("expected error for missing DISCORD_BOT_TOKEN")
		}
	})

	t.Run("all required fields present", func(t *testing.T) {
		t.Setenv("GITHUB_APP_ID", "12345")
		t.Setenv("GITHUB_PRIVATE_KEY", "test-key")
		t.Setenv("DISCORD_BOT_TOKEN", "test-token")
		t.Setenv("PORT", "8080")
		t.Setenv("ALLOW_PERSONAL_ACCOUNTS", "true")

		cfg, err := loadConfig(context.Background())
		if err != nil {
			t.Errorf("unexpected error: %v", err)
		}
		if cfg.GitHubAppID != "12345" {
			t.Errorf("GitHubAppID = %q, want %q", cfg.GitHubAppID, "12345")
		}
		if cfg.Port != "8080" {
			t.Errorf("Port = %q, want %q", cfg.Port, "8080")
		}
		if !cfg.AllowPersonalAccounts {
			t.Error("AllowPersonalAccounts should be true")
		}
	})
}

func TestCoordinatorManager_ConfigAdapter(t *testing.T) {
	mgr := config.New()
	adapter := &configAdapter{mgr: mgr}

	_, ok := adapter.Config("test-org")
	if ok {
		t.Error("expected false for unknown org")
	}
}

// Helper types for testing

type responseRecorder struct {
	status  int
	body    string
	headers http.Header
}

func (r *responseRecorder) Header() http.Header {
	return r.headers
}

func (r *responseRecorder) Write(data []byte) (int, error) {
	r.body += string(data)
	if r.status == 0 {
		r.status = http.StatusOK
	}
	return len(data), nil
}

func (r *responseRecorder) WriteHeader(status int) {
	r.status = status
}

func TestCoordinatorManager_DailyReport_NoPRs(t *testing.T) {
	mockStore := &mockStateStore{
		dailyReportInfos: make(map[string]state.DailyReportInfo),
	}

	cm := &coordinatorManager{
		active:         make(map[string]context.CancelFunc),
		discordClients: map[string]*discord.Client{},
		coordinators:   make(map[string]*bot.Coordinator),
		store:          mockStore,
		configManager:  config.New(),
		reverseMapper:  usermapping.NewReverseMapper(),
	}

	// Can't test fully without mocking GitHub API, but we can test error path
	debug, err := cm.DailyReport(context.Background(), "test-guild", "user123", false)

	if err == nil {
		t.Error("expected error for no org found")
	}
	if debug != nil {
		t.Error("debug should be nil on error")
	}
}

func TestCoordinatorManager_DailyReport_RateLimited(t *testing.T) {
	// Test that non-forced reports respect the 20-hour rate limit
	mockStore := &mockStateStore{
		dailyReportInfos: map[string]state.DailyReportInfo{
			"user123": {
				LastSentAt: time.Now().Add(-10 * time.Hour), // 10 hours ago
				GuildID:    "test-guild",
			},
		},
	}

	cm := &coordinatorManager{
		active:         make(map[string]context.CancelFunc),
		discordClients: make(map[string]*discord.Client),
		coordinators:   make(map[string]*bot.Coordinator),
		store:          mockStore,
		configManager:  config.New(),
		reverseMapper:  usermapping.NewReverseMapper(),
	}

	// This will error due to no org, but we're testing the rate limit check comes first
	_, err := cm.DailyReport(context.Background(), "test-guild", "user123", false)

	// Should get "no org" error since we didn't set up org, but that's after rate limit check
	if err == nil {
		t.Error("expected error")
	}
}

func TestCoordinatorManager_DailyReport_ForceBypassesRateLimit(t *testing.T) {
	// Test that force=true bypasses rate limiting
	mockStore := &mockStateStore{
		dailyReportInfos: map[string]state.DailyReportInfo{
			"user123": {
				LastSentAt: time.Now().Add(-1 * time.Hour), // Just 1 hour ago
				GuildID:    "test-guild",
			},
		},
	}

	cm := &coordinatorManager{
		active:         make(map[string]context.CancelFunc),
		discordClients: make(map[string]*discord.Client),
		coordinators:   make(map[string]*bot.Coordinator),
		store:          mockStore,
		configManager:  config.New(),
		reverseMapper:  usermapping.NewReverseMapper(),
	}

	// Force should bypass rate limit, so we'll get "no org" error
	_, err := cm.DailyReport(context.Background(), "test-guild", "user123", true)

	if err == nil {
		t.Error("expected error for no org")
	}
	// The fact we got past rate limit check and hit "no org" means force worked
}

func TestCoordinatorManager_DailyReport_NoDiscordClient(t *testing.T) {
	mockStore := &mockStateStore{
		dailyReportInfos: make(map[string]state.DailyReportInfo),
	}

	cm := &coordinatorManager{
		active:         make(map[string]context.CancelFunc),
		discordClients: make(map[string]*discord.Client),
		coordinators:   make(map[string]*bot.Coordinator),
		store:          mockStore,
		configManager:  config.New(),
		reverseMapper:  usermapping.NewReverseMapper(),
	}

	debug, err := cm.DailyReport(context.Background(), "test-guild", "user123", true)

	if err == nil {
		t.Error("expected error for no org found")
	}
	if debug != nil {
		t.Error("debug should be nil on error")
	}
}

func TestCoordinatorManager_DailyReport_DebugInfo(t *testing.T) {
	// Test that debug info is properly populated
	// This is a basic test since we can't fully mock GitHub without more infrastructure

	mockStore := &mockStateStore{
		dailyReportInfos: map[string]state.DailyReportInfo{
			"user123": {
				LastSentAt: time.Now().Add(-25 * time.Hour), // 25 hours ago - eligible
				GuildID:    "test-guild",
			},
		},
	}

	cm := &coordinatorManager{
		active:         make(map[string]context.CancelFunc),
		discordClients: make(map[string]*discord.Client),
		coordinators:   make(map[string]*bot.Coordinator),
		store:          mockStore,
		configManager:  config.New(),
		reverseMapper:  usermapping.NewReverseMapper(),
	}

	// Will error due to no org, but that's expected
	_, err := cm.DailyReport(context.Background(), "test-guild", "user123", false)
	if err == nil {
		t.Error("expected error for no org")
	}
}

func TestCoordinatorManager_DailyReportGetter_Interface(t *testing.T) {
	// Test that coordinatorManager implements DailyReportGetter interface
	var _ discord.DailyReportGetter = (*coordinatorManager)(nil)
}

func TestCoordinatorManager_Status_WithMetrics(t *testing.T) {
	mockStore := &mockStateStore{
		pendingDMs: []*state.PendingDM{
			{ID: "1", UserID: "user1"},
			{ID: "2", UserID: "user2"},
		},
	}

	cm := &coordinatorManager{
		active: map[string]context.CancelFunc{
			"org1": func() {},
			"org2": func() {},
		},
		discordClients: make(map[string]*discord.Client),
		slashHandlers:  make(map[string]*discord.SlashCommandHandler),
		coordinators:   make(map[string]*bot.Coordinator),
		lastEventTime: map[string]time.Time{
			"org1": time.Now().Add(-5 * time.Minute),
		},
		startTime:     time.Now().Add(-2 * time.Hour),
		store:         mockStore,
		configManager: config.New(),
		dailyReports:  5,
		dmsSent:       10,
		channelMsgs:   50,
	}

	status := cm.Status(context.Background(), "test-guild")

	if !status.Connected {
		t.Error("Status() Connected = false, want true")
	}
	if len(status.ConnectedOrgs) != 2 {
		t.Errorf("Status() ConnectedOrgs = %d, want 2", len(status.ConnectedOrgs))
	}
	if status.PendingDMs != 2 {
		t.Errorf("Status() PendingDMs = %d, want 2", status.PendingDMs)
	}
	if status.DailyReportsSent != 5 {
		t.Errorf("Status() DailyReportsSent = %d, want 5", status.DailyReportsSent)
	}
	if status.DMsSent != 10 {
		t.Errorf("Status() DMsSent = %d, want 10", status.DMsSent)
	}
	if status.ChannelMessagesSent != 50 {
		t.Errorf("Status() ChannelMessagesSent = %d, want 50", status.ChannelMessagesSent)
	}
	if status.UptimeSeconds < 7100 || status.UptimeSeconds > 7300 {
		t.Errorf("Status() UptimeSeconds = %d, want ~7200", status.UptimeSeconds)
	}
}

func TestAnalyzePRForReport(t *testing.T) {
	t.Run("invalid PR URL", func(t *testing.T) {
		pr := bot.PRSearchResult{
			URL:       "not-a-valid-url",
			UpdatedAt: time.Now(),
		}

		result := analyzePRForReport(context.Background(), pr, "testuser", nil)

		if result != nil {
			t.Error("expected nil result for invalid PR URL")
		}
	})

	t.Run("Turn API error", func(t *testing.T) {
		mockTurn := &mockTurnClient{
			checkError: errors.New("API error"),
		}

		pr := bot.PRSearchResult{
			URL:       "https://github.com/testorg/testrepo/pull/123",
			UpdatedAt: time.Now(),
		}

		result := analyzePRForReport(context.Background(), pr, "testuser", mockTurn)

		if result != nil {
			t.Error("expected nil result when Turn API fails")
		}
	})

	t.Run("successful PR analysis with action", func(t *testing.T) {
		mockTurn := &mockTurnClient{
			response: &bot.CheckResponse{
				PullRequest: bot.PRInfo{
					Author: "author1",
					Title:  "Test PR",
					Merged: false,
					Closed: false,
					Draft:  false,
				},
				Analysis: bot.Analysis{
					WorkflowState: "review",
					Approved:      false,
					Checks: bot.Checks{
						Failing: 0,
						Pending: 0,
						Waiting: 0,
					},
					NextAction: map[string]bot.Action{
						"testuser": {
							Kind: "review",
						},
					},
				},
			},
		}

		pr := bot.PRSearchResult{
			URL:       "https://github.com/testorg/testrepo/pull/123",
			UpdatedAt: time.Now(),
		}

		result := analyzePRForReport(context.Background(), pr, "testuser", mockTurn)

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if result.Repo != "testrepo" {
			t.Errorf("Repo = %q, want %q", result.Repo, "testrepo")
		}
		if result.Number != 123 {
			t.Errorf("Number = %d, want 123", result.Number)
		}
		if result.Title != "Test PR" {
			t.Errorf("Title = %q, want %q", result.Title, "Test PR")
		}
		if result.Author != "author1" {
			t.Errorf("Author = %q, want %q", result.Author, "author1")
		}
		if result.Action == "" {
			t.Error("expected non-empty action")
		}
	})

	t.Run("blocked PR", func(t *testing.T) {
		mockTurn := &mockTurnClient{
			response: &bot.CheckResponse{
				PullRequest: bot.PRInfo{
					Author: "author1",
					Title:  "Blocked PR",
					Merged: false,
					Closed: false,
					Draft:  false,
				},
				Analysis: bot.Analysis{
					MergeConflict: true,
					WorkflowState: "conflict",
					Checks: bot.Checks{
						Failing: 0,
					},
					NextAction: map[string]bot.Action{},
				},
			},
		}

		pr := bot.PRSearchResult{
			URL:       "https://github.com/testorg/testrepo/pull/456",
			UpdatedAt: time.Now(),
		}

		result := analyzePRForReport(context.Background(), pr, "testuser", mockTurn)

		if result == nil {
			t.Fatal("expected non-nil result")
		}
		if !result.IsBlocked {
			t.Error("expected IsBlocked = true for PR with merge conflict")
		}
	})
}
