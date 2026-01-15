package state

import (
	"context"
	"testing"
	"time"
)

func TestMemoryStore(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer store.Close() //nolint:errcheck // test cleanup

	t.Run("thread operations", func(t *testing.T) {
		// Initially no thread
		_, ok := store.Thread(ctx, "owner", "repo", 1, "chan1")
		if ok {
			t.Error("Thread() found non-existent thread")
		}

		// Save thread
		info := ThreadInfo{
			ThreadID:    "thread123",
			MessageID:   "msg456",
			ChannelID:   "chan1",
			ChannelType: "forum",
			LastState:   "needs_review",
		}
		if err := store.SaveThread(ctx, "owner", "repo", 1, "chan1", info); err != nil {
			t.Fatalf("SaveThread() error = %v", err)
		}

		// Retrieve thread
		got, ok := store.Thread(ctx, "owner", "repo", 1, "chan1")
		if !ok {
			t.Fatal("Thread() did not find saved thread")
		}
		if got.ThreadID != info.ThreadID {
			t.Errorf("Thread().ThreadID = %q, want %q", got.ThreadID, info.ThreadID)
		}
		if got.UpdatedAt.IsZero() {
			t.Error("Thread().UpdatedAt should be set")
		}

		// Different channel returns nothing
		_, ok = store.Thread(ctx, "owner", "repo", 1, "chan2")
		if ok {
			t.Error("Thread() should not find thread for different channel")
		}
	})

	t.Run("DM info operations", func(t *testing.T) {
		prURL := "https://github.com/owner/repo/pull/42"

		// Initially no DM info
		_, ok := store.DMInfo(ctx, "user1", prURL)
		if ok {
			t.Error("DMInfo() found non-existent info")
		}

		// Save DM info
		info := DMInfo{
			ChannelID:   "dmchan123",
			MessageID:   "dmmsg456",
			MessageText: "Hello",
			SentAt:      time.Now(),
		}
		if err := store.SaveDMInfo(ctx, "user1", prURL, info); err != nil {
			t.Fatalf("SaveDMInfo() error = %v", err)
		}

		// Retrieve DM info
		got, ok := store.DMInfo(ctx, "user1", prURL)
		if !ok {
			t.Fatal("DMInfo() did not find saved info")
		}
		if got.ChannelID != info.ChannelID {
			t.Errorf("DMInfo().ChannelID = %q, want %q", got.ChannelID, info.ChannelID)
		}

		// Different user returns nothing
		_, ok = store.DMInfo(ctx, "user2", prURL)
		if ok {
			t.Error("DMInfo() should not find info for different user")
		}
	})

	t.Run("event processing", func(t *testing.T) {
		eventKey := "event123"

		// Initially not processed
		if store.WasProcessed(ctx, eventKey) {
			t.Error("WasProcessed() returned true for unprocessed event")
		}

		// Mark processed
		if err := store.MarkProcessed(ctx, eventKey, time.Hour); err != nil {
			t.Fatalf("MarkProcessed() error = %v", err)
		}

		// Now processed
		if !store.WasProcessed(ctx, eventKey) {
			t.Error("WasProcessed() returned false for processed event")
		}
	})

	t.Run("pending DMs", func(t *testing.T) {
		now := time.Now()

		dm1 := &PendingDM{
			ID:          "dm1",
			UserID:      "user1",
			PRURL:       "https://github.com/o/r/pull/1",
			MessageText: "Hello",
			SendAt:      now.Add(-time.Hour), // Past
			GuildID:     "guild1",
		}
		dm2 := &PendingDM{
			ID:          "dm2",
			UserID:      "user2",
			PRURL:       "https://github.com/o/r/pull/2",
			MessageText: "World",
			SendAt:      now.Add(time.Hour), // Future
			GuildID:     "guild1",
		}

		if err := store.QueuePendingDM(ctx, dm1); err != nil {
			t.Fatalf("QueuePendingDM() error = %v", err)
		}
		if err := store.QueuePendingDM(ctx, dm2); err != nil {
			t.Fatalf("QueuePendingDM() error = %v", err)
		}

		// Get pending DMs due now
		pending, err := store.PendingDMs(ctx, now)
		if err != nil {
			t.Fatalf("PendingDMs() error = %v", err)
		}
		if len(pending) != 1 {
			t.Errorf("PendingDMs() returned %d, want 1", len(pending))
		}
		if len(pending) > 0 && pending[0].ID != "dm1" {
			t.Errorf("PendingDMs()[0].ID = %q, want dm1", pending[0].ID)
		}

		// Remove dm1
		if err := store.RemovePendingDM(ctx, "dm1"); err != nil {
			t.Fatalf("RemovePendingDM() error = %v", err)
		}

		// dm1 should be gone
		pending, err = store.PendingDMs(ctx, now)
		if err != nil {
			t.Fatalf("PendingDMs() error = %v", err)
		}
		if len(pending) != 0 {
			t.Errorf("PendingDMs() returned %d after removal, want 0", len(pending))
		}
	})

	t.Run("stats", func(t *testing.T) {
		stats := store.Stats()
		if stats.Threads < 1 {
			t.Errorf("Stats() threads = %d, want >= 1", stats.Threads)
		}
		if stats.DMs < 1 {
			t.Errorf("Stats() dms = %d, want >= 1", stats.DMs)
		}
		if stats.Events < 1 {
			t.Errorf("Stats() events = %d, want >= 1", stats.Events)
		}
		// pending could be 0 or 1 depending on previous test
		_ = stats.Pending
	})
}

func TestMemoryStore_Cleanup(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer store.Close() //nolint:errcheck // test cleanup

	// Manually set short retention periods for testing
	store.eventRetain = time.Millisecond
	store.threadRetain = time.Millisecond
	store.dmRetain = time.Millisecond

	// Add data
	if err := store.MarkProcessed(ctx, "old-event", time.Millisecond); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	info := ThreadInfo{ThreadID: "old-thread"}
	if err := store.SaveThread(ctx, "o", "r", 1, "c", info); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	dmInfo := DMInfo{ChannelID: "dm-chan", SentAt: time.Now()}
	if err := store.SaveDMInfo(ctx, "user", "pr-url", dmInfo); err != nil {
		t.Fatalf("SaveDMInfo() error = %v", err)
	}

	// Wait for expiration
	time.Sleep(10 * time.Millisecond)

	// Cleanup
	if err := store.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	// Verify event was cleaned up
	if store.WasProcessed(ctx, "old-event") {
		t.Error("old event should have been cleaned up")
	}

	// Thread should be cleaned up too
	_, ok := store.Thread(ctx, "o", "r", 1, "c")
	if ok {
		t.Error("old thread should have been cleaned up")
	}

	// DM info should be cleaned up
	_, ok = store.DMInfo(ctx, "user", "pr-url")
	if ok {
		t.Error("old DM info should have been cleaned up")
	}
}

func TestThreadKey(t *testing.T) {
	key := threadKey("owner", "repo", 42, "chan123")
	expected := "owner/repo#42:chan123"
	if key != expected {
		t.Errorf("threadKey() = %q, want %q", key, expected)
	}
}

func TestDMKey(t *testing.T) {
	key := dmKey("user123", "https://github.com/o/r/pull/1")
	expected := "user123:https://github.com/o/r/pull/1"
	if key != expected {
		t.Errorf("dmKey() = %q, want %q", key, expected)
	}
}

func TestMemoryStore_Close(t *testing.T) {
	store := NewMemoryStore()
	if err := store.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestMemoryStore_WasProcessed_Expired(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	store.eventRetain = time.Millisecond // Very short retention

	// Mark as processed
	if err := store.MarkProcessed(ctx, "expiring-event", time.Millisecond); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	// Wait for expiration
	time.Sleep(5 * time.Millisecond)

	// Should return false since event expired
	if store.WasProcessed(ctx, "expiring-event") {
		t.Error("WasProcessed() should return false for expired event")
	}
}

func TestMemoryStore_DailyReportInfo(t *testing.T) {
	ctx := context.Background()
	store := NewMemoryStore()
	defer store.Close() //nolint:errcheck // test cleanup

	userID := "user123"

	// Initially no report info
	_, ok := store.DailyReportInfo(ctx, userID)
	if ok {
		t.Error("DailyReportInfo() found non-existent info")
	}

	// Save report info
	info := DailyReportInfo{
		LastSentAt: time.Now(),
		GuildID:    "guild123",
	}
	if err := store.SaveDailyReportInfo(ctx, userID, info); err != nil {
		t.Fatalf("SaveDailyReportInfo() error = %v", err)
	}

	// Retrieve report info
	got, ok := store.DailyReportInfo(ctx, userID)
	if !ok {
		t.Fatal("DailyReportInfo() did not find saved info")
	}
	if got.GuildID != info.GuildID {
		t.Errorf("DailyReportInfo().GuildID = %q, want %q", got.GuildID, info.GuildID)
	}
	if got.LastSentAt.IsZero() {
		t.Error("DailyReportInfo().LastSentAt should not be zero")
	}

	// Different user returns nothing
	_, ok = store.DailyReportInfo(ctx, "other-user")
	if ok {
		t.Error("DailyReportInfo() should not find info for different user")
	}

	// Update existing
	newInfo := DailyReportInfo{
		LastSentAt: time.Now().Add(time.Hour),
		GuildID:    "guild456",
	}
	if err := store.SaveDailyReportInfo(ctx, userID, newInfo); err != nil {
		t.Fatalf("SaveDailyReportInfo() update error = %v", err)
	}

	got, ok = store.DailyReportInfo(ctx, userID)
	if !ok {
		t.Fatal("DailyReportInfo() did not find updated info")
	}
	if got.GuildID != newInfo.GuildID {
		t.Errorf("Updated DailyReportInfo().GuildID = %q, want %q", got.GuildID, newInfo.GuildID)
	}
}
