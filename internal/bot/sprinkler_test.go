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
	if client.serverURL != "wss://example.com/ws" {
		t.Errorf("serverURL = %q, want %q", client.serverURL, "wss://example.com/ws")
	}
	if client.organization != "testorg" {
		t.Errorf("organization = %q, want %q", client.organization, "testorg")
	}
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
