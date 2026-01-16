package notify

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/state"
)

// mockStore implements state.Store for testing
type mockStore struct {
	pendingDMs  []*state.PendingDM
	removedDMs  []string
	savedDMInfo map[string]state.DMInfo
	removeErr   error
	saveDMErr   error
	pendingErr  error
}

func newMockStore() *mockStore {
	return &mockStore{
		savedDMInfo: make(map[string]state.DMInfo),
	}
}

func (m *mockStore) Thread(_ context.Context, _, _ string, _ int, _ string) (state.ThreadInfo, bool) {
	return state.ThreadInfo{}, false
}

func (m *mockStore) SaveThread(_ context.Context, _, _ string, _ int, _ string, _ state.ThreadInfo) error {
	return nil
}

func (m *mockStore) DMInfo(_ context.Context, userID, prURL string) (state.DMInfo, bool) {
	key := userID + ":" + prURL
	info, ok := m.savedDMInfo[key]
	return info, ok
}

func (m *mockStore) SaveDMInfo(_ context.Context, userID, prURL string, info state.DMInfo) error {
	if m.saveDMErr != nil {
		return m.saveDMErr
	}
	key := userID + ":" + prURL
	m.savedDMInfo[key] = info
	return nil
}

func (m *mockStore) ListDMUsers(_ context.Context, _ string) []string {
	return nil
}

func (m *mockStore) WasProcessed(_ context.Context, _ string) bool {
	return false
}

func (m *mockStore) MarkProcessed(_ context.Context, _ string, _ time.Duration) error {
	return nil
}

func (m *mockStore) QueuePendingDM(_ context.Context, dm *state.PendingDM) error {
	m.pendingDMs = append(m.pendingDMs, dm)
	return nil
}

func (m *mockStore) PendingDMs(_ context.Context, before time.Time) ([]*state.PendingDM, error) {
	if m.pendingErr != nil {
		return nil, m.pendingErr
	}
	var result []*state.PendingDM
	for _, dm := range m.pendingDMs {
		if dm.SendAt.Before(before) || dm.SendAt.Equal(before) {
			result = append(result, dm)
		}
	}
	return result, nil
}

func (m *mockStore) RemovePendingDM(_ context.Context, id string) error {
	if m.removeErr != nil {
		return m.removeErr
	}
	m.removedDMs = append(m.removedDMs, id)
	return nil
}

func (m *mockStore) Cleanup(_ context.Context) error {
	return nil
}

func (m *mockStore) Close() error {
	return nil
}

func (m *mockStore) DailyReportInfo(_ context.Context, _ string) (state.DailyReportInfo, bool) {
	return state.DailyReportInfo{}, false
}

func (m *mockStore) SaveDailyReportInfo(_ context.Context, _ string, _ state.DailyReportInfo) error {
	return nil
}

// mockDMSender implements DiscordDMSender for testing
type mockDMSender struct {
	sentDMs   []sentDM
	sendErr   error
	channelID string
	messageID string
}

type sentDM struct {
	userID string
	text   string
}

func newMockDMSender() *mockDMSender {
	return &mockDMSender{
		channelID: "dm-channel-123",
		messageID: "dm-message-456",
	}
}

func (m *mockDMSender) SendDM(_ context.Context, userID, text string) (channelID, messageID string, err error) {
	if m.sendErr != nil {
		return "", "", m.sendErr
	}
	m.sentDMs = append(m.sentDMs, sentDM{userID: userID, text: text})
	return m.channelID, m.messageID, nil
}

func TestManager_RegisterGuild(t *testing.T) {
	store := newMockStore()
	manager := New(store, nil)

	sender := newMockDMSender()
	manager.RegisterGuild("guild123", sender)

	// Verify sender is registered
	manager.mu.RLock()
	got := manager.dmSenders["guild123"]
	manager.mu.RUnlock()

	if got != sender {
		t.Error("RegisterGuild() did not register sender")
	}
}

func TestManager_ProcessPendingDMs(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	manager := New(store, nil)

	sender := newMockDMSender()
	manager.RegisterGuild("guild1", sender)

	// Queue a DM
	dm := &state.PendingDM{
		ID:          "dm1",
		UserID:      "user1",
		GuildID:     "guild1",
		PRURL:       "https://github.com/o/r/pull/1",
		MessageText: "Hello",
		SendAt:      time.Now().Add(-time.Hour), // Past
	}
	store.pendingDMs = append(store.pendingDMs, dm)

	// Process
	manager.processPendingDMs(ctx)

	// Verify DM was sent
	if len(sender.sentDMs) != 1 {
		t.Fatalf("Expected 1 DM sent, got %d", len(sender.sentDMs))
	}
	if sender.sentDMs[0].userID != "user1" {
		t.Errorf("Sent to wrong user: %s", sender.sentDMs[0].userID)
	}

	// Verify DM was removed from queue
	if len(store.removedDMs) != 1 || store.removedDMs[0] != "dm1" {
		t.Errorf("DM not removed from queue: %v", store.removedDMs)
	}

	// Verify DM info was saved
	if len(store.savedDMInfo) != 1 {
		t.Error("DM info not saved")
	}
}

func TestManager_ProcessPendingDMs_NoSender(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	manager := New(store, nil)

	// Queue a DM but don't register sender
	dm := &state.PendingDM{
		ID:          "dm1",
		UserID:      "user1",
		GuildID:     "guild1",
		PRURL:       "https://github.com/o/r/pull/1",
		MessageText: "Hello",
		SendAt:      time.Now().Add(-time.Hour),
	}
	store.pendingDMs = append(store.pendingDMs, dm)

	// Process
	manager.processPendingDMs(ctx)

	// DM should be removed since no sender available
	// (current implementation returns nil error for missing sender)
}

func TestManager_ProcessPendingDMs_SendError(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	manager := New(store, nil)

	sender := newMockDMSender()
	sender.sendErr = errors.New("discord error")
	manager.RegisterGuild("guild1", sender)

	// Queue a DM
	dm := &state.PendingDM{
		ID:          "dm1",
		UserID:      "user1",
		GuildID:     "guild1",
		PRURL:       "https://github.com/o/r/pull/1",
		MessageText: "Hello",
		SendAt:      time.Now().Add(-time.Hour),
	}
	store.pendingDMs = append(store.pendingDMs, dm)

	// Process
	manager.processPendingDMs(ctx)

	// DM should NOT be removed from queue due to error
	if len(store.removedDMs) != 0 {
		t.Error("DM should not be removed when send fails")
	}
}

func TestManager_ProcessPendingDMs_RateLimit(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	manager := New(store, nil)

	sender := newMockDMSender()
	manager.RegisterGuild("guild1", sender)

	// Set recent DM time for user
	manager.lastDMTime["user1"] = time.Now()

	// Queue a DM
	dm := &state.PendingDM{
		ID:          "dm1",
		UserID:      "user1",
		GuildID:     "guild1",
		PRURL:       "https://github.com/o/r/pull/1",
		MessageText: "Hello",
		SendAt:      time.Now().Add(-time.Hour),
	}
	store.pendingDMs = append(store.pendingDMs, dm)

	// Process
	manager.processPendingDMs(ctx)

	// DM should NOT be sent due to rate limit
	if len(sender.sentDMs) != 0 {
		t.Error("DM should not be sent due to rate limit")
	}

	// Note: Current implementation returns nil for rate-limited DMs,
	// which causes them to be removed. This is a design choice -
	// rate-limited DMs are skipped and will be re-queued if needed.
}

func TestManager_ProcessPendingDMs_FetchError(t *testing.T) {
	ctx := context.Background()
	store := newMockStore()
	store.pendingErr = errors.New("db error")
	manager := New(store, nil)

	// Should not panic
	manager.processPendingDMs(ctx)
}

func TestManager_StartStop(t *testing.T) {
	store := newMockStore()
	manager := New(store, nil)

	ctx, cancel := context.WithCancel(context.Background())

	manager.Start(ctx)

	// Give it time to start
	time.Sleep(10 * time.Millisecond)

	// Stop via context
	cancel()
	manager.wg.Wait()

	// Test stop via Stop()
	manager2 := New(store, nil)
	ctx2 := context.Background()
	manager2.Start(ctx2)
	time.Sleep(10 * time.Millisecond)
	manager2.Stop()
}

func TestTracker_TaggedInChannel(t *testing.T) {
	tracker := NewTracker()

	prURL := "https://github.com/o/r/pull/1"
	userID := "user123"

	// Initially not tagged
	if tracker.WasTaggedInChannel(prURL, userID) {
		t.Error("WasTaggedInChannel() should return false initially")
	}

	// Mark as tagged
	tracker.MarkTaggedInChannel(prURL, userID)

	// Now tagged
	if !tracker.WasTaggedInChannel(prURL, userID) {
		t.Error("WasTaggedInChannel() should return true after marking")
	}

	// Different user not tagged
	if tracker.WasTaggedInChannel(prURL, "other-user") {
		t.Error("WasTaggedInChannel() should return false for different user")
	}

	// Different PR not tagged
	if tracker.WasTaggedInChannel("other-pr", userID) {
		t.Error("WasTaggedInChannel() should return false for different PR")
	}
}

func TestTracker_DMTime(t *testing.T) {
	tracker := NewTracker()

	userID := "user123"
	prURL := "https://github.com/o/r/pull/1"

	// Initially zero
	if !tracker.LastDMTime(userID, prURL).IsZero() {
		t.Error("LastDMTime() should return zero initially")
	}

	// Mark DM sent
	tracker.MarkDMSent(userID, prURL)

	// Now has time
	lastTime := tracker.LastDMTime(userID, prURL)
	if lastTime.IsZero() {
		t.Error("LastDMTime() should return non-zero after marking")
	}

	// Time should be recent
	if time.Since(lastTime) > time.Second {
		t.Error("LastDMTime() should return recent time")
	}

	// Different user has zero
	if !tracker.LastDMTime("other-user", prURL).IsZero() {
		t.Error("LastDMTime() should return zero for different user")
	}
}
