package dailyreport

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/discord"
	"github.com/codeGROOVE-dev/discordian/internal/format"
	"github.com/codeGROOVE-dev/discordian/internal/state"
)

// mockStateStore implements StateStore for testing
type mockStateStore struct {
	reports map[string]state.DailyReportInfo
}

func newMockStateStore() *mockStateStore {
	return &mockStateStore{
		reports: make(map[string]state.DailyReportInfo),
	}
}

func (m *mockStateStore) DailyReportInfo(_ context.Context, userID string) (state.DailyReportInfo, bool) {
	info, ok := m.reports[userID]
	return info, ok
}

func (m *mockStateStore) SaveDailyReportInfo(_ context.Context, userID string, info state.DailyReportInfo) error {
	m.reports[userID] = info
	return nil
}

// mockDMSender implements DiscordDMSender for testing
type mockDMSender struct {
	sentDMs []sentDM
	sendErr error
}

type sentDM struct {
	userID string
	text   string
}

func newMockDMSender() *mockDMSender {
	return &mockDMSender{}
}

func (m *mockDMSender) SendDM(_ context.Context, userID, text string) (channelID, messageID string, err error) {
	if m.sendErr != nil {
		return "", "", m.sendErr
	}
	m.sentDMs = append(m.sentDMs, sentDM{userID: userID, text: text})
	return "dm-channel-123", "dm-message-456", nil
}

func TestSender_ShouldSendReport_NoPRs(t *testing.T) {
	store := newMockStateStore()
	sender := NewSender(store, nil)

	userInfo := UserBlockingInfo{
		DiscordUserID: "user123",
		IncomingPRs:   nil,
		OutgoingPRs:   nil,
	}

	if sender.ShouldSendReport(context.Background(), userInfo) {
		t.Error("ShouldSendReport() should return false when no PRs")
	}
}

func TestSender_ShouldSendReport_RecentlySent(t *testing.T) {
	store := newMockStateStore()
	store.reports["user123"] = state.DailyReportInfo{
		LastSentAt: time.Now().Add(-1 * time.Hour), // Sent 1 hour ago
	}
	sender := NewSender(store, nil)

	userInfo := UserBlockingInfo{
		DiscordUserID: "user123",
		IncomingPRs:   []discord.PRSummary{{Repo: "test", Number: 1}},
	}

	if sender.ShouldSendReport(context.Background(), userInfo) {
		t.Error("ShouldSendReport() should return false when sent recently")
	}
}

func TestSender_ShouldSendReport_InvalidTimezone(t *testing.T) {
	store := newMockStateStore()
	sender := NewSender(store, nil)

	userInfo := UserBlockingInfo{
		DiscordUserID: "user123",
		Timezone:      "Invalid/Timezone",
		IncomingPRs:   []discord.PRSummary{{Repo: "test", Number: 1}},
	}

	// Should not panic, should fall back to UTC
	_ = sender.ShouldSendReport(context.Background(), userInfo)
}

func TestSender_SendReport(t *testing.T) {
	store := newMockStateStore()
	sender := NewSender(store, nil)
	dmSender := newMockDMSender()
	sender.RegisterGuild("guild1", dmSender)

	userInfo := UserBlockingInfo{
		DiscordUserID:  "user123",
		GitHubUsername: "ghuser",
		GuildID:        "guild1",
		IncomingPRs: []discord.PRSummary{
			{Repo: "myrepo", Number: 42, Title: "Fix bug", URL: "https://github.com/o/myrepo/pull/42"},
		},
	}

	err := sender.SendReport(context.Background(), userInfo)
	if err != nil {
		t.Fatalf("SendReport() error = %v", err)
	}

	if len(dmSender.sentDMs) != 1 {
		t.Fatalf("Expected 1 DM sent, got %d", len(dmSender.sentDMs))
	}

	if dmSender.sentDMs[0].userID != "user123" {
		t.Errorf("DM sent to wrong user: %s", dmSender.sentDMs[0].userID)
	}

	// Check that report was recorded
	info, exists := store.reports["user123"]
	if !exists {
		t.Error("Report send time should be recorded")
	}
	if time.Since(info.LastSentAt) > time.Second {
		t.Error("Report send time should be recent")
	}
}

func TestSender_SendReport_NoSender(t *testing.T) {
	store := newMockStateStore()
	sender := NewSender(store, nil)
	// Don't register a DM sender

	userInfo := UserBlockingInfo{
		DiscordUserID: "user123",
		GuildID:       "guild1",
		IncomingPRs:   []discord.PRSummary{{Repo: "test", Number: 1}},
	}

	// Should not error when no sender is registered
	err := sender.SendReport(context.Background(), userInfo)
	if err != nil {
		t.Errorf("SendReport() should not error when no sender: %v", err)
	}
}

func TestBuildReportMessage_Empty(t *testing.T) {
	msg := BuildReportMessage(nil, nil)

	if !strings.Contains(msg, "daily report") {
		t.Error("Message should contain 'daily report'")
	}
	if strings.Contains(msg, "Incoming PRs") {
		t.Error("Message should not contain 'Incoming PRs' when empty")
	}
	if strings.Contains(msg, "Your PRs") {
		t.Error("Message should not contain 'Your PRs' when empty")
	}
}

func TestBuildReportMessage_WithIncoming(t *testing.T) {
	incoming := []discord.PRSummary{
		{
			Repo:      "myrepo",
			Number:    42,
			Title:     "Fix the bug",
			URL:       "https://github.com/o/myrepo/pull/42",
			Action:    "needs review",
			IsBlocked: true,
		},
	}

	msg := BuildReportMessage(incoming, nil)

	if !strings.Contains(msg, "Incoming PRs") {
		t.Error("Message should contain 'Incoming PRs'")
	}
	if !strings.Contains(msg, "myrepo#42") {
		t.Error("Message should contain PR reference")
	}
	if !strings.Contains(msg, "needs review") {
		t.Error("Message should contain action")
	}
}

func TestBuildReportMessage_WithOutgoing(t *testing.T) {
	outgoing := []discord.PRSummary{
		{
			Repo:      "myrepo",
			Number:    99,
			Title:     "New feature",
			URL:       "https://github.com/o/myrepo/pull/99",
			Action:    "waiting for review",
			IsBlocked: false,
		},
	}

	msg := BuildReportMessage(nil, outgoing)

	if !strings.Contains(msg, "Your PRs") {
		t.Error("Message should contain 'Your PRs'")
	}
	if !strings.Contains(msg, "myrepo#99") {
		t.Error("Message should contain PR reference")
	}
}

func TestBuildReportMessage_LongTitle(t *testing.T) {
	longTitle := strings.Repeat("x", 100)
	incoming := []discord.PRSummary{
		{Repo: "repo", Number: 1, Title: longTitle, URL: "url"},
	}

	msg := BuildReportMessage(incoming, nil)

	// Title should be truncated
	if strings.Contains(msg, longTitle) {
		t.Error("Long title should be truncated")
	}
	if !strings.Contains(msg, "...") {
		t.Error("Truncated title should contain ellipsis")
	}
}

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"no truncation needed", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"very short max", "hello", 2, "he"},
		{"max 3", "hello", 3, "hel"},
		{"max 4", "hello", 4, "h..."},
		{"empty string", "", 10, ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := format.Truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
			if len(got) > tt.maxLen {
				t.Errorf("Truncate(%q, %d) length = %d, want <= %d", tt.input, tt.maxLen, len(got), tt.maxLen)
			}
		})
	}
}

func TestRandomGreeting(t *testing.T) {
	// Just verify it doesn't panic and returns non-empty
	greeting := randomGreeting()
	if greeting == "" {
		t.Error("randomGreeting() should return non-empty string")
	}
}

func TestSender_SendReport_DMError(t *testing.T) {
	store := newMockStateStore()
	sender := NewSender(store, nil)
	dmSender := newMockDMSender()
	dmSender.sendErr = context.DeadlineExceeded // Simulate an error
	sender.RegisterGuild("guild1", dmSender)

	userInfo := UserBlockingInfo{
		DiscordUserID:  "user123",
		GitHubUsername: "ghuser",
		GuildID:        "guild1",
		IncomingPRs:    []discord.PRSummary{{Repo: "test", Number: 1}},
	}

	err := sender.SendReport(context.Background(), userInfo)
	if err == nil {
		t.Error("SendReport() should return error when SendDM fails")
	}
	if !strings.Contains(err.Error(), "failed to send DM") {
		t.Errorf("error should mention 'failed to send DM', got: %v", err)
	}
}

func TestNewSender(t *testing.T) {
	store := newMockStateStore()

	t.Run("with nil logger", func(t *testing.T) {
		sender := NewSender(store, nil)
		if sender.logger == nil {
			t.Error("logger should default to slog.Default()")
		}
	})
}

func TestSender_RegisterGuild(t *testing.T) {
	store := newMockStateStore()
	sender := NewSender(store, nil)
	dmSender := newMockDMSender()

	sender.RegisterGuild("guild123", dmSender)

	if sender.dmSenders["guild123"] != dmSender {
		t.Error("RegisterGuild() should register the sender")
	}
}
