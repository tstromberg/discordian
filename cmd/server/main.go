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
		shutdownCtx, shutdownCancel := context.WithTimeout(context.WithoutCancel(ctx), 5*time.Second)
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

// coordinatorManager manages bot coordinators for multiple GitHub orgs.
type coordinatorManager struct {
	cfg            config.ServerConfig
	githubManager  *github.Manager
	configManager  *config.Manager
	guildManager   *discord.GuildManager
	store          state.Store
	notifyMgr      *notify.Manager
	active         map[string]context.CancelFunc           // org -> cancel func
	failed         map[string]time.Time                    // org -> last failure time
	discordClients map[string]*discord.Client              // guildID -> client
	slashHandlers  map[string]*discord.SlashCommandHandler // guildID -> handler
	coordinators   map[string]*bot.Coordinator             // org -> coordinator
	startTime      time.Time
	lastEventTime  map[string]time.Time // org -> last event time
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
	cm.refreshAndStart(ctx)

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
			cm.refreshAndStart(ctx)

		case <-retryTicker.C:
			cm.retryFailed(ctx)

		case <-cleanupTicker.C:
			if err := store.Cleanup(ctx); err != nil {
				slog.Warn("state cleanup failed", "error", err)
			}
		}
	}
}

func (cm *coordinatorManager) refreshAndStart(ctx context.Context) {
	if err := cm.githubManager.RefreshInstallations(ctx); err != nil {
		if !errors.Is(err, context.Canceled) {
			slog.Error("failed to refresh GitHub installations", "error", err)
		}
		return
	}

	cm.startCoordinators(ctx)
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

	// Get GitHub token for sprinkler
	ghToken, err := ghClient.InstallationToken(ctx)
	if err != nil {
		slog.Error("failed to get GitHub token", "org", org, "error", err)
		cm.failed[org] = time.Now()
		return false
	}

	// Create Turn client
	turnClient := bot.NewTurnClient(cm.cfg.TurnURL, ghToken)

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

	go func(org string, coord *bot.Coordinator, sprinklerURL, ghToken string) {
		slog.Info("starting coordinator",
			"org", org,
			"guild_id", guildID,
			"sprinkler_url", sprinklerURL)

		// Create sprinkler client
		sprinklerClient, err := bot.NewSprinklerClient(bot.SprinklerConfig{
			ServerURL:    sprinklerURL,
			Token:        ghToken,
			Organization: org,
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
	}(org, coordinator, cm.cfg.SprinklerURL, ghToken)

	return true
}

func (cm *coordinatorManager) discordClientForGuild(_ context.Context, guildID string) (*discord.Client, error) {
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

	// Set status and report getters
	slashHandler.SetStatusGetter(cm)
	slashHandler.SetReportGetter(cm)

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

func (cm *coordinatorManager) retryFailed(ctx context.Context) {
	cm.mu.Lock()
	failedCount := len(cm.failed)
	cm.mu.Unlock()

	if failedCount == 0 {
		return
	}

	slog.Info("retrying failed coordinators", "count", failedCount)
	cm.refreshAndStart(ctx)
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

	return nil
}

// Status implements discord.StatusGetter interface.
func (cm *coordinatorManager) Status(ctx context.Context, guildID string) discord.BotStatus {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	status := discord.BotStatus{
		Connected:     len(cm.active) > 0,
		ConnectedOrgs: make([]string, 0, len(cm.active)),
		UptimeSeconds: int64(time.Since(cm.startTime).Seconds()),
	}

	// Find orgs for this guild
	var orgForGuild string
	for org := range cm.active {
		status.ConnectedOrgs = append(status.ConnectedOrgs, org)

		cfg, exists := cm.configManager.Config(org)
		if exists && cfg.Global.GuildID == guildID {
			orgForGuild = org
		}
	}

	// Get pending DMs count
	pendingDMs, err := cm.store.PendingDMs(ctx, time.Now().Add(24*time.Hour))
	if err == nil {
		status.PendingDMs = len(pendingDMs)
	}

	// Get last event time for this guild's org
	if orgForGuild != "" {
		if lastEvent, exists := cm.lastEventTime[orgForGuild]; exists {
			status.LastEventTime = lastEvent.Format(time.RFC3339)
		}

		// Get configured repos and channels
		if cfg, exists := cm.configManager.Config(orgForGuild); exists {
			for channelName, channelConfig := range cfg.Channels {
				status.WatchedChannels = append(status.WatchedChannels, channelName)
				status.ConfiguredRepos = append(status.ConfiguredRepos, channelConfig.Repos...)
			}
		}
	}

	return status
}

// Report implements discord.ReportGetter interface.
func (cm *coordinatorManager) Report(ctx context.Context, guildID, userID string) (*discord.PRReport, error) {
	cm.mu.Lock()
	defer cm.mu.Unlock()

	// Find the org for this guild
	var orgForGuild string
	for org := range cm.active {
		cfg, exists := cm.configManager.Config(org)
		if exists && cfg.Global.GuildID == guildID {
			orgForGuild = org
			break
		}
	}

	if orgForGuild == "" {
		return nil, fmt.Errorf("no org found for guild %s", guildID)
	}

	// Get the Discord client for username lookup
	discordClient, exists := cm.discordClients[guildID]
	if !exists {
		return nil, fmt.Errorf("no Discord client found for guild %s", guildID)
	}

	// Get config for this org
	cfg, exists := cm.configManager.Config(orgForGuild)
	if !exists {
		return nil, fmt.Errorf("no config found for org %s", orgForGuild)
	}

	// Try to find GitHub username from user mappings in config
	var githubUsername string
	for ghUser, discordID := range cfg.Users {
		if discordID == userID {
			githubUsername = ghUser
			break
		}
	}

	if githubUsername == "" {
		// Try username-based lookup (Discord username == GitHub username)
		// Get all guild members and find this user
		members, err := discordClient.Session().GuildMembers(guildID, "", 1000)
		if err == nil {
			for _, member := range members {
				if member.User.ID == userID {
					githubUsername = member.User.Username
					break
				}
			}
		}
	}

	if githubUsername == "" {
		return nil, errors.New("could not map Discord user to GitHub username")
	}

	// Get the PR searcher from the coordinator (we don't expose this currently)
	// For now, we'll need to search open PRs and filter for this user
	ghClient, exists := cm.githubManager.ClientForOrg(orgForGuild)
	if !exists {
		return nil, fmt.Errorf("no GitHub client for org %s", orgForGuild)
	}

	searcher := github.NewSearcher(cm.githubManager.AppClient(), slog.Default())
	openPRs, err := searcher.ListOpenPRs(ctx, orgForGuild, 24)
	if err != nil {
		return nil, fmt.Errorf("failed to list PRs: %w", err)
	}

	// Get GitHub token for Turn API calls
	ghToken, err := ghClient.InstallationToken(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to get GitHub token: %w", err)
	}

	turnClient := bot.NewTurnClient(cm.cfg.TurnURL, ghToken)

	var incomingPRs []discord.PRSummary
	var outgoingPRs []discord.PRSummary

	// Analyze each PR for this user
	for _, pr := range openPRs {
		prInfo, ok := bot.ParsePRURL(pr.URL)
		if !ok {
			continue
		}

		// Call Turn API to analyze this PR for this user
		checkResp, err := turnClient.Check(ctx, pr.URL, githubUsername, pr.UpdatedAt)
		if err != nil {
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

		// Determine if PR is blocked
		isBlocked := prState == format.StateTestsBroken ||
			prState == format.StateChanges ||
			prState == format.StateConflict

		// Check if user has an action on this PR
		action, hasAction := checkResp.Analysis.NextAction[githubUsername]
		isAuthor := checkResp.PullRequest.Author == githubUsername

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

	return &discord.PRReport{
		IncomingPRs: incomingPRs,
		OutgoingPRs: outgoingPRs,
		GeneratedAt: time.Now().Format(time.RFC3339),
	}, nil
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
		return cfg, errors.New("GITHUB_APP_ID is required")
	}
	if cfg.GitHubPrivateKey == "" {
		return cfg, errors.New("GITHUB_PRIVATE_KEY is required")
	}
	if cfg.DiscordBotToken == "" {
		return cfg, errors.New("DISCORD_BOT_TOKEN is required")
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
