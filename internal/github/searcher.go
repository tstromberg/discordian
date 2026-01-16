package github

import (
	"context"
	"fmt"
	"log/slog"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/bot"
	"github.com/google/go-github/v50/github"
)

// Searcher queries GitHub for PRs using the search API.
type Searcher struct {
	appClient *AppClient
	logger    *slog.Logger
}

// NewSearcher creates a new PR searcher.
func NewSearcher(appClient *AppClient, logger *slog.Logger) *Searcher {
	if logger == nil {
		logger = slog.Default()
	}
	return &Searcher{
		appClient: appClient,
		logger:    logger,
	}
}

// ListOpenPRs returns open PRs for an org updated within the given hours.
func (s *Searcher) ListOpenPRs(ctx context.Context, org string, updatedWithinHours int) ([]bot.PRSearchResult, error) {
	client, err := s.appClient.ClientForOrg(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("get client for org: %w", err)
	}

	since := time.Now().Add(-time.Duration(updatedWithinHours) * time.Hour)
	query := fmt.Sprintf("org:%s is:pr is:open updated:>%s", org, since.Format("2006-01-02"))

	s.logger.Debug("searching for open PRs",
		"org", org,
		"query", query,
		"updated_within_hours", updatedWithinHours)

	return s.searchPRs(ctx, client, query)
}

// ListClosedPRs returns recently closed/merged PRs for catching terminal states.
func (s *Searcher) ListClosedPRs(ctx context.Context, org string, closedWithinHours int) ([]bot.PRSearchResult, error) {
	client, err := s.appClient.ClientForOrg(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("get client for org: %w", err)
	}

	since := time.Now().Add(-time.Duration(closedWithinHours) * time.Hour)
	query := fmt.Sprintf("org:%s is:pr is:closed closed:>%s", org, since.Format("2006-01-02T15:04:05Z"))

	s.logger.Debug("searching for closed PRs",
		"org", org,
		"query", query,
		"closed_within_hours", closedWithinHours)

	return s.searchPRs(ctx, client, query)
}

func (s *Searcher) searchPRs(ctx context.Context, client *github.Client, query string) ([]bot.PRSearchResult, error) {
	opts := &github.SearchOptions{
		Sort:  "updated",
		Order: "desc",
		ListOptions: github.ListOptions{
			PerPage: 100,
		},
	}

	var results []bot.PRSearchResult

	for {
		result, resp, err := client.Search.Issues(ctx, query, opts)
		if err != nil {
			s.logger.Error("GitHub search API failed",
				"query", query,
				"error", err)
			return nil, fmt.Errorf("search issues: %w", err)
		}

		for _, issue := range result.Issues {
			if issue.PullRequestLinks == nil {
				continue // Not a PR
			}

			// Parse owner/repo from repository URL
			owner, repo := parseRepoFromIssue(issue)
			if owner == "" || repo == "" {
				continue
			}

			pr := bot.PRSearchResult{
				URL:       issue.PullRequestLinks.GetHTMLURL(),
				Owner:     owner,
				Repo:      repo,
				Number:    issue.GetNumber(),
				UpdatedAt: issue.GetUpdatedAt().Time,
			}

			// If HTML URL is empty, construct it
			if pr.URL == "" {
				pr.URL = fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, pr.Number)
			}

			results = append(results, pr)
		}

		if resp.NextPage == 0 {
			break
		}
		opts.Page = resp.NextPage
	}

	s.logger.Debug("PR search completed",
		"query", query,
		"results", len(results))

	return results, nil
}

// parseRepoFromIssue extracts owner and repo from an issue's repository URL.
func parseRepoFromIssue(issue *github.Issue) (owner, repo string) {
	if issue.RepositoryURL == nil {
		return "", ""
	}

	// Repository URL format: https://api.github.com/repos/owner/repo
	url := *issue.RepositoryURL
	const prefix = "https://api.github.com/repos/"
	if len(url) <= len(prefix) {
		return "", ""
	}

	// Parse owner/repo from the path
	path := url[len(prefix):]
	for i := range len(path) {
		if path[i] == '/' {
			return path[:i], path[i+1:]
		}
	}

	return "", ""
}
