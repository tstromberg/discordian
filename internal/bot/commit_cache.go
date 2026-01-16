package bot

import (
	"sync"
	"time"
)

const (
	commitCacheEntryTTL = 10 * time.Minute // Keep entries for 10 minutes
	maxCacheEntries     = 1000             // Limit cache size
)

// commitPREntry maps a commit SHA to PR information.
type commitPREntry struct {
	addedAt  time.Time
	owner    string
	repo     string
	prNumber int
}

// CommitPRCache caches commit SHA → PR number mappings.
// This allows quick lookup when check events arrive with just a commit SHA,
// avoiding expensive GitHub API calls for recently-seen PRs.
type CommitPRCache struct {
	entries map[string][]commitPREntry // commitSHA -> list of PRs
	recent  map[string]int             // "owner/repo" -> most recent PR number
	mu      sync.RWMutex
}

// NewCommitPRCache creates a new commit→PR cache.
func NewCommitPRCache() *CommitPRCache {
	return &CommitPRCache{
		entries: make(map[string][]commitPREntry),
		recent:  make(map[string]int),
	}
}

// RecordPR records all commits for a PR.
func (c *CommitPRCache) RecordPR(owner, repo string, prNumber int, commits []string) {
	if len(commits) == 0 {
		return
	}

	c.mu.Lock()
	defer c.mu.Unlock()

	repoKey := owner + "/" + repo
	now := time.Now()

	// Update most recent PR for this repo
	if existing, ok := c.recent[repoKey]; !ok || prNumber > existing {
		c.recent[repoKey] = prNumber
	}

	// Record each commit
	for _, sha := range commits {
		if sha == "" {
			continue
		}

		entry := commitPREntry{
			owner:    owner,
			repo:     repo,
			prNumber: prNumber,
			addedAt:  now,
		}

		c.entries[sha] = append(c.entries[sha], entry)
	}

	// Cleanup old entries if cache is too large
	if len(c.entries) > maxCacheEntries {
		c.cleanup()
	}
}

// FindPRsForCommit returns PR numbers associated with a commit SHA.
func (c *CommitPRCache) FindPRsForCommit(owner, repo, commitSHA string) []int {
	if commitSHA == "" {
		return nil
	}

	c.mu.RLock()
	defer c.mu.RUnlock()

	entries, ok := c.entries[commitSHA]
	if !ok {
		return nil
	}

	var prNumbers []int
	now := time.Now()

	for _, entry := range entries {
		// Skip expired entries
		if now.Sub(entry.addedAt) > commitCacheEntryTTL {
			continue
		}

		// Only return PRs for the requested repo
		if entry.owner == owner && entry.repo == repo {
			prNumbers = append(prNumbers, entry.prNumber)
		}
	}

	return prNumbers
}

// MostRecentPR returns the most recent PR number seen for a repo.
func (c *CommitPRCache) MostRecentPR(owner, repo string) int {
	c.mu.RLock()
	defer c.mu.RUnlock()

	repoKey := owner + "/" + repo
	return c.recent[repoKey]
}

// cleanup removes expired entries (must be called with lock held).
func (c *CommitPRCache) cleanup() {
	now := time.Now()
	for sha, entries := range c.entries {
		var kept []commitPREntry
		for _, entry := range entries {
			if now.Sub(entry.addedAt) <= commitCacheEntryTTL {
				kept = append(kept, entry)
			}
		}

		if len(kept) == 0 {
			delete(c.entries, sha)
		} else {
			c.entries[sha] = kept
		}
	}
}
