// Package main provides the entry point for the discordian server.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/codeGROOVE-dev/gsm"
	"github.com/gorilla/mux"
	"golang.org/x/sync/errgroup"

	"github.com/codeGROOVE-dev/discordian/internal/bot"
	"github.com/codeGROOVE-dev/discordian/internal/config"
	"github.com/codeGROOVE-dev/discordian/internal/discord"
	"github.com/codeGROOVE-dev/discordian/internal/format"
	"github.com/codeGROOVE-dev/discordian/internal/github"
	"github.com/codeGROOVE-dev/discordian/internal/notify"
	"github.com/codeGROOVE-dev/discordian/internal/state"
	"github.com/codeGROOVE-dev/discordian/internal/usermapping"
)

const (
	serverReadTimeout  = 15 * time.Second
	serverWriteTimeout = 15 * time.Second
)

func main() {
	// Setup structured logging
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithCancel(context.Background())

	// Handle graceful shutdown
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		sig := <-sigCh
		slog.Warn("shutdown signal received", "signal", sig.String())
		cancel()
	}()

	// Run the server
	exitCode := run(ctx, cancel)
	cancel() // Ensure cleanup before exit
	os.Exit(exitCode)
}

func run(ctx context.Context, cancel context.CancelFunc) int {
	// Load server configuration from environment
	cfg, err := loadConfig(ctx)
	if err != nil {
		slog.Error("failed to load configuration", "error", err)
		return 1
	}

	slog.Info("configuration loaded",
		"has_github_app_id", cfg.GitHubAppID != "",
		"has_github_private_key", cfg.GitHubPrivateKey != "",
		"has_discord_bot_token", cfg.DiscordBotToken != "",
		"sprinkler_url", cfg.SprinklerURL)

	// Create GitHub App client
	ghApp, err := github.NewAppClient(cfg.GitHubAppID, cfg.GitHubPrivateKey, slog.Default())
	if err != nil {
		slog.Error("failed to create GitHub App client", "error", err)
		return 1
	}

	// Initialize GitHub installation manager
	githubManager, err := github.NewManager(ctx, ghApp, cfg.AllowPersonalAccounts)
	if err != nil {
		slog.Error("failed to initialize GitHub installation manager", "error", err)
		return 1
	}

	// Create state store using fido (CloudRun backend auto-detects environment)
	store, err := state.NewFidoStore(ctx)
	if err != nil {
		slog.Error("failed to create fido store", "error", err)
		return 1
	}
	defer func() {
		if err := store.Close(); err != nil {
			slog.Warn("failed to close store", "error", err)
		}
	}()

	// Create config manager
	configMgr := config.New()

	// Create notification manager
	notifyMgr := notify.New(store, slog.Default())

	// Create Discord guild manager
	guildManager := discord.NewGuildManager(slog.Default())

	// Create HTTP router
	router := mux.NewRouter()
	router.Use(securityHeadersMiddleware)

	// Health endpoints
	router.HandleFunc("/", healthHandler).Methods("GET")
	router.HandleFunc("/health", healthHandler).Methods("GET")
	router.HandleFunc("/healthz", makeHealthzHandler(githubManager)).Methods("GET")

	// Create HTTP server
	port := cfg.Port
	if port == "" {
		port = "9119"
	}

	server := &http.Server{
		Addr:         ":" + port,
		Handler:      router,
		ReadTimeout:  serverReadTimeout,
		WriteTimeout: serverWriteTimeout,
		IdleTimeout:  120 * time.Second,
	}

	// Start services
	eg, ctx := errgroup.WithContext(ctx)

	// HTTP server
	eg.Go(func() error {
		slog.Info("starting server", "port", port)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			return err
		}
		return nil
	})

	eg.Go(func() error {
		<-ctx.Done()
		slog.Info("shutting down HTTP server")
		// Fast shutdown for quick handoff during deployments (250ms)
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 250*time.Millisecond)
		defer shutdownCancel()
		return server.Shutdown(shutdownCtx)
	})

	// Start notification manager
	eg.Go(func() error {
		notifyMgr.Start(ctx)
		<-ctx.Done()
		notifyMgr.Stop()
		return nil
	})

	// Start coordinator manager for all GitHub installations
	eg.Go(func() error {
		return runCoordinators(ctx, cfg, githubManager, configMgr, guildManager, store, notifyMgr)
	})

	// Wait for all services
	if err := eg.Wait(); err != nil && !errors.Is(err, context.Canceled) && !errors.Is(err, http.ErrServerClosed) {
		slog.Error("server error", "error", err)
		cancel()
		return 1
	}

	slog.Info("shutdown complete")
	return 0
}

// configAdapter adapts config.Manager to usermapping.ReverseConfigLookup.
type configAdapter struct {
	mgr *config.Manager
}

func (ca *configAdapter) Config(org string) (usermapping.OrgConfig, bool) {
	cfg, ok := ca.mgr.Config(org)
	return cfg, ok
}

// coordinatorManager manages bot coordinators for multiple GitHub orgs.
type coordinatorManager struct {
	cfg            config.ServerConfig
	githubManager  *github.Manager
	configManager  *config.Manager
	guildManager   *discord.GuildManager
	store          state.Store
	notifyMgr      *notify.Manager
	reverseMapper  *usermapping.ReverseMapper              // Discord ID -> GitHub username
	active         map[string]context.CancelFunc           // org -> cancel func
	failed         map[string]time.Time                    // org -> last failure time
	discordClients map[string]*discord.Client              // guildID -> client
	slashHandlers  map[string]*discord.SlashCommandHandler // guildID -> handler
	coordinators   map[string]*bot.Coordinator             // org -> coordinator
	startTime      time.Time
	lastEventTime  map[string]time.Time // org -> last event time
	dmsSent        int64                // Total DMs sent since start
	dailyReports   int64                // Total daily reports sent since start
	channelMsgs    int64                // Total channel messages sent since start
	mu             sync.Mutex
}

func runCoordinators(
	ctx context.Context,
	cfg config.ServerConfig,
	githubManager *github.Manager,
	configManager *config.Manager,
	guildManager *discord.GuildManager,
	store state.Store,
	notifyMgr *notify.Manager,
) error {
	cm := &coordinatorManager{
		cfg:            cfg,
		githubManager:  githubManager,
		configManager:  configManager,
		guildManager:   guildManager,
		store:          store,
		notifyMgr:      notifyMgr,
		reverseMapper:  usermapping.NewReverseMapper(),
		active:         make(map[string]context.CancelFunc),
		failed:         make(map[string]time.Time),
		discordClients: make(map[string]*discord.Client),
		slashHandlers:  make(map[string]*discord.SlashCommandHandler),
		coordinators:   make(map[string]*bot.Coordinator),
		startTime:      time.Now(),
		lastEventTime:  make(map[string]time.Time),
	}

	// Initial discovery
	slog.Info("discovering GitHub installations")
	if err := cm.githubManager.RefreshInstallations(ctx); err != nil {
		slog.Error("failed to refresh GitHub installations", "error", err)
	} else {
		cm.startCoordinators(ctx)
	}

	// Periodic refresh every 5 minutes
	refreshTicker := time.NewTicker(5 * time.Minute)
	defer refreshTicker.Stop()

	// Retry failed coordinators every minute
	retryTicker := time.NewTicker(1 * time.Minute)
	defer retryTicker.Stop()

	// Cleanup old state hourly
	cleanupTicker := time.NewTicker(1 * time.Hour)
	defer cleanupTicker.Stop()

	for {
		select {
		case <-ctx.Done():
			return cm.shutdown()

		case <-refreshTicker.C:
			if err := cm.githubManager.RefreshInstallations(ctx); err != nil && !errors.Is(err, context.Canceled) {
				slog.Error("failed to refresh GitHub installations", "error", err)
			} else {
				cm.startCoordinators(ctx)
			}

		case <-retryTicker.C:
			cm.mu.Lock()
			failedCount := len(cm.failed)
			cm.mu.Unlock()
			if failedCount > 0 {
				slog.Info("retrying failed coordinators", "count", failedCount)
				if err := cm.githubManager.RefreshInstallations(ctx); err != nil && !errors.Is(err, context.Canceled) {
					slog.Error("failed to refresh GitHub installations", "error", err)
				} else {
					cm.startCoordinators(ctx)
				}
			}

		case <-cleanupTicker.C:
			if err := store.Cleanup(ctx); err != nil {
				slog.Warn("state cleanup failed", "error", err)
			}
		}
	}
}

func (cm *coordinatorManager) startCoordinators(ctx context.Context) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	orgs := cm.githubManager.AllOrgs()
	slog.Info("checking GitHub installations", "total_orgs", len(orgs))

	// Track current orgs
	currentOrgs := make(map[string]bool)
	for _, org := range orgs {
		currentOrgs[org] = true
	}

	// Stop coordinators for removed orgs
	for org, cancel := range cm.active {
		if !currentOrgs[org] {
			slog.Info("stopping coordinator for removed org", "org", org)
			cancel()
			delete(cm.active, org)
		}
	}

	// Start coordinators for new orgs
	for _, org := range orgs {
		cm.startSingleCoordinator(ctx, org)
	}
}

func (cm *coordinatorManager) startSingleCoordinator(ctx context.Context, org string) bool {
	// Skip if already running
	if _, exists := cm.active[org]; exists {
		return true
	}

	// Get GitHub client for this org
	ghClient, exists := cm.githubManager.ClientForOrg(org)
	if !exists {
		slog.Warn("no GitHub client for org", "org", org)
		return false
	}

	// Set GitHub client in config manager
	cm.configManager.SetGitHubClient(org, ghClient.Client())

	// Try to load discord.yaml config
	if err := cm.configManager.LoadConfig(ctx, org); err != nil {
		slog.Debug("failed to load config for org (may not have discord.yaml)", "org", org, "error", err)
		return false
	}

	cfg, exists := cm.configManager.Config(org)
	if !exists || cfg.Global.GuildID == "" {
		slog.Debug("skipping org without Discord configuration", "org", org)
		return false
	}

	guildID := cfg.Global.GuildID

	// Get or create Discord client for this guild
	discordClient, err := cm.discordClientForGuild(ctx, guildID)
	if err != nil {
		slog.Error("failed to get Discord client",
			"org", org,
			"guild_id", guildID,
			"error", err)
		cm.failed[org] = time.Now()
		return false
	}

	// Register with notification manager
	cm.notifyMgr.RegisterGuild(guildID, discordClient)

	// Create user mapper
	userMapper := usermapping.New(org, cm.configManager, discordClient)

	// Create Turn client with token provider (will fetch fresh tokens automatically)
	turnClient := bot.NewTurnClient(cm.cfg.TurnURL, ghClient)

	// Create PR searcher for polling backup
	searcher := github.NewSearcher(cm.githubManager.AppClient(), slog.Default())

	// Create coordinator
	coordinator := bot.NewCoordinator(bot.CoordinatorConfig{
		Org:        org,
		Discord:    discordClient,
		Config:     cm.configManager,
		Store:      cm.store,
		Turn:       turnClient,
		UserMapper: userMapper,
		Searcher:   searcher,
		Logger:     slog.Default(),
	})

	// Start coordinator in goroutine
	orgCtx, cancel := context.WithCancel(ctx)
	cm.active[org] = cancel
	cm.coordinators[org] = coordinator
	delete(cm.failed, org)

	go func(org string, coord *bot.Coordinator, sprinklerURL string, tokenProvider bot.TokenProvider) {
		slog.Info("starting coordinator",
			"org", org,
			"guild_id", guildID,
			"sprinkler_url", sprinklerURL)

		// Create sprinkler client with token provider (will fetch fresh tokens automatically)
		sprinklerClient, err := bot.NewSprinklerClient(bot.SprinklerConfig{
			ServerURL:     sprinklerURL,
			TokenProvider: tokenProvider,
			Organization:  org,
			OnEvent: func(event bot.SprinklerEvent) {
				coord.ProcessEvent(orgCtx, event)
				// Track last event time
				cm.mu.Lock()
				cm.lastEventTime[org] = time.Now()
				cm.mu.Unlock()
			},
			OnConnect: func() {
				slog.Info("connected to sprinkler", "org", org)
			},
			OnDisconnect: func(err error) {
				slog.Warn("disconnected from sprinkler", "org", org, "error", err)
			},
			Logger: slog.Default(),
		})
		if err != nil {
			slog.Error("failed to create sprinkler client", "org", org, "error", err)
			cm.handleCoordinatorExit(org, err)
			return
		}

		// Start polling ticker for backup reconciliation
		pollTicker := time.NewTicker(5 * time.Minute)
		defer pollTicker.Stop()

		// Start cleanup ticker for lock garbage collection
		cleanupTicker := time.NewTicker(10 * time.Minute)
		defer cleanupTicker.Stop()

		// Run initial reconciliation on startup
		coord.PollAndReconcile(orgCtx)

		// Start sprinkler in goroutine
		sprinklerDone := make(chan error, 1)
		go func() {
			sprinklerDone <- sprinklerClient.Start(orgCtx)
		}()

		// Main loop: handle polling, cleanup, and sprinkler exit
		for {
			select {
			case <-orgCtx.Done():
				return
			case <-pollTicker.C:
				coord.PollAndReconcile(orgCtx)
			case <-cleanupTicker.C:
				coord.CleanupLocks()
			case err := <-sprinklerDone:
				cm.handleCoordinatorExit(org, err)
				return
			}
		}
	}(org, coordinator, cm.cfg.SprinklerURL, ghClient)

	return true
}

func (cm *coordinatorManager) discordClientForGuild(_ context.Context, guildID string) (*discord.Client, error) {
	// Check if client already exists (caller must hold cm.mu lock)
	if client, exists := cm.discordClients[guildID]; exists {
		return client, nil
	}

	client, err := discord.New(cm.cfg.DiscordBotToken)
	if err != nil {
		return nil, fmt.Errorf("create Discord client: %w", err)
	}

	client.SetGuildID(guildID)

	if err := client.Open(); err != nil {
		return nil, fmt.Errorf("open Discord connection: %w", err)
	}

	// Register with guild manager
	cm.guildManager.RegisterClient(guildID, client)

	// Set up slash command handler
	slashHandler := discord.NewSlashCommandHandler(client.Session(), slog.Default())
	slashHandler.SetupHandler()

	// Set status, report, usermap, and channel map getters
	slashHandler.SetStatusGetter(cm)
	slashHandler.SetReportGetter(cm)
	slashHandler.SetUserMapGetter(cm)
	slashHandler.SetChannelMapGetter(cm)

	// Register slash commands with Discord
	if err := slashHandler.RegisterCommands(guildID); err != nil {
		slog.Warn("failed to register slash commands",
			"guild_id", guildID,
			"error", err)
		// Don't fail - continue without slash commands
	}

	cm.discordClients[guildID] = client
	cm.slashHandlers[guildID] = slashHandler
	slog.Info("created Discord client for guild", "guild_id", guildID)

	return client, nil
}

func (cm *coordinatorManager) handleCoordinatorExit(org string, err error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	if err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("coordinator exited with error", "org", org, "error", err)
		cm.failed[org] = time.Now()
	} else {
		slog.Info("coordinator stopped", "org", org)
	}

	delete(cm.active, org)
	delete(cm.coordinators, org)
}

func (cm *coordinatorManager) shutdown() error {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	slog.Info("stopping all coordinators", "count", len(cm.active))

	for org, cancel := range cm.active {
		slog.Info("stopping coordinator", "org", org)
		cancel()
	}

	// Close Discord clients
	for guildID, client := range cm.discordClients {
		if err := client.Close(); err != nil {
			slog.Warn("failed to close Discord client", "guild_id", guildID, "error", err)
		}
	}

	slog.Info("all coordinators stopped")
	return nil
}

// Status implements discord.StatusGetter interface.
func (cm *coordinatorManager) Status(ctx context.Context, guildID string) discord.BotStatus {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	status := discord.BotStatus{
		Connected:            len(cm.active) > 0,
		ConnectedOrgs:        make([]string, 0, len(cm.active)),
		UptimeSeconds:        int64(time.Since(cm.startTime).Seconds()),
		SprinklerConnections: len(cm.active), // Each active org has a sprinkler connection
		DMsSent:              cm.dmsSent,
		DailyReportsSent:     cm.dailyReports,
		ChannelMessagesSent:  cm.channelMsgs,
	}

	// Find all orgs for this guild (a guild may monitor multiple orgs)
	var orgsForGuild []string
	for org := range cm.active {
		status.ConnectedOrgs = append(status.ConnectedOrgs, org)

		cfg, exists := cm.configManager.Config(org)
		if exists && cfg.Global.GuildID == guildID {
			orgsForGuild = append(orgsForGuild, org)
		}
	}

	// Count cached users from both forward and reverse mappers
	if cm.reverseMapper != nil {
		status.UsersCached = len(cm.reverseMapper.ExportCache())
	}
	for _, org := range orgsForGuild {
		if coord, exists := cm.coordinators[org]; exists {
			status.UsersCached += len(coord.ExportUserMapperCache())
		}
	}

	// Get pending DMs count
	pendingDMs, err := cm.store.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err == nil {
		status.PendingDMs = len(pendingDMs)
	}

	// Aggregate data from all orgs for this guild
	var lastEventTime time.Time
	for _, org := range orgsForGuild {
		// Track most recent event across all orgs
		if lastEvent, exists := cm.lastEventTime[org]; exists {
			if lastEvent.After(lastEventTime) {
				lastEventTime = lastEvent
			}
		}

		// Collect channels and repos from all orgs
		if cfg, exists := cm.configManager.Config(org); exists {
			for channelName, channelConfig := range cfg.Channels {
				status.WatchedChannels = append(status.WatchedChannels, channelName)
				status.ConfiguredRepos = append(status.ConfiguredRepos, channelConfig.Repos...)
			}
		}
	}

	if !lastEventTime.IsZero() {
		status.LastEventTime = lastEventTime.Format(time.RFC3339)
	}

	return status
}

// Report implements discord.ReportGetter interface.
func (cm *coordinatorManager) Report(ctx context.Context, guildID, userID string) (*discord.PRReport, error) {
	slog.Info("report requested",
		"guild_id", guildID,
		"user_id", userID)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Find all orgs for this guild (a guild may monitor multiple orgs)
	var orgsForGuild []string
	var allOrgs []string
	for org := range cm.active {
		allOrgs = append(allOrgs, org)
		cfg, exists := cm.configManager.Config(org)
		if exists && cfg.Global.GuildID == guildID {
			orgsForGuild = append(orgsForGuild, org)
		}
	}

	slog.Info("found orgs for guild",
		"guild_id", guildID,
		"user_id", userID,
		"orgs_for_guild", orgsForGuild,
		"all_active_orgs", allOrgs)

	if len(orgsForGuild) == 0 {
		slog.Error("no org found for guild - report generation failed",
			"guild_id", guildID,
			"user_id", userID,
			"active_orgs", allOrgs,
			"active_org_count", len(cm.active))
		return nil, fmt.Errorf("no org found for guild %s", guildID)
	}

	slog.Info("looking up GitHub username for Discord user",
		"guild_id", guildID,
		"user_id", userID,
		"orgs", orgsForGuild,
		"org_count", len(orgsForGuild))

	// First try forward mapper caches (these contain discovered/cached mappings)
	var githubUsername string
	for _, org := range orgsForGuild {
		coord, exists := cm.coordinators[org]
		if !exists {
			continue
		}
		forwardCache := coord.ExportUserMapperCache()
		slog.Debug("checking forward mapper cache for reverse lookup",
			"org", org,
			"discord_user_id", userID,
			"cache_size", len(forwardCache))
		for ghUser, discordID := range forwardCache {
			if discordID == userID {
				githubUsername = ghUser
				slog.Info("found GitHub username via forward mapper cache",
					"discord_user_id", userID,
					"github_username", githubUsername,
					"org", org)
				break
			}
		}
		if githubUsername != "" {
			break
		}
	}

	// If not found in forward caches, try reverse mapper (checks config only)
	if githubUsername == "" {
		slog.Debug("GitHub username not found in forward mapper caches, trying reverse mapper",
			"discord_user_id", userID)
		githubUsername = cm.reverseMapper.GitHubUsername(ctx, userID, &configAdapter{cm.configManager}, orgsForGuild)
	}

	if githubUsername == "" {
		slog.Warn("no GitHub username mapping found for Discord user - add to config.yaml users section",
			"guild_id", guildID,
			"user_id", userID,
			"orgs_checked", orgsForGuild)
		return nil, errors.New("no GitHub username mapping found for Discord user")
	}

	slog.Info("mapped Discord user to GitHub username",
		"guild_id", guildID,
		"user_id", userID,
		"github_username", githubUsername,
		"orgs", orgsForGuild)

	// Aggregate PRs from all orgs for this guild
	var incomingPRs []discord.PRSummary
	var outgoingPRs []discord.PRSummary

	searcher := github.NewSearcher(cm.githubManager.AppClient(), slog.Default())

	for _, org := range orgsForGuild {
		slog.Info("searching PRs for org",
			"org", org,
			"github_username", githubUsername,
			"guild_id", guildID)

		// Get GitHub client for this org
		client, exists := cm.githubManager.ClientForOrg(org)
		if !exists {
			slog.Warn("no GitHub client for org, skipping",
				"org", org,
				"github_username", githubUsername,
				"guild_id", guildID)
			continue
		}

		// Create Turn client for this org
		turn := bot.NewTurnClient(cm.cfg.TurnURL, client)

		// Search for PRs authored by this user (outgoing)
		slog.Info("searching authored PRs",
			"org", org,
			"github_username", githubUsername)
		authored, err := searcher.ListAuthoredPRs(ctx, org, githubUsername)
		if err != nil {
			slog.Warn("failed to search authored PRs", "org", org, "error", err)
		} else {
			slog.Info("found authored PRs",
				"github_username", githubUsername,
				"org", org,
				"count", len(authored))

			for _, pr := range authored {
				summary := analyzePRForReport(ctx, pr, githubUsername, turn)
				if summary != nil {
					outgoingPRs = append(outgoingPRs, *summary)
				}
			}
		}

		// Search for PRs where user is requested to review (incoming)
		slog.Info("searching review-requested PRs",
			"org", org,
			"github_username", githubUsername)
		review, err := searcher.ListReviewRequestedPRs(ctx, org, githubUsername)
		if err != nil {
			slog.Warn("failed to search review-requested PRs", "org", org, "error", err)
		} else {
			slog.Info("found review-requested PRs",
				"github_username", githubUsername,
				"org", org,
				"count", len(review))

			for _, pr := range review {
				summary := analyzePRForReport(ctx, pr, githubUsername, turn)
				if summary != nil {
					incomingPRs = append(incomingPRs, *summary)
				}
			}
		}
	}

	slog.Info("report generation complete",
		"github_username", githubUsername,
		"guild_id", guildID,
		"user_id", userID,
		"incoming_prs", len(incomingPRs),
		"outgoing_prs", len(outgoingPRs))

	return &discord.PRReport{
		IncomingPRs: incomingPRs,
		OutgoingPRs: outgoingPRs,
		GeneratedAt: time.Now().Format(time.RFC3339),
	}, nil
}

// UserMappings implements discord.UserMapGetter interface.
func (cm *coordinatorManager) UserMappings(ctx context.Context, guildID string) (*discord.UserMappings, error) {
	slog.Info("user mappings requested",
		"guild_id", guildID)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Find all orgs for this guild
	var orgsForGuild []string
	for org := range cm.active {
		cfg, exists := cm.configManager.Config(org)
		if exists && cfg.Global.GuildID == guildID {
			orgsForGuild = append(orgsForGuild, org)
		}
	}

	if len(orgsForGuild) == 0 {
		slog.Warn("no org found for guild when retrieving user mappings",
			"guild_id", guildID)
		return &discord.UserMappings{}, nil
	}

	slog.Info("found orgs for guild when retrieving user mappings",
		"guild_id", guildID,
		"orgs", orgsForGuild)

	var configMappings []discord.UserMapping
	var discoveredMappings []discord.UserMapping
	seen := make(map[string]bool) // Track seen GitHub usernames to avoid duplicates

	// Collect config mappings from each org
	for _, org := range orgsForGuild {
		cfg, exists := cm.configManager.Config(org)
		if !exists {
			continue
		}

		// Get the users map from config (githubUsername -> discordID)
		users := cfg.UserMappings()
		slog.Debug("collecting config user mappings",
			"org", org,
			"user_count", len(users))
		for githubUsername, discordID := range users {
			slog.Debug("found config user mapping",
				"org", org,
				"github_username", githubUsername,
				"discord_id", discordID)
			key := fmt.Sprintf("%s:%s", org, githubUsername)
			if seen[key] {
				continue
			}
			seen[key] = true

			configMappings = append(configMappings, discord.UserMapping{
				GitHubUsername: githubUsername,
				DiscordUserID:  discordID,
				Source:         "config",
				Org:            org,
			})
		}
	}

	// Collect cached mappings from reverse mapper (Discord -> GitHub)
	reverseCache := cm.reverseMapper.ExportCache()
	for discordID, githubUsername := range reverseCache {
		// Check if this is already in config mappings
		found := false
		for i := range configMappings {
			if configMappings[i].GitHubUsername == githubUsername && configMappings[i].DiscordUserID == discordID {
				found = true
				break
			}
		}
		if !found {
			discoveredMappings = append(discoveredMappings, discord.UserMapping{
				GitHubUsername: githubUsername,
				DiscordUserID:  discordID,
				Source:         "cached",
				Org:            "", // Reverse cache doesn't track org
			})
		}
	}

	// Collect cached mappings from each org's forward mapper (GitHub -> Discord)
	for _, org := range orgsForGuild {
		coord, exists := cm.coordinators[org]
		if !exists {
			continue
		}

		// Get cached forward mappings
		forwardCache := coord.ExportUserMapperCache()
		slog.Debug("collecting forward mapper cache",
			"org", org,
			"cached_user_count", len(forwardCache))
		for githubUsername, discordID := range forwardCache {
			slog.Debug("found forward mapper cached mapping",
				"org", org,
				"github_username", githubUsername,
				"discord_id", discordID)
			// Check if already in config or discovered
			found := false
			for i := range configMappings {
				if configMappings[i].GitHubUsername == githubUsername && configMappings[i].DiscordUserID == discordID {
					found = true
					break
				}
			}
			if !found {
				for i := range discoveredMappings {
					if discoveredMappings[i].GitHubUsername == githubUsername && discoveredMappings[i].DiscordUserID == discordID {
						found = true
						break
					}
				}
			}
			if !found {
				discoveredMappings = append(discoveredMappings, discord.UserMapping{
					GitHubUsername: githubUsername,
					DiscordUserID:  discordID,
					Source:         "username_match",
					Org:            org,
				})
			}
		}
	}

	totalUsers := len(configMappings) + len(discoveredMappings)

	slog.Info("user mappings retrieved",
		"guild_id", guildID,
		"config_mappings", len(configMappings),
		"discovered_mappings", len(discoveredMappings),
		"total_users", totalUsers)

	return &discord.UserMappings{
		ConfigMappings:     configMappings,
		DiscoveredMappings: discoveredMappings,
		TotalUsers:         totalUsers,
	}, nil
}

// ChannelMappings implements discord.ChannelMapGetter interface.
func (cm *coordinatorManager) ChannelMappings(ctx context.Context, guildID string) (*discord.ChannelMappings, error) {
	slog.Info("channel mappings requested",
		"guild_id", guildID)

	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Find all orgs for this guild
	var orgsForGuild []string
	for org := range cm.active {
		cfg, exists := cm.configManager.Config(org)
		if exists && cfg.Global.GuildID == guildID {
			orgsForGuild = append(orgsForGuild, org)
		}
	}

	if len(orgsForGuild) == 0 {
		slog.Warn("no org found for guild when retrieving channel mappings",
			"guild_id", guildID)
		return &discord.ChannelMappings{}, nil
	}

	slog.Info("found orgs for guild when retrieving channel mappings",
		"guild_id", guildID,
		"orgs", orgsForGuild)

	var repoMappings []discord.RepoChannelMapping
	seenRepos := make(map[string]bool) // Track seen repos to avoid duplicates

	// Collect channel mappings from each org's config
	for _, org := range orgsForGuild {
		cfg, exists := cm.configManager.Config(org)
		if !exists {
			continue
		}

		// Build reverse map: repo -> channels
		repoChannels := make(map[string][]string)
		repoChannelTypes := make(map[string][]string)

		for channelName, channelConfig := range cfg.Channels {
			channelType := cm.configManager.ChannelType(org, channelName)
			for _, repo := range channelConfig.Repos {
				fullRepo := fmt.Sprintf("%s/%s", org, repo)
				repoChannels[fullRepo] = append(repoChannels[fullRepo], channelName)
				repoChannelTypes[fullRepo] = append(repoChannelTypes[fullRepo], channelType)
			}
		}

		// Convert to RepoChannelMapping structs
		for repo, channels := range repoChannels {
			if seenRepos[repo] {
				continue
			}
			seenRepos[repo] = true

			repoMappings = append(repoMappings, discord.RepoChannelMapping{
				Repo:         repo,
				Channels:     channels,
				ChannelTypes: repoChannelTypes[repo],
				Org:          org,
			})
		}
	}

	totalRepos := len(repoMappings)

	slog.Info("channel mappings retrieved",
		"guild_id", guildID,
		"total_repos", totalRepos)

	return &discord.ChannelMappings{
		RepoMappings: repoMappings,
		TotalRepos:   totalRepos,
	}, nil
}

// analyzePRForReport analyzes a single PR and returns a summary if relevant.
func analyzePRForReport(
	ctx context.Context,
	pr bot.PRSearchResult,
	githubUsername string,
	turn bot.TurnClient,
) *discord.PRSummary {
	info, ok := bot.ParsePRURL(pr.URL)
	if !ok {
		slog.Warn("failed to parse PR URL",
			"pr_url", pr.URL,
			"github_username", githubUsername)
		return nil
	}

	slog.Info("analyzing PR for user report",
		"pr_url", pr.URL,
		"github_username", githubUsername,
		"updated_at", pr.UpdatedAt.Format(time.RFC3339))

	// Call Turn API to analyze this PR for this user
	resp, err := turn.Check(ctx, pr.URL, githubUsername, pr.UpdatedAt)
	if err != nil {
		slog.Warn("Turn API failed for PR in report generation",
			"pr_url", pr.URL,
			"github_username", githubUsername,
			"error", err)
		return nil
	}

	slog.Info("Turn API response for PR",
		"pr_url", pr.URL,
		"github_username", githubUsername,
		"pr_author", resp.PullRequest.Author,
		"pr_title", resp.PullRequest.Title,
		"workflow_state", resp.Analysis.WorkflowState,
		"next_action_count", len(resp.Analysis.NextAction))

	// Determine PR state
	st := format.StateFromAnalysis(format.StateAnalysisParams{
		Merged:             resp.PullRequest.Merged,
		Closed:             resp.PullRequest.Closed,
		Draft:              resp.PullRequest.Draft,
		MergeConflict:      resp.Analysis.MergeConflict,
		Approved:           resp.Analysis.Approved,
		ChecksFailing:      resp.Analysis.Checks.Failing,
		ChecksPending:      resp.Analysis.Checks.Pending,
		ChecksWaiting:      resp.Analysis.Checks.Waiting,
		UnresolvedComments: resp.Analysis.UnresolvedComments,
		WorkflowState:      resp.Analysis.WorkflowState,
	})

	// Determine if PR is blocked
	blocked := st == format.StateTestsBroken ||
		st == format.StateChanges ||
		st == format.StateConflict

	// Check if user has an action on this PR
	action, hasAction := resp.Analysis.NextAction[githubUsername]

	summary := discord.PRSummary{
		Repo:      info.Repo,
		Number:    info.Number,
		Title:     resp.PullRequest.Title,
		Author:    resp.PullRequest.Author,
		State:     string(st),
		URL:       pr.URL,
		UpdatedAt: pr.UpdatedAt.Format(time.RFC3339),
		IsBlocked: blocked,
	}

	if hasAction {
		summary.Action = format.ActionLabel(action.Kind)
		slog.Info("user has action on PR",
			"pr_url", pr.URL,
			"github_username", githubUsername,
			"action_kind", action.Kind,
			"is_author", resp.PullRequest.Author == githubUsername)
	}

	return &summary
}

func loadConfig(ctx context.Context) (config.ServerConfig, error) {
	// Helper function to get secret values
	// Environment variables take precedence, then Secret Manager
	getSecret := func(name string) string {
		if v := os.Getenv(name); v != "" {
			slog.Debug("using environment variable", "name", name)
			return v
		}

		// Try Secret Manager using gsm library
		value, err := gsm.Fetch(ctx, name)
		if err != nil {
			slog.Debug("secret not found in Secret Manager", "name", name, "error", err)
			return ""
		}
		if value != "" {
			slog.Info("loaded secret from Secret Manager", "name", name)
		}
		return value
	}

	// Load GitHub private key from environment, file, or Secret Manager
	githubPrivateKey := getSecret("GITHUB_PRIVATE_KEY")
	if githubPrivateKey == "" {
		if keyPath := os.Getenv("GITHUB_PRIVATE_KEY_PATH"); keyPath != "" {
			data, err := os.ReadFile(keyPath)
			if err != nil {
				return config.ServerConfig{}, fmt.Errorf("read private key: %w", err)
			}
			githubPrivateKey = string(data)
		}
	}

	cfg := config.ServerConfig{
		GitHubAppID:           os.Getenv("GITHUB_APP_ID"),
		GitHubPrivateKey:      githubPrivateKey,
		SprinklerURL:          getEnv("SPRINKLER_URL", "wss://webhook.github.codegroove.app/ws"),
		TurnURL:               getEnv("TURN_URL", "https://turn.github.codegroove.app"),
		DiscordBotToken:       getSecret("DISCORD_BOT_TOKEN"),
		GCPProject:            os.Getenv("GCP_PROJECT"),
		Port:                  getEnv("PORT", "9119"),
		AllowPersonalAccounts: os.Getenv("ALLOW_PERSONAL_ACCOUNTS") == "true",
	}

	// Validate required fields
	if cfg.GitHubAppID == "" {
		return cfg, errors.New("GITHUB_APP_ID environment variable is required")
	}
	if cfg.GitHubPrivateKey == "" {
		return cfg, errors.New("GITHUB_PRIVATE_KEY environment variable is required")
	}
	if cfg.DiscordBotToken == "" {
		return cfg, errors.New("DISCORD_BOT_TOKEN environment variable is required")
	}

	return cfg, nil
}

func getEnv(key, defaultValue string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return defaultValue
}

func healthHandler(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain")
	w.WriteHeader(http.StatusOK)
	if _, err := w.Write([]byte("ok\n")); err != nil {
		slog.Debug("health write error", "error", err)
	}
}

func makeHealthzHandler(githubManager *github.Manager) http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) {
		orgs := githubManager.AllOrgs()
		w.Header().Set("Content-Type", "text/plain")
		w.WriteHeader(http.StatusOK)
		if _, err := fmt.Fprintf(w, "ok - %d orgs\n", len(orgs)); err != nil {
			slog.Debug("healthz write error", "error", err)
		}
	}
}

func securityHeadersMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("X-XSS-Protection", "1; mode=block")
		w.Header().Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains")
		w.Header().Set("Content-Security-Policy", "default-src 'none'")
		next.ServeHTTP(w, r)
	})
}
