// Package bot provides the core bot coordinator logic.
package bot

import (
	"context"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/config"
	"github.com/codeGROOVE-dev/discordian/internal/state"
)

// DiscordClient defines Discord operations needed by the bot.
type DiscordClient interface {
	// Text channel operations
	PostMessage(ctx context.Context, channelID, text string) (messageID string, err error)
	UpdateMessage(ctx context.Context, channelID, messageID, text string) error

	// Forum channel operations
	PostForumThread(ctx context.Context, forumID, title, content string) (threadID, messageID string, err error)
	UpdateForumPost(ctx context.Context, threadID, messageID, newTitle, newContent string) error
	ArchiveThread(ctx context.Context, threadID string) error

	// Direct message operations
	SendDM(ctx context.Context, userID, text string) (channelID, messageID string, err error)
	UpdateDM(ctx context.Context, channelID, messageID, newText string) error

	// Lookup operations
	ResolveChannelID(ctx context.Context, channelName string) string
	LookupUserByUsername(ctx context.Context, username string) string
	IsBotInChannel(ctx context.Context, channelID string) bool
	IsUserInGuild(ctx context.Context, userID string) bool
	IsForumChannel(ctx context.Context, channelID string) bool

	// Guild info
	GuildID() string

	// Search operations (for cross-instance race prevention)
	FindForumThread(ctx context.Context, forumID, prURL string) (threadID, messageID string, found bool)
	FindChannelMessage(ctx context.Context, channelID, prURL string) (messageID string, found bool)
	FindDMForPR(ctx context.Context, userID, prURL string) (channelID, messageID string, found bool)
	MessageContent(ctx context.Context, channelID, messageID string) (string, error)
}

// ConfigManager defines configuration operations.
type ConfigManager interface {
	LoadConfig(ctx context.Context, org string) error
	ReloadConfig(ctx context.Context, org string) error
	Config(org string) (*config.DiscordConfig, bool)
	ChannelsForRepo(org, repo string) []string
	ChannelType(org, channel string) string
	DiscordUserID(org, githubUsername string) string
	ReminderDMDelay(org, channel string) int
	GuildID(org string) string
	SetGitHubClient(org string, client any)
}

// StateStore defines state persistence operations.
type StateStore interface {
	Thread(ctx context.Context, owner, repo string, number int, channelID string) (state.ThreadInfo, bool)
	SaveThread(ctx context.Context, owner, repo string, number int, channelID string, info state.ThreadInfo) error
	DMInfo(ctx context.Context, userID, prURL string) (state.DMInfo, bool)
	SaveDMInfo(ctx context.Context, userID, prURL string, info state.DMInfo) error
	ListDMUsers(ctx context.Context, prURL string) []string // Returns all user IDs who received DMs for this PR
	WasProcessed(ctx context.Context, eventKey string) bool
	MarkProcessed(ctx context.Context, eventKey string, ttl time.Duration) error
	QueuePendingDM(ctx context.Context, dm *state.PendingDM) error
	PendingDMs(ctx context.Context, before time.Time) ([]*state.PendingDM, error)
	RemovePendingDM(ctx context.Context, id string) error
	DailyReportInfo(ctx context.Context, userID string) (state.DailyReportInfo, bool)
	SaveDailyReportInfo(ctx context.Context, userID string, info state.DailyReportInfo) error
	Cleanup(ctx context.Context) error
}

// TurnClient defines PR analysis operations.
type TurnClient interface {
	Check(ctx context.Context, prURL, username string, updatedAt time.Time) (*CheckResponse, error)
}

// CheckResponse represents the Turn API response.
type CheckResponse struct {
	PullRequest PRInfo   `json:"pull_request"`
	Analysis    Analysis `json:"analysis"`
}

// PRInfo contains pull request metadata.
type PRInfo struct {
	Title     string `json:"title"`
	Author    string `json:"author"`
	State     string `json:"state"`
	UpdatedAt string `json:"updated_at"`
	Draft     bool   `json:"draft"`
	Merged    bool   `json:"merged"`
	Closed    bool   `json:"closed"`
}

// Analysis contains the PR analysis result.
type Analysis struct {
	Tags               []string          `json:"tags"`
	NextAction         map[string]Action `json:"next_action"`
	Checks             Checks            `json:"checks"`
	WorkflowState      string            `json:"workflow_state"`
	Size               string            `json:"size"`
	UnresolvedComments int               `json:"unresolved_comments"`
	ReadyToMerge       bool              `json:"ready_to_merge"`
	Approved           bool              `json:"approved"`
	MergeConflict      bool              `json:"merge_conflict"`
}

// Action represents what a user needs to do.
type Action struct {
	Kind   string `json:"kind"`
	Reason string `json:"reason"`
}

// Checks contains CI check status.
type Checks struct {
	Pending int `json:"pending"`
	Passing int `json:"passing"`
	Failing int `json:"failing"`
	Waiting int `json:"waiting"`
}

// SprinklerEvent represents an event from the sprinkler WebSocket.
type SprinklerEvent struct {
	Type       string    `json:"type"`
	URL        string    `json:"url"`
	Timestamp  time.Time `json:"timestamp"`
	DeliveryID string    `json:"delivery_id"`
	CommitSHA  string    `json:"commit_sha,omitempty"`
}

// UserMapper defines user mapping operations.
type UserMapper interface {
	DiscordID(ctx context.Context, githubUsername string) string
	Mention(ctx context.Context, githubUsername string) string
}

// NotificationTracker tracks who has been notified.
type NotificationTracker interface {
	WasTaggedInChannel(prURL, userID string) bool
	MarkTaggedInChannel(prURL, userID string)
	LastDMTime(userID, prURL string) time.Time
	MarkDMSent(userID, prURL string)
}

// PRSearcher queries GitHub for PRs.
type PRSearcher interface {
	// ListOpenPRs returns open PRs for an org updated within the given hours.
	ListOpenPRs(ctx context.Context, org string, updatedWithinHours int) ([]PRSearchResult, error)
	// ListClosedPRs returns recently closed/merged PRs for catching terminal states.
	ListClosedPRs(ctx context.Context, org string, closedWithinHours int) ([]PRSearchResult, error)
}

// PRSearchResult contains basic PR info for polling.
type PRSearchResult struct {
	UpdatedAt time.Time
	URL       string
	Owner     string
	Repo      string
	Number    int
}
