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
	// MinHoursBetweenReports is the minimum time between daily reports (23 hours).
	MinHoursBetweenReports = 23

	// WindowStartHour is when the daily report window opens (6am local time).
	WindowStartHour = 6

	// WindowEndHour is when the daily report window closes (before noon).
	WindowEndHour = 12
)

// UserBlockingInfo contains information about PRs a user is blocking.
type UserBlockingInfo struct {
	GitHubUsername string
	DiscordUserID  string
	GuildID        string
	Timezone       string // IANA timezone name, defaults to "UTC"
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

	// Get user's timezone (default to UTC)
	tzName := userInfo.Timezone
	if tzName == "" {
		tzName = "UTC"
	}

	// Parse timezone
	loc, err := time.LoadLocation(tzName)
	if err != nil {
		s.logger.Debug("invalid timezone, using UTC",
			"user", userInfo.DiscordUserID,
			"timezone", tzName,
			"error", err)
		loc = time.UTC
	}

	// Check if it's within the 6am-11:30am window in user's timezone
	now := time.Now().In(loc)
	h := now.Hour()
	m := now.Minute()

	// Window is 6:00am - 11:29am (before 11:30am)
	outsideWindow := h < WindowStartHour ||
		h >= WindowEndHour ||
		(h == 11 && m >= 30)

	if outsideWindow {
		s.logger.Debug("skipping report - outside time window",
			"user", userInfo.DiscordUserID,
			"time", fmt.Sprintf("%02d:%02d", h, m),
			"window", "6:00am-11:29am")
		return false
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

// randomGreeting returns a friendly greeting based on the current time of day.
func randomGreeting() string {
	now := time.Now()
	h := now.Hour()

	var greetings []string

	switch {
	case h >= 6 && h < 12:
		greetings = []string{
			"Good morning!",
			"Coffee's ready!",
			"Happy morning!",
			"Hello sunshine!",
			"Morning vibes!",
		}
	case h >= 12 && h < 17:
		greetings = []string{
			"Hey there!",
			"Good afternoon!",
			"Time to create!",
			"Hey friend!",
			"Greetings!",
		}
	case h >= 17 && h < 22:
		greetings = []string{
			"Good evening!",
			"Hey there!",
			"Evening check-in!",
			"Still going strong!",
		}
	default:
		greetings = []string{
			"Burning the midnight oil?",
			"Night owl!",
			"Still at it!",
			"Late night vibes!",
		}
	}

	// Pick greeting based on time for variety
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
