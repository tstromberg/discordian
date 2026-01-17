package state

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/fido/pkg/store/null"
)

// newTestFidoStore creates a FidoStore with null stores for testing.
func newTestFidoStore(t *testing.T) *FidoStore {
	t.Helper()
	ctx := context.Background()

	store, err := NewFidoStore(ctx,
		WithThreadStore(null.New[string, ThreadInfo]()),
		WithDMStore(null.New[string, DMInfo]()),
		WithDMUserStore(null.New[string, dmUserList]()),
		WithReportStore(null.New[string, DailyReportInfo]()),
		WithPendingStore(null.New[string, pendingDMQueue]()),
		WithEventStore(null.New[string, time.Time]()),
	)
	if err != nil {
		t.Fatalf("failed to create test fido store: %v", err)
	}
	return store
}

func TestFidoStore_Thread(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()

	// Initially not found
	_, ok := store.Thread(ctx, "owner", "repo", 1, "chan1")
	if ok {
		t.Error("Thread() should return false for non-existent thread")
	}

	// Save thread
	info := ThreadInfo{
		ThreadID:  "thread123",
		MessageID: "msg456",
	}
	if err := store.SaveThread(ctx, "owner", "repo", 1, "chan1", info); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	// Should be found (fido TieredCache caches in memory)
	got, ok := store.Thread(ctx, "owner", "repo", 1, "chan1")
	if !ok {
		t.Fatal("Thread() should find saved thread")
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
}

func TestFidoStore_DMInfo(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	prURL := "https://github.com/owner/repo/pull/42"

	// Null store always returns not found
	_, ok := store.DMInfo(ctx, "user1", prURL)
	if ok {
		t.Error("DMInfo() should return false with null store")
	}

	// Save should succeed
	info := DMInfo{
		ChannelID: "dmchan123",
		MessageID: "dmmsg456",
	}
	if err := store.SaveDMInfo(ctx, "user1", prURL, info); err != nil {
		t.Fatalf("SaveDMInfo() error = %v", err)
	}
}

func TestFidoStore_DailyReportInfo(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()

	// Null store always returns not found
	_, ok := store.DailyReportInfo(ctx, "user1")
	if ok {
		t.Error("DailyReportInfo() should return false with null store")
	}

	// Save should succeed
	info := DailyReportInfo{
		LastSentAt: time.Now(),
		GuildID:    "guild123",
	}
	if err := store.SaveDailyReportInfo(ctx, "user1", info); err != nil {
		t.Fatalf("SaveDailyReportInfo() error = %v", err)
	}
}

func TestFidoStore_EventProcessing(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	eventKey := "event123"

	// Initially not processed (in-memory only)
	if store.WasProcessed(ctx, eventKey) {
		t.Error("WasProcessed() should return false for new event")
	}

	// Mark as processed
	if err := store.MarkProcessed(ctx, eventKey, time.Hour); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	// Now processed
	if !store.WasProcessed(ctx, eventKey) {
		t.Error("WasProcessed() should return true after marking")
	}
}

func TestFidoStore_EventProcessing_Expired(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	eventKey := "expiring-event"

	// Mark as processed with very short TTL
	if err := store.MarkProcessed(ctx, eventKey, time.Millisecond); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	// Wait for expiration
	time.Sleep(5 * time.Millisecond)

	// Should return false since expired
	if store.WasProcessed(ctx, eventKey) {
		t.Error("WasProcessed() should return false for expired event")
	}
}

func TestFidoStore_PendingDMs(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	now := time.Now()

	dm1 := &PendingDM{
		ID:          "dm1",
		UserID:      "user1",
		PRURL:       "https://github.com/o/r/pull/1",
		MessageText: "Hello",
		SendAt:      now.Add(-time.Hour), // Past - should be returned
		GuildID:     "guild1",
	}
	dm2 := &PendingDM{
		ID:          "dm2",
		UserID:      "user2",
		PRURL:       "https://github.com/o/r/pull/2",
		MessageText: "World",
		SendAt:      now.Add(time.Hour), // Future - should NOT be returned
		GuildID:     "guild1",
	}

	// Queue DMs
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
}

func TestFidoStore_Cleanup(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()

	// Add some events
	if err := store.MarkProcessed(ctx, "event1", time.Millisecond); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}
	if err := store.MarkProcessed(ctx, "event2", time.Hour); err != nil {
		t.Fatalf("MarkProcessed() error = %v", err)
	}

	// Wait for first event to expire
	time.Sleep(5 * time.Millisecond)

	// Cleanup
	if err := store.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	// event1 should be cleaned up
	if store.WasProcessed(ctx, "event1") {
		t.Error("event1 should have been cleaned up")
	}

	// event2 should still be there
	if !store.WasProcessed(ctx, "event2") {
		t.Error("event2 should still be processed")
	}
}

func TestFidoStore_Close(t *testing.T) {
	store := newTestFidoStore(t)

	// Close should not error with null stores
	if err := store.Close(); err != nil {
		t.Errorf("Close() error = %v", err)
	}
}

func TestFidoStoreOptions(t *testing.T) {
	// Test option functions don't panic
	threadStore := null.New[string, ThreadInfo]()
	dmStore := null.New[string, DMInfo]()
	dmUserStore := null.New[string, dmUserList]()
	reportStore := null.New[string, DailyReportInfo]()
	pendingStore := null.New[string, pendingDMQueue]()

	var opts fidoStoreOptions
	WithThreadStore(threadStore)(&opts)
	WithDMStore(dmStore)(&opts)
	WithDMUserStore(dmUserStore)(&opts)
	WithReportStore(reportStore)(&opts)
	WithPendingStore(pendingStore)(&opts)

	if opts.threadStore != threadStore {
		t.Error("WithThreadStore didn't set threadStore")
	}
	if opts.dmStore != dmStore {
		t.Error("WithDMStore didn't set dmStore")
	}
	if opts.dmUserStore != dmUserStore {
		t.Error("WithDMUserStore didn't set dmUserStore")
	}
	if opts.reportStore != reportStore {
		t.Error("WithReportStore didn't set reportStore")
	}
	if opts.pendingStore != pendingStore {
		t.Error("WithPendingStore didn't set pendingStore")
	}
}

func TestFidoStore_QueuePendingDM_SetsCreatedAt(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()

	dm := &PendingDM{
		ID:          "dm1",
		UserID:      "user1",
		MessageText: "Hello",
		// CreatedAt is zero
	}

	if err := store.QueuePendingDM(ctx, dm); err != nil {
		t.Fatalf("QueuePendingDM() error = %v", err)
	}

	// CreatedAt should be set
	if dm.CreatedAt.IsZero() {
		t.Error("QueuePendingDM should set CreatedAt if zero")
	}
}

func TestFidoStore_Cleanup_StaleDMs(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()

	// Add a stale DM (SendAt is far in the past)
	dm := &PendingDM{
		ID:          "stale-dm",
		UserID:      "user1",
		PRURL:       "https://github.com/o/r/pull/1",
		MessageText: "Stale",
		SendAt:      time.Now().Add(-5 * time.Hour), // Past the pendingDMTTL
		GuildID:     "guild1",
	}
	if err := store.QueuePendingDM(ctx, dm); err != nil {
		t.Fatalf("QueuePendingDM() error = %v", err)
	}

	// Cleanup should remove stale DM
	if err := store.Cleanup(ctx); err != nil {
		t.Fatalf("Cleanup() error = %v", err)
	}

	// DM should be cleaned up
	pending, err := store.PendingDMs(ctx, time.Now())
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) > 0 {
		t.Errorf("Stale DM should have been cleaned up, got %d pending", len(pending))
	}
}

func TestFidoStore_DMInfo_SaveAndRetrieve(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	prURL := "https://github.com/owner/repo/pull/42"

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
}

func TestFidoStore_DailyReportInfo_SaveAndRetrieve(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()

	// Save report info
	info := DailyReportInfo{
		LastSentAt: time.Now(),
		GuildID:    "guild123",
	}
	if err := store.SaveDailyReportInfo(ctx, "user1", info); err != nil {
		t.Fatalf("SaveDailyReportInfo() error = %v", err)
	}

	// Retrieve report info
	got, ok := store.DailyReportInfo(ctx, "user1")
	if !ok {
		t.Fatal("DailyReportInfo() did not find saved info")
	}
	if got.GuildID != info.GuildID {
		t.Errorf("DailyReportInfo().GuildID = %q, want %q", got.GuildID, info.GuildID)
	}

	// Different user returns nothing
	_, ok = store.DailyReportInfo(ctx, "other-user")
	if ok {
		t.Error("DailyReportInfo() should not find info for different user")
	}
}

func TestFidoStore_RemovePendingDM_EmptyQueue(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()

	// Remove from empty queue should not error
	if err := store.RemovePendingDM(ctx, "nonexistent"); err != nil {
		t.Errorf("RemovePendingDM() on empty queue error = %v", err)
	}
}

func TestFidoStore_ListDMUsers(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	prURL := "https://github.com/owner/repo/pull/42"

	// Initially no users
	users := store.ListDMUsers(ctx, prURL)
	if len(users) != 0 {
		t.Errorf("ListDMUsers() = %v, want empty", users)
	}

	// Save DM for user1
	info1 := DMInfo{
		ChannelID: "dmchan1",
		MessageID: "dmmsg1",
		LastState: "needs_review",
	}
	if err := store.SaveDMInfo(ctx, "user1", prURL, info1); err != nil {
		t.Fatalf("SaveDMInfo() error = %v", err)
	}

	// Should have 1 user
	users = store.ListDMUsers(ctx, prURL)
	if len(users) != 1 {
		t.Errorf("ListDMUsers() returned %d users, want 1", len(users))
	}

	// Save DM for user2
	info2 := DMInfo{
		ChannelID: "dmchan2",
		MessageID: "dmmsg2",
		LastState: "needs_review",
	}
	if err := store.SaveDMInfo(ctx, "user2", prURL, info2); err != nil {
		t.Fatalf("SaveDMInfo() error = %v", err)
	}

	// Should have 2 users
	users = store.ListDMUsers(ctx, prURL)
	if len(users) != 2 {
		t.Errorf("ListDMUsers() returned %d users, want 2", len(users))
	}

	// Different PR should have no users
	users = store.ListDMUsers(ctx, "https://github.com/owner/repo/pull/99")
	if len(users) != 0 {
		t.Errorf("ListDMUsers() for different PR = %v, want empty", users)
	}
}

func TestFidoStore_DMInfo_LastState(t *testing.T) {
	store := newTestFidoStore(t)
	defer store.Close() //nolint:errcheck // test cleanup

	ctx := context.Background()
	prURL := "https://github.com/owner/repo/pull/42"

	// Save DM with LastState
	info := DMInfo{
		ChannelID:   "dmchan123",
		MessageID:   "dmmsg456",
		MessageText: "Test message",
		LastState:   "needs_review",
		SentAt:      time.Now(),
	}
	if err := store.SaveDMInfo(ctx, "user1", prURL, info); err != nil {
		t.Fatalf("SaveDMInfo() error = %v", err)
	}

	// Retrieve and verify LastState is preserved
	got, ok := store.DMInfo(ctx, "user1", prURL)
	if !ok {
		t.Fatal("DMInfo() should find saved info")
	}
	if got.LastState != "needs_review" {
		t.Errorf("DMInfo().LastState = %q, want %q", got.LastState, "needs_review")
	}
}

// TestFidoStore_ClaimThread tests thread claim locking with claim store.
func TestFidoStore_ClaimThread(t *testing.T) {
	ctx := context.Background()

	// Create store with claim store
	store, err := NewFidoStore(ctx,
		WithThreadStore(null.New[string, ThreadInfo]()),
		WithDMStore(null.New[string, DMInfo]()),
		WithDMUserStore(null.New[string, dmUserList]()),
		WithReportStore(null.New[string, DailyReportInfo]()),
		WithPendingStore(null.New[string, pendingDMQueue]()),
		WithEventStore(null.New[string, time.Time]()),
		WithClaimStore(null.New[string, time.Time]()),
	)
	if err != nil {
		t.Fatalf("NewFidoStore() error = %v", err)
	}
	defer store.Close() //nolint:errcheck // test cleanup

	// First claim should succeed
	if !store.ClaimThread(ctx, "owner", "repo", 1, "chan1", time.Second) {
		t.Error("ClaimThread() should succeed on first attempt")
	}

	// Immediate second claim should fail (locked)
	if store.ClaimThread(ctx, "owner", "repo", 1, "chan1", time.Second) {
		t.Error("ClaimThread() should fail when already claimed")
	}

	// Different thread should succeed
	if !store.ClaimThread(ctx, "owner", "repo", 2, "chan1", time.Second) {
		t.Error("ClaimThread() should succeed for different PR")
	}

	// Wait for lock to expire
	time.Sleep(1100 * time.Millisecond)

	// Should be able to claim again after expiry
	if !store.ClaimThread(ctx, "owner", "repo", 1, "chan1", time.Second) {
		t.Error("ClaimThread() should succeed after lock expiry")
	}
}

// TestFidoStore_ClaimDM tests DM claim locking with claim store.
func TestFidoStore_ClaimDM(t *testing.T) {
	ctx := context.Background()

	// Create store with claim store
	store, err := NewFidoStore(ctx,
		WithThreadStore(null.New[string, ThreadInfo]()),
		WithDMStore(null.New[string, DMInfo]()),
		WithDMUserStore(null.New[string, dmUserList]()),
		WithReportStore(null.New[string, DailyReportInfo]()),
		WithPendingStore(null.New[string, pendingDMQueue]()),
		WithEventStore(null.New[string, time.Time]()),
		WithClaimStore(null.New[string, time.Time]()),
	)
	if err != nil {
		t.Fatalf("NewFidoStore() error = %v", err)
	}
	defer store.Close() //nolint:errcheck // test cleanup

	userID := "user123"
	prURL := "https://github.com/owner/repo/pull/1"

	// First claim should succeed
	if !store.ClaimDM(ctx, userID, prURL, time.Second) {
		t.Error("ClaimDM() should succeed on first attempt")
	}

	// Immediate second claim should fail (locked)
	if store.ClaimDM(ctx, userID, prURL, time.Second) {
		t.Error("ClaimDM() should fail when already claimed")
	}

	// Different PR should succeed
	prURL2 := "https://github.com/owner/repo/pull/2"
	if !store.ClaimDM(ctx, userID, prURL2, time.Second) {
		t.Error("ClaimDM() should succeed for different PR")
	}

	// Different user should succeed
	if !store.ClaimDM(ctx, "user456", prURL, time.Second) {
		t.Error("ClaimDM() should succeed for different user")
	}

	// Wait for lock to expire
	time.Sleep(1100 * time.Millisecond)

	// Should be able to claim again after expiry
	if !store.ClaimDM(ctx, userID, prURL, time.Second) {
		t.Error("ClaimDM() should succeed after lock expiry")
	}
}
