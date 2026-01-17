//nolint:revive // Package defines slash commands and all related types (14 public types)
package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	"github.com/bwmarrin/discordgo"

	"github.com/codeGROOVE-dev/discordian/internal/format"
)

// SlashCommandHandler handles Discord slash commands.
type SlashCommandHandler struct {
	session           *discordgo.Session
	logger            *slog.Logger
	statusGetter      StatusGetter
	reportGetter      ReportGetter
	userMapGetter     UserMapGetter
	channelMapGetter  ChannelMapGetter
	dailyReportGetter DailyReportGetter
	dashboardURL      string
}

// StatusGetter provides bot status information.
type StatusGetter interface {
	// Status returns the current bot status for a guild.
	Status(ctx context.Context, guildID string) BotStatus
}

// ReportGetter provides PR report generation.
type ReportGetter interface {
	// Report generates a PR report for a user.
	Report(ctx context.Context, guildID, userID string) (*PRReport, error)
}

// UserMapGetter provides user mapping information.
type UserMapGetter interface {
	// UserMappings returns all user mappings for a guild.
	UserMappings(ctx context.Context, guildID string) (*UserMappings, error)
}

// ChannelMapGetter provides channel mapping information.
type ChannelMapGetter interface {
	// ChannelMappings returns all repo to channel mappings for a guild.
	ChannelMappings(ctx context.Context, guildID string) (*ChannelMappings, error)
}

// DailyReportGetter provides daily report generation and debugging.
type DailyReportGetter interface {
	// DailyReport generates and sends a daily report for a user, returning debug info.
	DailyReport(ctx context.Context, guildID, userID string, force bool) (*DailyReportDebug, error)
}

// DailyReportDebug contains debug information about daily report eligibility.
type DailyReportDebug struct {
	LastSentAt         time.Time
	NextEligibleAt     time.Time
	LastSeenActiveAt   time.Time
	Reason             string // Why report was/wasn't sent
	HoursSinceLastSent float64
	MinutesActive      float64
	IncomingPRCount    int
	OutgoingPRCount    int
	UserOnline         bool
	Eligible           bool
	ReportSent         bool
}

// ChannelMappings contains all channel mapping information.
type ChannelMappings struct {
	RepoMappings []RepoChannelMapping
	TotalRepos   int
}

// RepoChannelMapping represents repo to channels mapping.
type RepoChannelMapping struct {
	Repo         string
	Org          string
	Channels     []string
	ChannelTypes []string
}

// UserMappings contains all user mapping information.
type UserMappings struct {
	ConfigMappings     []UserMapping // Hardcoded in config
	DiscoveredMappings []UserMapping // Auto-discovered or cached
	TotalUsers         int
}

// UserMapping represents a single user mapping.
type UserMapping struct {
	GitHubUsername string
	DiscordUserID  string
	DiscordName    string // Optional: Discord display name if available
	Source         string // "config", "username_match", "cached"
	Org            string
}

// BotStatus contains bot status information.
type BotStatus struct {
	LastEventTime        string
	ConfiguredRepos      []string
	ConnectedOrgs        []string
	WatchedChannels      []string
	PendingDMs           int
	UptimeSeconds        int64
	ActivePRs            int
	SprinklerConnections int
	UsersCached          int
	DMsSent              int64
	DailyReportsSent     int64
	ChannelMessagesSent  int64
	Connected            bool
}

// PRReport contains PR summary for a user.
type PRReport struct {
	GeneratedAt string
	IncomingPRs []PRSummary
	OutgoingPRs []PRSummary
}

// PRSummary contains summary info for a single PR.
type PRSummary struct {
	Repo      string
	Title     string
	Author    string
	State     string
	URL       string
	Action    string
	UpdatedAt string
	Number    int
	IsBlocked bool
}

// NewSlashCommandHandler creates a new slash command handler.
func NewSlashCommandHandler(session *discordgo.Session, logger *slog.Logger) *SlashCommandHandler {
	if logger == nil {
		logger = slog.Default()
	}

	return &SlashCommandHandler{
		session:      session,
		logger:       logger,
		dashboardURL: "https://reviewgoose.dev",
	}
}

// SetStatusGetter sets the status provider.
func (h *SlashCommandHandler) SetStatusGetter(getter StatusGetter) {
	h.statusGetter = getter
}

// SetReportGetter sets the report provider.
func (h *SlashCommandHandler) SetReportGetter(getter ReportGetter) {
	h.reportGetter = getter
}

// SetUserMapGetter sets the user mapping provider.
func (h *SlashCommandHandler) SetUserMapGetter(getter UserMapGetter) {
	h.userMapGetter = getter
}

// SetChannelMapGetter sets the channel mapping provider.
func (h *SlashCommandHandler) SetChannelMapGetter(getter ChannelMapGetter) {
	h.channelMapGetter = getter
}

// SetDailyReportGetter sets the daily report provider.
func (h *SlashCommandHandler) SetDailyReportGetter(getter DailyReportGetter) {
	h.dailyReportGetter = getter
}

// SetDashboardURL sets the dashboard URL.
func (h *SlashCommandHandler) SetDashboardURL(url string) {
	h.dashboardURL = url
}

// RegisterCommands registers the slash commands with Discord.
func (h *SlashCommandHandler) RegisterCommands(guildID string) error {
	commands := []*discordgo.ApplicationCommand{
		{
			Name:        "goose",
			Description: "reviewGOOSE - GitHub PR notifications",
			Options: []*discordgo.ApplicationCommandOption{
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "status",
					Description: "Show bot status and statistics",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "dash",
					Description: "Get your PR report and dashboard links",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "report",
					Description: "Get your daily report with debug info",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "help",
					Description: "Show help information",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "users",
					Description: "Show GitHub to Discord user mappings",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "channels",
					Description: "Show repository to channel mappings",
				},
			},
		},
	}

	for _, cmd := range commands {
		_, err := h.session.ApplicationCommandCreate(h.session.State.User.ID, guildID, cmd)
		if err != nil {
			return fmt.Errorf("create command %s: %w", cmd.Name, err)
		}
		h.logger.Info("registered slash command",
			"command", cmd.Name,
			"guild_id", guildID)
	}

	return nil
}

// SetupHandler sets up the interaction handler.
func (h *SlashCommandHandler) SetupHandler() {
	h.session.AddHandler(h.handleInteraction)
}

func (h *SlashCommandHandler) handleInteraction(s *discordgo.Session, i *discordgo.InteractionCreate) {
	if i.Type != discordgo.InteractionApplicationCommand {
		return
	}

	data := i.ApplicationCommandData()

	if data.Name == "goose" {
		h.handleGooseCommand(s, i, data)
	}
}

func (h *SlashCommandHandler) handleGooseCommand(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	data discordgo.ApplicationCommandInteractionData,
) {
	if len(data.Options) == 0 {
		h.logger.Warn("goose command called without subcommand",
			"guild_id", i.GuildID,
			"user_id", i.Member.User.ID)
		h.respondError(s, i, "Please specify a subcommand: /goose status, /goose report, /goose dashboard, or /goose help")
		return
	}

	subcommand := data.Options[0].Name

	h.logger.Info("processing goose command",
		"guild_id", i.GuildID,
		"user_id", i.Member.User.ID,
		"subcommand", subcommand)

	switch subcommand {
	case "status":
		h.handleStatusCommand(s, i)
	case "dash":
		h.handleDashCommand(s, i)
	case "report":
		h.handleReportCommand(s, i)
	case "help":
		h.handleHelpCommand(s, i)
	case "users":
		h.handleUsersCommand(s, i)
	case "channels":
		h.handleChannelsCommand(s, i)
	default:
		h.respondError(s, i, "Unknown subcommand")
	}
}

func (h *SlashCommandHandler) handleStatusCommand(session *discordgo.Session, interaction *discordgo.InteractionCreate) {
	// Context is created here because this is a callback from discordgo library
	// which doesn't provide context in its handler signature
	ctx := context.Background()
	guildID := interaction.GuildID

	h.logger.Info("handling status command",
		"guild_id", guildID,
		"user_id", interaction.Member.User.ID)

	var status BotStatus
	if h.statusGetter != nil {
		status = h.statusGetter.Status(ctx, guildID)
	}

	h.logger.Info("returning status",
		"guild_id", guildID,
		"connected", status.Connected,
		"active_prs", status.ActivePRs,
		"pending_dms", status.PendingDMs,
		"connected_orgs", len(status.ConnectedOrgs))

	conn := "❌ Disconnected"
	if status.Connected {
		conn = "✅ Connected"
	}

	// Format uptime
	uptime := time.Duration(status.UptimeSeconds) * time.Second
	uptimeStr := formatDuration(uptime)

	// Format orgs list
	orgsStr := "None"
	if len(status.ConnectedOrgs) > 0 {
		orgsStr = strings.Join(status.ConnectedOrgs, ", ")
	}

	color := 0x57F287 // Discord green - healthy
	if !status.Connected {
		color = 0xED4245 // Discord red - disconnected
	}

	embed := &discordgo.MessageEmbed{
		Color: color,
		Author: &discordgo.MessageEmbedAuthor{
			Name: "reviewGOOSE Status",
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Status",
				Value:  conn,
				Inline: true,
			},
			{
				Name:   "Uptime",
				Value:  uptimeStr,
				Inline: true,
			},
			{
				Name:   "Active PRs",
				Value:  strconv.Itoa(status.ActivePRs),
				Inline: true,
			},
			{
				Name:   "Pending DMs",
				Value:  strconv.Itoa(status.PendingDMs),
				Inline: true,
			},
			{
				Name:   "Users",
				Value:  strconv.Itoa(status.UsersCached),
				Inline: true,
			},
			{
				Name:   "Repos",
				Value:  strconv.Itoa(len(status.ConfiguredRepos)),
				Inline: true,
			},
		},
	}

	// Add organizations as description for better readability
	if len(status.ConnectedOrgs) > 0 {
		embed.Description = fmt.Sprintf("**Organizations:** %s", orgsStr)
	}

	h.respond(session, interaction, embed)
}

// formatDuration formats a duration in a human-readable way.
func formatDuration(d time.Duration) string {
	days := int(d.Hours() / 24)
	hours := int(d.Hours()) % 24
	minutes := int(d.Minutes()) % 60

	if days > 0 {
		return fmt.Sprintf("%dd %dh %dm", days, hours, minutes)
	}
	if hours > 0 {
		return fmt.Sprintf("%dh %dm", hours, minutes)
	}
	return fmt.Sprintf("%dm", minutes)
}

func (h *SlashCommandHandler) handleDashCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.logger.Info("handling dash command",
		"guild_id", i.GuildID,
		"user_id", i.Member.User.ID,
		"interaction_id", i.ID)

	// Acknowledge immediately since report generation may take time
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		h.logger.Error("failed to defer response",
			"error", err,
			"guild_id", i.GuildID,
			"user_id", i.Member.User.ID,
			"interaction_id", i.ID,
			"interaction_token_length", len(i.Token))
		return
	}

	h.logger.Debug("deferred response for dash command",
		"guild_id", i.GuildID,
		"user_id", i.Member.User.ID)

	// Generate report and dashboard links asynchronously
	go h.generateAndSendDash(s, i)
}

func (h *SlashCommandHandler) generateAndSendDash(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Context is created here because this is a callback from discordgo library
	// which doesn't provide context in its handler signature
	ctx := context.Background()
	guildID := i.GuildID
	userID := i.Member.User.ID

	h.logger.Info("starting dashboard generation",
		"guild_id", guildID,
		"user_id", userID,
		"interaction_id", i.ID)

	// Generate report if available
	var report *PRReport
	if h.reportGetter != nil {
		h.logger.Info("calling report getter",
			"guild_id", guildID,
			"user_id", userID)

		var err error
		report, err = h.reportGetter.Report(ctx, guildID, userID)
		if err != nil {
			h.logger.Error("report generation failed",
				"error", err,
				"user_id", userID,
				"guild_id", guildID,
				"interaction_id", i.ID)
		} else {
			h.logger.Info("report generated successfully",
				"guild_id", guildID,
				"user_id", userID,
				"has_report", report != nil,
				"interaction_id", i.ID)
		}
	}

	// Get org info for dashboard links
	var orgLinks string
	if h.statusGetter != nil {
		status := h.statusGetter.Status(ctx, guildID)
		if len(status.ConnectedOrgs) > 0 {
			orgLinks = "\n\n**Organization Dashboards:**\n"
			var orgLinksSb435 strings.Builder
			for _, org := range status.ConnectedOrgs {
				orgLinksSb435.WriteString(fmt.Sprintf("• %s: [View Dashboard](%s/orgs/%s)\n", org, h.dashboardURL, org))
			}
			orgLinks += orgLinksSb435.String()
		}
	}

	// Build personal dashboard link
	dashboardLink := h.dashboardURL
	if userID != "" {
		dashboardLink = fmt.Sprintf("%s/?user=%s", h.dashboardURL, userID)
	}

	// Format the embed
	embed := h.formatDashboardEmbed(report, dashboardLink, orgLinks)

	h.logger.Info("sending dashboard to user",
		"user_id", userID,
		"guild_id", guildID,
		"interaction_id", i.ID)

	h.editResponse(s, i, "", embed)
}

func (*SlashCommandHandler) formatDashboardEmbed(report *PRReport, dashboardLink, orgLinks string) *discordgo.MessageEmbed {
	// Use Discord green if there are no PRs to review, yellow if there are
	color := 0x57F287 // Discord green - all clear
	if report != nil && len(report.IncomingPRs) > 0 {
		color = 0xFEE75C // Discord yellow - needs attention
	}

	embed := &discordgo.MessageEmbed{
		Color: color,
		Author: &discordgo.MessageEmbedAuthor{
			Name: "reviewGOOSE",
		},
	}

	// Add PR sections if report is available
	if report != nil {
		// Incoming PRs section
		if len(report.IncomingPRs) > 0 {
			var b strings.Builder
			for i := range report.IncomingPRs {
				pr := &report.IncomingPRs[i]
				b.WriteString(fmt.Sprintf("**[%s#%d](%s)** %s",
					pr.Repo, pr.Number, pr.URL, format.Truncate(pr.Title, 50)))
				if pr.Author != "" {
					b.WriteString(fmt.Sprintf(" • `%s`", pr.Author))
				}
				b.WriteString("\n")
			}
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:  fmt.Sprintf("📥 Reviewing (%d)", len(report.IncomingPRs)),
				Value: strings.TrimSpace(b.String()),
			})
		}

		// Outgoing PRs section
		if len(report.OutgoingPRs) > 0 {
			var b strings.Builder
			for i := range report.OutgoingPRs {
				pr := &report.OutgoingPRs[i]
				b.WriteString(fmt.Sprintf("**[%s#%d](%s)** %s\n",
					pr.Repo, pr.Number, pr.URL, format.Truncate(pr.Title, 50)))
			}
			embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
				Name:  fmt.Sprintf("📤 Your PRs (%d)", len(report.OutgoingPRs)),
				Value: strings.TrimSpace(b.String()),
			})
		}

		// No PRs message
		if len(report.IncomingPRs) == 0 && len(report.OutgoingPRs) == 0 {
			embed.Description = "✅ All caught up! No PRs need your attention."
		}
	}

	// Add dashboard links
	var links strings.Builder
	links.WriteString(fmt.Sprintf("[Personal](%s)", dashboardLink))
	if orgLinks != "" {
		// orgLinks already contains the org list with links
		links.WriteString(orgLinks)
	}

	if links.Len() > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  "🔗 Links",
			Value: links.String(),
		})
	}

	return embed
}

func (h *SlashCommandHandler) handleReportCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.logger.Info("handling report command",
		"guild_id", i.GuildID,
		"user_id", i.Member.User.ID,
		"interaction_id", i.ID)

	// Acknowledge immediately since report generation may take time
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		h.logger.Error("failed to defer response",
			"error", err,
			"guild_id", i.GuildID,
			"user_id", i.Member.User.ID,
			"interaction_id", i.ID)
		return
	}

	// Generate report and debug info asynchronously
	go h.generateAndSendReportWithDebug(s, i)
}

func (h *SlashCommandHandler) generateAndSendReportWithDebug(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	guildID := i.GuildID
	userID := i.Member.User.ID

	h.logger.Info("starting daily report generation",
		"guild_id", guildID,
		"user_id", userID,
		"interaction_id", i.ID)

	if h.dailyReportGetter == nil {
		h.editResponse(s, i, "❌ Daily reports are not configured", nil)
		return
	}

	// Force generation of daily report with debug info
	debug, err := h.dailyReportGetter.DailyReport(ctx, guildID, userID, true)
	if err != nil {
		h.logger.Error("daily report generation failed",
			"error", err,
			"user_id", userID,
			"guild_id", guildID,
			"interaction_id", i.ID)
		h.editResponse(s, i, fmt.Sprintf("❌ Failed to generate daily report: %s", err), nil)
		return
	}

	// Format the debug info
	embed := h.formatDailyReportEmbed(debug)

	h.logger.Info("sending daily report to user",
		"user_id", userID,
		"guild_id", guildID,
		"interaction_id", i.ID,
		"report_sent", debug.ReportSent)

	h.editResponse(s, i, "", embed)
}

func (*SlashCommandHandler) formatDailyReportEmbed(debug *DailyReportDebug) *discordgo.MessageEmbed {
	// Use green if report was sent, yellow if eligible but not sent, red if not eligible
	color := 0xED4245 // Discord red - not eligible
	if debug.ReportSent {
		color = 0x57F287 // Discord green - sent
	} else if debug.Eligible {
		color = 0xFEE75C // Discord yellow - eligible but not sent
	}

	embed := &discordgo.MessageEmbed{
		Title: "📊 Daily Report Status",
		Color: color,
	}

	// User Status
	onlineStatus := "🔴 Offline"
	if debug.UserOnline {
		onlineStatus = "🟢 Online"
	}

	// Combine appends
	embed.Fields = append(embed.Fields,
		&discordgo.MessageEmbedField{
			Name:   "User Status",
			Value:  onlineStatus,
			Inline: true,
		},
		&discordgo.MessageEmbedField{
			Name:   "PRs Found",
			Value:  fmt.Sprintf("📥 %d incoming\n📤 %d outgoing", debug.IncomingPRCount, debug.OutgoingPRCount),
			Inline: true,
		})

	// Last Sent
	lastSentText := "Never"
	if !debug.LastSentAt.IsZero() {
		lastSentText = fmt.Sprintf("<t:%d:R>", debug.LastSentAt.Unix())
	}
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Last Report Sent",
		Value:  lastSentText,
		Inline: true,
	})

	// Next Eligible
	nextEligibleText := "Now"
	if !debug.NextEligibleAt.IsZero() && time.Now().Before(debug.NextEligibleAt) {
		nextEligibleText = fmt.Sprintf("<t:%d:R>", debug.NextEligibleAt.Unix())
	}
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Next Eligible",
		Value:  nextEligibleText,
		Inline: true,
	})

	// Last Seen Active
	lastActiveText := "Unknown"
	if !debug.LastSeenActiveAt.IsZero() {
		lastActiveText = fmt.Sprintf("<t:%d:R>", debug.LastSeenActiveAt.Unix())
	}
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Last Seen Active",
		Value:  lastActiveText,
		Inline: true,
	})

	// Time Since Last Report
	hoursSinceText := "N/A"
	if debug.HoursSinceLastSent > 0 {
		hoursSinceText = fmt.Sprintf("%.1f hours", debug.HoursSinceLastSent)
	}
	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:   "Hours Since Last",
		Value:  hoursSinceText,
		Inline: true,
	})

	// Status Message
	statusIcon := "❌"
	statusText := "Not sent"
	if debug.ReportSent {
		statusIcon = "✅"
		statusText = "Report sent via DM"
	}

	embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
		Name:  "Status",
		Value: fmt.Sprintf("%s %s\n\n**Reason:** %s", statusIcon, statusText, debug.Reason),
	})

	return embed
}

func (h *SlashCommandHandler) handleHelpCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.logger.Info("handling help command",
		"guild_id", i.GuildID,
		"user_id", i.Member.User.ID)

	embed := &discordgo.MessageEmbed{
		Description: "Tracks pull requests and notifies you when action is needed.",
		Color:       0x5865F2, // Discord blurple
		Author: &discordgo.MessageEmbedAuthor{
			Name: "reviewGOOSE",
		},
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "Commands",
				Value: "**`/goose dash`** • View your PRs and dashboard\n" +
					"**`/goose report`** • Generate daily report with debug info\n" +
					"**`/goose status`** • Bot status and stats\n" +
					"**`/goose users`** • User mappings\n" +
					"**`/goose channels`** • Channel mappings",
			},
			{
				Name:  "Support",
				Value: "🔗 [codegroove.dev/reviewgoose](https://codegroove.dev/reviewgoose/)",
			},
		},
	}

	h.respond(s, i, embed)
}

func (h *SlashCommandHandler) handleUsersCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.logger.Info("handling users command",
		"guild_id", i.GuildID,
		"user_id", i.Member.User.ID)

	// Context is created here because this is a callback from discordgo library
	// which doesn't provide context in its handler signature
	ctx := context.Background()
	guildID := i.GuildID

	if h.userMapGetter == nil {
		h.respondError(s, i, "User mapping information is not available.")
		return
	}

	mappings, err := h.userMapGetter.UserMappings(ctx, guildID)
	if err != nil {
		h.logger.Error("failed to get user mappings",
			"error", err,
			"guild_id", guildID,
			"user_id", i.Member.User.ID)
		h.respondError(s, i, "Failed to retrieve user mappings.")
		return
	}

	embed := h.formatUserMappingsEmbed(mappings)
	h.respond(s, i, embed)
}

func (*SlashCommandHandler) formatUserMappingsEmbed(mappings *UserMappings) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Color: 0x5865F2, // Discord blurple
		Author: &discordgo.MessageEmbedAuthor{
			Name: "User Mappings",
		},
	}

	// Config mappings section
	if len(mappings.ConfigMappings) > 0 {
		var b strings.Builder
		for i := range mappings.ConfigMappings {
			m := &mappings.ConfigMappings[i]
			mention := fmt.Sprintf("<@%s>", m.DiscordUserID)
			b.WriteString(fmt.Sprintf("`%s` → %s", m.GitHubUsername, mention))
			if m.Org != "" {
				b.WriteString(fmt.Sprintf(" • `%s`", m.Org))
			}
			b.WriteString("\n")
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("⚙️ Config (%d)", len(mappings.ConfigMappings)),
			Value: b.String(),
		})
	}

	// Discovered mappings section
	if len(mappings.DiscoveredMappings) > 0 {
		var b strings.Builder
		for i := range mappings.DiscoveredMappings {
			m := &mappings.DiscoveredMappings[i]
			mention := fmt.Sprintf("<@%s>", m.DiscordUserID)
			b.WriteString(fmt.Sprintf("`%s` → %s", m.GitHubUsername, mention))
			if m.Org != "" {
				b.WriteString(fmt.Sprintf(" • `%s`", m.Org))
			}
			b.WriteString("\n")
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("🔍 Discovered (%d)", len(mappings.DiscoveredMappings)),
			Value: b.String(),
		})
	}

	// Summary
	if len(mappings.ConfigMappings) == 0 && len(mappings.DiscoveredMappings) == 0 {
		embed.Description = "No user mappings found."
	}

	return embed
}

func (h *SlashCommandHandler) handleChannelsCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	h.logger.Info("handling channels command",
		"guild_id", i.GuildID,
		"user_id", i.Member.User.ID)

	// Context is created here because this is a callback from discordgo library
	// which doesn't provide context in its handler signature
	ctx := context.Background()
	guildID := i.GuildID

	if h.channelMapGetter == nil {
		h.respondError(s, i, "Channel mapping information is not available.")
		return
	}

	mappings, err := h.channelMapGetter.ChannelMappings(ctx, guildID)
	if err != nil {
		h.logger.Error("failed to get channel mappings",
			"error", err,
			"guild_id", guildID,
			"user_id", i.Member.User.ID)
		h.respondError(s, i, "Failed to retrieve channel mappings.")
		return
	}

	embed := h.formatChannelMappingsEmbed(mappings)
	h.respond(s, i, embed)
}

func (*SlashCommandHandler) formatChannelMappingsEmbed(mappings *ChannelMappings) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Color: 0x5865F2, // Discord blurple
		Author: &discordgo.MessageEmbedAuthor{
			Name: "Channel Mappings",
		},
	}

	if len(mappings.RepoMappings) == 0 {
		embed.Description = "No channel mappings found."
		return embed
	}

	// Group by org for better readability
	orgRepos := make(map[string][]RepoChannelMapping)
	for i := range mappings.RepoMappings {
		mapping := &mappings.RepoMappings[i]
		orgRepos[mapping.Org] = append(orgRepos[mapping.Org], *mapping)
	}

	for org, repos := range orgRepos {
		var b strings.Builder
		for i := range repos {
			rm := &repos[i]
			// Format channels
			var channelStrs []string
			for _, ch := range rm.Channels {
				channelStrs = append(channelStrs, fmt.Sprintf("<#%s>", ch))
			}
			channelsStr := strings.Join(channelStrs, ", ")
			b.WriteString(fmt.Sprintf("`%s` → %s\n", rm.Repo, channelsStr))
		}

		fieldName := fmt.Sprintf("📁 %s (%d repos)", org, len(repos))
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  fieldName,
			Value: b.String(),
		})
	}

	return embed
}

func (h *SlashCommandHandler) respond(
	session *discordgo.Session,
	i *discordgo.InteractionCreate,
	embed *discordgo.MessageEmbed,
) {
	var embeds []*discordgo.MessageEmbed
	if embed != nil {
		embeds = []*discordgo.MessageEmbed{embed}
	}

	err := session.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Embeds: embeds,
			Flags:  discordgo.MessageFlagsEphemeral, // Only visible to the user
		},
	})
	if err != nil {
		h.logger.Error("failed to respond to interaction",
			"error", err,
			"guild_id", i.GuildID,
			"user_id", i.Member.User.ID,
			"interaction_id", i.ID,
			"interaction_token_length", len(i.Token))
	}
}

func (h *SlashCommandHandler) editResponse(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	content string,
	embed *discordgo.MessageEmbed,
) {
	var embeds []*discordgo.MessageEmbed
	if embed != nil {
		embeds = []*discordgo.MessageEmbed{embed}
	}

	h.logger.Info("editing interaction response",
		"guild_id", i.GuildID,
		"user_id", i.Member.User.ID,
		"interaction_id", i.ID,
		"has_content", content != "",
		"has_embed", embed != nil)

	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
		Embeds:  &embeds,
	})
	if err != nil {
		h.logger.Error("failed to edit response",
			"error", err,
			"guild_id", i.GuildID,
			"user_id", i.Member.User.ID,
			"interaction_id", i.ID)
	} else {
		h.logger.Info("successfully edited response",
			"guild_id", i.GuildID,
			"user_id", i.Member.User.ID,
			"interaction_id", i.ID)
	}
}

func (h *SlashCommandHandler) respondError(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	message string,
) {
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: fmt.Sprintf("Error: %s", message),
			Flags:   discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		h.logger.Error("failed to respond with error", "error", err)
	}
}

// RemoveCommands removes all registered commands for a guild.
func (h *SlashCommandHandler) RemoveCommands(guildID string) error {
	commands, err := h.session.ApplicationCommands(h.session.State.User.ID, guildID)
	if err != nil {
		return fmt.Errorf("list commands: %w", err)
	}

	for _, cmd := range commands {
		if err := h.session.ApplicationCommandDelete(h.session.State.User.ID, guildID, cmd.ID); err != nil {
			h.logger.Warn("failed to delete command",
				"command", cmd.Name,
				"error", err)
		}
	}

	return nil
}
