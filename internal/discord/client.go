// Package discord provides Discord API client functionality.
package discord

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/bwmarrin/discordgo"
	"github.com/codeGROOVE-dev/retry"

	"github.com/codeGROOVE-dev/discordian/internal/format"
)

// Client wraps discordgo.Session with a clean interface for bot operations.
type Client struct {
	session          *discordgo.Session
	channelCache     map[string]string                // channel name -> ID
	channelTypeCache map[string]discordgo.ChannelType // channel ID -> type
	userCache        map[string]string                // username -> ID
	guildID          string
	mu               sync.RWMutex
}

// New creates a new Discord client for a specific guild.
func New(token string) (*Client, error) {
	session, err := discordgo.New("Bot " + token)
	if err != nil {
		return nil, fmt.Errorf("failed to create Discord session: %w", err)
	}

	return &Client{
		session:          session,
		channelCache:     make(map[string]string),
		channelTypeCache: make(map[string]discordgo.ChannelType),
		userCache:        make(map[string]string),
	}, nil
}

// retryableCtx wraps a function with standard retry configuration.
func retryableCtx(ctx context.Context, fn func() error) error {
	return retry.Do(
		fn,
		retry.Context(ctx),
		retry.Attempts(3),
		retry.Delay(500*time.Millisecond),
		retry.MaxDelay(5*time.Second),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.RetryIf(func(err error) bool {
			return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
		}),
	)
}

// SetGuildID sets the guild ID for this client.
func (c *Client) SetGuildID(guildID string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.guildID = guildID
}

// GuildID returns the current guild ID.
func (c *Client) GuildID() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.guildID
}

// openTimeout is the maximum time to wait for Discord connection.
const openTimeout = 30 * time.Second

// Open opens the WebSocket connection to Discord with a timeout.
func (c *Client) Open() error {
	done := make(chan error, 1)
	go func() {
		done <- c.session.Open()
	}()

	select {
	case err := <-done:
		return err
	case <-time.After(openTimeout):
		// Try to close the session to clean up
		c.session.Close() //nolint:errcheck,gosec // best-effort close on timeout
		return errors.New("timeout waiting for Discord connection")
	}
}

// Close closes the WebSocket connection.
func (c *Client) Close() error {
	return c.session.Close()
}

// Session returns the underlying discordgo session.
func (c *Client) Session() *discordgo.Session {
	return c.session
}

// PostMessage sends a plain text message to a channel with link embeds suppressed.
func (c *Client) PostMessage(ctx context.Context, channelID, text string) (string, error) {
	msg, err := c.session.ChannelMessageSendComplex(channelID, &discordgo.MessageSend{
		Content: text,
		Flags:   discordgo.MessageFlagsSuppressEmbeds,
	})
	if err != nil {
		return "", fmt.Errorf("failed to send message: %w", err)
	}

	slog.Info("posted channel message",
		"channel_id", channelID,
		"message_id", msg.ID,
		"content", text)

	return msg.ID, nil
}

// UpdateMessage edits an existing message.
func (c *Client) UpdateMessage(ctx context.Context, channelID, messageID, newText string) error {
	_, err := c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:      messageID,
		Channel: channelID,
		Content: &newText,
		Flags:   discordgo.MessageFlagsSuppressEmbeds,
	})
	if err != nil {
		return fmt.Errorf("failed to edit message: %w", err)
	}

	slog.Info("updated channel message",
		"channel_id", channelID,
		"message_id", messageID,
		"content", newText)

	return nil
}

// PostForumThread creates a forum post with title and content, with link embeds suppressed.
func (c *Client) PostForumThread(ctx context.Context, channelID, title, content string) (threadID, messageID string, err error) {
	thread, err := c.session.ForumThreadStartComplex(channelID, &discordgo.ThreadStart{
		Name: format.Truncate(title, 100), // Discord limits thread names
	}, &discordgo.MessageSend{
		Content: content,
		Flags:   discordgo.MessageFlagsSuppressEmbeds,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to create forum thread: %w", err)
	}

	c.mu.RLock()
	guildID := c.guildID
	c.mu.RUnlock()

	slog.Info("created forum thread",
		"guild_id", guildID,
		"channel_id", channelID,
		"thread_id", thread.ID,
		"title", title,
		"content", content)

	// Get the first message in the thread to return its ID
	messages, err := c.session.ChannelMessages(thread.ID, 1, "", "", "")
	if err == nil && len(messages) > 0 {
		return thread.ID, messages[0].ID, nil
	}

	return thread.ID, "", nil
}

// UpdateForumPost updates both the thread title and starter message.
func (c *Client) UpdateForumPost(ctx context.Context, threadID, messageID, newTitle, newContent string) error {
	_, err := c.session.ChannelEdit(threadID, &discordgo.ChannelEdit{
		Name: format.Truncate(newTitle, 100),
	})
	if err != nil {
		return fmt.Errorf("failed to update thread title: %w", err)
	}

	if messageID != "" {
		_, err = c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
			ID:      messageID,
			Channel: threadID,
			Content: &newContent,
			Flags:   discordgo.MessageFlagsSuppressEmbeds,
		})
		if err != nil {
			return fmt.Errorf("failed to update thread message: %w", err)
		}
	}

	slog.Info("updated forum post",
		"thread_id", threadID,
		"title", newTitle,
		"content", newContent)

	return nil
}

// ArchiveThread archives a forum thread.
func (c *Client) ArchiveThread(ctx context.Context, threadID string) error {
	archived := true
	_, err := c.session.ChannelEdit(threadID, &discordgo.ChannelEdit{
		Archived: &archived,
	})
	if err != nil {
		return fmt.Errorf("failed to archive thread: %w", err)
	}

	slog.Debug("archived thread", "thread_id", threadID)
	return nil
}

// SendDM sends a direct message to a user with link embeds suppressed.
func (c *Client) SendDM(ctx context.Context, userID, text string) (channelID, messageID string, err error) {
	channel, err := c.session.UserChannelCreate(userID)
	if err != nil {
		return "", "", fmt.Errorf("failed to create DM channel: %w", err)
	}

	msg, err := c.session.ChannelMessageSendComplex(channel.ID, &discordgo.MessageSend{
		Content: text,
		Flags:   discordgo.MessageFlagsSuppressEmbeds,
	})
	if err != nil {
		return "", "", fmt.Errorf("failed to send DM: %w", err)
	}

	slog.Info("sent DM",
		"user_id", userID,
		"channel_id", channel.ID,
		"message_id", msg.ID,
		"content", text)

	return channel.ID, msg.ID, nil
}

// UpdateDM updates an existing DM message.
func (c *Client) UpdateDM(ctx context.Context, channelID, messageID, newText string) error {
	_, err := c.session.ChannelMessageEditComplex(&discordgo.MessageEdit{
		ID:      messageID,
		Channel: channelID,
		Content: &newText,
		Flags:   discordgo.MessageFlagsSuppressEmbeds,
	})
	if err != nil {
		return fmt.Errorf("failed to update DM: %w", err)
	}

	slog.Info("updated DM",
		"channel_id", channelID,
		"message_id", messageID,
		"content", newText)

	return nil
}

// ResolveChannelID resolves a channel name to its ID.
func (c *Client) ResolveChannelID(ctx context.Context, channelName string) string {
	// If it looks like an ID already (long numeric string), return it
	if len(channelName) > 15 {
		isID := true
		for _, r := range channelName {
			if r < '0' || r > '9' {
				isID = false
				break
			}
		}
		if isID {
			return channelName
		}
	}

	// Check cache
	c.mu.RLock()
	if id, ok := c.channelCache[channelName]; ok {
		c.mu.RUnlock()
		return id
	}
	guildID := c.guildID
	c.mu.RUnlock()

	if guildID == "" {
		return channelName
	}

	channels, err := c.session.GuildChannels(guildID)
	if err != nil {
		slog.Warn("failed to fetch guild channels",
			"guild_id", guildID,
			"error", err)
		return channelName
	}

	name := strings.TrimPrefix(channelName, "#")

	for _, ch := range channels {
		if ch.Name != name {
			continue
		}

		c.mu.Lock()
		c.channelCache[channelName] = ch.ID
		c.mu.Unlock()

		slog.Debug("resolved channel",
			"name", channelName,
			"id", ch.ID)

		return ch.ID
	}

	slog.Debug("channel not found",
		"name", channelName,
		"guild_id", guildID)

	return channelName
}

// ChannelType returns the type of a channel (forum, text, etc.).
func (c *Client) ChannelType(ctx context.Context, channelID string) (discordgo.ChannelType, error) {
	// Check cache first
	c.mu.RLock()
	if channelType, ok := c.channelTypeCache[channelID]; ok {
		c.mu.RUnlock()
		return channelType, nil
	}
	c.mu.RUnlock()

	channel, err := c.session.Channel(channelID)
	if err != nil {
		return 0, fmt.Errorf("failed to fetch channel: %w", err)
	}

	// Cache the result
	c.mu.Lock()
	c.channelTypeCache[channelID] = channel.Type
	c.mu.Unlock()

	return channel.Type, nil
}

// IsForumChannel returns true if the channel is a forum channel.
func (c *Client) IsForumChannel(ctx context.Context, channelID string) bool {
	channelType, err := c.ChannelType(ctx, channelID)
	if err != nil {
		slog.Debug("failed to check channel type", "channel_id", channelID, "error", err)
		return false
	}
	return channelType == discordgo.ChannelTypeGuildForum
}

// LookupUserByUsername finds a Discord user ID by username match.
// Uses multi-tier matching: exact, case-insensitive, then prefix (if unambiguous).
func (c *Client) LookupUserByUsername(ctx context.Context, username string) string {
	c.mu.RLock()
	if id, ok := c.userCache[username]; ok {
		c.mu.RUnlock()
		return id
	}
	guildID := c.guildID
	c.mu.RUnlock()

	if guildID == "" {
		return ""
	}

	members, err := c.session.GuildMembers(guildID, "", 1000)
	if err != nil {
		slog.Warn("failed to fetch guild members",
			"guild_id", guildID,
			"error", err)
		return ""
	}

	// Skip empty usernames
	if username == "" {
		slog.Debug("empty username provided")
		return ""
	}

	slog.Debug("searching for user in guild",
		"username", username,
		"guild_id", guildID,
		"total_members", len(members))

	// Tier 1: Exact match (Username takes precedence over GlobalName)
	for _, member := range members {
		if member.User.Username == username {
			c.mu.Lock()
			c.userCache[username] = member.User.ID
			c.mu.Unlock()

			slog.Debug("found user by exact username match",
				"username", username,
				"user_id", member.User.ID,
				"discord_username", member.User.Username,
				"discord_global_name", member.User.GlobalName)

			return member.User.ID
		}
	}
	for _, member := range members {
		if member.User.GlobalName == username {
			c.mu.Lock()
			c.userCache[username] = member.User.ID
			c.mu.Unlock()

			slog.Debug("found user by exact global name match",
				"username", username,
				"user_id", member.User.ID,
				"discord_username", member.User.Username,
				"discord_global_name", member.User.GlobalName)

			return member.User.ID
		}
	}

	// Tier 2: Case-insensitive match (Username takes precedence over GlobalName)
	for _, member := range members {
		if strings.EqualFold(member.User.Username, username) {
			c.mu.Lock()
			c.userCache[username] = member.User.ID
			c.mu.Unlock()

			slog.Info("found user by case-insensitive username match",
				"username", username,
				"user_id", member.User.ID,
				"discord_username", member.User.Username,
				"discord_global_name", member.User.GlobalName)

			return member.User.ID
		}
	}
	for _, member := range members {
		if strings.EqualFold(member.User.GlobalName, username) {
			c.mu.Lock()
			c.userCache[username] = member.User.ID
			c.mu.Unlock()

			slog.Info("found user by case-insensitive global name match",
				"username", username,
				"user_id", member.User.ID,
				"discord_username", member.User.Username,
				"discord_global_name", member.User.GlobalName)

			return member.User.ID
		}
	}

	lowerUsername := strings.ToLower(username)

	// Tier 3: Prefix match (only if unambiguous)
	type prefixMatch struct {
		member    *discordgo.Member
		matchType string
	}
	var matches []prefixMatch

	for _, member := range members {
		usernamePrefix := strings.HasPrefix(strings.ToLower(member.User.Username), lowerUsername)
		globalNamePrefix := strings.HasPrefix(strings.ToLower(member.User.GlobalName), lowerUsername)

		if usernamePrefix {
			matches = append(matches, prefixMatch{member: member, matchType: "username_prefix"})
		} else if globalNamePrefix {
			matches = append(matches, prefixMatch{member: member, matchType: "global_name_prefix"})
		}
	}

	if len(matches) == 1 {
		match := matches[0]
		c.mu.Lock()
		c.userCache[username] = match.member.User.ID
		c.mu.Unlock()

		slog.Info("found user by unambiguous prefix match",
			"username", username,
			"user_id", match.member.User.ID,
			"match_type", match.matchType,
			"discord_username", match.member.User.Username,
			"discord_global_name", match.member.User.GlobalName)

		return match.member.User.ID
	}

	if len(matches) > 1 {
		slog.Warn("ambiguous prefix matches found, not using fuzzy match",
			"username", username,
			"match_count", len(matches))
		for i, match := range matches {
			if i >= 3 {
				break
			}
			slog.Debug("ambiguous match candidate",
				"username", username,
				"discord_username", match.member.User.Username,
				"discord_global_name", match.member.User.GlobalName)
		}
	}

	// Log first 5 members to help debug
	for i, member := range members {
		if i >= 5 {
			break
		}
		slog.Debug("sample guild member",
			"index", i,
			"discord_username", member.User.Username,
			"discord_global_name", member.User.GlobalName,
			"user_id", member.User.ID)
	}

	slog.Debug("user not found",
		"username", username,
		"guild_id", guildID)

	return ""
}

// IsBotInChannel checks if the bot has permission to send messages in a channel.
func (c *Client) IsBotInChannel(ctx context.Context, channelID string) bool {
	if c.session.State == nil || c.session.State.User == nil {
		return false
	}

	perms, err := c.session.UserChannelPermissions(c.session.State.User.ID, channelID)
	if err != nil {
		slog.Debug("failed to check channel permissions",
			"channel_id", channelID,
			"error", err)
		return false
	}

	return perms&discordgo.PermissionSendMessages != 0
}

// IsUserInGuild checks if a user is a member of the guild.
func (c *Client) IsUserInGuild(ctx context.Context, userID string) bool {
	c.mu.RLock()
	guildID := c.guildID
	c.mu.RUnlock()

	if guildID == "" {
		slog.Debug("cannot check guild membership - no guild ID set",
			"user_id", userID)
		return false
	}

	_, err := c.session.GuildMember(guildID, userID)
	if err != nil {
		slog.Debug("user not found in guild",
			"user_id", userID,
			"guild_id", guildID,
			"error", err)
		return false
	}

	slog.Debug("user is guild member",
		"user_id", userID,
		"guild_id", guildID)
	return true
}

// IsUserActive checks if a user is currently online, idle, or do-not-disturb (not offline).
func (c *Client) IsUserActive(ctx context.Context, userID string) bool {
	c.mu.RLock()
	guildID := c.guildID
	c.mu.RUnlock()

	if guildID == "" {
		slog.Debug("cannot check user presence - no guild ID set",
			"user_id", userID)
		return false
	}

	// Get guild from state
	guild, err := c.session.State.Guild(guildID)
	if err != nil {
		slog.Debug("failed to get guild from state",
			"guild_id", guildID,
			"error", err)
		return false
	}

	// Find user's presence
	for _, p := range guild.Presences {
		if p.User.ID == userID {
			// Consider "online", "idle", and "dnd" as active
			// "offline" or "" means not active
			isActive := p.Status == "online" || p.Status == "idle" || p.Status == "dnd"
			slog.Debug("user presence check",
				"user_id", userID,
				"status", p.Status,
				"is_active", isActive)
			return isActive
		}
	}

	// User presence not found - assume offline
	slog.Debug("user presence not found - assuming offline",
		"user_id", userID)
	return false
}

// GuildInfo holds basic guild information.
type GuildInfo struct {
	ID   string
	Name string
}

// GuildInfo returns information about the current guild.
func (c *Client) GuildInfo(ctx context.Context) (GuildInfo, error) {
	c.mu.RLock()
	guildID := c.guildID
	c.mu.RUnlock()

	if guildID == "" {
		return GuildInfo{}, errors.New("no guild ID set")
	}

	guild, err := c.session.Guild(guildID)
	if err != nil {
		return GuildInfo{}, fmt.Errorf("failed to fetch guild: %w", err)
	}

	return GuildInfo{
		ID:   guild.ID,
		Name: guild.Name,
	}, nil
}

// BotInfo holds basic bot user information.
type BotInfo struct {
	UserID   string
	Username string
}

// BotInfo returns the bot's user information.
func (c *Client) BotInfo(ctx context.Context) (BotInfo, error) {
	if c.session.State == nil || c.session.State.User == nil {
		return BotInfo{}, errors.New("bot user not available")
	}

	return BotInfo{
		UserID:   c.session.State.User.ID,
		Username: c.session.State.User.Username,
	}, nil
}

// ChannelMessages retrieves recent messages from a channel.
func (c *Client) ChannelMessages(ctx context.Context, channelID string, limit int) ([]*discordgo.Message, error) {
	messages, err := c.session.ChannelMessages(channelID, limit, "", "", "")
	if err != nil {
		return nil, fmt.Errorf("failed to fetch channel messages: %w", err)
	}
	return messages, nil
}

// FindForumThread searches for an existing forum thread by PR URL in the content.
// Returns threadID, messageID if found. Uses retry logic for API calls.
func (c *Client) FindForumThread(ctx context.Context, forumID, prURL string) (threadID, messageID string, found bool) {
	// Get active threads in the forum with retry
	var threads *discordgo.ThreadsList
	err := retryableCtx(ctx, func() error {
		var err error
		threads, err = c.session.GuildThreadsActive(c.guildID)
		return err
	})
	if err != nil {
		slog.Warn("failed to fetch active threads after retries", "forum_id", forumID, "error", err)
		return "", "", false
	}

	for _, thread := range threads.Threads {
		if thread.ParentID != forumID {
			continue
		}
		messages, err := c.session.ChannelMessages(thread.ID, 1, "", "", "")
		if err != nil || len(messages) == 0 {
			continue
		}
		if strings.Contains(messages[0].Content, prURL) {
			slog.Debug("found existing forum thread",
				"forum_id", forumID,
				"thread_id", thread.ID,
				"pr_url", prURL)
			return thread.ID, messages[0].ID, true
		}
	}

	// Also check archived threads (recently archived) with retry
	var archivedThreads *discordgo.ThreadsList
	err = retryableCtx(ctx, func() error {
		var err error
		archivedThreads, err = c.session.ThreadsArchived(forumID, nil, 50)
		return err
	})
	if err != nil {
		slog.Warn("failed to fetch archived threads after retries", "forum_id", forumID, "error", err)
		return "", "", false
	}

	for _, thread := range archivedThreads.Threads {
		messages, err := c.session.ChannelMessages(thread.ID, 1, "", "", "")
		if err != nil || len(messages) == 0 {
			continue
		}
		if strings.Contains(messages[0].Content, prURL) {
			slog.Debug("found existing archived forum thread",
				"forum_id", forumID,
				"thread_id", thread.ID,
				"pr_url", prURL)
			return thread.ID, messages[0].ID, true
		}
	}

	return "", "", false
}

// FindChannelMessage searches for an existing message containing the PR URL.
// Returns messageID if found.
func (c *Client) FindChannelMessage(ctx context.Context, channelID, prURL string) (messageID string, found bool) {
	var messages []*discordgo.Message
	err := retryableCtx(ctx, func() error {
		var err error
		messages, err = c.session.ChannelMessages(channelID, 100, "", "", "")
		return err
	})
	if err != nil {
		slog.Warn("failed to fetch channel messages after retries", "channel_id", channelID, "error", err)
		return "", false
	}

	var botID string
	if c.session.State != nil && c.session.State.User != nil {
		botID = c.session.State.User.ID
	}

	slog.Info("searching for existing channel message",
		"channel_id", channelID,
		"pr_url", prURL,
		"bot_id", botID,
		"messages_fetched", len(messages))

	for i, msg := range messages {
		// Log first 5 messages for debugging
		if i < 5 {
			preview := msg.Content
			if len(preview) > 100 {
				preview = preview[:100] + "..."
			}
			slog.Debug("checking message",
				"index", i,
				"message_id", msg.ID,
				"author_id", msg.Author.ID,
				"is_bot_message", botID != "" && msg.Author.ID == botID,
				"content_preview", preview)
		}

		if botID != "" && msg.Author.ID != botID {
			continue
		}
		if strings.Contains(msg.Content, prURL) {
			slog.Info("found existing channel message",
				"channel_id", channelID,
				"message_id", msg.ID,
				"message_index", i,
				"pr_url", prURL)
			return msg.ID, true
		}
	}

	slog.Warn("did not find existing channel message",
		"channel_id", channelID,
		"pr_url", prURL,
		"bot_id", botID,
		"messages_checked", len(messages))

	return "", false
}

// FindDMForPR searches for an existing DM about a specific PR.
// Returns channelID, messageID if found.
func (c *Client) FindDMForPR(ctx context.Context, userID, prURL string) (channelID, messageID string, found bool) {
	var channel *discordgo.Channel
	err := retryableCtx(ctx, func() error {
		var err error
		channel, err = c.session.UserChannelCreate(userID)
		return err
	})
	if err != nil {
		slog.Warn("failed to get DM channel after retries", "user_id", userID, "error", err)
		return "", "", false
	}

	var botID string
	if c.session.State != nil && c.session.State.User != nil {
		botID = c.session.State.User.ID
	}

	var messages []*discordgo.Message
	err = retryableCtx(ctx, func() error {
		var err error
		messages, err = c.session.ChannelMessages(channel.ID, 50, "", "", "")
		return err
	})
	if err != nil {
		slog.Warn("failed to fetch DM messages after retries", "channel_id", channel.ID, "error", err)
		return "", "", false
	}

	for _, msg := range messages {
		if botID != "" && msg.Author.ID != botID {
			continue
		}
		if strings.Contains(msg.Content, prURL) {
			slog.Debug("found existing DM for PR",
				"user_id", userID,
				"channel_id", channel.ID,
				"message_id", msg.ID,
				"pr_url", prURL)
			return channel.ID, msg.ID, true
		}
	}

	return "", "", false
}

// MessageContent retrieves the content of a specific message.
func (c *Client) MessageContent(ctx context.Context, channelID, messageID string) (string, error) {
	var msg *discordgo.Message
	err := retryableCtx(ctx, func() error {
		var err error
		msg, err = c.session.ChannelMessage(channelID, messageID)
		return err
	})
	if err != nil {
		return "", fmt.Errorf("failed to fetch message: %w", err)
	}
	return msg.Content, nil
}
