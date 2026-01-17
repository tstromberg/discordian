// Package dailyreport provides functionality for generating and sending daily PR reports to users.
package dailyreport

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/discord"
	"github.com/codeGROOVE-dev/discordian/internal/format"
	"github.com/codeGROOVE-dev/discordian/internal/state"
)

const (
	// MinHoursBetweenReports is the minimum time between daily reports (20 hours).
	MinHoursBetweenReports = 20
)

// UserBlockingInfo contains information about PRs a user is blocking.
type UserBlockingInfo struct {
	GitHubUsername string
	DiscordUserID  string
	GuildID        string
	IncomingPRs    []discord.PRSummary
	OutgoingPRs    []discord.PRSummary
}

// StateStore handles persistence of report send times.
type StateStore interface {
	DailyReportInfo(ctx context.Context, userID string) (state.DailyReportInfo, bool)
	SaveDailyReportInfo(ctx context.Context, userID string, info state.DailyReportInfo) error
}

// DiscordDMSender sends DMs to users.
type DiscordDMSender interface {
	SendDM(ctx context.Context, userID, text string) (channelID, messageID string, err error)
}

// Sender handles sending daily reports to users.
type Sender struct {
	stateStore StateStore
	dmSenders  map[string]DiscordDMSender // guildID -> sender
	logger     *slog.Logger
}

// NewSender creates a new daily report sender.
func NewSender(stateStore StateStore, logger *slog.Logger) *Sender {
	if logger == nil {
		logger = slog.Default()
	}
	return &Sender{
		stateStore: stateStore,
		dmSenders:  make(map[string]DiscordDMSender),
		logger:     logger,
	}
}

// RegisterGuild registers a DM sender for a guild.
func (s *Sender) RegisterGuild(guildID string, sender DiscordDMSender) {
	s.dmSenders[guildID] = sender
}

// ShouldSendReport determines if a report should be sent to a user now.
// Note: Caller should check if user is active before calling this.
func (s *Sender) ShouldSendReport(ctx context.Context, userInfo UserBlockingInfo) bool {
	// Must have PRs to report
	if len(userInfo.IncomingPRs) == 0 && len(userInfo.OutgoingPRs) == 0 {
		return false
	}

	// Check when we last sent a report
	info, exists := s.stateStore.DailyReportInfo(ctx, userInfo.DiscordUserID)
	if exists {
		hoursSince := time.Since(info.LastSentAt).Hours()
		if hoursSince < MinHoursBetweenReports {
			s.logger.Debug("skipping report - sent too recently",
				"user", userInfo.DiscordUserID,
				"hours_since_last", hoursSince,
				"min_hours", MinHoursBetweenReports)
			return false
		}
	}

	return true
}

// SendReport sends a daily report to a user.
func (s *Sender) SendReport(ctx context.Context, userInfo UserBlockingInfo) error {
	sender, ok := s.dmSenders[userInfo.GuildID]
	if !ok {
		s.logger.Warn("no DM sender for guild",
			"guild_id", userInfo.GuildID,
			"user_id", userInfo.DiscordUserID)
		return nil
	}

	// Build the report message
	message := BuildReportMessage(userInfo.IncomingPRs, userInfo.OutgoingPRs)

	// Send the DM
	_, _, err := sender.SendDM(ctx, userInfo.DiscordUserID, message)
	if err != nil {
		return fmt.Errorf("failed to send DM: %w", err)
	}

	// Record that we sent the report
	if err := s.stateStore.SaveDailyReportInfo(ctx, userInfo.DiscordUserID, state.DailyReportInfo{
		LastSentAt: time.Now(),
		GuildID:    userInfo.GuildID,
	}); err != nil {
		s.logger.Warn("failed to record report send time",
			"user", userInfo.DiscordUserID,
			"error", err)
		// Don't fail - report was sent successfully
	}

	s.logger.Info("sent daily report",
		"user", userInfo.DiscordUserID,
		"github_user", userInfo.GitHubUsername,
		"incoming_count", len(userInfo.IncomingPRs),
		"outgoing_count", len(userInfo.OutgoingPRs))

	return nil
}

// randomGreeting returns a friendly greeting (timezone-agnostic).
func randomGreeting() string {
	greetings := []string{
		"👋 Hey there!",
		"🎉 Hello!",
		"✨ Greetings!",
		"🚀 Welcome back!",
		"🤝 Hey friend!",
		"👍 What's up!",
		"🎨 Ready to ship!",
		"💪 Let's do this!",
		"🔥 Time to review!",
		"⚡ PR time!",
		"🌟 Here we go!",
		"🎯 Focus mode!",
		"🧑‍💻 Code time!",
		"📝 Review time!",
		"🎊 Daily update!",
		"🇫🇷 Bonjour!",
		"🇪🇸 Hola!",
		"🇩🇪 Guten Tag!",
		"🇮🇹 Ciao!",
		"🇯🇵 こんにちは!",
	}

	// Pick greeting based on time for variety (using minutes to cycle through greetings)
	now := time.Now()
	i := (now.Hour()*60 + now.Minute()) % len(greetings)
	return greetings[i]
}

// BuildReportMessage creates a formatted message for a daily report.
func BuildReportMessage(incoming, outgoing []discord.PRSummary) string {
	var b strings.Builder
	fmt.Fprintf(&b, "**%s** Here is your daily report:\n\n", randomGreeting())

	if len(incoming) > 0 {
		fmt.Fprintf(&b, "**Incoming PRs** (%d)\n", len(incoming))
		for i := range incoming {
			pr := &incoming[i]
			indicator := "🟢"
			if pr.IsBlocked {
				indicator = "🔴"
			}
			title := format.Truncate(pr.Title, 40)
			fmt.Fprintf(&b, "%s [%s#%d](%s): %s", indicator, pr.Repo, pr.Number, pr.URL, title)
			if pr.Action != "" {
				fmt.Fprintf(&b, " — *%s*", pr.Action)
			}
			b.WriteByte('\n')
		}
		b.WriteByte('\n')
	}

	if len(outgoing) > 0 {
		fmt.Fprintf(&b, "**Your PRs** (%d)\n", len(outgoing))
		for i := range outgoing {
			pr := &outgoing[i]
			indicator := "🟢"
			if pr.IsBlocked {
				indicator = "🔴"
			}
			title := format.Truncate(pr.Title, 40)
			fmt.Fprintf(&b, "%s [%s#%d](%s): %s", indicator, pr.Repo, pr.Number, pr.URL, title)
			if pr.Action != "" {
				fmt.Fprintf(&b, " — *%s*", pr.Action)
			}
			b.WriteByte('\n')
		}
	}

	return b.String()
}
