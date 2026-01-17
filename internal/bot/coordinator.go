package bot

import (
	"context"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/dailyreport"
	"github.com/codeGROOVE-dev/discordian/internal/discord"
	"github.com/codeGROOVE-dev/discordian/internal/format"
	"github.com/codeGROOVE-dev/discordian/internal/state"
	"github.com/codeGROOVE-dev/discordian/internal/usermapping"
	"github.com/google/uuid"
)

const (
	eventDeduplicationTTL  = time.Hour
	maxConcurrentEvents    = 10
	pollOpenPRHours        = 24                     // Look back 24 hours for open PRs
	pollClosedPRHours      = 1                      // Look back 1 hour for closed PRs
	crossInstanceRaceDelay = 100 * time.Millisecond // Delay before creating to allow cross-instance race detection
	maxTagTrackerEntries   = 5000                   // Max PRs to track before cleanup
	lockCleanupInterval    = 10 * time.Minute       // How often to clean up unused locks
	lockIdleTimeout        = 30 * time.Minute       // Remove locks not used for this duration
)

// timedLock wraps a mutex with last-access tracking for cleanup.
type timedLock struct {
	lastUsed time.Time
	mu       sync.Mutex
}

// lockMap manages locks with automatic cleanup of idle entries.
type lockMap struct {
	locks sync.Map // key -> *timedLock
}

func (lm *lockMap) get(key string) *sync.Mutex {
	val, _ := lm.locks.LoadOrStore(key, &timedLock{lastUsed: time.Now()})
	tl := val.(*timedLock) //nolint:errcheck,forcetypeassert,revive // type assertion always succeeds - we control what's stored
	tl.lastUsed = time.Now()
	return &tl.mu
}

func (lm *lockMap) cleanup(idleTimeout time.Duration) int {
	now := time.Now()
	removed := 0
	lm.locks.Range(func(key, val any) bool {
		tl := val.(*timedLock) //nolint:errcheck,forcetypeassert,revive // type assertion always succeeds
		// Only delete if lock is not currently held and is idle
		if now.Sub(tl.lastUsed) > idleTimeout {
			// Try to acquire lock before deleting to ensure not in use
			if tl.mu.TryLock() {
				tl.mu.Unlock()
				lm.locks.Delete(key)
				removed++
			}
		}
		return true
	})
	return removed
}

// Coordinator orchestrates event processing for a GitHub organization.
type Coordinator struct {
	discord    DiscordClient
	config     ConfigManager
	store      StateStore
	turn       TurnClient
	userMapper UserMapper
	searcher   PRSearcher
	logger     *slog.Logger
	eventSem   chan struct{}
	tagTracker *tagTracker
	prLocks    lockMap // PR URL -> mutex (serializes channel operations per PR)
	dmLocks    lockMap // userID:prURL -> mutex (serializes DM operations per user+PR)
	org        string
	wg         sync.WaitGroup
}

// CoordinatorConfig holds configuration for creating a coordinator.
type CoordinatorConfig struct {
	Discord    DiscordClient
	Config     ConfigManager
	Store      StateStore
	Turn       TurnClient
	UserMapper UserMapper
	Searcher   PRSearcher
	Logger     *slog.Logger
	Org        string
}

// NewCoordinator creates a new coordinator for an organization.
func NewCoordinator(cfg CoordinatorConfig) *Coordinator {
	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &Coordinator{
		org:        cfg.Org,
		discord:    cfg.Discord,
		config:     cfg.Config,
		store:      cfg.Store,
		turn:       cfg.Turn,
		userMapper: cfg.UserMapper,
		searcher:   cfg.Searcher,
		logger:     logger.With("org", cfg.Org),
		eventSem:   make(chan struct{}, maxConcurrentEvents),
		tagTracker: newTagTracker(),
	}
}

// ProcessEvent handles an incoming sprinkler event.
func (c *Coordinator) ProcessEvent(ctx context.Context, event SprinklerEvent) {
	// Acquire semaphore
	select {
	case c.eventSem <- struct{}{}:
	case <-ctx.Done():
		return
	}

	c.wg.Go(func() {
		defer func() { <-c.eventSem }()

		if err := c.processEventSync(ctx, event); err != nil {
			c.logger.Error("failed to process event",
				"error", err,
				"url", event.URL,
				"type", event.Type)
		}
	})
}

func (c *Coordinator) processEventSync(ctx context.Context, event SprinklerEvent) error {
	// Parse PR URL
	prInfo, ok := ParsePRURL(event.URL)
	if !ok {
		return fmt.Errorf("invalid PR URL: %s", event.URL)
	}
	owner, repo, number := prInfo.Owner, prInfo.Repo, prInfo.Number

	// Auto-reload config when .codeGROOVE repo is updated
	if repo == ".codeGROOVE" {
		c.logger.Info("config repo updated, reloading config", "org", c.org)
		if err := c.config.ReloadConfig(ctx, c.org); err != nil {
			c.logger.Warn("failed to reload config", "error", err)
		}
		return nil // Don't post notifications for config repo PRs
	}

	// Check if event already processed
	eventKey := fmt.Sprintf("%s:%s", event.DeliveryID, event.URL)
	if c.store.WasProcessed(ctx, eventKey) {
		c.logger.Debug("event already processed, skipping",
			"delivery_id", event.DeliveryID,
			"event_key", eventKey,
			"pr_url", event.URL,
			"type", event.Type)
		return nil
	}

	// Lock per PR URL to prevent duplicate threads/messages
	prLock := c.prLock(event.URL)
	prLock.Lock()
	defer prLock.Unlock()

	c.logger.Info("processing event",
		"type", event.Type,
		"repo", repo,
		"number", number)

	// Load config
	if err := c.config.LoadConfig(ctx, c.org); err != nil {
		c.logger.Warn("failed to load config, using defaults", "error", err)
	}

	// Call Turn API for PR analysis
	// Use event.Timestamp (not PR's UpdatedAt) because some events like check runs
	// don't update the PR's UpdatedAt field, but we need Turn to analyze current state
	checkResp, err := c.turn.Check(ctx, event.URL, "", event.Timestamp)
	if err != nil {
		c.logger.Warn("turn API call failed", "error", err)
		// Continue with limited info
		checkResp = &CheckResponse{}
	}

	// Determine state using same logic as slacker
	prState := format.StateFromAnalysis(format.StateAnalysisParams{
		Merged:             checkResp.PullRequest.Merged,
		Closed:             checkResp.PullRequest.Closed,
		Draft:              checkResp.PullRequest.Draft,
		MergeConflict:      checkResp.Analysis.MergeConflict,
		Approved:           checkResp.Analysis.Approved,
		ChecksFailing:      checkResp.Analysis.Checks.Failing,
		ChecksPending:      checkResp.Analysis.Checks.Pending,
		ChecksWaiting:      checkResp.Analysis.Checks.Waiting,
		UnresolvedComments: checkResp.Analysis.UnresolvedComments,
		WorkflowState:      checkResp.Analysis.WorkflowState,
	})

	// Build action users
	actionUsers := c.buildActionUsers(ctx, checkResp)

	// Get channels for this repo
	channels := c.config.ChannelsForRepo(c.org, repo)
	if len(channels) == 0 {
		c.logger.Warn("no channels found for repo - check that a channel named the same as the repo exists in Discord",
			"repo", repo,
			"org", c.org)
		return nil
	}

	// Process each channel
	for _, channelName := range channels {
		if err := c.processChannel(ctx, channelName, owner, repo, number, checkResp, prState, actionUsers); err != nil {
			c.logger.Error("failed to process channel",
				"channel", channelName,
				"error", err)
		}
	}

	// Queue DM notifications
	c.queueDMNotifications(ctx, owner, repo, number, checkResp, prState)

	// Mark event as processed after successful completion
	if err := c.store.MarkProcessed(ctx, eventKey, eventDeduplicationTTL); err != nil {
		c.logger.Warn("failed to mark event as processed", "error", err, "delivery_id", event.DeliveryID)
	}

	return nil
}

func (c *Coordinator) buildActionUsers(ctx context.Context, checkResp *CheckResponse) []format.ActionUser {
	var users []format.ActionUser

	c.logger.Debug("building action users",
		"next_action_count", len(checkResp.Analysis.NextAction),
		"next_action", checkResp.Analysis.NextAction)

	for username, action := range checkResp.Analysis.NextAction {
		// Skip _system pseudo-user - these are system-level actions without a human assignee
		if username == "_system" {
			c.logger.Debug("skipping _system action", "action", action.Kind)
			continue
		}

		mention := username
		if c.userMapper != nil {
			mention = c.userMapper.Mention(ctx, username)
		}

		actionLabel := format.ActionLabel(action.Kind)
		c.logger.Debug("adding action user",
			"username", username,
			"mention", mention,
			"raw_action", action.Kind,
			"action_label", actionLabel)

		users = append(users, format.ActionUser{
			Username: username,
			Mention:  mention,
			Action:   actionLabel,
		})
	}

	c.logger.Debug("built action users", "count", len(users))
	return users
}

// shouldPostThread determines if a PR thread should be posted based on configured threshold.
// Returns (shouldPost bool, reason string).
func (c *Coordinator) shouldPostThread(checkResult *CheckResponse, when string) (shouldPost bool, reason string) {
	if checkResult == nil {
		return false, "no_check_result"
	}

	pr := checkResult.PullRequest
	analysis := checkResult.Analysis

	// Terminal states ALWAYS post (ensure visibility)
	if pr.Merged {
		return true, "pr_merged"
	}
	if pr.State == "closed" {
		return true, "pr_closed"
	}

	switch when {
	case "immediate":
		return true, "immediate_mode"

	case "assigned":
		// Post when PR has assignees
		if len(pr.Assignees) > 0 {
			return true, fmt.Sprintf("has_%d_assignees", len(pr.Assignees))
		}
		return false, "no_assignees"

	case "blocked":
		// Post when someone needs to take action
		// Count real users in NextAction (excluding _system sentinel)
		blockedCount := 0
		for username := range analysis.NextAction {
			if username != "_system" {
				blockedCount++
			}
		}
		if blockedCount > 0 {
			return true, fmt.Sprintf("blocked_on_%d_users", blockedCount)
		}
		return false, "not_blocked_yet"

	case "passing":
		// Post when tests pass - use WorkflowState as primary signal
		switch analysis.WorkflowState {
		case "assigned_waiting_for_review",
			"reviewed_needs_refinement",
			"refined_waiting_for_approval",
			"approved_waiting_for_merge":
			return true, fmt.Sprintf("workflow_state_%s", analysis.WorkflowState)

		case "newly_published",
			"in_draft",
			"published_waiting_for_tests",
			"tested_waiting_for_assignment":
			return false, fmt.Sprintf("waiting_for_%s", analysis.WorkflowState)

		default:
			// Fallback: check test status directly
			if analysis.Checks.Failing > 0 {
				return false, "tests_failing"
			}
			if analysis.Checks.Pending > 0 || analysis.Checks.Waiting > 0 {
				return false, "tests_pending"
			}
			return true, "tests_passed_fallback"
		}

	default:
		c.logger.Warn("invalid when value, defaulting to immediate", "when", when)
		return true, "invalid_config_default_immediate"
	}
}

func (c *Coordinator) processChannel(
	ctx context.Context,
	channelName string,
	owner, repo string,
	number int,
	checkResp *CheckResponse,
	prState format.PRState,
	actionUsers []format.ActionUser,
) error {
	// Resolve channel ID
	channelID := c.discord.ResolveChannelID(ctx, channelName)
	if channelID == channelName {
		// Resolution failed, channel doesn't exist
		c.logger.Debug("channel not found", "channel", channelName)
		return nil
	}

	// Check if bot can send to channel
	if !c.discord.IsBotInChannel(ctx, channelID) {
		c.logger.Debug("bot not in channel", "channel", channelName)
		return nil
	}

	// Build message params
	prURL := FormatPRURL(owner, repo, number)
	params := format.ChannelMessageParams{
		Owner:       owner,
		Repo:        repo,
		Number:      number,
		Title:       checkResp.PullRequest.Title,
		Author:      checkResp.PullRequest.Author,
		State:       prState,
		ActionUsers: actionUsers,
		PRURL:       prURL,
		ChannelName: channelName,
	}

	// Check for existing thread/message
	threadInfo, exists := c.store.Thread(ctx, owner, repo, number, channelID)

	// Skip channel message if we don't have basic PR info (Turn API failed)
	// However, if message already exists in Discord, we should try to update it when Turn API recovers
	if params.Title == "" || params.Author == "" {
		if !exists || threadInfo.MessageID == "" {
			// No existing message - skip creating a malformed one
			c.logger.Warn("skipping channel message creation due to missing PR info",
				"channel", channelName,
				"pr", prURL,
				"has_title", params.Title != "",
				"has_author", params.Author != "")
			return nil
		}
		// Message exists but Turn API failed - log but continue to allow cleanup/archival
		c.logger.Warn("proceeding with limited PR info for existing message",
			"channel", channelName,
			"pr", prURL,
			"message_id", threadInfo.MessageID,
			"has_title", params.Title != "",
			"has_author", params.Author != "")
	}

	// Auto-detect forum channels from Discord API
	if c.discord.IsForumChannel(ctx, channelID) {
		return c.processForumChannel(ctx, channelProcessParams{
			channelID:  channelID,
			owner:      owner,
			repo:       repo,
			number:     number,
			params:     params,
			checkResp:  checkResp,
			threadInfo: threadInfo,
			exists:     exists,
		})
	}

	return c.processTextChannel(ctx, channelProcessParams{
		channelID:  channelID,
		owner:      owner,
		repo:       repo,
		number:     number,
		params:     params,
		checkResp:  checkResp,
		threadInfo: threadInfo,
		exists:     exists,
	})
}

type channelProcessParams struct {
	channelID  string
	owner      string
	repo       string
	number     int
	params     format.ChannelMessageParams
	checkResp  *CheckResponse
	threadInfo state.ThreadInfo
	exists     bool
}

func (c *Coordinator) processForumChannel(ctx context.Context, p channelProcessParams) error {
	title := format.ForumThreadTitle(p.params.Repo, p.params.Number, p.params.Title)
	content := format.ChannelMessage(p.params)

	if p.exists && p.threadInfo.ThreadID != "" {
		// Content comparison: skip update if content unchanged
		if p.threadInfo.MessageText == content {
			c.logger.Debug("forum post unchanged, skipping update",
				"thread_id", p.threadInfo.ThreadID,
				"pr", p.params.PRURL)
			c.trackTaggedUsers(p.params)
			return nil
		}

		// Update existing thread
		err := c.discord.UpdateForumPost(ctx, p.threadInfo.ThreadID, p.threadInfo.MessageID, title, content)
		if err == nil {
			// Update state
			p.threadInfo.MessageText = content
			p.threadInfo.LastState = string(p.params.State)
			if err := c.store.SaveThread(ctx, p.owner, p.repo, p.number, p.channelID, p.threadInfo); err != nil {
				c.logger.Warn("failed to save thread info", "error", err)
			}

			// Archive if merged/closed
			if p.params.State == format.StateMerged || p.params.State == format.StateClosed {
				if err := c.discord.ArchiveThread(ctx, p.threadInfo.ThreadID); err != nil {
					c.logger.Warn("failed to archive thread", "error", err)
				}
			}

			c.trackTaggedUsers(p.params)
			return nil
		}
		c.logger.Warn("failed to update forum post, will search/create", "error", err)
	}

	// Thread doesn't exist - check if we should create it based on "when" threshold
	when := c.config.When(p.owner, p.params.ChannelName)
	if when != "immediate" {
		shouldPost, reason := c.shouldPostThread(p.checkResp, when)

		if !shouldPost {
			c.logger.Debug("not creating forum thread - threshold not met",
				"pr", p.params.PRURL,
				"channel", p.params.ChannelName,
				"when", when,
				"reason", reason)
			return nil // Don't create thread yet - next event will check again
		}

		c.logger.Info("creating forum thread - threshold met",
			"pr", p.params.PRURL,
			"channel", p.params.ChannelName,
			"when", when,
			"reason", reason)
	}

	// Try to claim this thread creation
	const claimTTL = 10 * time.Second
	if !c.store.ClaimThread(ctx, p.owner, p.repo, p.number, p.channelID, claimTTL) {
		// Another instance claimed it, search for their thread
		c.logger.Info("another instance claimed forum thread, searching for their thread",
			"pr", p.params.PRURL)

		// Wait briefly for the other instance to post
		time.Sleep(crossInstanceRaceDelay * 2)

		// Search for existing thread created by the other instance
		if foundThreadID, foundMsgID, found := c.discord.FindForumThread(ctx, p.channelID, p.params.PRURL); found {
			c.logger.Info("found forum thread created by another instance",
				"thread_id", foundThreadID,
				"pr", p.params.PRURL)

			// Save the found thread and update it
			newInfo := state.ThreadInfo{
				ThreadID:    foundThreadID,
				MessageID:   foundMsgID,
				ChannelID:   p.channelID,
				ChannelType: "forum",
				LastState:   string(p.params.State),
				MessageText: content,
			}
			if err := c.store.SaveThread(ctx, p.owner, p.repo, p.number, p.channelID, newInfo); err != nil {
				c.logger.Warn("failed to save found thread info", "error", err)
			}

			// Update the found thread with current content
			if err := c.discord.UpdateForumPost(ctx, foundThreadID, foundMsgID, title, content); err != nil {
				c.logger.Warn("failed to update found forum post", "error", err)
			}

			c.trackTaggedUsers(p.params)
			return nil
		}

		c.logger.Warn("another instance claimed forum thread but not found, proceeding to create",
			"pr", p.params.PRURL)
	}

	// We claimed it - brief delay then search once more before creating
	time.Sleep(crossInstanceRaceDelay)

	// Search for existing thread (in case claim race occurred)
	if foundThreadID, foundMsgID, found := c.discord.FindForumThread(ctx, p.channelID, p.params.PRURL); found {
		c.logger.Info("found existing forum thread from search",
			"thread_id", foundThreadID,
			"pr", p.params.PRURL)

		// Save the found thread and update it
		newInfo := state.ThreadInfo{
			ThreadID:    foundThreadID,
			MessageID:   foundMsgID,
			ChannelID:   p.channelID,
			ChannelType: "forum",
			LastState:   string(p.params.State),
			MessageText: content,
		}
		if err := c.store.SaveThread(ctx, p.owner, p.repo, p.number, p.channelID, newInfo); err != nil {
			c.logger.Warn("failed to save found thread info", "error", err)
		}

		// Update the found thread with current content
		if err := c.discord.UpdateForumPost(ctx, foundThreadID, foundMsgID, title, content); err != nil {
			c.logger.Warn("failed to update found forum post", "error", err)
		}

		c.trackTaggedUsers(p.params)
		return nil
	}

	// Create new forum thread
	threadID, messageID, err := c.discord.PostForumThread(ctx, p.channelID, title, content)
	if err != nil {
		return fmt.Errorf("create forum thread: %w", err)
	}

	// Save thread info
	newInfo := state.ThreadInfo{
		ThreadID:    threadID,
		MessageID:   messageID,
		ChannelID:   p.channelID,
		ChannelType: "forum",
		LastState:   string(p.params.State),
		MessageText: content,
	}
	if err := c.store.SaveThread(ctx, p.owner, p.repo, p.number, p.channelID, newInfo); err != nil {
		c.logger.Warn("failed to save thread info", "error", err)
	}

	c.trackTaggedUsers(p.params)
	return nil
}

func (c *Coordinator) processTextChannel(ctx context.Context, p channelProcessParams) error {
	content := format.ChannelMessage(p.params)

	if p.exists && p.threadInfo.MessageID != "" {
		c.logger.Info("found thread in cache",
			"message_id", p.threadInfo.MessageID,
			"channel_id", p.channelID,
			"pr", p.params.PRURL,
			"last_state", p.threadInfo.LastState)

		// Content comparison: skip update if content unchanged
		if p.threadInfo.MessageText == content {
			c.logger.Info("channel message unchanged, skipping update",
				"message_id", p.threadInfo.MessageID,
				"pr", p.params.PRURL)
			c.trackTaggedUsers(p.params)
			return nil
		}

		// Update existing message
		err := c.discord.UpdateMessage(ctx, p.channelID, p.threadInfo.MessageID, content)
		if err == nil {
			p.threadInfo.MessageText = content
			p.threadInfo.LastState = string(p.params.State)
			if err := c.store.SaveThread(ctx, p.owner, p.repo, p.number, p.channelID, p.threadInfo); err != nil {
				c.logger.Warn("failed to save thread info", "error", err)
			}

			c.trackTaggedUsers(p.params)
			return nil
		}
		c.logger.Warn("failed to update message, will search/create", "error", err)
	} else {
		c.logger.Info("thread not found in cache, will search channel history",
			"channel_id", p.channelID,
			"pr", p.params.PRURL,
			"exists", p.exists,
			"has_message_id", p.exists && p.threadInfo.MessageID != "")
	}

	// Message doesn't exist - check if we should create it based on "when" threshold
	when := c.config.When(p.owner, p.params.ChannelName)
	if when != "immediate" {
		shouldPost, reason := c.shouldPostThread(p.checkResp, when)

		if !shouldPost {
			c.logger.Debug("not creating channel message - threshold not met",
				"pr", p.params.PRURL,
				"channel", p.params.ChannelName,
				"when", when,
				"reason", reason)
			return nil // Don't create message yet - next event will check again
		}

		c.logger.Info("creating channel message - threshold met",
			"pr", p.params.PRURL,
			"channel", p.params.ChannelName,
			"when", when,
			"reason", reason)
	}

	// Try to claim this thread creation
	const claimTTL = 10 * time.Second
	if !c.store.ClaimThread(ctx, p.owner, p.repo, p.number, p.channelID, claimTTL) {
		// Another instance claimed it, search for their message
		c.logger.Info("another instance claimed thread, searching for their message",
			"pr", p.params.PRURL)

		// Wait briefly for the other instance to post
		time.Sleep(crossInstanceRaceDelay * 2)

		// Search for existing message created by the other instance
		if foundMsgID, found := c.discord.FindChannelMessage(ctx, p.channelID, p.params.PRURL); found {
			c.logger.Info("found message created by another instance",
				"message_id", foundMsgID,
				"pr", p.params.PRURL)

			// Check if content needs updating
			currentContent, err := c.discord.MessageContent(ctx, p.channelID, foundMsgID)
			if err == nil && currentContent == content {
				c.logger.Info("found message content unchanged, skipping update",
					"message_id", foundMsgID,
					"pr", p.params.PRURL)
			} else if err := c.discord.UpdateMessage(ctx, p.channelID, foundMsgID, content); err != nil {
				c.logger.Warn("failed to update found message", "error", err)
			}

			// Save thread info to cache
			newInfo := state.ThreadInfo{
				MessageID:   foundMsgID,
				ChannelID:   p.channelID,
				ChannelType: "text",
				LastState:   string(p.params.State),
				MessageText: content,
			}
			if err := c.store.SaveThread(ctx, p.owner, p.repo, p.number, p.channelID, newInfo); err != nil {
				c.logger.Warn("failed to save found message info", "error", err)
			}

			c.trackTaggedUsers(p.params)
			return nil
		}

		c.logger.Warn("another instance claimed but message not found, proceeding to create",
			"pr", p.params.PRURL)
	}

	// We claimed it - brief delay then search once more before creating
	time.Sleep(crossInstanceRaceDelay)

	// Search for existing message (in case claim race occurred)
	if foundMsgID, found := c.discord.FindChannelMessage(ctx, p.channelID, p.params.PRURL); found {
		c.logger.Info("found existing channel message from search",
			"message_id", foundMsgID,
			"pr", p.params.PRURL)

		// Check if content actually changed before updating
		currentContent, err := c.discord.MessageContent(ctx, p.channelID, foundMsgID)
		if err == nil && currentContent == content {
			c.logger.Info("found message content unchanged, skipping update",
				"message_id", foundMsgID,
				"pr", p.params.PRURL)
		} else if err := c.discord.UpdateMessage(ctx, p.channelID, foundMsgID, content); err != nil {
			c.logger.Warn("failed to update found message", "error", err)
		}

		// Save thread info to cache
		newInfo := state.ThreadInfo{
			MessageID:   foundMsgID,
			ChannelID:   p.channelID,
			ChannelType: "text",
			LastState:   string(p.params.State),
			MessageText: content,
		}
		if err := c.store.SaveThread(ctx, p.owner, p.repo, p.number, p.channelID, newInfo); err != nil {
			c.logger.Warn("failed to save found message info", "error", err)
		}

		c.trackTaggedUsers(p.params)
		return nil
	}

	// Create new message
	messageID, err := c.discord.PostMessage(ctx, p.channelID, content)
	if err != nil {
		return fmt.Errorf("post message: %w", err)
	}

	// Save message info
	newInfo := state.ThreadInfo{
		MessageID:   messageID,
		ChannelID:   p.channelID,
		ChannelType: "text",
		LastState:   string(p.params.State),
		MessageText: content,
	}
	if err := c.store.SaveThread(ctx, p.owner, p.repo, p.number, p.channelID, newInfo); err != nil {
		c.logger.Warn("failed to save thread info", "error", err)
	}

	c.trackTaggedUsers(p.params)
	return nil
}

func (c *Coordinator) trackTaggedUsers(params format.ChannelMessageParams) {
	prURL := params.PRURL
	for _, au := range params.ActionUsers {
		// Only track if we have a Discord ID (mention contains <@)
		if au.Mention != "" && au.Mention[0] == '<' {
			c.tagTracker.mark(prURL, au.Username)
		}
	}
}

func (c *Coordinator) queueDMNotifications(
	ctx context.Context,
	owner, repo string,
	number int,
	checkResp *CheckResponse,
	prState format.PRState,
) {
	prURL := FormatPRURL(owner, repo, number)

	// For merged/closed PRs, update ALL previous DM recipients
	if prState == format.StateMerged || prState == format.StateClosed {
		c.updateAllDMsForClosedPR(ctx, owner, repo, number, checkResp, prState, prURL)
		return
	}

	// For active PRs, process each user who has a next action
	for username, action := range checkResp.Analysis.NextAction {
		c.processDMForUser(ctx, dmProcessParams{
			owner:      owner,
			repo:       repo,
			number:     number,
			checkResp:  checkResp,
			prState:    prState,
			prURL:      prURL,
			username:   username,
			actionKind: action.Kind,
		})
	}
}

type dmProcessParams struct {
	owner      string
	repo       string
	number     int
	checkResp  *CheckResponse
	prState    format.PRState
	prURL      string
	username   string
	actionKind string
}

// processDMForUser handles DM notification for a single user.
// Uses per-user-PR locking to prevent duplicate DMs.
func (c *Coordinator) processDMForUser(ctx context.Context, p dmProcessParams) {
	// Get Discord ID
	var discordID string
	if c.userMapper != nil {
		discordID = c.userMapper.DiscordID(ctx, p.username)
	}
	if discordID == "" {
		c.logger.Debug("skipping DM - no Discord mapping",
			"github_user", p.username)
		return
	}

	// Lock per user+PR to prevent concurrent duplicate DMs
	dmLock := c.dmLock(discordID, p.prURL)
	dmLock.Lock()
	defer dmLock.Unlock()

	// Check if user is in guild
	if !c.discord.IsUserInGuild(ctx, discordID) {
		c.logger.Debug("skipping DM - user not in guild",
			"github_user", p.username,
			"discord_id", discordID)
		return
	}

	// Build DM message
	params := format.ChannelMessageParams{
		Owner:  p.owner,
		Repo:   p.repo,
		Number: p.number,
		Title:  p.checkResp.PullRequest.Title,
		Author: p.checkResp.PullRequest.Author,
		State:  p.prState,
		PRURL:  p.prURL,
	}
	newMessage := format.DMMessage(params, format.ActionLabel(p.actionKind))

	// Check for existing queued DMs for this user+PR
	pendingDMs, err := c.store.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err != nil {
		c.logger.Warn("failed to check pending DMs", "error", err)
	}
	var existingPending *state.PendingDM
	for _, dm := range pendingDMs {
		if dm.UserID == discordID && dm.PRURL == p.prURL {
			existingPending = dm
			break
		}
	}

	// If there's a queued DM, update or cancel it based on state
	if existingPending != nil {
		if existingPending.MessageText == newMessage {
			// State unchanged, keep existing queued DM
			c.logger.Debug("DM already queued with same state",
				"user", p.username,
				"pr_url", p.prURL)
			return
		}
		// State changed - remove old queued DM and queue new one
		if err := c.store.RemovePendingDM(ctx, existingPending.ID); err != nil {
			c.logger.Warn("failed to remove old pending DM", "error", err)
		}
		c.logger.Debug("updating queued DM with new state",
			"user", p.username,
			"pr_url", p.prURL)
	}

	// Check for existing sent DM in store
	dmInfo, dmExists := c.store.DMInfo(ctx, discordID, p.prURL)

	// If no stored DM info, search DM history as fallback (handles restarts)
	if !dmExists {
		if foundChannelID, foundMsgID, found := c.discord.FindDMForPR(ctx, discordID, p.prURL); found {
			c.logger.Info("found existing DM in history",
				"user_id", discordID,
				"pr_url", p.prURL)
			dmInfo = state.DMInfo{
				ChannelID: foundChannelID,
				MessageID: foundMsgID,
			}
			dmExists = true
			// Note: we don't know the LastState, so we'll update it
		}
	}

	// Idempotency: skip if state unchanged
	if dmExists && dmInfo.LastState == string(p.prState) {
		c.logger.Info("DM skipped - state unchanged",
			"user", p.username,
			"pr_url", p.prURL,
			"state", p.prState)
		return
	}

	// If we have an existing DM, update it immediately
	if dmExists && dmInfo.ChannelID != "" && dmInfo.MessageID != "" {
		// Content comparison: skip if message unchanged
		if dmInfo.MessageText == newMessage {
			c.logger.Info("DM content unchanged, skipping update",
				"user", p.username,
				"pr_url", p.prURL)
			return
		}

		err := c.discord.UpdateDM(ctx, dmInfo.ChannelID, dmInfo.MessageID, newMessage)
		if err == nil {
			// Save updated DM info
			dmInfo.MessageText = newMessage
			dmInfo.LastState = string(p.prState)
			dmInfo.SentAt = time.Now()
			if err := c.store.SaveDMInfo(ctx, discordID, p.prURL, dmInfo); err != nil {
				c.logger.Warn("failed to save updated DM info", "error", err)
			}
			c.logger.Info("updated DM notification",
				"user_id", discordID,
				"github_user", p.username,
				"pr_url", p.prURL,
				"state", p.prState)
			return
		}
		c.logger.Warn("failed to update DM",
			"error", err,
			"user_id", discordID,
			"pr_url", p.prURL)
		// Fall through to potentially queue new DM
	}

	// New DM - try to claim it to prevent duplicate DMs from multiple instances
	const dmClaimTTL = 10 * time.Second
	if !c.store.ClaimDM(ctx, discordID, p.prURL, dmClaimTTL) {
		// Another instance claimed this DM, search for it
		c.logger.Debug("another instance claimed DM, searching for their message",
			"user", p.username,
			"pr_url", p.prURL)

		// Brief wait for other instance to create/queue DM
		time.Sleep(200 * time.Millisecond)

		// Check if DM was created by other instance
		if foundChannelID, foundMsgID, found := c.discord.FindDMForPR(ctx, discordID, p.prURL); found {
			c.logger.Info("found DM created by another instance",
				"user_id", discordID,
				"pr_url", p.prURL)

			// Save the found DM info
			dmInfo = state.DMInfo{
				ChannelID:   foundChannelID,
				MessageID:   foundMsgID,
				MessageText: newMessage,
				LastState:   string(p.prState),
				SentAt:      time.Now(),
			}
			if err := c.store.SaveDMInfo(ctx, discordID, p.prURL, dmInfo); err != nil {
				c.logger.Warn("failed to save found DM info", "error", err)
			}
			return
		}

		// DM not found yet, maybe queued - check pending DMs
		pendingDMs, err := c.store.PendingDMs(ctx, time.Now().Add(24*time.Hour))
		if err == nil {
			for _, pendingDM := range pendingDMs {
				if pendingDM.UserID == discordID && pendingDM.PRURL == p.prURL {
					c.logger.Debug("DM already queued by another instance",
						"user", p.username,
						"pr_url", p.prURL)
					return
				}
			}
		}

		c.logger.Warn("another instance claimed DM but not found, proceeding to queue",
			"user", p.username,
			"pr_url", p.prURL)
	}

	// Check delay configuration
	channels := c.config.ChannelsForRepo(c.org, p.repo)
	delay := 65 // default
	if len(channels) > 0 {
		delay = c.config.ReminderDMDelay(c.org, channels[0])
	}

	if delay == 0 {
		c.logger.Debug("skipping DM - notifications disabled",
			"github_user", p.username,
			"repo", p.repo)
		return
	}

	// Calculate send time
	sendAt := time.Now()
	if c.tagTracker.wasTagged(p.prURL, p.username) {
		// User was tagged in channel, delay DM
		sendAt = sendAt.Add(time.Duration(delay) * time.Minute)
	}

	// Queue the DM
	now := time.Now()
	dm := &state.PendingDM{
		ID:          uuid.New().String(),
		UserID:      discordID,
		PRURL:       p.prURL,
		MessageText: newMessage,
		SendAt:      sendAt,
		CreatedAt:   now,
		ExpiresAt:   now.Add(7 * 24 * time.Hour), // Expire after 7 days
		GuildID:     c.discord.GuildID(),
		Org:         c.org,
		RetryCount:  0,
	}

	if err := c.store.QueuePendingDM(ctx, dm); err != nil {
		c.logger.Warn("failed to queue DM",
			"error", err,
			"user", p.username)
		return
	}
	c.logger.Debug("queued DM notification",
		"user", p.username,
		"discord_id", discordID,
		"send_at", sendAt)
}

// updateAllDMsForClosedPR updates DMs for all users who received notifications about this PR.
// This ensures users see the final merged/closed state.
func (c *Coordinator) updateAllDMsForClosedPR(
	ctx context.Context,
	owner, repo string,
	number int,
	checkResp *CheckResponse,
	prState format.PRState,
	prURL string,
) {
	// Get all users who received DMs for this PR
	userIDs := c.store.ListDMUsers(ctx, prURL)
	if len(userIDs) == 0 {
		c.logger.Info("no DM recipients found for closed PR",
			"pr_url", prURL,
			"state", prState)
		// Also cancel any pending DMs
		c.cancelPendingDMsForPR(ctx, prURL)
		return
	}

	c.logger.Info("updating DMs for closed/merged PR",
		"pr_url", prURL,
		"state", prState,
		"recipient_count", len(userIDs))

	// Build the final message (no action since PR is closed)
	params := format.ChannelMessageParams{
		Owner:  owner,
		Repo:   repo,
		Number: number,
		Title:  checkResp.PullRequest.Title,
		Author: checkResp.PullRequest.Author,
		State:  prState,
		PRURL:  prURL,
	}
	finalMessage := format.DMMessage(params, "") // No action for closed PRs

	// Update each user's DM
	for _, discordID := range userIDs {
		c.updateDMForClosedPR(ctx, discordID, prURL, prState, finalMessage)
	}

	// Cancel any pending DMs for this PR
	c.cancelPendingDMsForPR(ctx, prURL)
}

// updateDMForClosedPR updates a single user's DM for a closed PR.
func (c *Coordinator) updateDMForClosedPR(ctx context.Context, discordID, prURL string, prState format.PRState, msg string) {
	lock := c.dmLock(discordID, prURL)
	lock.Lock()
	defer lock.Unlock()

	dmInfo, exists := c.store.DMInfo(ctx, discordID, prURL)
	if !exists || dmInfo.ChannelID == "" || dmInfo.MessageID == "" {
		return
	}

	// Idempotency check
	if dmInfo.LastState == string(prState) {
		return
	}

	if err := c.discord.UpdateDM(ctx, dmInfo.ChannelID, dmInfo.MessageID, msg); err != nil {
		c.logger.Warn("failed to update DM for closed PR",
			"error", err,
			"user_id", discordID,
			"pr_url", prURL)
		return
	}

	dmInfo.MessageText = msg
	dmInfo.LastState = string(prState)
	dmInfo.SentAt = time.Now()
	if err := c.store.SaveDMInfo(ctx, discordID, prURL, dmInfo); err != nil {
		c.logger.Warn("failed to save DM info", "error", err)
	}
	c.logger.Debug("updated DM for closed PR",
		"user_id", discordID,
		"pr_url", prURL,
		"state", prState)
}

// cancelPendingDMsForPR removes all queued DMs for a PR.
func (c *Coordinator) cancelPendingDMsForPR(ctx context.Context, prURL string) {
	pendingDMs, err := c.store.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err != nil {
		c.logger.Warn("failed to get pending DMs for cancellation", "error", err)
		return
	}

	for _, dm := range pendingDMs {
		if dm.PRURL != prURL {
			continue
		}
		if err := c.store.RemovePendingDM(ctx, dm.ID); err != nil {
			c.logger.Warn("failed to cancel pending DM",
				"dm_id", dm.ID,
				"pr_url", prURL,
				"error", err)
			continue
		}
		c.logger.Debug("cancelled pending DM for closed PR",
			"dm_id", dm.ID,
			"user_id", dm.UserID,
			"pr_url", prURL)
	}
}

// Wait waits for all pending event processing to complete.
func (c *Coordinator) Wait() {
	c.wg.Wait()
}

// prLock returns a mutex for serializing operations on a specific PR.
func (c *Coordinator) prLock(url string) *sync.Mutex {
	return c.prLocks.get(url)
}

// dmLock returns a mutex for serializing DM operations for a specific user+PR.
func (c *Coordinator) dmLock(userID, prURL string) *sync.Mutex {
	return c.dmLocks.get(userID + ":" + prURL)
}

// CleanupLocks removes idle locks to prevent unbounded memory growth.
// Should be called periodically from the main event loop.
func (c *Coordinator) CleanupLocks() {
	prRemoved := c.prLocks.cleanup(lockIdleTimeout)
	dmRemoved := c.dmLocks.cleanup(lockIdleTimeout)
	if prRemoved > 0 || dmRemoved > 0 {
		c.logger.Debug("cleaned up idle locks",
			"pr_locks_removed", prRemoved,
			"dm_locks_removed", dmRemoved)
	}
}

// ExportUserMapperCache returns the cached user mappings (githubUsername -> discordID).
// Returns empty map if the user mapper doesn't support cache export.
func (c *Coordinator) ExportUserMapperCache() map[string]string {
	if mapper, ok := c.userMapper.(*usermapping.Mapper); ok {
		return mapper.ExportCache()
	}
	return make(map[string]string)
}

// tagTracker tracks which users were tagged in channel messages.
// Implements bounded storage to prevent memory exhaustion.
type tagTracker struct {
	tagged map[string]map[string]bool // prURL -> username -> tagged
	order  []string                   // Insertion order for LRU-like cleanup
	mu     sync.RWMutex
}

func newTagTracker() *tagTracker {
	return &tagTracker{
		tagged: make(map[string]map[string]bool),
		order:  make([]string, 0, maxTagTrackerEntries),
	}
}

func (t *tagTracker) mark(prURL, username string) {
	t.mu.Lock()
	defer t.mu.Unlock()

	// Track insertion order for cleanup
	isNew := t.tagged[prURL] == nil
	if isNew {
		t.tagged[prURL] = make(map[string]bool)
		t.order = append(t.order, prURL)
	}
	t.tagged[prURL][username] = true

	// Cleanup oldest entries if we exceed the limit
	if len(t.tagged) > maxTagTrackerEntries {
		// Remove oldest 10% to avoid frequent cleanups
		toRemove := max(maxTagTrackerEntries/10, 1)
		for i := 0; i < toRemove && len(t.order) > 0; i++ {
			oldPR := t.order[0]
			t.order = t.order[1:]
			delete(t.tagged, oldPR)
		}
		slog.Debug("tag tracker cleanup performed",
			"removed", toRemove,
			"remaining", len(t.tagged))
	}
}

func (t *tagTracker) wasTagged(prURL, username string) bool {
	t.mu.RLock()
	defer t.mu.RUnlock()

	if t.tagged[prURL] == nil {
		return false
	}
	return t.tagged[prURL][username]
}

// PollAndReconcile queries GitHub for PRs and reconciles their state.
// This serves as a backup mechanism when sprinkler events are missed.
func (c *Coordinator) PollAndReconcile(ctx context.Context) {
	if c.searcher == nil {
		c.logger.Debug("skipping poll - no PR searcher configured")
		return
	}

	c.logger.Info("starting PR poll and reconcile")

	// Poll open PRs (updated in last 24 hours)
	openPRs, err := c.searcher.ListOpenPRs(ctx, c.org, pollOpenPRHours)
	if err != nil {
		c.logger.Error("failed to list open PRs", "error", err)
		openPRs = nil // Clear for final log
	}
	c.logger.Debug("found open PRs to reconcile", "count", len(openPRs))
	for _, pr := range openPRs {
		c.reconcilePR(ctx, pr)
	}

	// Poll closed PRs (closed in last hour) to catch terminal states
	closedPRs, err := c.searcher.ListClosedPRs(ctx, c.org, pollClosedPRHours)
	if err != nil {
		c.logger.Error("failed to list closed PRs", "error", err)
		closedPRs = nil // Clear for final log
	}
	c.logger.Debug("found closed PRs to reconcile", "count", len(closedPRs))
	for _, pr := range closedPRs {
		c.reconcilePR(ctx, pr)
	}

	c.logger.Info("PR poll and reconcile complete",
		"open_prs", len(openPRs),
		"closed_prs", len(closedPRs))

	// Check and send daily reports after reconciliation
	c.checkDailyReports(ctx, openPRs)
}

// reconcilePR checks a single PR's state and updates Discord if needed.
func (c *Coordinator) reconcilePR(ctx context.Context, pr PRSearchResult) {
	c.logger.Debug("reconciling PR",
		"pr_url", pr.URL,
		"updated_at", pr.UpdatedAt.Format(time.RFC3339))

	// Create a synthetic event for processing
	// Use UpdatedAt for both timestamp and deliveryID. Real webhook events use their
	// own event timestamp (which may be newer than UpdatedAt for events like check runs),
	// but for polling we use UpdatedAt since that's all we have from the GitHub API.
	// This also provides deduplication: we only re-process when the PR actually updates.
	event := SprinklerEvent{
		Type:       "poll",
		URL:        pr.URL,
		Timestamp:  pr.UpdatedAt,
		DeliveryID: fmt.Sprintf("poll-%s-%s", pr.URL, pr.UpdatedAt.Format(time.RFC3339)),
	}

	// Process the event (reuses all the normal event processing logic)
	// This will check Discord state and update if needed
	if err := c.processEventSync(ctx, event); err != nil {
		c.logger.Warn("failed to reconcile PR",
			"url", pr.URL,
			"error", err)
	}
}

// checkDailyReports checks if daily reports should be sent to users.
func (c *Coordinator) checkDailyReports(ctx context.Context, prs []PRSearchResult) {
	if len(prs) == 0 {
		c.logger.Debug("skipping daily reports - no PRs found")
		return
	}

	// Create daily report sender
	sender := dailyreport.NewSender(c.store, c.logger)

	// Load config to check if daily reports are disabled
	cfg, exists := c.config.Config(c.org)
	if !exists {
		c.logger.Debug("skipping daily reports - no config found")
		return
	}

	// Register Discord client as DM sender
	guildID := cfg.Global.GuildID
	if guildID == "" {
		c.logger.Debug("skipping daily reports - no guild ID in config")
		return
	}
	sender.RegisterGuild(guildID, c.discord)

	// Extract unique GitHub users from PRs
	userMap := make(map[string]bool)
	for _, pr := range prs {
		// For each PR, we'll need to check Turn API to see which users have actions
		// For now, just collect PR authors as a starting point
		if _, ok := ParsePRURL(pr.URL); !ok {
			continue
		}

		// Call Turn API to get next actions for this PR
		checkResp, err := c.turn.Check(ctx, pr.URL, "", pr.UpdatedAt)
		if err != nil {
			c.logger.Debug("failed to check PR for daily report",
				"pr_url", pr.URL,
				"error", err)
			continue
		}

		// Add all users with next actions
		for username := range checkResp.Analysis.NextAction {
			if username != "_system" {
				userMap[username] = true
			}
		}

		// Also add PR author for outgoing PRs
		if checkResp.PullRequest.Author != "" {
			userMap[checkResp.PullRequest.Author] = true
		}
	}

	c.logger.Debug("checking daily reports for users",
		"user_count", len(userMap),
		"pr_count", len(prs))

	// Check each user for daily report eligibility
	for githubUsername := range userMap {
		c.checkUserDailyReport(ctx, sender, githubUsername, guildID, prs)
	}
}

// checkUserDailyReport checks and sends a daily report for a specific user.
func (c *Coordinator) checkUserDailyReport(
	ctx context.Context,
	sender *dailyreport.Sender,
	githubUsername string,
	guildID string,
	prs []PRSearchResult,
) {
	// Get Discord ID for this GitHub user
	discordID := ""
	if c.userMapper != nil {
		discordID = c.userMapper.DiscordID(ctx, githubUsername)
	}
	if discordID == "" {
		c.logger.Debug("skipping daily report - no Discord mapping",
			"github_user", githubUsername)
		return
	}

	// Check if user is in guild
	if !c.discord.IsUserInGuild(ctx, discordID) {
		c.logger.Debug("skipping daily report - user not in guild",
			"github_user", githubUsername,
			"discord_id", discordID)
		return
	}

	// Build list of incoming and outgoing PRs for this user
	var incomingPRs []discord.PRSummary
	var outgoingPRs []discord.PRSummary

	for _, pr := range prs {
		prInfo, ok := ParsePRURL(pr.URL)
		if !ok {
			continue
		}

		// Call Turn API to analyze this PR for this user
		checkResp, err := c.turn.Check(ctx, pr.URL, githubUsername, pr.UpdatedAt)
		if err != nil {
			c.logger.Debug("failed to check PR for user report",
				"pr_url", pr.URL,
				"github_user", githubUsername,
				"error", err)
			continue
		}

		// Determine PR state
		prState := format.StateFromAnalysis(format.StateAnalysisParams{
			Merged:             checkResp.PullRequest.Merged,
			Closed:             checkResp.PullRequest.Closed,
			Draft:              checkResp.PullRequest.Draft,
			MergeConflict:      checkResp.Analysis.MergeConflict,
			Approved:           checkResp.Analysis.Approved,
			ChecksFailing:      checkResp.Analysis.Checks.Failing,
			ChecksPending:      checkResp.Analysis.Checks.Pending,
			ChecksWaiting:      checkResp.Analysis.Checks.Waiting,
			UnresolvedComments: checkResp.Analysis.UnresolvedComments,
			WorkflowState:      checkResp.Analysis.WorkflowState,
		})

		// Check if user has an action on this PR
		action, hasAction := checkResp.Analysis.NextAction[githubUsername]
		isAuthor := checkResp.PullRequest.Author == githubUsername

		// Determine if PR is blocked (needs immediate attention)
		isBlocked := prState == format.StateTestsBroken ||
			prState == format.StateChanges ||
			prState == format.StateConflict

		summary := discord.PRSummary{
			Repo:      prInfo.Repo,
			Number:    prInfo.Number,
			Title:     checkResp.PullRequest.Title,
			Author:    checkResp.PullRequest.Author,
			State:     string(prState),
			URL:       pr.URL,
			UpdatedAt: pr.UpdatedAt.Format(time.RFC3339),
			IsBlocked: isBlocked,
		}

		if hasAction {
			summary.Action = format.ActionLabel(action.Kind)
		}

		// Categorize as incoming or outgoing
		if isAuthor {
			outgoingPRs = append(outgoingPRs, summary)
		} else if hasAction {
			incomingPRs = append(incomingPRs, summary)
		}
	}

	// Skip if no PRs for this user
	if len(incomingPRs) == 0 && len(outgoingPRs) == 0 {
		return
	}

	// Check if user is currently active on Discord
	if !c.discord.IsUserActive(ctx, discordID) {
		c.logger.Debug("skipping daily report - user not active",
			"github_user", githubUsername,
			"discord_id", discordID)
		return
	}

	// Build user blocking info
	userInfo := dailyreport.UserBlockingInfo{
		GitHubUsername: githubUsername,
		DiscordUserID:  discordID,
		GuildID:        guildID,
		IncomingPRs:    incomingPRs,
		OutgoingPRs:    outgoingPRs,
	}

	// Check if report should be sent
	if !sender.ShouldSendReport(ctx, userInfo) {
		return
	}

	// Send the report
	if err := sender.SendReport(ctx, userInfo); err != nil {
		c.logger.Error("failed to send daily report",
			"error", err,
			"github_user", githubUsername,
			"discord_id", discordID)
	}
}
