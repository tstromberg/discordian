package bot

import (
	"context"
	"fmt"
	"time"
)

// MockTurnClient is a programmable mock for TurnClient
type MockTurnClient struct {
	// Programmable responses - map PR URL to response
	Responses map[string]*CheckResponse
	Errors    map[string]error

	// Default response for unmapped PRs
	DefaultResponse *CheckResponse
	DefaultError    error

	// Track calls
	Calls []*TurnCall
}

type TurnCall struct {
	PRURL     string
	Username  string
	UpdatedAt time.Time
}

func NewMockTurnClient() *MockTurnClient {
	return &MockTurnClient{
		Responses: make(map[string]*CheckResponse),
		Errors:    make(map[string]error),
		Calls:     make([]*TurnCall, 0),
	}
}

func (m *MockTurnClient) Check(ctx context.Context, prURL, username string, updatedAt time.Time) (*CheckResponse, error) {
	m.Calls = append(m.Calls, &TurnCall{
		PRURL:     prURL,
		Username:  username,
		UpdatedAt: updatedAt,
	})

	// Check for specific error
	if err, ok := m.Errors[prURL]; ok {
		return nil, err
	}

	// Check for specific response
	if resp, ok := m.Responses[prURL]; ok {
		return resp, nil
	}

	// Use default error if set
	if m.DefaultError != nil {
		return nil, m.DefaultError
	}

	// Use default response if set
	if m.DefaultResponse != nil {
		return m.DefaultResponse, nil
	}

	// Return empty response
	return &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Default PR",
			Author: username,
		},
		Analysis: Analysis{},
	}, nil
}

// SetResponse sets a specific response for a PR URL
func (m *MockTurnClient) SetResponse(prURL string, resp *CheckResponse) {
	m.Responses[prURL] = resp
}

// SetError sets a specific error for a PR URL
func (m *MockTurnClient) SetError(prURL string, err error) {
	m.Errors[prURL] = err
}

// NewMockCheckResponse creates a mock TURN API response
func NewMockCheckResponse(title, author string, merged, closed, draft bool) *CheckResponse {
	return &CheckResponse{
		PullRequest: PRInfo{
			Title:  title,
			Author: author,
			Merged: merged,
			Closed: closed,
			Draft:  draft,
		},
		Analysis: Analysis{
			WorkflowState: "active",
			NextAction:    make(map[string]Action),
		},
	}
}

// WithAction adds a next action to the response
func (r *CheckResponse) WithAction(username, kind string) *CheckResponse {
	r.Analysis.NextAction[username] = Action{
		Kind: kind,
	}
	return r
}

// WithState sets the workflow state
func (r *CheckResponse) WithState(state string) *CheckResponse {
	r.Analysis.WorkflowState = state
	return r
}

// WithApproved sets the approved flag
func (r *CheckResponse) WithApproved(approved bool) *CheckResponse {
	r.Analysis.Approved = approved
	return r
}

// WithChecks sets check status
func (r *CheckResponse) WithChecks(failing, pending, waiting int) *CheckResponse {
	r.Analysis.Checks = Checks{
		Failing: failing,
		Pending: pending,
		Waiting: waiting,
	}
	return r
}

// WithComments sets unresolved comments
func (r *CheckResponse) WithComments(count int) *CheckResponse {
	r.Analysis.UnresolvedComments = count
	return r
}

// WithConflict sets merge conflict flag
func (r *CheckResponse) WithConflict(hasConflict bool) *CheckResponse {
	r.Analysis.MergeConflict = hasConflict
	return r
}

// MockGitHubClient is a programmable mock for GitHub client operations
type MockGitHubClient struct {
	PRSearchResults map[string][]PRSearchResult
	SearchErrors    map[string]error
}

func NewMockGitHubClient() *MockGitHubClient {
	return &MockGitHubClient{
		PRSearchResults: make(map[string][]PRSearchResult),
		SearchErrors:    make(map[string]error),
	}
}

// NewMockPRSearchResult creates a mock PR search result
func NewMockPRSearchResult(owner, repo string, number int, updatedAt time.Time) PRSearchResult {
	return PRSearchResult{
		URL:       fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number),
		Owner:     owner,
		Repo:      repo,
		Number:    number,
		UpdatedAt: updatedAt,
	}
}
