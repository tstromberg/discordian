// Package github provides GitHub App authentication and API access.
package github

import (
	"context"
	"crypto/rsa"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/go-github/v50/github"
	"golang.org/x/oauth2"
)

const (
	jwtExpiration        = 10 * time.Minute
	tokenRefreshBuffer   = 5 * time.Minute
	installationCacheTTL = time.Hour
)

// AppClient manages GitHub App authentication.
type AppClient struct {
	privateKey      *rsa.PrivateKey
	logger          *slog.Logger
	tokens          map[int64]*tokenEntry // Installation token cache
	installations   map[string]int64      // org -> installationID
	appID           string
	tokensMu        sync.RWMutex
	installationsMu sync.RWMutex
}

type tokenEntry struct {
	expiresAt time.Time
	token     string
}

// NewAppClient creates a new GitHub App client.
func NewAppClient(appID, privateKeyPEM string, logger *slog.Logger) (*AppClient, error) {
	if logger == nil {
		logger = slog.Default()
	}

	key, err := jwt.ParseRSAPrivateKeyFromPEM([]byte(privateKeyPEM))
	if err != nil {
		return nil, fmt.Errorf("parse private key: %w", err)
	}

	return &AppClient{
		appID:         appID,
		privateKey:    key,
		logger:        logger,
		tokens:        make(map[int64]*tokenEntry),
		installations: make(map[string]int64),
	}, nil
}

// ClientForOrg returns an authenticated GitHub client for an organization.
func (c *AppClient) ClientForOrg(ctx context.Context, org string) (*github.Client, error) {
	installationID, err := c.installationID(ctx, org)
	if err != nil {
		return nil, fmt.Errorf("get installation ID: %w", err)
	}

	token, err := c.installationToken(ctx, installationID)
	if err != nil {
		return nil, fmt.Errorf("get installation token: %w", err)
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)

	return github.NewClient(tc), nil
}

func (c *AppClient) installationID(ctx context.Context, org string) (int64, error) {
	// Check cache.
	c.installationsMu.RLock()
	if id, ok := c.installations[org]; ok {
		c.installationsMu.RUnlock()
		return id, nil
	}
	c.installationsMu.RUnlock()

	// Get JWT client.
	jwtClient := c.jwtClient(ctx)

	// List installations and find the one for this org.
	installations, _, err := jwtClient.Apps.ListInstallations(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("list installations: %w", err)
	}

	for _, inst := range installations {
		if inst.Account == nil || inst.Account.Login == nil || *inst.Account.Login != org {
			continue
		}

		// Cache it.
		c.installationsMu.Lock()
		c.installations[org] = *inst.ID
		c.installationsMu.Unlock()

		c.logger.Debug("found installation",
			"org", org,
			"installation_id", *inst.ID)

		return *inst.ID, nil
	}

	return 0, fmt.Errorf("no installation found for org: %s", org)
}

func (c *AppClient) installationToken(ctx context.Context, installationID int64) (string, error) {
	// Check cache.
	c.tokensMu.RLock()
	if entry, ok := c.tokens[installationID]; ok && time.Until(entry.expiresAt) > tokenRefreshBuffer {
		c.tokensMu.RUnlock()
		return entry.token, nil
	}
	c.tokensMu.RUnlock()

	// Get new token.
	jwtClient := c.jwtClient(ctx)
	token, _, err := jwtClient.Apps.CreateInstallationToken(ctx, installationID, nil)
	if err != nil {
		return "", fmt.Errorf("create installation token: %w", err)
	}

	// Cache it.
	c.tokensMu.Lock()
	c.tokens[installationID] = &tokenEntry{
		token:     token.GetToken(),
		expiresAt: token.GetExpiresAt().Time,
	}
	c.tokensMu.Unlock()

	c.logger.Debug("refreshed installation token",
		"installation_id", installationID,
		"expires_at", token.GetExpiresAt())

	return token.GetToken(), nil
}

func (c *AppClient) jwtClient(ctx context.Context) *github.Client {
	token := c.generateJWT()
	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	tc := oauth2.NewClient(ctx, ts)
	return github.NewClient(tc)
}

func (c *AppClient) generateJWT() string {
	now := time.Now()
	claims := jwt.RegisteredClaims{
		IssuedAt:  jwt.NewNumericDate(now),
		ExpiresAt: jwt.NewNumericDate(now.Add(jwtExpiration)),
		Issuer:    c.appID,
	}

	token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	signed, err := token.SignedString(c.privateKey)
	if err != nil {
		c.logger.Error("failed to sign JWT", "error", err)
		return ""
	}

	return signed
}

// TokenForOrg returns an installation token for an organization.
func (c *AppClient) TokenForOrg(ctx context.Context, org string) (string, error) {
	installationID, err := c.installationID(ctx, org)
	if err != nil {
		return "", err
	}
	return c.installationToken(ctx, installationID)
}

// HTTPClient returns an authenticated HTTP client for an organization.
func (c *AppClient) HTTPClient(ctx context.Context, org string) (*http.Client, error) {
	token, err := c.TokenForOrg(ctx, org)
	if err != nil {
		return nil, err
	}

	ts := oauth2.StaticTokenSource(&oauth2.Token{AccessToken: token})
	return oauth2.NewClient(ctx, ts), nil
}
