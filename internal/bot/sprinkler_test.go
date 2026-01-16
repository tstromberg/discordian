package bot

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestNewSprinklerClient_MissingServerURL(t *testing.T) {
	_, err := NewSprinklerClient(SprinklerConfig{
		Organization:  "testorg",
		TokenProvider: &mockTokenProvider{token: "token"},
	})
	if err == nil {
		t.Error("expected error for missing serverURL")
	}
}

func TestNewSprinklerClient_MissingOrganization(t *testing.T) {
	_, err := NewSprinklerClient(SprinklerConfig{
		ServerURL:     "wss://example.com/ws",
		TokenProvider: &mockTokenProvider{token: "token"},
	})
	if err == nil {
		t.Error("expected error for missing organization")
	}
}

func TestNewSprinklerClient_MissingTokenProvider(t *testing.T) {
	_, err := NewSprinklerClient(SprinklerConfig{
		ServerURL:    "wss://example.com/ws",
		Organization: "testorg",
	})
	if err == nil {
		t.Error("expected error for missing tokenProvider")
	}
}

func TestNewSprinklerClient_Success(t *testing.T) {
	client, err := NewSprinklerClient(SprinklerConfig{
		ServerURL:     "wss://example.com/ws",
		Organization:  "testorg",
		TokenProvider: &mockTokenProvider{token: "token"},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	// Configuration is verified at creation - successful creation means
	// serverURL and organization were properly set in the wrapped client
}

func TestSprinklerClient_Stop(t *testing.T) {
	client, err := NewSprinklerClient(SprinklerConfig{
		ServerURL:     "wss://example.com/ws",
		Organization:  "testorg",
		TokenProvider: &mockTokenProvider{token: "token"},
	})
	if err != nil {
		t.Fatalf("NewSprinklerClient() error = %v", err)
	}

	// Stop should not panic even when called multiple times
	client.Stop()
	client.Stop()
}

func TestAuthError_Error(t *testing.T) {
	err := &authError{message: "test error"}
	if err.Error() != "test error" {
		t.Errorf("Error() = %q, want %q", err.Error(), "test error")
	}
}

func TestIsAuthError(t *testing.T) {
	authErr := &authError{message: "auth failed"}
	if !isAuthError(authErr) {
		t.Error("isAuthError should return true for authError")
	}

	otherErr := context.DeadlineExceeded
	if isAuthError(otherErr) {
		t.Error("isAuthError should return false for non-authError")
	}
}

// mockTokenProvider is a test implementation of TokenProvider.
type mockTokenProvider struct {
	token string
	err   error
}

func (m *mockTokenProvider) InstallationToken(_ context.Context) (string, error) {
	if m.err != nil {
		return "", m.err
	}
	return m.token, nil
}

func TestNewTurnClient(t *testing.T) {
	provider := &mockTokenProvider{token: "token123"}
	client := NewTurnClient("https://turn.example.com/", provider)
	if client == nil {
		t.Fatal("expected non-nil client")
	}
	if client.baseURL != "https://turn.example.com" {
		t.Errorf("baseURL = %q, want %q", client.baseURL, "https://turn.example.com")
	}
	if client.tokenProvider == nil {
		t.Error("expected non-nil tokenProvider")
	}
}

func TestTurnHTTPClient_Check_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			t.Errorf("expected POST, got %s", r.Method)
		}
		if r.URL.Path != "/v1/validate" {
			t.Errorf("expected /v1/validate, got %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer testtoken" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}

		resp := CheckResponse{
			PullRequest: PRInfo{
				Title:  "Test PR",
				Author: "alice",
				State:  "open",
			},
			Analysis: Analysis{
				WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
				NextAction: map[string]Action{
					"bob": {Kind: "review", Reason: "needs review"},
				},
				Checks: Checks{
					Passing: 5,
					Pending: 1,
				},
			},
		}
		_ = json.NewEncoder(w).Encode(resp) //nolint:errcheck // test handler
	}))
	defer server.Close()

	client := NewTurnClient(server.URL, &mockTokenProvider{token: "testtoken"})
	ctx := context.Background()

	resp, err := client.Check(ctx, "https://github.com/owner/repo/pull/123", "user", time.Now())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if resp.PullRequest.Title != "Test PR" {
		t.Errorf("Title = %q, want %q", resp.PullRequest.Title, "Test PR")
	}
	if resp.Analysis.WorkflowState != "ASSIGNED_WAITING_FOR_REVIEW" {
		t.Errorf("WorkflowState = %q", resp.Analysis.WorkflowState)
	}
	if len(resp.Analysis.NextAction) != 1 {
		t.Errorf("NextAction count = %d, want 1", len(resp.Analysis.NextAction))
	}
}

func TestTurnHTTPClient_Check_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte("internal server error")) //nolint:errcheck // test handler
	}))
	defer server.Close()

	client := NewTurnClient(server.URL, &mockTokenProvider{token: "testtoken"})
	ctx := context.Background()

	_, err := client.Check(ctx, "https://github.com/owner/repo/pull/123", "user", time.Now())
	if err == nil {
		t.Error("expected error for server error response")
	}
}

func TestTurnHTTPClient_Check_InvalidJSON(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("not json")) //nolint:errcheck // test handler
	}))
	defer server.Close()

	client := NewTurnClient(server.URL, &mockTokenProvider{token: "testtoken"})
	ctx := context.Background()

	_, err := client.Check(ctx, "https://github.com/owner/repo/pull/123", "user", time.Now())
	if err == nil {
		t.Error("expected error for invalid JSON response")
	}
}

func TestTurnHTTPClient_Check_RequestError(t *testing.T) {
	client := NewTurnClient("http://invalid.invalid.invalid:99999", &mockTokenProvider{token: "testtoken"})
	ctx := context.Background()

	_, err := client.Check(ctx, "https://github.com/owner/repo/pull/123", "user", time.Now())
	if err == nil {
		t.Error("expected error for request failure")
	}
}

func TestTurnHTTPClient_Check_ContextCanceled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		time.Sleep(time.Second)
		_ = json.NewEncoder(w).Encode(CheckResponse{}) //nolint:errcheck // test handler
	}))
	defer server.Close()

	client := NewTurnClient(server.URL, &mockTokenProvider{token: "testtoken"})
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	_, err := client.Check(ctx, "https://github.com/owner/repo/pull/123", "user", time.Now())
	if err == nil {
		t.Error("expected error for canceled context")
	}
}

func TestParsePRURL_EdgeCases(t *testing.T) {
	tests := []struct {
		name      string
		url       string
		wantValid bool
		wantOwner string
		wantRepo  string
		wantNum   int
	}{
		{
			name:      "valid PR URL",
			url:       "https://github.com/owner/repo/pull/123",
			wantValid: true,
			wantOwner: "owner",
			wantRepo:  "repo",
			wantNum:   123,
		},
		{
			name:      "http scheme",
			url:       "http://github.com/owner/repo/pull/123",
			wantValid: false,
		},
		{
			name:      "wrong host",
			url:       "https://gitlab.com/owner/repo/pull/123",
			wantValid: false,
		},
		{
			name:      "missing pull segment",
			url:       "https://github.com/owner/repo/issues/123",
			wantValid: false,
		},
		{
			name:      "invalid PR number",
			url:       "https://github.com/owner/repo/pull/abc",
			wantValid: false,
		},
		{
			name:      "zero PR number",
			url:       "https://github.com/owner/repo/pull/0",
			wantValid: false,
		},
		{
			name:      "negative PR number",
			url:       "https://github.com/owner/repo/pull/-1",
			wantValid: false,
		},
		{
			name:      "path traversal in owner",
			url:       "https://github.com/../evil/repo/pull/123",
			wantValid: false,
		},
		{
			name:      "path traversal in repo",
			url:       "https://github.com/owner/../evil/pull/123",
			wantValid: false,
		},
		{
			name:      "invalid owner name",
			url:       "https://github.com/owner@evil/repo/pull/123",
			wantValid: false,
		},
		{
			name:      "empty URL",
			url:       "",
			wantValid: false,
		},
		{
			name:      "malformed URL",
			url:       "not a url",
			wantValid: false,
		},
		{
			name:      "too few path segments",
			url:       "https://github.com/owner/repo",
			wantValid: false,
		},
		{
			name:      "too many path segments",
			url:       "https://github.com/owner/repo/pull/123/extra",
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, valid := ParsePRURL(tt.url)
			if valid != tt.wantValid {
				t.Errorf("ParsePRURL(%q) valid = %v, want %v", tt.url, valid, tt.wantValid)
			}
			if valid {
				if info.Owner != tt.wantOwner {
					t.Errorf("Owner = %q, want %q", info.Owner, tt.wantOwner)
				}
				if info.Repo != tt.wantRepo {
					t.Errorf("Repo = %q, want %q", info.Repo, tt.wantRepo)
				}
				if info.Number != tt.wantNum {
					t.Errorf("Number = %d, want %d", info.Number, tt.wantNum)
				}
			}
		})
	}
}

func TestFormatPRURL(t *testing.T) {
	tests := []struct {
		name   string
		owner  string
		repo   string
		number int
		want   string
	}{
		{
			name:   "basic PR",
			owner:  "owner",
			repo:   "repo",
			number: 123,
			want:   "https://github.com/owner/repo/pull/123",
		},
		{
			name:   "PR number 1",
			owner:  "org",
			repo:   "project",
			number: 1,
			want:   "https://github.com/org/project/pull/1",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FormatPRURL(tt.owner, tt.repo, tt.number)
			if got != tt.want {
				t.Errorf("FormatPRURL(%q, %q, %d) = %q, want %q", tt.owner, tt.repo, tt.number, got, tt.want)
			}
		})
	}
}
