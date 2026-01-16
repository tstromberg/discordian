package main

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/bot"
	"github.com/codeGROOVE-dev/discordian/internal/config"
	"github.com/codeGROOVE-dev/discordian/internal/discord"
	"github.com/codeGROOVE-dev/discordian/internal/state"
)

// Mock implementations for testing

type mockStateStore struct {
	pendingDMs       []*state.PendingDM
	dailyReportInfos map[string]state.DailyReportInfo
}

func (m *mockStateStore) Thread(_ context.Context, _, _ string, _ int, _ string) (state.ThreadInfo, bool) {
	return state.ThreadInfo{}, false
}

func (m *mockStateStore) SaveThread(_ context.Context, _, _ string, _ int, _ string, _ state.ThreadInfo) error {
	return nil
}

func (m *mockStateStore) DMInfo(_ context.Context, _, _ string) (state.DMInfo, bool) {
	return state.DMInfo{}, false
}

func (m *mockStateStore) SaveDMInfo(_ context.Context, _, _ string, _ state.DMInfo) error {
	return nil
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
		}

		_, err := cm.Report(context.Background(), "unknown-guild", "user123")

		if err == nil {
			t.Error("expected error for unknown guild")
		}
		if err != nil && err.Error() != "no org found for guild unknown-guild" {
			t.Errorf("unexpected error message: %v", err)
		}
	})

	t.Run("no Discord client for guild", func(t *testing.T) {
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
			configManager:  config.New(),
		}

		_, err := cm.Report(context.Background(), "test-guild", "user123")

		if err == nil {
			t.Error("expected error for missing Discord client")
		}
	})
}

func TestGetEnv(t *testing.T) {
	t.Run("with value set", func(t *testing.T) {
		t.Setenv("TEST_VAR", "test-value")
		result := getEnv("TEST_VAR", "default")
		if result != "test-value" {
			t.Errorf("getEnv() = %q, want 'test-value'", result)
		}
	})

	t.Run("with default", func(t *testing.T) {
		result := getEnv("NONEXISTENT_VAR", "default-value")
		if result != "default-value" {
			t.Errorf("getEnv() = %q, want 'default-value'", result)
		}
	})

	t.Run("empty string uses default", func(t *testing.T) {
		t.Setenv("EMPTY_VAR", "")
		result := getEnv("EMPTY_VAR", "default")
		if result != "default" {
			t.Errorf("getEnv() = %q, want 'default' for empty string", result)
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
