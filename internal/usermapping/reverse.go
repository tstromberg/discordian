package usermapping

import (
	"context"
	"log/slog"
	"sync"
	"time"
)

// OrgConfig represents an org's config with user mappings.
type OrgConfig interface {
	UserMappings() map[string]string
}

// ReverseConfigLookup defines the interface for reverse config-based user lookup.
type ReverseConfigLookup interface {
	Config(org string) (OrgConfig, bool)
}

// ReverseMapper maps Discord user IDs to GitHub usernames.
type ReverseMapper struct {
	cache   map[string]reverseEntry // discordUserID -> GitHub username
	cacheMu sync.RWMutex
}

type reverseEntry struct {
	githubUsername string
	cachedAt       time.Time
}

// NewReverseMapper creates a new reverse mapper.
func NewReverseMapper() *ReverseMapper {
	return &ReverseMapper{
		cache: make(map[string]reverseEntry),
	}
}

// GitHubUsername returns the GitHub username for a Discord user ID by searching through orgs.
// Uses a cache with 24-hour TTL, populated from config.
func (m *ReverseMapper) GitHubUsername(ctx context.Context, discordUserID string, configLookup ReverseConfigLookup, orgs []string) string {
	// Check cache first
	m.cacheMu.RLock()
	if entry, ok := m.cache[discordUserID]; ok {
		if time.Since(entry.cachedAt) < cacheTTL {
			m.cacheMu.RUnlock()
			slog.Debug("using cached Discord-to-GitHub mapping",
				"discord_user_id", discordUserID,
				"github_username", entry.githubUsername)
			return entry.githubUsername
		}
		// Expired, will re-lookup below
	}
	m.cacheMu.RUnlock()

	slog.Info("searching for GitHub username for Discord user",
		"discord_user_id", discordUserID,
		"orgs_to_check", orgs,
		"org_count", len(orgs))

	// No cache hit, search through all org configs
	if configLookup == nil {
		slog.Warn("config lookup is nil, cannot resolve Discord user",
			"discord_user_id", discordUserID)
		return ""
	}

	// The config stores githubUsername → discordUserID mapping
	// We need to reverse it to find githubUsername for this discordUserID
	totalUsersChecked := 0
	for _, org := range orgs {
		orgCfg, exists := configLookup.Config(org)
		if !exists {
			slog.Debug("org config not found, skipping",
				"org", org,
				"discord_user_id", discordUserID)
			continue
		}

		// Search for matching Discord user ID
		users := orgCfg.UserMappings()
		totalUsersChecked += len(users)

		slog.Debug("checking org user mappings",
			"org", org,
			"user_count", len(users),
			"discord_user_id", discordUserID)

		for githubUsername, mappedDiscordID := range users {
			slog.Debug("comparing Discord user IDs",
				"org", org,
				"github_username", githubUsername,
				"config_discord_id", mappedDiscordID,
				"searching_for_discord_id", discordUserID,
				"match", mappedDiscordID == discordUserID)

			if mappedDiscordID == discordUserID {
				// Found a match, cache it
				m.cacheResult(discordUserID, githubUsername)
				slog.Info("mapped Discord user to GitHub via config",
					"discord_user_id", discordUserID,
					"github_username", githubUsername,
					"org", org)
				return githubUsername
			}
		}
	}

	// No mapping found
	slog.Warn("no GitHub username mapping found for Discord user - user must be added to config.yaml users section",
		"discord_user_id", discordUserID,
		"orgs_checked", orgs,
		"total_users_checked", totalUsersChecked)
	return ""
}

func (m *ReverseMapper) cacheResult(discordUserID, githubUsername string) {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	m.cache[discordUserID] = reverseEntry{
		githubUsername: githubUsername,
		cachedAt:       time.Now(),
	}
}

// ClearCache clears the reverse mapping cache.
func (m *ReverseMapper) ClearCache() {
	m.cacheMu.Lock()
	defer m.cacheMu.Unlock()
	m.cache = make(map[string]reverseEntry)
}

// ExportCache returns a copy of the cache for inspection (discordID -> githubUsername).
func (m *ReverseMapper) ExportCache() map[string]string {
	m.cacheMu.RLock()
	defer m.cacheMu.RUnlock()

	result := make(map[string]string, len(m.cache))
	for discordID, entry := range m.cache {
		result[discordID] = entry.githubUsername
	}
	return result
}
