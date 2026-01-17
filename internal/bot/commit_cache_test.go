package bot

import (
	"fmt"
	"testing"
	"time"
)

func TestNewCommitPRCache(t *testing.T) {
	cache := NewCommitPRCache()

	if cache == nil {
		t.Fatal("NewCommitPRCache() returned nil")
	}

	if cache.entries == nil {
		t.Error("entries map should be initialized")
	}

	if cache.recent == nil {
		t.Error("recent map should be initialized")
	}
}

func TestCommitPRCache_RecordPR(t *testing.T) {
	t.Run("record single PR with commits", func(t *testing.T) {
		cache := NewCommitPRCache()
		commits := []string{"abc123", "def456", "ghi789"}

		cache.RecordPR("owner1", "repo1", 42, commits)

		// Verify commits were recorded
		prs := cache.FindPRsForCommit("owner1", "repo1", "abc123")
		if len(prs) != 1 {
			t.Fatalf("FindPRsForCommit returned %d PRs, want 1", len(prs))
		}
		if prs[0] != 42 {
			t.Errorf("PR number = %d, want 42", prs[0])
		}

		// Verify all commits were recorded
		for _, sha := range commits {
			prs := cache.FindPRsForCommit("owner1", "repo1", sha)
			if len(prs) != 1 || prs[0] != 42 {
				t.Errorf("Commit %s not properly recorded", sha)
			}
		}
	})

	t.Run("record empty commits list", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner1", "repo1", 42, []string{})

		// Should not record anything
		recent := cache.MostRecentPR("owner1", "repo1")
		if recent != 0 {
			t.Errorf("MostRecentPR = %d, want 0 (nothing recorded)", recent)
		}
	})

	t.Run("skip empty commit SHAs", func(t *testing.T) {
		cache := NewCommitPRCache()
		commits := []string{"abc123", "", "def456"}

		cache.RecordPR("owner1", "repo1", 42, commits)

		// Should have 2 commits recorded (empty one skipped)
		cache.mu.RLock()
		totalEntries := 0
		for _, entries := range cache.entries {
			totalEntries += len(entries)
		}
		cache.mu.RUnlock()

		if totalEntries != 2 {
			t.Errorf("Total cache entries = %d, want 2 (empty SHA skipped)", totalEntries)
		}
	})

	t.Run("update most recent PR", func(t *testing.T) {
		cache := NewCommitPRCache()

		cache.RecordPR("owner1", "repo1", 10, []string{"sha1"})
		if cache.MostRecentPR("owner1", "repo1") != 10 {
			t.Error("MostRecentPR should be 10")
		}

		cache.RecordPR("owner1", "repo1", 20, []string{"sha2"})
		if cache.MostRecentPR("owner1", "repo1") != 20 {
			t.Error("MostRecentPR should be updated to 20")
		}

		// Lower PR number shouldn't update
		cache.RecordPR("owner1", "repo1", 15, []string{"sha3"})
		if cache.MostRecentPR("owner1", "repo1") != 20 {
			t.Error("MostRecentPR should remain 20")
		}
	})

	t.Run("multiple PRs for same commit", func(t *testing.T) {
		cache := NewCommitPRCache()
		sha := "shared-commit"

		cache.RecordPR("owner1", "repo1", 10, []string{sha})
		cache.RecordPR("owner1", "repo1", 20, []string{sha})

		prs := cache.FindPRsForCommit("owner1", "repo1", sha)
		if len(prs) != 2 {
			t.Fatalf("FindPRsForCommit returned %d PRs, want 2", len(prs))
		}

		// Should contain both PR numbers
		prMap := make(map[int]bool)
		for _, pr := range prs {
			prMap[pr] = true
		}
		if !prMap[10] || !prMap[20] {
			t.Errorf("PRs should contain both 10 and 20, got %v", prs)
		}
	})
}

func TestCommitPRCache_FindPRsForCommit(t *testing.T) {
	t.Run("find existing commit", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner1", "repo1", 42, []string{"abc123"})

		prs := cache.FindPRsForCommit("owner1", "repo1", "abc123")
		if len(prs) != 1 {
			t.Fatalf("FindPRsForCommit returned %d PRs, want 1", len(prs))
		}
		if prs[0] != 42 {
			t.Errorf("PR number = %d, want 42", prs[0])
		}
	})

	t.Run("commit not found", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner1", "repo1", 42, []string{"abc123"})

		prs := cache.FindPRsForCommit("owner1", "repo1", "nonexistent")
		if prs != nil {
			t.Errorf("FindPRsForCommit returned %v, want nil for nonexistent commit", prs)
		}
	})

	t.Run("empty commit SHA", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner1", "repo1", 42, []string{"abc123"})

		prs := cache.FindPRsForCommit("owner1", "repo1", "")
		if prs != nil {
			t.Errorf("FindPRsForCommit with empty SHA returned %v, want nil", prs)
		}
	})

	t.Run("wrong repo", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner1", "repo1", 42, []string{"abc123"})

		// Same owner, different repo
		prs := cache.FindPRsForCommit("owner1", "repo2", "abc123")
		if len(prs) != 0 {
			t.Errorf("FindPRsForCommit for wrong repo returned %d PRs, want 0", len(prs))
		}

		// Different owner, same repo
		prs = cache.FindPRsForCommit("owner2", "repo1", "abc123")
		if len(prs) != 0 {
			t.Errorf("FindPRsForCommit for wrong owner returned %d PRs, want 0", len(prs))
		}
	})

	t.Run("skip expired entries", func(t *testing.T) {
		cache := NewCommitPRCache()
		sha := "test-commit"

		// Record a PR
		cache.RecordPR("owner1", "repo1", 42, []string{sha})

		// Manually set the entry to be expired
		cache.mu.Lock()
		if entries, ok := cache.entries[sha]; ok && len(entries) > 0 {
			entries[0].addedAt = time.Now().Add(-commitCacheEntryTTL - time.Minute)
			cache.entries[sha] = entries
		}
		cache.mu.Unlock()

		// Should not return expired entry
		prs := cache.FindPRsForCommit("owner1", "repo1", sha)
		if len(prs) != 0 {
			t.Errorf("FindPRsForCommit returned %d PRs, want 0 (entry expired)", len(prs))
		}
	})
}

func TestCommitPRCache_MostRecentPR(t *testing.T) {
	t.Run("get most recent PR", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner1", "repo1", 42, []string{"sha1"})

		recent := cache.MostRecentPR("owner1", "repo1")
		if recent != 42 {
			t.Errorf("MostRecentPR = %d, want 42", recent)
		}
	})

	t.Run("no PRs for repo", func(t *testing.T) {
		cache := NewCommitPRCache()

		recent := cache.MostRecentPR("owner1", "repo1")
		if recent != 0 {
			t.Errorf("MostRecentPR = %d, want 0 (no PRs)", recent)
		}
	})

	t.Run("tracks per repo", func(t *testing.T) {
		cache := NewCommitPRCache()
		cache.RecordPR("owner1", "repo1", 10, []string{"sha1"})
		cache.RecordPR("owner1", "repo2", 20, []string{"sha2"})
		cache.RecordPR("owner2", "repo1", 30, []string{"sha3"})

		if cache.MostRecentPR("owner1", "repo1") != 10 {
			t.Error("owner1/repo1 should be 10")
		}
		if cache.MostRecentPR("owner1", "repo2") != 20 {
			t.Error("owner1/repo2 should be 20")
		}
		if cache.MostRecentPR("owner2", "repo1") != 30 {
			t.Error("owner2/repo1 should be 30")
		}
	})
}

func TestCommitPRCache_Cleanup(t *testing.T) {
	t.Run("cleanup removes expired entries", func(t *testing.T) {
		cache := NewCommitPRCache()

		// Add some entries
		cache.RecordPR("owner1", "repo1", 10, []string{"sha1", "sha2", "sha3"})

		// Manually expire some entries
		cache.mu.Lock()
		expiredTime := time.Now().Add(-commitCacheEntryTTL - time.Minute)
		for sha := range cache.entries {
			entries := cache.entries[sha]
			if len(entries) > 0 {
				entries[0].addedAt = expiredTime
				cache.entries[sha] = entries
			}
		}
		cache.mu.Unlock()

		// Trigger cleanup
		cache.mu.Lock()
		cache.cleanup()
		cache.mu.Unlock()

		// All entries should be removed
		cache.mu.RLock()
		entryCount := len(cache.entries)
		cache.mu.RUnlock()

		if entryCount != 0 {
			t.Errorf("After cleanup, cache has %d entries, want 0", entryCount)
		}
	})

	t.Run("cleanup keeps fresh entries", func(t *testing.T) {
		cache := NewCommitPRCache()

		// Add mix of fresh and expired entries
		cache.RecordPR("owner1", "repo1", 10, []string{"fresh1", "fresh2"})

		// Manually expire one entry
		cache.mu.Lock()
		if entries, ok := cache.entries["fresh1"]; ok && len(entries) > 0 {
			entries[0].addedAt = time.Now().Add(-commitCacheEntryTTL - time.Minute)
			cache.entries["fresh1"] = entries
		}
		cache.mu.Unlock()

		// Trigger cleanup
		cache.mu.Lock()
		cache.cleanup()
		cache.mu.Unlock()

		// Fresh entry should remain
		prs := cache.FindPRsForCommit("owner1", "repo1", "fresh2")
		if len(prs) != 1 {
			t.Errorf("Fresh entry should remain, got %d PRs", len(prs))
		}

		// Expired entry should be gone
		prs = cache.FindPRsForCommit("owner1", "repo1", "fresh1")
		if len(prs) != 0 {
			t.Errorf("Expired entry should be removed, got %d PRs", len(prs))
		}
	})

	t.Run("automatic cleanup when cache exceeds limit", func(t *testing.T) {
		cache := NewCommitPRCache()

		// Fill cache beyond limit - first half are expired
		now := time.Now()
		expiredTime := now.Add(-commitCacheEntryTTL - time.Minute)

		// Add maxCacheEntries + 100 entries, with first 100 being expired
		for i := range maxCacheEntries + 100 {
			sha := fmt.Sprintf("commit-%06d", i)
			cache.RecordPR("owner1", "repo1", i, []string{sha})

			// Make first 100 entries expired
			if i < 100 {
				cache.mu.Lock()
				if entries, ok := cache.entries[sha]; ok && len(entries) > 0 {
					entries[0].addedAt = expiredTime
					cache.entries[sha] = entries
				}
				cache.mu.Unlock()
			}
		}

		// Last RecordPR should have triggered cleanup (since we exceeded maxCacheEntries)
		cache.mu.RLock()
		entryCount := len(cache.entries)
		cache.mu.RUnlock()

		// After cleanup, expired entries should be removed
		// We had maxCacheEntries+100, first 100 were expired, so should have ~maxCacheEntries left
		if entryCount < maxCacheEntries-100 || entryCount > maxCacheEntries+10 {
			t.Errorf("Cache has %d entries, want around %d (expired entries cleaned up)", entryCount, maxCacheEntries)
		}
	})
}

func TestCommitPRCache_Concurrency(t *testing.T) {
	t.Run("concurrent reads and writes", func(t *testing.T) {
		cache := NewCommitPRCache()

		// Launch multiple goroutines to write
		done := make(chan bool)
		for i := range 10 {
			go func(id int) {
				for j := range 100 {
					sha := string(rune('a'+id)) + string(rune('0'+j))
					cache.RecordPR("owner", "repo", id*100+j, []string{sha})
				}
				done <- true
			}(i)
		}

		// Launch multiple goroutines to read
		for i := range 10 {
			go func(id int) {
				for j := range 100 {
					sha := string(rune('a'+id)) + string(rune('0'+j))
					cache.FindPRsForCommit("owner", "repo", sha)
					cache.MostRecentPR("owner", "repo")
				}
				done <- true
			}(i)
		}

		// Wait for all goroutines
		for range 20 {
			<-done
		}

		// If we get here without deadlock or race, test passes
	})
}
