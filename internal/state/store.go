// Package state provides persistent state management for the bot.
package state

import (
	"context"
	"time"
)

// ThreadInfo stores Discord thread/message info for a PR.
type ThreadInfo struct {
	UpdatedAt   time.Time `json:"updated_at"`
	ThreadID    string    `json:"thread_id"`
	MessageID   string    `json:"message_id"`
	ChannelID   string    `json:"channel_id"`
	ChannelType string    `json:"channel_type"` // "forum" or "text"
	LastState   string    `json:"last_state"`
	MessageText string    `json:"message_text"`
}

// DMInfo stores DM message info for updating.
type DMInfo struct {
	SentAt      time.Time `json:"sent_at"`
	ChannelID   string    `json:"channel_id"`
	MessageID   string    `json:"message_id"`
	MessageText string    `json:"message_text"`
	LastState   string    `json:"last_state"` // PR state when DM was sent/updated
}

// PendingDM represents a scheduled DM notification.
type PendingDM struct {
	SendAt      time.Time `json:"send_at"`
	CreatedAt   time.Time `json:"created_at"`
	ExpiresAt   time.Time `json:"expires_at"`
	ID          string    `json:"id"`
	UserID      string    `json:"user_id"`
	PRURL       string    `json:"pr_url"`
	MessageText string    `json:"message_text"`
	GuildID     string    `json:"guild_id"`
	Org         string    `json:"org"`
	RetryCount  int       `json:"retry_count"`
}

// DailyReportInfo tracks daily report state for a user.
type DailyReportInfo struct {
	LastSentAt time.Time `json:"last_sent_at"`
	GuildID    string    `json:"guild_id"`
}

// Store provides persistent state operations.
type Store interface {
	// Thread/post tracking - maps PR to Discord thread/message
	Thread(ctx context.Context, owner, repo string, number int, channelID string) (ThreadInfo, bool)
	SaveThread(ctx context.Context, owner, repo string, number int, channelID string, info ThreadInfo) error

	// Distributed claim mechanism to prevent duplicate thread/message creation across instances
	// Returns true if claim was successful, false if another instance already claimed it
	ClaimThread(ctx context.Context, owner, repo string, number int, channelID string, ttl time.Duration) bool

	// DM tracking
	DMInfo(ctx context.Context, userID, prURL string) (DMInfo, bool)
	SaveDMInfo(ctx context.Context, userID, prURL string, info DMInfo) error
	ListDMUsers(ctx context.Context, prURL string) []string // Returns all user IDs who received DMs for this PR

	// Distributed claim mechanism for DMs
	ClaimDM(ctx context.Context, userID, prURL string, ttl time.Duration) bool

	// Event deduplication
	WasProcessed(ctx context.Context, eventKey string) bool
	MarkProcessed(ctx context.Context, eventKey string, ttl time.Duration) error

	// Pending DM queue
	QueuePendingDM(ctx context.Context, dm *PendingDM) error
	PendingDMs(ctx context.Context, before time.Time) ([]*PendingDM, error)
	RemovePendingDM(ctx context.Context, id string) error

	// Daily report tracking
	DailyReportInfo(ctx context.Context, userID string) (DailyReportInfo, bool)
	SaveDailyReportInfo(ctx context.Context, userID string, info DailyReportInfo) error

	// Lifecycle
	Cleanup(ctx context.Context) error
	Close() error
}
