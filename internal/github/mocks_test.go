package github

import (
	"context"
	"fmt"
	"time"

	"github.com/google/go-github/v50/github"
)

// MockGitHubAppsService mocks the GitHub Apps API
type MockGitHubAppsService struct {
	Installations      []*github.Installation
	InstallationTokens map[int64]string
	ListInstallsError  error
	CreateTokenError   error
}

func (m *MockGitHubAppsService) ListInstallations(ctx context.Context, opts *github.ListOptions) ([]*github.Installation, *github.Response, error) {
	if m.ListInstallsError != nil {
		return nil, nil, m.ListInstallsError
	}
	return m.Installations, &github.Response{}, nil
}

func (m *MockGitHubAppsService) CreateInstallationToken(ctx context.Context, id int64, opts *github.InstallationTokenOptions) (*github.InstallationToken, *github.Response, error) {
	if m.CreateTokenError != nil {
		return nil, nil, m.CreateTokenError
	}

	token := "ghs_mock_token"
	if m.InstallationTokens != nil {
		if t, ok := m.InstallationTokens[id]; ok {
			token = t
		}
	}

	expiresAt := github.Timestamp{Time: time.Now().Add(1 * time.Hour)}
	return &github.InstallationToken{
		Token:     &token,
		ExpiresAt: &expiresAt,
	}, &github.Response{}, nil
}

// MockSearchService mocks the GitHub Search API
type MockSearchService struct {
	IssueResults map[string]*github.IssuesSearchResult
	SearchError  error
}

func (m *MockSearchService) Issues(ctx context.Context, query string, opts *github.SearchOptions) (*github.IssuesSearchResult, *github.Response, error) {
	if m.SearchError != nil {
		return nil, nil, m.SearchError
	}

	if m.IssueResults != nil {
		if result, ok := m.IssueResults[query]; ok {
			return result, &github.Response{}, nil
		}
	}

	// Default empty result
	return &github.IssuesSearchResult{
		Total:  github.Int(0),
		Issues: []*github.Issue{},
	}, &github.Response{}, nil
}

// NewMockInstallation creates a mock GitHub installation
func NewMockInstallation(id int64, login, accountType string) *github.Installation {
	return &github.Installation{
		ID: &id,
		Account: &github.User{
			Login: &login,
			Type:  &accountType,
		},
	}
}

// NewMockPRIssue creates a mock GitHub issue representing a PR
func NewMockPRIssue(owner, repo string, number int, title string) *github.Issue {
	repoURL := fmt.Sprintf("https://api.github.com/repos/%s/%s", owner, repo)
	htmlURL := fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
	updatedAt := github.Timestamp{Time: time.Now()}

	return &github.Issue{
		Number:        &number,
		Title:         &title,
		RepositoryURL: &repoURL,
		UpdatedAt:     &updatedAt,
		PullRequestLinks: &github.PullRequestLinks{
			HTMLURL: &htmlURL,
		},
	}
}
