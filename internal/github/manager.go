package github

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/google/go-github/v50/github"
)

// Manager manages GitHub App installations across multiple orgs.
type Manager struct {
	appClient             *AppClient
	clients               map[string]*OrgClient // org -> client
	allowPersonalAccounts bool
	mu                    sync.RWMutex
}

// OrgClient wraps a GitHub client for an org with additional functionality.
type OrgClient struct {
	appClient      *AppClient
	client         *github.Client
	org            string
	installationID int64
}

// NewManager creates a new GitHub installation manager.
func NewManager(ctx context.Context, appClient *AppClient, allowPersonalAccounts bool) (*Manager, error) {
	m := &Manager{
		appClient:             appClient,
		clients:               make(map[string]*OrgClient),
		allowPersonalAccounts: allowPersonalAccounts,
	}

	// Discover installations on startup
	if err := m.RefreshInstallations(ctx); err != nil {
		return nil, fmt.Errorf("initial installation discovery: %w", err)
	}

	return m, nil
}

// RefreshInstallations discovers all GitHub App installations.
func (m *Manager) RefreshInstallations(ctx context.Context) error {
	jwtClient := m.appClient.jwtClient(ctx)

	installations, _, err := jwtClient.Apps.ListInstallations(ctx, nil)
	if err != nil {
		return fmt.Errorf("list installations: %w", err)
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	// Track which orgs we found
	foundOrgs := make(map[string]bool)

	for _, inst := range installations {
		if inst.Account == nil || inst.Account.Login == nil {
			continue
		}

		org := *inst.Account.Login
		accountType := inst.Account.GetType()

		// Skip personal accounts unless explicitly allowed
		if accountType == "User" && !m.allowPersonalAccounts {
			slog.Debug("skipping personal account installation",
				"account", org,
				"type", accountType)
			continue
		}

		foundOrgs[org] = true

		// Always recreate client to get fresh token
		// GitHub App installation tokens expire after ~1 hour
		ghClient, err := m.appClient.ClientForOrg(ctx, org)
		if err != nil {
			slog.Error("failed to create client for org",
				"org", org,
				"error", err)
			continue
		}

		_, isNew := m.clients[org]
		m.clients[org] = &OrgClient{
			org:            org,
			installationID: *inst.ID,
			appClient:      m.appClient,
			client:         ghClient,
		}

		if !isNew {
			slog.Debug("refreshed GitHub client with new token",
				"org", org,
				"installation_id", *inst.ID)
		} else {
			slog.Info("discovered GitHub installation",
				"org", org,
				"installation_id", *inst.ID,
				"account_type", accountType)
		}
	}

	// Remove clients for orgs that no longer have installations
	for org := range m.clients {
		if !foundOrgs[org] {
			slog.Info("removing client for uninstalled org", "org", org)
			delete(m.clients, org)
		}
	}

	slog.Info("GitHub installations refreshed",
		"total", len(m.clients))

	return nil
}

// AllOrgs returns all orgs with GitHub App installations.
func (m *Manager) AllOrgs() []string {
	m.mu.RLock()
	defer m.mu.RUnlock()

	orgs := make([]string, 0, len(m.clients))
	for org := range m.clients {
		orgs = append(orgs, org)
	}
	return orgs
}

// ClientForOrg returns the client for an org.
func (m *Manager) ClientForOrg(org string) (*OrgClient, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	client, exists := m.clients[org]
	return client, exists
}

// Client returns the underlying GitHub API client.
func (c *OrgClient) Client() *github.Client {
	return c.client
}

// InstallationToken returns a fresh installation token.
func (c *OrgClient) InstallationToken(ctx context.Context) (string, error) {
	return c.appClient.installationToken(ctx, c.installationID)
}

// Org returns the organization name.
func (c *OrgClient) Org() string {
	return c.org
}

// AppClient returns the underlying AppClient for creating searchers.
func (m *Manager) AppClient() *AppClient {
	return m.appClient
}
