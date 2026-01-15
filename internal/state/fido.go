package state

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/fido"
	"github.com/codeGROOVE-dev/fido/pkg/store/cloudrun"
)

// TTLs for different data types.
const (
	threadTTL      = 30 * 24 * time.Hour // 30 days - PRs can be open a while
	dmInfoTTL      = 7 * 24 * time.Hour  // 7 days
	eventTTL       = 2 * time.Hour       // Short - just for dedup
	dailyReportTTL = 36 * time.Hour      // Slightly over 1 day to handle timezone edge cases
	pendingDMTTL   = 4 * time.Hour       // Max time a DM can be pending
)

// pendingDMQueue stores all pending DMs in a single persisted value.
// This ensures the queue survives restarts.
type pendingDMQueue struct {
	DMs map[string]PendingDM `json:"dms"`
}

// FidoStore implements Store using fido with CloudRun backend.
//
// Requires these Datastore databases (must be created before use):
//   - discordian-threads: PR to Discord thread/message mapping
//   - discordian-dms: DM message tracking
//   - discordian-reports: Daily report tracking
//   - discordian-pending: Pending DM queue
//
// Event deduplication is in-memory only (2h TTL, not worth persisting).
type FidoStore struct {
	threads      *fido.TieredCache[string, ThreadInfo]
	dmInfo       *fido.TieredCache[string, DMInfo]
	dailyReports *fido.TieredCache[string, DailyReportInfo]
	pendingDMs   *fido.TieredCache[string, pendingDMQueue]

	// Event dedup is in-memory only - short TTL, not critical to persist
	events   map[string]time.Time
	eventsMu sync.RWMutex

	// Reverse index: prURL -> userIDs who received DMs
	// Populated when SaveDMInfo is called
	dmUserIndex   map[string]map[string]bool
	dmUserIndexMu sync.RWMutex

	pendingMu sync.Mutex // Serializes pending DM operations
}

// FidoStoreOption configures a FidoStore.
type FidoStoreOption func(*fidoStoreOptions)

type fidoStoreOptions struct {
	threadStore  fido.Store[string, ThreadInfo]
	dmStore      fido.Store[string, DMInfo]
	reportStore  fido.Store[string, DailyReportInfo]
	pendingStore fido.Store[string, pendingDMQueue]
}

// WithThreadStore sets a custom store for thread data.
func WithThreadStore(s fido.Store[string, ThreadInfo]) FidoStoreOption {
	return func(o *fidoStoreOptions) { o.threadStore = s }
}

// WithDMStore sets a custom store for DM data.
func WithDMStore(s fido.Store[string, DMInfo]) FidoStoreOption {
	return func(o *fidoStoreOptions) { o.dmStore = s }
}

// WithReportStore sets a custom store for daily report data.
func WithReportStore(s fido.Store[string, DailyReportInfo]) FidoStoreOption {
	return func(o *fidoStoreOptions) { o.reportStore = s }
}

// WithPendingStore sets a custom store for pending DM data.
func WithPendingStore(s fido.Store[string, pendingDMQueue]) FidoStoreOption {
	return func(o *fidoStoreOptions) { o.pendingStore = s }
}

// NewFidoStore creates a new fido-backed store.
// Uses CloudRun backend which auto-detects environment.
// Use WithThreadStore, WithDMStore, etc. to inject custom stores for testing.
func NewFidoStore(ctx context.Context, opts ...FidoStoreOption) (*FidoStore, error) {
	var o fidoStoreOptions
	for _, opt := range opts {
		opt(&o)
	}

	// Use provided stores or create cloudrun stores
	threadStore := o.threadStore
	if threadStore == nil {
		var err error
		threadStore, err = cloudrun.New[string, ThreadInfo](ctx, "discordian-threads")
		if err != nil {
			return nil, fmt.Errorf("create thread store: %w", err)
		}
	}

	dmStore := o.dmStore
	if dmStore == nil {
		var err error
		dmStore, err = cloudrun.New[string, DMInfo](ctx, "discordian-dms")
		if err != nil {
			return nil, fmt.Errorf("create dm store: %w", err)
		}
	}

	reportStore := o.reportStore
	if reportStore == nil {
		var err error
		reportStore, err = cloudrun.New[string, DailyReportInfo](ctx, "discordian-reports")
		if err != nil {
			return nil, fmt.Errorf("create report store: %w", err)
		}
	}

	pendingStore := o.pendingStore
	if pendingStore == nil {
		var err error
		pendingStore, err = cloudrun.New[string, pendingDMQueue](ctx, "discordian-pending")
		if err != nil {
			return nil, fmt.Errorf("create pending store: %w", err)
		}
	}

	threads, err := fido.NewTiered(threadStore, fido.TTL(threadTTL))
	if err != nil {
		return nil, fmt.Errorf("create thread cache: %w", err)
	}

	dmInfo, err := fido.NewTiered(dmStore, fido.TTL(dmInfoTTL))
	if err != nil {
		return nil, fmt.Errorf("create dm cache: %w", err)
	}

	dailyReports, err := fido.NewTiered(reportStore, fido.TTL(dailyReportTTL))
	if err != nil {
		return nil, fmt.Errorf("create report cache: %w", err)
	}

	pendingDMs, err := fido.NewTiered(pendingStore, fido.TTL(pendingDMTTL))
	if err != nil {
		return nil, fmt.Errorf("create pending cache: %w", err)
	}

	slog.Info("initialized fido store")
	return &FidoStore{
		threads:      threads,
		dmInfo:       dmInfo,
		dailyReports: dailyReports,
		pendingDMs:   pendingDMs,
		events:       make(map[string]time.Time),
		dmUserIndex:  make(map[string]map[string]bool),
	}, nil
}

// Thread retrieves thread info for a PR.
func (s *FidoStore) Thread(ctx context.Context, owner, repo string, number int, channelID string) (ThreadInfo, bool) {
	key := fmt.Sprintf("%s/%s/%d/%s", owner, repo, number, channelID)
	info, found, err := s.threads.Get(ctx, key)
	if err != nil {
		slog.Debug("thread lookup error", "key", key, "error", err)
		return ThreadInfo{}, false
	}
	return info, found
}

// SaveThread stores thread info for a PR.
func (s *FidoStore) SaveThread(ctx context.Context, owner, repo string, number int, channelID string, info ThreadInfo) error {
	key := fmt.Sprintf("%s/%s/%d/%s", owner, repo, number, channelID)
	info.UpdatedAt = time.Now()
	return s.threads.Set(ctx, key, info)
}

// DMInfo retrieves DM info for a user/PR.
func (s *FidoStore) DMInfo(ctx context.Context, userID, prURL string) (DMInfo, bool) {
	key := fmt.Sprintf("%s:%s", userID, prURL)
	info, found, err := s.dmInfo.Get(ctx, key)
	if err != nil {
		slog.Debug("dm info lookup error", "key", key, "error", err)
		return DMInfo{}, false
	}
	return info, found
}

// SaveDMInfo stores DM info for a user/PR.
func (s *FidoStore) SaveDMInfo(ctx context.Context, userID, prURL string, info DMInfo) error {
	key := fmt.Sprintf("%s:%s", userID, prURL)

	// Update reverse index
	s.dmUserIndexMu.Lock()
	if s.dmUserIndex[prURL] == nil {
		s.dmUserIndex[prURL] = make(map[string]bool)
	}
	s.dmUserIndex[prURL][userID] = true
	s.dmUserIndexMu.Unlock()

	return s.dmInfo.Set(ctx, key, info)
}

// ListDMUsers returns all user IDs who received DMs for a PR.
func (s *FidoStore) ListDMUsers(_ context.Context, prURL string) []string {
	s.dmUserIndexMu.RLock()
	defer s.dmUserIndexMu.RUnlock()

	users := s.dmUserIndex[prURL]
	if users == nil {
		return nil
	}

	result := make([]string, 0, len(users))
	for userID := range users {
		result = append(result, userID)
	}
	return result
}

// WasProcessed checks if an event was already processed.
func (s *FidoStore) WasProcessed(_ context.Context, eventKey string) bool {
	s.eventsMu.RLock()
	expiry, found := s.events[eventKey]
	s.eventsMu.RUnlock()

	if !found {
		return false
	}
	return time.Now().Before(expiry)
}

// MarkProcessed marks an event as processed.
func (s *FidoStore) MarkProcessed(_ context.Context, eventKey string, ttl time.Duration) error {
	s.eventsMu.Lock()
	s.events[eventKey] = time.Now().Add(ttl)
	s.eventsMu.Unlock()
	return nil
}

// DailyReportInfo retrieves daily report info for a user.
func (s *FidoStore) DailyReportInfo(ctx context.Context, userID string) (DailyReportInfo, bool) {
	info, found, err := s.dailyReports.Get(ctx, userID)
	if err != nil {
		slog.Debug("daily report lookup error", "user", userID, "error", err)
		return DailyReportInfo{}, false
	}
	return info, found
}

// SaveDailyReportInfo stores daily report info for a user.
func (s *FidoStore) SaveDailyReportInfo(ctx context.Context, userID string, info DailyReportInfo) error {
	return s.dailyReports.Set(ctx, userID, info)
}

const pendingQueueKey = "queue" // Single key for all pending DMs

// QueuePendingDM adds a pending DM to the queue.
func (s *FidoStore) QueuePendingDM(ctx context.Context, dm *PendingDM) error {
	if dm.CreatedAt.IsZero() {
		dm.CreatedAt = time.Now()
	}

	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	// Get current queue
	queue, _, err := s.pendingDMs.Get(ctx, pendingQueueKey)
	if err != nil {
		slog.Debug("pending queue fetch error, starting fresh", "error", err)
	}
	if queue.DMs == nil {
		queue.DMs = make(map[string]PendingDM)
	}

	// Add new DM
	queue.DMs[dm.ID] = *dm

	// Save back
	return s.pendingDMs.Set(ctx, pendingQueueKey, queue)
}

// PendingDMs returns all pending DMs that should be sent before the given time.
func (s *FidoStore) PendingDMs(ctx context.Context, before time.Time) ([]*PendingDM, error) {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	queue, _, err := s.pendingDMs.Get(ctx, pendingQueueKey)
	if err != nil {
		slog.Debug("pending queue fetch error", "error", err)
		return nil, nil
	}

	var result []*PendingDM
	for id := range queue.DMs {
		dm := queue.DMs[id]
		if dm.SendAt.Before(before) || dm.SendAt.Equal(before) {
			result = append(result, &dm)
		}
	}

	return result, nil
}

// RemovePendingDM removes a pending DM from the queue.
func (s *FidoStore) RemovePendingDM(ctx context.Context, id string) error {
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	queue, _, err := s.pendingDMs.Get(ctx, pendingQueueKey)
	if err != nil {
		return nil // Queue doesn't exist, nothing to remove
	}

	if queue.DMs == nil {
		return nil
	}

	delete(queue.DMs, id)
	return s.pendingDMs.Set(ctx, pendingQueueKey, queue)
}

// Cleanup removes expired entries.
func (s *FidoStore) Cleanup(ctx context.Context) error {
	// Clean up expired events from memory
	s.eventsMu.Lock()
	now := time.Now()
	for key, expiry := range s.events {
		if now.After(expiry) {
			delete(s.events, key)
		}
	}
	s.eventsMu.Unlock()

	// Clean up stale pending DMs
	s.pendingMu.Lock()
	defer s.pendingMu.Unlock()

	queue, _, err := s.pendingDMs.Get(ctx, pendingQueueKey)
	if err != nil {
		return nil
	}

	if queue.DMs == nil {
		return nil
	}

	modified := false
	for id := range queue.DMs {
		dm := queue.DMs[id]
		if now.Sub(dm.SendAt) > pendingDMTTL {
			delete(queue.DMs, id)
			modified = true
		}
	}

	if modified {
		return s.pendingDMs.Set(ctx, pendingQueueKey, queue)
	}
	return nil
}

// Close releases resources.
func (s *FidoStore) Close() error {
	var errs []error

	if err := s.threads.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close threads: %w", err))
	}
	if err := s.dmInfo.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close dmInfo: %w", err))
	}
	if err := s.dailyReports.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close dailyReports: %w", err))
	}
	if err := s.pendingDMs.Close(); err != nil {
		errs = append(errs, fmt.Errorf("close pendingDMs: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("close errors: %v", errs)
	}
	return nil
}
