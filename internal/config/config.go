// Package config manages server and repository configurations.
package config

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/google/go-github/v50/github"
	"gopkg.in/yaml.v3"
)

const (
	defaultReminderDMDelayMinutes = 65
	defaultConfigCacheTTL         = 20 * time.Minute
	maxRetryAttempts              = 5
	retryDelay                    = time.Second
	maxRetryDelay                 = 2 * time.Minute
)

// ServerConfig holds server configuration from environment variables.
type ServerConfig struct {
	GitHubAppID           string
	GitHubPrivateKey      string
	SprinklerURL          string
	TurnURL               string
	DiscordBotToken       string
	GCPProject            string
	Port                  string
	AllowPersonalAccounts bool
}

// DiscordConfig represents the discord.yaml configuration for a GitHub org.
type DiscordConfig struct {
	Channels map[string]ChannelConfig `yaml:"channels"`
	Users    map[string]string        `yaml:"users"` // GitHub username -> Discord ID
	Global   GlobalConfig             `yaml:"global"`
}

// UserMappings returns the user mappings (for reverse lookup interface).
func (c *DiscordConfig) UserMappings() map[string]string {
	return c.Users
}

// GlobalConfig holds global settings for the org.
type GlobalConfig struct {
	GuildID         string `yaml:"guild_id"`
	ReminderDMDelay int    `yaml:"reminder_dm_delay"`
	When            string `yaml:"when"` // When to post threads: "immediate" (default), "assigned", "blocked", "passing"
}

// ChannelConfig holds per-channel settings.
type ChannelConfig struct {
	Repos           []string `yaml:"repos"`
	ReminderDMDelay *int     `yaml:"reminder_dm_delay"`
	When            *string  `yaml:"when"` // Optional: when to post threads ("immediate", "assigned", "blocked", "passing")
	Type            string   `yaml:"type"` // "forum" or "text"
	Mute            bool     `yaml:"mute"`
}

type configCacheEntry struct {
	config    *DiscordConfig
	timestamp time.Time
}

type configCache struct {
	entries map[string]configCacheEntry
	ttl     time.Duration
	mu      sync.RWMutex
	hits    atomic.Int64
	misses  atomic.Int64
}

func (c *configCache) get(org string) (*DiscordConfig, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()

	entry, exists := c.entries[org]
	if !exists {
		c.misses.Add(1)
		return nil, false
	}

	if time.Since(entry.timestamp) > c.ttl {
		c.misses.Add(1)
		return nil, false
	}

	c.hits.Add(1)
	return entry.config, true
}

func (c *configCache) set(org string, cfg *DiscordConfig) {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.entries[org] = configCacheEntry{
		config:    cfg,
		timestamp: time.Now(),
	}
}

func (c *configCache) invalidate(org string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	delete(c.entries, org)
	slog.Info("invalidated config cache", "org", org)
}

func (c *configCache) stats() (hits, misses int64) {
	return c.hits.Load(), c.misses.Load()
}

// Manager manages repository configurations.
type Manager struct {
	configs map[string]*DiscordConfig
	clients map[string]any
	cache   *configCache
	mu      sync.RWMutex
}

// New creates a new config manager.
func New() *Manager {
	return &Manager{
		configs: make(map[string]*DiscordConfig),
		clients: make(map[string]any),
		cache: &configCache{
			entries: make(map[string]configCacheEntry),
			ttl:     defaultConfigCacheTTL,
		},
	}
}

// SetGitHubClient sets the GitHub client for a specific org.
func (m *Manager) SetGitHubClient(org string, client any) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.clients[org] = client
}

func createDefaultConfig() *DiscordConfig {
	return &DiscordConfig{
		Global: GlobalConfig{
			ReminderDMDelay: defaultReminderDMDelayMinutes,
		},
		Channels: make(map[string]ChannelConfig),
		Users:    make(map[string]string),
	}
}

// LoadConfig loads discord.yaml configuration for a GitHub org with retry logic.
func (m *Manager) LoadConfig(ctx context.Context, org string) error {
	if cfg, found := m.cache.get(org); found {
		hits, misses := m.cache.stats()
		slog.Debug("using cached config",
			"org", org,
			"cache_hits", hits,
			"cache_misses", misses)

		m.mu.Lock()
		m.configs[org] = cfg
		m.mu.Unlock()
		return nil
	}

	slog.Info("loading config", "org", org, "config_file", ".codeGROOVE/discord.yaml")

	m.mu.Lock()
	clientAny := m.clients[org]
	m.mu.Unlock()

	if clientAny == nil {
		return fmt.Errorf("github client not initialized for org: %s", org)
	}

	client, ok := clientAny.(*github.Client)
	if !ok {
		return fmt.Errorf("invalid github client type for org: %s", org)
	}

	cfg, err := m.fetchConfig(ctx, client, org)
	if err != nil {
		// Check if we have a previous config to fall back to
		m.mu.Lock()
		previousCfg, hasPrevious := m.configs[org]
		m.mu.Unlock()

		if hasPrevious {
			slog.Warn("failed to fetch config, keeping previous config",
				"org", org,
				"reason", err,
				"channels", len(previousCfg.Channels),
				"users", len(previousCfg.Users))
			return nil
		}

		slog.Warn("no previous config available, using default config",
			"org", org,
			"reason", err)
		cfg = createDefaultConfig()
	}

	m.mu.Lock()
	m.configs[org] = cfg
	m.mu.Unlock()

	m.cache.set(org, cfg)

	slog.Info("config loaded",
		"org", org,
		"channels", len(cfg.Channels),
		"users", len(cfg.Users),
		"guild_id", cfg.Global.GuildID)

	// Log detailed config at debug level
	for channelName, channelCfg := range cfg.Channels {
		slog.Debug("config channel",
			"org", org,
			"channel", channelName,
			"repos", channelCfg.Repos,
			"type", channelCfg.Type,
			"mute", channelCfg.Mute)
	}
	for ghUser, discordID := range cfg.Users {
		slog.Debug("config user mapping",
			"org", org,
			"github_user", ghUser,
			"discord_id", discordID)
	}

	return nil
}

func (*Manager) fetchConfig(ctx context.Context, client *github.Client, org string) (*DiscordConfig, error) {
	var content *github.RepositoryContent

	slog.Debug("fetching discord.yaml config from GitHub",
		"org", org,
		"repo", ".codeGROOVE",
		"file", "discord.yaml")

	err := retry.Do(
		func() error {
			var fetchErr error
			content, _, _, fetchErr = client.Repositories.GetContents(ctx, org, ".codeGROOVE", "discord.yaml", nil)
			return fetchErr
		},
		retry.Context(ctx),
		retry.Attempts(maxRetryAttempts),
		retry.Delay(retryDelay),
		retry.MaxDelay(maxRetryDelay),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(n uint, err error) {
			slog.Warn("GitHub API call failed, retrying",
				"org", org,
				"attempt", n+1,
				"max_attempts", maxRetryAttempts,
				"error", err)
		}),
		retry.RetryIf(func(err error) bool {
			// Don't retry on 404 (config not found)
			var ghErr *github.ErrorResponse
			if errors.As(err, &ghErr) && ghErr.Response.StatusCode == http.StatusNotFound {
				return false
			}
			// Don't retry on context cancellation
			return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
		}),
	)
	if err != nil {
		// Check if it's a 404 to return cleaner error
		var ghErr *github.ErrorResponse
		if errors.As(err, &ghErr) && ghErr.Response.StatusCode == http.StatusNotFound {
			slog.Debug("config file not found in GitHub repo", "org", org)
			return nil, errors.New("config not found")
		}
		return nil, fmt.Errorf("failed to fetch config: %w", err)
	}

	if content == nil || content.Content == nil {
		return nil, errors.New("config file empty")
	}

	configContent, err := content.GetContent()
	if err != nil {
		return nil, fmt.Errorf("failed to decode config: %w", err)
	}

	var cfg DiscordConfig
	if err := yaml.Unmarshal([]byte(configContent), &cfg); err != nil {
		return nil, fmt.Errorf("failed to parse YAML: %w", err)
	}

	return &cfg, nil
}

// Config returns the configuration for a GitHub org.
func (m *Manager) Config(org string) (*DiscordConfig, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	cfg, exists := m.configs[org]
	return cfg, exists
}

// ChannelsForRepo returns the Discord channels configured for a specific repo.
func (m *Manager) ChannelsForRepo(org, repo string) []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg, exists := m.configs[org]
	if !exists {
		return []string{strings.ToLower(repo)}
	}

	var channels []string
	var explicitlyConfigured bool

	for channelName, channelCfg := range cfg.Channels {
		normalizedName := strings.ToLower(channelName)

		for _, configRepo := range channelCfg.Repos {
			if configRepo == "*" || configRepo == repo {
				explicitlyConfigured = true
				if channelCfg.Mute {
					continue
				}
				channels = append(channels, normalizedName)
				break
			}
		}
	}

	// Auto-discover: add repo-named channel unless already included or muted
	autoChannel := strings.ToLower(repo)
	addAuto := true

	// Check if already included
	for _, existing := range channels {
		if existing == autoChannel {
			addAuto = false
			break
		}
	}

	// Check if auto-discovered channel is muted
	if addAuto {
		for yamlChannelName, channelCfg := range cfg.Channels {
			if strings.EqualFold(yamlChannelName, autoChannel) && channelCfg.Mute {
				addAuto = false
				break
			}
		}
	}

	if addAuto {
		channels = append(channels, autoChannel)
	}

	if len(channels) > 0 {
		slog.Debug("resolved channels",
			"org", org,
			"repo", repo,
			"channels", channels,
			"explicit", explicitlyConfigured)
	}

	return channels
}

// ChannelType returns the channel type ("forum" or "text").
func (m *Manager) ChannelType(org, channel string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg, exists := m.configs[org]
	if !exists {
		return "text"
	}

	if ch, ok := cfg.Channels[channel]; ok && ch.Type == "forum" {
		return "forum"
	}
	return "text"
}

// DiscordUserID returns the mapped Discord ID for a GitHub username.
func (m *Manager) DiscordUserID(org, githubUsername string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg, exists := m.configs[org]
	if !exists {
		return ""
	}
	return cfg.Users[githubUsername]
}

// ReminderDMDelay returns the DM delay in minutes for a channel.
func (m *Manager) ReminderDMDelay(org, channel string) int {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg, exists := m.configs[org]
	if !exists {
		return defaultReminderDMDelayMinutes
	}

	if channelCfg, ok := cfg.Channels[channel]; ok && channelCfg.ReminderDMDelay != nil {
		return *channelCfg.ReminderDMDelay
	}

	if cfg.Global.ReminderDMDelay > 0 {
		return cfg.Global.ReminderDMDelay
	}
	return defaultReminderDMDelayMinutes
}

// When returns the posting threshold for a channel.
// Returns "immediate" (default), "assigned", "blocked", or "passing".
func (m *Manager) When(org, channel string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg, exists := m.configs[org]
	if !exists {
		return "immediate" // Default
	}

	// Check for channel-specific override
	if channelCfg, ok := cfg.Channels[channel]; ok && channelCfg.When != nil {
		return *channelCfg.When
	}

	// Return global setting (or default if not set)
	if cfg.Global.When != "" {
		return cfg.Global.When
	}

	return "immediate" // Default
}

// GuildID returns the guild ID for an organization.
func (m *Manager) GuildID(org string) string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	cfg, exists := m.configs[org]
	if !exists {
		return ""
	}
	return cfg.Global.GuildID
}

// ReloadConfig reloads the configuration for an org.
func (m *Manager) ReloadConfig(ctx context.Context, org string) error {
	m.cache.invalidate(org)
	return m.LoadConfig(ctx, org)
}

// CacheStats returns cache statistics.
func (m *Manager) CacheStats() (hits, misses int64) {
	return m.cache.stats()
}
