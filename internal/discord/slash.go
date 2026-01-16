package discord

import (
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"

	"github.com/bwmarrin/discordgo"

	"github.com/codeGROOVE-dev/discordian/internal/format"
)

// SlashCommandHandler handles Discord slash commands.
type SlashCommandHandler struct {
	session      *discordgo.Session
	logger       *slog.Logger
	statusGetter StatusGetter
	reportGetter ReportGetter
	dashboardURL string
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

// BotStatus contains bot status information.
type BotStatus struct {
	Connected       bool
	ActivePRs       int
	PendingDMs      int
	ConnectedOrgs   []string
	UptimeSeconds   int64
	LastEventTime   string
	ConfiguredRepos []string
	WatchedChannels []string
}

// PRReport contains PR summary for a user.
type PRReport struct {
	IncomingPRs []PRSummary // PRs needing user's review
	OutgoingPRs []PRSummary // User's own PRs
	GeneratedAt string
}

// PRSummary contains summary info for a single PR.
type PRSummary struct {
	Repo      string
	Number    int
	Title     string
	Author    string
	State     string
	URL       string
	Action    string // What action is needed
	UpdatedAt string
	IsBlocked bool // Is this PR blocked/blocking
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
					Name:        "report",
					Description: "Get your personal PR report",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "dashboard",
					Description: "Open the web dashboard",
				},
				{
					Type:        discordgo.ApplicationCommandOptionSubCommand,
					Name:        "help",
					Description: "Show help information",
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
		h.respondError(s, i, "Please specify a subcommand: /goose status, /goose report, /goose dashboard, or /goose help")
		return
	}

	subcommand := data.Options[0].Name

	switch subcommand {
	case "status":
		h.handleStatusCommand(s, i)
	case "report":
		h.handleReportCommand(s, i)
	case "dashboard":
		h.handleDashboardCommand(s, i)
	case "help":
		h.handleHelpCommand(s, i)
	default:
		h.respondError(s, i, "Unknown subcommand")
	}
}

func (h *SlashCommandHandler) handleStatusCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	guildID := i.GuildID

	var status BotStatus
	if h.statusGetter != nil {
		status = h.statusGetter.Status(ctx, guildID)
	}

	connectionValue := "Disconnected"
	if status.Connected {
		connectionValue = "Connected"
	}

	embed := &discordgo.MessageEmbed{
		Title: "reviewGOOSE Status",
		Color: 0x00ff00, // Green
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:   "Connection",
				Value:  connectionValue,
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
		},
	}

	if len(status.ConnectedOrgs) > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Connected Organizations",
			Value:  strconv.Itoa(len(status.ConnectedOrgs)),
			Inline: true,
		})
	}

	if status.LastEventTime != "" {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Last Event",
			Value:  status.LastEventTime,
			Inline: true,
		})
	}

	if len(status.WatchedChannels) > 0 {
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:   "Watched Channels",
			Value:  strconv.Itoa(len(status.WatchedChannels)) + " total",
			Inline: true,
		})
	}

	h.respond(s, i, "", embed)
}

func (h *SlashCommandHandler) handleReportCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	// Acknowledge immediately since report generation may take time
	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseDeferredChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Flags: discordgo.MessageFlagsEphemeral,
		},
	})
	if err != nil {
		h.logger.Error("failed to defer response", "error", err)
		return
	}

	// Generate report asynchronously
	go h.generateAndSendReport(s, i)
}

func (h *SlashCommandHandler) generateAndSendReport(s *discordgo.Session, i *discordgo.InteractionCreate) {
	ctx := context.Background()
	guildID := i.GuildID
	userID := i.Member.User.ID

	if h.reportGetter == nil {
		h.editResponse(s, i, "Report generation is not configured.", nil)
		return
	}

	report, err := h.reportGetter.Report(ctx, guildID, userID)
	if err != nil {
		h.logger.Error("failed to generate report", "error", err, "user_id", userID)
		h.editResponse(s, i, "Failed to generate report. Please try again later.", nil)
		return
	}

	if report == nil || (len(report.IncomingPRs) == 0 && len(report.OutgoingPRs) == 0) {
		h.editResponse(s, i, "No PRs require your attention right now.", nil)
		return
	}

	embed := h.formatReportEmbed(report)
	h.editResponse(s, i, "", embed)
}

func (*SlashCommandHandler) formatReportEmbed(report *PRReport) *discordgo.MessageEmbed {
	embed := &discordgo.MessageEmbed{
		Title: "Your PR Report",
		Color: 0x0099ff,
	}

	// Incoming PRs section
	if len(report.IncomingPRs) > 0 {
		var b strings.Builder
		for i := range report.IncomingPRs {
			pr := &report.IncomingPRs[i]
			indicator := "◽"
			if pr.IsBlocked {
				indicator = "🔴"
			}
			b.WriteString(fmt.Sprintf("%s [%s#%d](%s) %s\n", indicator, pr.Repo, pr.Number, pr.URL, format.Truncate(pr.Title, 40)))
			if pr.Action != "" {
				b.WriteString(fmt.Sprintf("   ↳ %s\n", pr.Action))
			}
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("Incoming PRs (%d)", len(report.IncomingPRs)),
			Value: b.String(),
		})
	}

	// Outgoing PRs section
	if len(report.OutgoingPRs) > 0 {
		var b strings.Builder
		for i := range report.OutgoingPRs {
			pr := &report.OutgoingPRs[i]
			indicator := "◽"
			if pr.IsBlocked {
				indicator = "🟢"
			}
			b.WriteString(fmt.Sprintf("%s [%s#%d](%s) %s\n", indicator, pr.Repo, pr.Number, pr.URL, format.Truncate(pr.Title, 40)))
			if pr.Action != "" {
				b.WriteString(fmt.Sprintf("   ↳ %s\n", pr.Action))
			}
		}
		embed.Fields = append(embed.Fields, &discordgo.MessageEmbedField{
			Name:  fmt.Sprintf("Your PRs (%d)", len(report.OutgoingPRs)),
			Value: b.String(),
		})
	}

	embed.Footer = &discordgo.MessageEmbedFooter{
		Text: "Generated at " + report.GeneratedAt,
	}

	return embed
}

func (h *SlashCommandHandler) handleDashboardCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	userID := i.Member.User.ID

	dashboardLink := h.dashboardURL
	if userID != "" {
		dashboardLink = fmt.Sprintf("%s/?user=%s", h.dashboardURL, userID)
	}

	embed := &discordgo.MessageEmbed{
		Title:       "reviewGOOSE Dashboard",
		Description: fmt.Sprintf("View your PRs and configure settings on the web dashboard.\n\n[Open Dashboard](%s)", dashboardLink),
		Color:       0x0099ff,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name:  "Tip",
				Value: "Use `/goose report` for a quick PR summary right here in Discord.",
			},
		},
	}

	h.respond(s, i, "", embed)
}

func (h *SlashCommandHandler) handleHelpCommand(s *discordgo.Session, i *discordgo.InteractionCreate) {
	embed := &discordgo.MessageEmbed{
		Title:       "reviewGOOSE Help",
		Description: "I notify you about pull requests that need your attention.",
		Color:       0x0099ff,
		Fields: []*discordgo.MessageEmbedField{
			{
				Name: "How it works",
				Value: "When a PR needs your review, approval, or has changes for you to address, " +
					"you'll see a notification in the configured channel(s). If you don't respond " +
					"in time, you'll get a DM reminder.",
			},
			{
				Name: "Commands",
				Value: "`/goose status` - Show bot status\n" +
					"`/goose report` - Get your personal PR report\n" +
					"`/goose dashboard` - Open the web dashboard\n" +
					"`/goose help` - Show this help",
			},
			{
				Name: "Channel Types",
				Value: "**Forum channels**: Each PR gets its own thread (recommended)\n" +
					"**Text channels**: PR updates posted as messages",
			},
			{
				Name: "Configuration",
				Value: "Bot configuration is managed via `.codeGROOVE/discord.yaml` " +
					"in your GitHub organization's repository.",
			},
			{
				Name:  "Support",
				Value: "Visit [codegroove.dev/reviewgoose](https://codegroove.dev/reviewgoose/) for help.",
			},
		},
		Footer: &discordgo.MessageEmbedFooter{
			Text: "reviewGOOSE",
		},
	}

	h.respond(s, i, "", embed)
}

func (h *SlashCommandHandler) respond(
	s *discordgo.Session,
	i *discordgo.InteractionCreate,
	content string,
	embed *discordgo.MessageEmbed,
) {
	var embeds []*discordgo.MessageEmbed
	if embed != nil {
		embeds = []*discordgo.MessageEmbed{embed}
	}

	err := s.InteractionRespond(i.Interaction, &discordgo.InteractionResponse{
		Type: discordgo.InteractionResponseChannelMessageWithSource,
		Data: &discordgo.InteractionResponseData{
			Content: content,
			Embeds:  embeds,
			Flags:   discordgo.MessageFlagsEphemeral, // Only visible to the user
		},
	})
	if err != nil {
		h.logger.Error("failed to respond to interaction", "error", err)
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

	_, err := s.InteractionResponseEdit(i.Interaction, &discordgo.WebhookEdit{
		Content: &content,
		Embeds:  &embeds,
	})
	if err != nil {
		h.logger.Error("failed to edit response", "error", err)
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
