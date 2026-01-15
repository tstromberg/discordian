package bot

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/gorilla/websocket"
)

const (
	// Ping/pong intervals.
	pingInterval = 30 * time.Second
	pongWait     = 90 * time.Second
	writeWait    = 10 * time.Second

	// Reconnection settings.
	maxReconnectDelay = 2 * time.Minute
	initialDelay      = time.Second
)

// SprinklerClient manages the WebSocket connection to sprinkler.
type SprinklerClient struct {
	logger       *slog.Logger
	onEvent      func(SprinklerEvent)
	onConnect    func()
	onDisconnect func(error)
	conn         *websocket.Conn
	stopCh       chan struct{}
	serverURL    string
	token        string
	organization string
	mu           sync.RWMutex
	stopOnce     sync.Once
}

// SprinklerConfig holds configuration for the sprinkler client.
type SprinklerConfig struct {
	Logger       *slog.Logger
	OnEvent      func(SprinklerEvent)
	OnConnect    func()
	OnDisconnect func(error)
	ServerURL    string
	Token        string
	Organization string
}

// NewSprinklerClient creates a new sprinkler WebSocket client.
func NewSprinklerClient(cfg SprinklerConfig) (*SprinklerClient, error) {
	if cfg.ServerURL == "" {
		return nil, errors.New("serverURL is required")
	}
	if cfg.Organization == "" {
		return nil, errors.New("organization is required")
	}
	if cfg.Token == "" {
		return nil, errors.New("token is required")
	}

	logger := cfg.Logger
	if logger == nil {
		logger = slog.Default()
	}

	return &SprinklerClient{
		serverURL:    cfg.ServerURL,
		token:        cfg.Token,
		organization: cfg.Organization,
		onEvent:      cfg.OnEvent,
		onConnect:    cfg.OnConnect,
		onDisconnect: cfg.OnDisconnect,
		logger:       logger,
		stopCh:       make(chan struct{}),
	}, nil
}

// Start begins the connection with automatic reconnection.
func (c *SprinklerClient) Start(ctx context.Context) error {
	delay := initialDelay

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopCh:
			return nil
		default:
		}

		err := c.connect(ctx)
		if err == nil {
			delay = initialDelay
			continue
		}

		// Check for authentication errors
		if isAuthError(err) {
			c.logger.Error("authentication failed", "error", err)
			return err
		}

		c.logger.Warn("connection lost, reconnecting",
			"error", err,
			"delay", delay)

		if c.onDisconnect != nil {
			c.onDisconnect(err)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopCh:
			return nil
		case <-time.After(delay):
		}

		// Exponential backoff
		delay *= 2
		if delay > maxReconnectDelay {
			delay = maxReconnectDelay
		}
	}
}

// Stop gracefully stops the client.
func (c *SprinklerClient) Stop() {
	c.stopOnce.Do(func() {
		close(c.stopCh)
		c.mu.Lock()
		if c.conn != nil {
			c.conn.Close() //nolint:errcheck,gosec // best-effort close during shutdown
		}
		c.mu.Unlock()
	})
}

func (c *SprinklerClient) connect(ctx context.Context) error {
	c.logger.Info("connecting to sprinkler", "url", c.serverURL, "org", c.organization)

	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	header := http.Header{}
	header.Set("Authorization", "Bearer "+c.token)

	conn, resp, err := dialer.DialContext(ctx, c.serverURL, header)
	if resp != nil && resp.Body != nil {
		resp.Body.Close() //nolint:errcheck,gosec // response body must be closed
	}
	if err != nil {
		if resp != nil {
			if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusUnauthorized {
				return &authError{message: fmt.Sprintf("auth failed: %d", resp.StatusCode)}
			}
		}
		return fmt.Errorf("dial: %w", err)
	}

	c.mu.Lock()
	c.conn = conn
	c.mu.Unlock()

	defer func() {
		c.mu.Lock()
		c.conn = nil
		conn.Close() //nolint:errcheck,gosec // best-effort close
		c.mu.Unlock()
	}()

	// Send subscription
	sub := map[string]any{
		"organization":     c.organization,
		"user_events_only": false,
	}

	if err := conn.WriteJSON(sub); err != nil {
		return fmt.Errorf("write subscription: %w", err)
	}

	// Read subscription confirmation
	if err := conn.SetReadDeadline(time.Now().Add(5 * time.Second)); err != nil {
		return fmt.Errorf("set read deadline: %w", err)
	}

	var response map[string]any
	if err := conn.ReadJSON(&response); err != nil {
		return fmt.Errorf("read subscription response: %w", err)
	}

	// Check for error response
	if respType, ok := response["type"].(string); ok && respType == "error" {
		errCode, _ := response["error"].(string) //nolint:errcheck // optional field
		msg, _ := response["message"].(string)   //nolint:errcheck // optional field
		if errCode == "access_denied" || errCode == "authentication_failed" {
			return &authError{message: fmt.Sprintf("%s: %s", errCode, msg)}
		}
		return fmt.Errorf("subscription rejected: %s - %s", errCode, msg)
	}

	c.logger.Info("connected to sprinkler", "org", c.organization)

	if c.onConnect != nil {
		c.onConnect()
	}

	// Start ping sender
	pingDone := make(chan struct{})
	go func() {
		defer close(pingDone)
		c.pingLoop(ctx, conn)
	}()

	// Read events
	err = c.readLoop(ctx, conn)

	// Wait for ping loop to finish
	<-pingDone

	return err
}

func (c *SprinklerClient) pingLoop(ctx context.Context, conn *websocket.Conn) {
	ticker := time.NewTicker(pingInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-c.stopCh:
			return
		case <-ticker.C:
			c.mu.Lock()
			conn.SetWriteDeadline(time.Now().Add(writeWait)) //nolint:errcheck,gosec // best-effort deadline
			err := conn.WriteJSON(map[string]string{"type": "ping"})
			c.mu.Unlock()

			if err != nil {
				c.logger.Debug("ping failed", "error", err)
				return
			}
		}
	}
}

func (c *SprinklerClient) readLoop(ctx context.Context, conn *websocket.Conn) error {
	conn.SetReadDeadline(time.Now().Add(pongWait)) //nolint:errcheck,gosec // best-effort deadline

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-c.stopCh:
			return nil
		default:
		}

		var msg map[string]any
		err := conn.ReadJSON(&msg)
		if err != nil {
			if websocket.IsCloseError(err, websocket.CloseNormalClosure, websocket.CloseGoingAway) {
				return nil
			}
			return fmt.Errorf("read: %w", err)
		}

		// Reset read deadline on any message
		conn.SetReadDeadline(time.Now().Add(pongWait)) //nolint:errcheck,gosec // best-effort deadline

		msgType, _ := msg["type"].(string) //nolint:errcheck // optional field

		// Handle ping/pong
		if msgType == "ping" {
			c.mu.Lock()
			conn.SetWriteDeadline(time.Now().Add(writeWait))  //nolint:errcheck,gosec // best-effort deadline
			conn.WriteJSON(map[string]string{"type": "pong"}) //nolint:errcheck,gosec // best-effort pong
			c.mu.Unlock()
			continue
		}

		if msgType == "pong" {
			continue
		}

		// Parse event
		event := SprinklerEvent{
			Type: msgType,
		}

		if url, ok := msg["url"].(string); ok {
			event.URL = url
		}

		if ts, ok := msg["timestamp"].(string); ok {
			if t, parseErr := time.Parse(time.RFC3339, ts); parseErr == nil {
				event.Timestamp = t
			}
		}

		if deliveryID, ok := msg["delivery_id"].(string); ok {
			event.DeliveryID = deliveryID
		}

		if commitSHA, ok := msg["commit_sha"].(string); ok {
			event.CommitSHA = commitSHA
		}

		// Skip events without PR URL
		if event.URL == "" || !strings.Contains(event.URL, "/pull/") {
			continue
		}

		c.logger.Debug("received event",
			"type", event.Type,
			"url", event.URL,
			"delivery_id", event.DeliveryID)

		if c.onEvent != nil {
			c.onEvent(event)
		}
	}
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

// ParsePRURL extracts owner, repo, and number from a GitHub PR URL.
func ParsePRURL(url string) (PRURLInfo, bool) {
	// https://github.com/owner/repo/pull/123
	parts := strings.Split(url, "/")
	if len(parts) < 7 || parts[2] != "github.com" || parts[5] != "pull" {
		return PRURLInfo{}, false
	}

	var n int
	if _, err := fmt.Sscanf(parts[6], "%d", &n); err != nil {
		return PRURLInfo{}, false
	}

	return PRURLInfo{Owner: parts[3], Repo: parts[4], Number: n}, true
}

// FormatPRURL creates a GitHub PR URL from components.
func FormatPRURL(owner, repo string, number int) string {
	return fmt.Sprintf("https://github.com/%s/%s/pull/%d", owner, repo, number)
}

// TurnHTTPClient implements TurnClient using HTTP.
type TurnHTTPClient struct {
	client  *http.Client
	baseURL string
	token   string
}

// NewTurnClient creates a new Turn API client.
func NewTurnClient(baseURL, token string) *TurnHTTPClient {
	return &TurnHTTPClient{
		baseURL: strings.TrimSuffix(baseURL, "/"),
		token:   token,
		client:  &http.Client{Timeout: 30 * time.Second},
	}
}

// Check calls the Turn API to analyze a PR.
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

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/v1/validate", strings.NewReader(string(body)))
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

	resp, err := c.client.Do(req)
	duration := time.Since(start)
	if err != nil {
		slog.Error("Turn API request failed",
			"url", prURL,
			"error", err,
			"duration_ms", duration.Milliseconds())
		return nil, fmt.Errorf("do request: %w", err)
	}
	defer resp.Body.Close() //nolint:errcheck // response body must be closed

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body) //nolint:errcheck // best effort to log response body
		slog.Error("Turn API returned error",
			"url", prURL,
			"status", resp.StatusCode,
			"body", string(respBody),
			"duration_ms", duration.Milliseconds())
		return nil, fmt.Errorf("unexpected status: %d", resp.StatusCode)
	}

	var result CheckResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		slog.Error("Turn API response decode failed",
			"url", prURL,
			"error", err,
			"duration_ms", duration.Milliseconds())
		return nil, fmt.Errorf("decode response: %w", err)
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
