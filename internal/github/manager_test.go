package github

import (
	"testing"

	"github.com/google/go-github/v50/github"
)

// TestManager_AllOrgs tests retrieving all organizations.
func TestManager_AllOrgs(t *testing.T) {
	// Create a manager with a minimal AppClient
	manager := &Manager{
		appClient: &AppClient{},
		clients:   make(map[string]*OrgClient),
	}

	// Initially should be empty
	orgs := manager.AllOrgs()
	if len(orgs) != 0 {
		t.Errorf("AllOrgs() returned %d orgs, want 0", len(orgs))
	}

	// Add organizations via the manager's clients map
	manager.mu.Lock()
	manager.clients["org1"] = &OrgClient{org: "org1"}
	manager.clients["org2"] = &OrgClient{org: "org2"}
	manager.mu.Unlock()

	orgs = manager.AllOrgs()
	if len(orgs) != 2 {
		t.Errorf("AllOrgs() returned %d orgs, want 2", len(orgs))
	}

	// Check that both orgs are present
	orgMap := make(map[string]bool)
	for _, org := range orgs {
		orgMap[org] = true
	}

	if !orgMap["org1"] || !orgMap["org2"] {
		t.Errorf("AllOrgs() = %v, want [org1, org2]", orgs)
	}
}

// TestManager_ClientForOrg tests client retrieval for an organization.
func TestManager_ClientForOrg(t *testing.T) {
	manager := &Manager{
		appClient: &AppClient{},
		clients:   make(map[string]*OrgClient),
	}

	// Set up a client for an org
	orgClient := &OrgClient{org: "testorg"}
	manager.mu.Lock()
	manager.clients["testorg"] = orgClient
	manager.mu.Unlock()

	// Test getting existing org client
	got, ok := manager.ClientForOrg("testorg")
	if !ok {
		t.Error("ClientForOrg() returned false for existing org")
	}
	if got != orgClient {
		t.Error("ClientForOrg() returned wrong client")
	}

	// Test getting non-existent org client
	_, ok = manager.ClientForOrg("nonexistent")
	if ok {
		t.Error("ClientForOrg() returned true for non-existent org")
	}
}

// TestManager_AppClient tests the AppClient getter.
func TestManager_AppClient(t *testing.T) {
	appClient := &AppClient{}
	manager := &Manager{
		appClient: appClient,
		clients:   make(map[string]*OrgClient),
	}

	returnedClient := manager.AppClient()
	if returnedClient != appClient {
		t.Error("AppClient() should return the same client")
	}
}

// TestOrgClient_Org tests the Org getter.
func TestOrgClient_Org(t *testing.T) {
	orgClient := &OrgClient{org: "testorg"}

	got := orgClient.Org()
	if got != "testorg" {
		t.Errorf("Org() = %q, want %q", got, "testorg")
	}
}

// TestOrgClient_Client tests the Client getter.
func TestOrgClient_Client(t *testing.T) {
	mockClient := &github.Client{}
	orgClient := &OrgClient{client: mockClient}

	got := orgClient.Client()
	if got != mockClient {
		t.Error("Client() should return the same client")
	}
}
