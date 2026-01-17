package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/codeGROOVE-dev/retry"
	"github.com/codeGROOVE-dev/sprinkler/pkg/client"
)

// TokenProvider provides fresh GitHub installation tokens.
type TokenProvider interface {
	InstallationToken(ctx context.Context) (string, error)
}

// SprinklerClient manages the WebSocket connection to sprinkler.
// This is now a thin wrapper around the library client.
type SprinklerClient struct {
	client        *client.Client
	tokenProvider TokenProvider
	logger        *slog.Logger
	onConnect     func()
	onDisconnect  func(error)
}

// SprinklerConfig holds configuration for the sprinkler client.
type SprinklerConfig struct {
	Logger        *slog.Logger
	OnEvent       func(SprinklerEvent)
	OnConnect     func()
	OnDisconnect  func(error)
	ServerURL     string
	TokenProvider TokenProvider
	Organization  string
}

// NewSprinklerClient creates a new sprinkler WebSocket client using the library.
func NewSprinklerClient(ctx context.Context, cfg SprinklerConfig) (*SprinklerClient, error) {
	if cfg.ServerURL == "" {
		return nil, errors.New("serverURL is required")
	}
	if cfg.Organization == "" {
		return nil, errors.New("organization is required")
	}
	if cfg.TokenProvider == nil {
		return nil, errors.New("tokenProvider is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	sc := &SprinklerClient{
		tokenProvider: cfg.TokenProvider,
		logger:        logger,
		onConnect:     cfg.OnConnect,
		onDisconnect:  cfg.OnDisconnect,
	}

	// Create library client config
	libConfig := client.Config{
		Logger:       logger,
		ServerURL:    cfg.ServerURL,
		Organization: cfg.Organization,
		UserAgent:    "discordian/v1.0.0",
		TokenProvider: func() (string, error) {
			// Use context from parent scope for token fetching
			return cfg.TokenProvider.InstallationToken(ctx)
		},
		OnConnect:    cfg.OnConnect,
		OnDisconnect: cfg.OnDisconnect,
		OnEvent: func(e client.Event) {
			if cfg.OnEvent != nil {
				// Convert library event to our event type
				cfg.OnEvent(SprinklerEvent{
					Type:       e.Type,
					URL:        e.URL,
					Timestamp:  e.Timestamp,
					DeliveryID: e.DeliveryID,
					CommitSHA:  e.CommitSHA,
					Raw:        e.Raw,
				})
			}
		},
	}

	libClient, err := client.New(libConfig)
	if err != nil {
		return nil, fmt.Errorf("create library client: %w", err)
	}

	sc.client = libClient
	return sc, nil
}

// Start begins the connection with automatic reconnection.
func (c *SprinklerClient) Start(ctx context.Context) error {
	return c.client.Start(ctx)
}

// Stop gracefully stops the client.
func (c *SprinklerClient) Stop() {
	c.client.Stop()
}

type authError struct {
	message string
}

func (e *authError) Error() string {
	return e.message
}

func isAuthError(err error) bool {
	var ae *authError
	return errors.As(err, &ae)
}

// PRURLInfo contains parsed PR URL components.
type PRURLInfo struct {
	Owner  string
	Repo   string
	Number int
}

// validGitHubName matches valid GitHub owner/repo names.
// GitHub allows alphanumeric, hyphens, underscores, and dots (with restrictions).
var validGitHubName = regexp.MustCompile(`^[a-zA-Z0-9][-a-zA-Z0-9_.]*$`)

// ParsePRURL extracts owner, repo, and number from a GitHub PR URL.
// Uses proper URL parsing to prevent injection attacks.
func ParsePRURL(rawURL string) (PRURLInfo, bool) {
	// Parse URL properly to validate structure
	u, err := url.Parse(rawURL)
	if err != nil {
		return PRURLInfo{}, false
	}

	// Must be HTTPS to github.com
	if u.Scheme != "https" || u.Host != "github.com" {
		return PRURLInfo{}, false
	}

	// Path must be /owner/repo/pull/number
	// Use TrimPrefix to get clean path
	path := strings.TrimPrefix(u.Path, "/")
	parts := strings.Split(path, "/")

	if len(parts) != 4 || parts[2] != "pull" {
		return PRURLInfo{}, false
	}

	owner, repo, numStr := parts[0], parts[1], parts[3]

	// Validate owner and repo names against GitHub naming rules
	if !validGitHubName.MatchString(owner) || !validGitHubName.MatchString(repo) {
		return PRURLInfo{}, false
	}

	// Prevent path traversal attempts
	if strings.Contains(owner, "..") || strings.Contains(repo, "..") {
		return PRURLInfo{}, false
	}

	// Parse PR number
	n, err := strconv.Atoi(numStr)
	if err != nil || n <= 0 {
		return PRURLInfo{}, false
	}

	return PRURLInfo{Owner: owner, Repo: repo, Number: n}, true
}

// FormatPRURL creates a GitHub PR URL from components.
func FormatPRURL(owner, repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
}

// TurnHTTPClient implements TurnClient using HTTP.
type TurnHTTPClient struct {
	tokenProvider TokenProvider
	client        *http.Client
	baseURL       string
}

// NewTurnClient creates a new Turn API client.
func NewTurnClient(baseURL string, tokenProvider TokenProvider) *TurnHTTPClient {
	return &TurnHTTPClient{
		baseURL:       strings.TrimSuffix(baseURL, "/"),
		tokenProvider: tokenProvider,
		client:        &http.Client{Timeout: 30 * time.Second},
	}
}

// Check calls the Turn API to analyze a PR with retry logic.
func (c *TurnHTTPClient) Check(ctx context.Context, prURL, username string, updatedAt time.Time) (*CheckResponse, error) {
	reqBody := map[string]any{
		"url":        prURL,
		"user":       username,
		"updated_at": updatedAt.Format(time.RFC3339),
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshal request: %w", err)
	}

	start := time.Now()
	slog.Debug("calling Turn API",
		"url", prURL,
		"username", username,
		"updated_at", updatedAt.Format(time.RFC3339))

	var result CheckResponse
	err = retry.Do(
		func() error {
			return c.doTurnRequest(ctx, body, prURL, &result)
		},
		retry.Context(ctx),
		retry.Attempts(5),
		retry.Delay(time.Second),
		retry.MaxDelay(2*time.Minute),
		retry.DelayType(retry.BackOffDelay),
		retry.LastErrorOnly(true),
		retry.OnRetry(func(n uint, err error) {
			slog.Warn("Turn API call failed, retrying",
				"url", prURL,
				"attempt", n+1,
				"error", err)
		}),
		retry.RetryIf(func(err error) bool {
			// Don't retry on context cancellation
			return !errors.Is(err, context.Canceled) && !errors.Is(err, context.DeadlineExceeded)
		}),
	)

	duration := time.Since(start)
	if err != nil {
		slog.Error("Turn API request failed after retries",
			"url", prURL,
			"error", err,
			"duration_ms", duration.Milliseconds())
		return nil, err
	}

	slog.Debug("Turn API response received",
		"url", prURL,
		"workflow_state", result.Analysis.WorkflowState,
		"ready_to_merge", result.Analysis.ReadyToMerge,
		"checks_failing", result.Analysis.Checks.Failing,
		"next_action_count", len(result.Analysis.NextAction),
		"next_action", result.Analysis.NextAction,
		"duration_ms", duration.Milliseconds())

	return &result, nil
}

// doTurnRequest performs a single Turn API request.
func (c *TurnHTTPClient) doTurnRequest(ctx context.Context, body []byte, prURL string, result *CheckResponse) error {
	// Get fresh token for this request
	token, err := c.tokenProvider.InstallationToken(ctx)
	if err != nil {
		return fmt.Errorf("failed to get fresh token: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/validate", strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := c.client.Do(req)
	if err != nil {
		return fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body must be closed

	if resp.StatusCode != http.StatusOK {
		// Limit error response body to 4KB to prevent memory exhaustion attacks
		respBody, _ := io.ReadAll(io.LimitReader(resp.Body, 4096)) //nolint:errcheck // best effort to log response body
		slog.Debug("Turn API returned error",
			"url", prURL,
			"status", resp.StatusCode,
			"body", string(respBody))
		return fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	if err := json.NewDecoder(resp.Body).Decode(result); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}

	return nil
}
