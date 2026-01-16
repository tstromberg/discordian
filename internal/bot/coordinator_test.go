package bot

import (
	"context"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/config"
	"github.com/codeGROOVE-dev/discordian/internal/format"
	"github.com/codeGROOVE-dev/discordian/internal/state"
)

// Mock implementations

type mockDiscordClient struct {
	postedMessages  []postedMessage
	updatedMessages []updatedMessage
	forumThreads    []forumThread
	sentDMs         []sentDM
	channelIDs      map[string]string
	forumChannels   map[string]bool
	usersInGuild    map[string]bool
	botInChannel    map[string]bool
	guildID         string
}

type postedMessage struct {
	channelID string
	text      string
}

type updatedMessage struct {
	channelID string
	messageID string
	text      string
}

type forumThread struct {
	forumID string
	title   string
	content string
}

type sentDM struct {
	userID string
	text   string
}

func newMockDiscordClient() *mockDiscordClient {
	return &mockDiscordClient{
		channelIDs:    make(map[string]string),
		forumChannels: make(map[string]bool),
		usersInGuild:  make(map[string]bool),
		botInChannel:  make(map[string]bool),
		guildID:       "test-guild",
	}
}

func (m *mockDiscordClient) PostMessage(_ context.Context, channelID, text string) (string, error) {
	m.postedMessages = append(m.postedMessages, postedMessage{channelID, text})
	return "msg-" + channelID, nil
}

func (m *mockDiscordClient) UpdateMessage(_ context.Context, channelID, messageID, text string) error {
	m.updatedMessages = append(m.updatedMessages, updatedMessage{channelID, messageID, text})
	return nil
}

func (m *mockDiscordClient) PostForumThread(_ context.Context, forumID, title, content string) (threadID, messageID string, err error) {
	m.forumThreads = append(m.forumThreads, forumThread{forumID, title, content})
	return "thread-" + forumID, "msg-" + forumID, nil
}

func (m *mockDiscordClient) UpdateForumPost(_ context.Context, _, _, _, _ string) error {
	return nil
}

func (m *mockDiscordClient) ArchiveThread(_ context.Context, _ string) error {
	return nil
}

func (m *mockDiscordClient) SendDM(_ context.Context, userID, text string) (channelID, messageID string, err error) {
	m.sentDMs = append(m.sentDMs, sentDM{userID, text})
	return "dm-chan-" + userID, "dm-msg-" + userID, nil
}

func (m *mockDiscordClient) UpdateDM(_ context.Context, _, _, _ string) error {
	return nil
}

func (m *mockDiscordClient) ResolveChannelID(_ context.Context, channelName string) string {
	if id, ok := m.channelIDs[channelName]; ok {
		return id
	}
	return channelName // Return name if not found (signals not found)
}

func (m *mockDiscordClient) LookupUserByUsername(_ context.Context, _ string) string {
	return ""
}

func (m *mockDiscordClient) IsBotInChannel(_ context.Context, channelID string) bool {
	return m.botInChannel[channelID]
}

func (m *mockDiscordClient) IsUserInGuild(_ context.Context, userID string) bool {
	return m.usersInGuild[userID]
}

func (m *mockDiscordClient) IsUserActive(_ context.Context, userID string) bool {
	// In tests, assume all users in guild are active
	return m.usersInGuild[userID]
}

func (m *mockDiscordClient) IsForumChannel(_ context.Context, channelID string) bool {
	return m.forumChannels[channelID]
}

func (m *mockDiscordClient) GuildID() string {
	return m.guildID
}

func (m *mockDiscordClient) FindForumThread(_ context.Context, _, _ string) (threadID, messageID string, found bool) {
	return "", "", false
}

func (m *mockDiscordClient) FindChannelMessage(_ context.Context, _, _ string) (string, bool) {
	return "", false
}

func (m *mockDiscordClient) FindDMForPR(_ context.Context, _, _ string) (channelID, messageID string, found bool) {
	return "", "", false
}

func (m *mockDiscordClient) MessageContent(_ context.Context, _, _ string) (string, error) {
	return "", nil
}

type mockConfigManager struct {
	configs  map[string]*config.DiscordConfig
	channels map[string][]string // org:repo -> channels
}

func newMockConfigManager() *mockConfigManager {
	return &mockConfigManager{
		configs:  make(map[string]*config.DiscordConfig),
		channels: make(map[string][]string),
	}
}

func (m *mockConfigManager) LoadConfig(_ context.Context, _ string) error {
	return nil
}

func (m *mockConfigManager) ReloadConfig(_ context.Context, _ string) error {
	return nil
}

func (m *mockConfigManager) Config(org string) (*config.DiscordConfig, bool) {
	cfg, ok := m.configs[org]
	return cfg, ok
}

func (m *mockConfigManager) ChannelsForRepo(org, repo string) []string {
	key := org + ":" + repo
	if channels, ok := m.channels[key]; ok {
		return channels
	}
	return []string{repo} // Default: use repo name as channel
}

func (m *mockConfigManager) ChannelType(_, _ string) string {
	return "text"
}

func (m *mockConfigManager) DiscordUserID(_, _ string) string {
	return ""
}

func (m *mockConfigManager) ReminderDMDelay(_, _ string) int {
	return 65
}

func (m *mockConfigManager) GuildID(_ string) string {
	return "test-guild"
}

func (m *mockConfigManager) SetGitHubClient(_ string, _ any) {}

type mockTurnClient struct {
	responses map[string]*CheckResponse
}

func newMockTurnClient() *mockTurnClient {
	return &mockTurnClient{
		responses: make(map[string]*CheckResponse),
	}
}

func (m *mockTurnClient) Check(_ context.Context, prURL, _ string, _ time.Time) (*CheckResponse, error) {
	if resp, ok := m.responses[prURL]; ok {
		return resp, nil
	}
	return &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "testuser",
			State:  "open",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
		},
	}, nil
}

type mockUserMapper struct {
	mappings map[string]string // github -> discord
}

func newMockUserMapper() *mockUserMapper {
	return &mockUserMapper{
		mappings: make(map[string]string),
	}
}

func (m *mockUserMapper) DiscordID(_ context.Context, githubUsername string) string {
	return m.mappings[githubUsername]
}

func (m *mockUserMapper) Mention(_ context.Context, githubUsername string) string {
	if id, ok := m.mappings[githubUsername]; ok {
		return "<@" + id + ">"
	}
	return githubUsername
}

// Tests

func TestNewCoordinator(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	if coord == nil {
		t.Fatal("NewCoordinator() returned nil")
	}
	if coord.org != "testorg" {
		t.Errorf("coord.org = %q, want %q", coord.org, "testorg")
	}
}

func TestCoordinator_ProcessEvent_InvalidURL(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "not-a-valid-url",
		Type:       "push",
		DeliveryID: "delivery-1",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should not post any messages for invalid URL
	if len(discord.postedMessages) > 0 {
		t.Errorf("Should not post messages for invalid URL, got %d", len(discord.postedMessages))
	}
}

func TestCoordinator_ProcessEvent_ConfigRepoSkipped(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/.codeGROOVE/pull/1",
		Type:       "push",
		DeliveryID: "delivery-1",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should not post messages for .codeGROOVE repo
	if len(discord.postedMessages) > 0 {
		t.Errorf("Should not post messages for .codeGROOVE repo, got %d", len(discord.postedMessages))
	}
}

func TestCoordinator_ProcessEvent_DeduplicateByDeliveryID(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-123",
	}

	// Process same event twice
	coord.ProcessEvent(ctx, event)
	coord.Wait()
	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should only post once due to deduplication
	if len(discord.postedMessages) != 1 {
		t.Errorf("Should post only once due to dedup, got %d messages", len(discord.postedMessages))
	}
}

func TestCoordinator_ProcessEvent_TextChannel(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = false

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR Title",
			Author: "alice",
			State:  "open",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-text-1",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	if len(discord.postedMessages) != 1 {
		t.Fatalf("Expected 1 posted message, got %d", len(discord.postedMessages))
	}
	if discord.postedMessages[0].channelID != "chan-testrepo" {
		t.Errorf("Message posted to wrong channel: %s", discord.postedMessages[0].channelID)
	}
}

func TestCoordinator_ProcessEvent_ForumChannel(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/99",
		Type:       "push",
		DeliveryID: "delivery-forum-1",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	if len(discord.forumThreads) != 1 {
		t.Fatalf("Expected 1 forum thread, got %d", len(discord.forumThreads))
	}
}

func TestCoordinator_ProcessEvent_NoChannelsForRepo(t *testing.T) {
	discord := newMockDiscordClient()
	// Don't register any channel for the repo

	configMgr := newMockConfigManager()
	configMgr.channels["testorg:unknown-repo"] = []string{} // Explicitly no channels

	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/unknown-repo/pull/1",
		Type:       "push",
		DeliveryID: "delivery-nochan-1",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No messages should be posted
	if len(discord.postedMessages) > 0 || len(discord.forumThreads) > 0 {
		t.Error("Should not post when no channels configured for repo")
	}
}

func TestTagTracker(t *testing.T) {
	tracker := newTagTracker()

	prURL := "https://github.com/o/r/pull/1"
	username := "alice"

	// Initially not tagged
	if tracker.wasTagged(prURL, username) {
		t.Error("wasTagged() should return false initially")
	}

	// Mark as tagged
	tracker.mark(prURL, username)

	// Now should be tagged
	if !tracker.wasTagged(prURL, username) {
		t.Error("wasTagged() should return true after marking")
	}

	// Different user not tagged
	if tracker.wasTagged(prURL, "bob") {
		t.Error("wasTagged() should return false for different user")
	}

	// Different PR not tagged
	if tracker.wasTagged("https://github.com/o/r/pull/2", username) {
		t.Error("wasTagged() should return false for different PR")
	}
}

func TestBuildActionUsers(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	userMapper := newMockUserMapper()
	userMapper.mappings["alice"] = "111111111"
	userMapper.mappings["bob"] = "222222222"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	checkResp := &CheckResponse{
		Analysis: Analysis{
			NextAction: map[string]Action{
				"alice":   {Kind: "review", Reason: "needs review"},
				"bob":     {Kind: "fix_tests", Reason: "tests failing"},
				"_system": {Kind: "waiting", Reason: "system action"},
			},
		},
	}

	ctx := context.Background()
	users := coord.buildActionUsers(ctx, checkResp)

	// Should have 2 users (excluding _system)
	if len(users) != 2 {
		t.Errorf("buildActionUsers() returned %d users, want 2", len(users))
	}

	// Check that _system was skipped
	for _, u := range users {
		if u.Username == "_system" {
			t.Error("_system user should be skipped")
		}
	}

	// Check mentions are formatted correctly
	foundAlice := false
	for _, u := range users {
		if u.Username == "alice" {
			foundAlice = true
			if u.Mention != "<@111111111>" {
				t.Errorf("alice.Mention = %q, want <@111111111>", u.Mention)
			}
			if u.Action != format.ActionLabel("review") {
				t.Errorf("alice.Action = %q, want %q", u.Action, format.ActionLabel("review"))
			}
		}
	}
	if !foundAlice {
		t.Error("alice not found in action users")
	}
}

func TestParsePRURL(t *testing.T) {
	tests := []struct {
		name       string
		url        string
		wantOwner  string
		wantRepo   string
		wantNumber int
		wantOK     bool
	}{
		{
			name:       "valid URL",
			url:        "https://github.com/owner/repo/pull/123",
			wantOwner:  "owner",
			wantRepo:   "repo",
			wantNumber: 123,
			wantOK:     true,
		},
		{
			name:   "invalid - not enough parts",
			url:    "https://github.com/owner/repo",
			wantOK: false,
		},
		{
			name:   "invalid - not github.com",
			url:    "https://gitlab.com/owner/repo/pull/123",
			wantOK: false,
		},
		{
			name:   "invalid - not pull",
			url:    "https://github.com/owner/repo/issues/123",
			wantOK: false,
		},
		{
			name:   "invalid - non-numeric PR number",
			url:    "https://github.com/owner/repo/pull/abc",
			wantOK: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, ok := ParsePRURL(tt.url)
			if ok != tt.wantOK {
				t.Errorf("ParsePRURL() ok = %v, want %v", ok, tt.wantOK)
			}
			if ok && tt.wantOK {
				if info.Owner != tt.wantOwner {
					t.Errorf("ParsePRURL() owner = %q, want %q", info.Owner, tt.wantOwner)
				}
				if info.Repo != tt.wantRepo {
					t.Errorf("ParsePRURL() repo = %q, want %q", info.Repo, tt.wantRepo)
				}
				if info.Number != tt.wantNumber {
					t.Errorf("ParsePRURL() number = %d, want %d", info.Number, tt.wantNumber)
				}
			}
		})
	}
}

func TestFormatPRURL(t *testing.T) {
	url := FormatPRURL("owner", "repo", 123)
	expected := "https://github.com/owner/repo/pull/123"
	if url != expected {
		t.Errorf("FormatPRURL() = %q, want %q", url, expected)
	}
}

func TestCoordinator_Wait(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	// Wait should not block when no events are processing
	done := make(chan struct{})
	go func() {
		coord.Wait()
		close(done)
	}()

	select {
	case <-done:
		// Good, Wait returned
	case <-time.After(time.Second):
		t.Error("Wait() blocked unexpectedly")
	}
}

func TestCoordinator_PollAndReconcile_NoSearcher(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord:  discord,
		Config:   configMgr,
		Store:    store,
		Turn:     turn,
		Org:      "testorg",
		Searcher: nil, // No searcher
	})

	ctx := context.Background()

	// Should not panic without searcher
	coord.PollAndReconcile(ctx)
}

// mockPRSearcher implements PRSearcher for testing
type mockPRSearcher struct {
	openPRs   []PRSearchResult
	closedPRs []PRSearchResult
	openErr   error
	closedErr error
}

func (m *mockPRSearcher) ListOpenPRs(_ context.Context, _ string, _ int) ([]PRSearchResult, error) {
	return m.openPRs, m.openErr
}

func (m *mockPRSearcher) ListClosedPRs(_ context.Context, _ string, _ int) ([]PRSearchResult, error) {
	return m.closedPRs, m.closedErr
}

func TestCoordinator_PollAndReconcile_WithSearcher(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	searcher := &mockPRSearcher{
		openPRs: []PRSearchResult{
			{
				URL:       "https://github.com/testorg/testrepo/pull/1",
				UpdatedAt: time.Now(),
			},
		},
		closedPRs: []PRSearchResult{
			{
				URL:       "https://github.com/testorg/testrepo/pull/2",
				UpdatedAt: time.Now(),
			},
		},
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord:  discord,
		Config:   configMgr,
		Store:    store,
		Turn:     turn,
		Org:      "testorg",
		Searcher: searcher,
	})

	ctx := context.Background()
	coord.PollAndReconcile(ctx)

	// Should have processed both PRs
	if len(discord.postedMessages) != 2 {
		t.Errorf("Expected 2 posted messages, got %d", len(discord.postedMessages))
	}
}

func TestCoordinator_PollAndReconcile_SearcherErrors(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	searcher := &mockPRSearcher{
		openErr:   context.DeadlineExceeded,
		closedErr: context.DeadlineExceeded,
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord:  discord,
		Config:   configMgr,
		Store:    store,
		Turn:     turn,
		Org:      "testorg",
		Searcher: searcher,
	})

	ctx := context.Background()

	// Should not panic on errors
	coord.PollAndReconcile(ctx)
}

func TestCoordinator_ProcessEvent_ForumChannelUpdate(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	// Pre-save thread info so update path is taken
	ctx := context.Background()
	threadInfo := state.ThreadInfo{
		ThreadID:    "existing-thread",
		MessageID:   "existing-msg",
		ChannelID:   "chan-testrepo",
		ChannelType: "forum",
	}
	if err := store.SaveThread(ctx, "testorg", "testrepo", 42, "chan-testrepo", threadInfo); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-forum-update",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should not create new thread since update succeeded
	if len(discord.forumThreads) != 0 {
		t.Errorf("Should update existing thread, not create new. Got %d new threads", len(discord.forumThreads))
	}
}

func TestCoordinator_ProcessEvent_ForumChannelArchive(t *testing.T) {
	discord := &mockDiscordClientWithArchive{
		mockDiscordClient: newMockDiscordClient(),
		archived:          make([]string, 0),
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
			Merged: true, // This should trigger archive
		},
	}

	// Pre-save thread info
	ctx := context.Background()
	threadInfo := state.ThreadInfo{
		ThreadID:    "existing-thread",
		MessageID:   "existing-msg",
		ChannelID:   "chan-testrepo",
		ChannelType: "forum",
	}
	if err := store.SaveThread(ctx, "testorg", "testrepo", 42, "chan-testrepo", threadInfo); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-forum-archive",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should have archived the thread
	if len(discord.archived) != 1 {
		t.Errorf("Expected 1 archived thread, got %d", len(discord.archived))
	}
}

type mockDiscordClientWithArchive struct {
	*mockDiscordClient

	archived []string
}

func (m *mockDiscordClientWithArchive) ArchiveThread(_ context.Context, threadID string) error {
	m.archived = append(m.archived, threadID)
	return nil
}

func TestCoordinator_ProcessEvent_TextChannelUpdate(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = false

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	// Pre-save message info
	ctx := context.Background()
	threadInfo := state.ThreadInfo{
		MessageID:   "existing-msg",
		ChannelID:   "chan-testrepo",
		ChannelType: "text",
	}
	if err := store.SaveThread(ctx, "testorg", "testrepo", 42, "chan-testrepo", threadInfo); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-text-update",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should update existing message, not create new
	if len(discord.postedMessages) != 0 {
		t.Errorf("Should update existing message, not create new. Got %d new messages", len(discord.postedMessages))
	}
	if len(discord.updatedMessages) != 1 {
		t.Errorf("Expected 1 updated message, got %d", len(discord.updatedMessages))
	}
}

func TestCoordinator_QueueDMNotifications_MergedPR(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-alice"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
			Merged: true, // Merged PR - no DMs
		},
		Analysis: Analysis{
			NextAction: map[string]Action{
				"alice": {Kind: "none", Reason: "merged"},
			},
		},
	}

	userMapper := newMockUserMapper()
	userMapper.mappings["alice"] = "discord-alice"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-merged",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No DMs should be queued for merged PR
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending DMs for merged PR, got %d", len(pending))
	}
}

func TestCoordinator_QueueDMNotifications_NoMapping(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	userMapper := newMockUserMapper()
	// No mapping for bob

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-nomap",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No DMs should be queued - no mapping
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending DMs without mapping, got %d", len(pending))
	}
}

func TestCoordinator_QueueDMNotifications_UserNotInGuild(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	// bob is NOT in guild

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	userMapper := newMockUserMapper()
	userMapper.mappings["bob"] = "discord-bob"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-notinguild",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No DMs should be queued - user not in guild
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending DMs for user not in guild, got %d", len(pending))
	}
}

func TestCoordinator_QueueDMNotifications_DelayDisabled(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-bob"] = true

	configMgr := &mockConfigManagerWithDelay{
		mockConfigManager: newMockConfigManager(),
		delay:             0, // DMs disabled
	}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	userMapper := newMockUserMapper()
	userMapper.mappings["bob"] = "discord-bob"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-nodelay",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No DMs should be queued - delay is 0 (disabled)
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending DMs with delay=0, got %d", len(pending))
	}
}

type mockConfigManagerWithDelay struct {
	*mockConfigManager

	delay int
}

func (m *mockConfigManagerWithDelay) ReminderDMDelay(_, _ string) int {
	return m.delay
}

func TestCoordinator_QueueDMNotifications_Tagged(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-bob"] = true

	configMgr := &mockConfigManagerWithDelay{
		mockConfigManager: newMockConfigManager(),
		delay:             30, // 30 minute delay
	}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	userMapper := newMockUserMapper()
	userMapper.mappings["bob"] = "discord-bob"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-tagged",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// DM should be queued but delayed (user was tagged in channel)
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("Expected 1 pending DM, got %d", len(pending))
	}

	// Since user was tagged in channel (mention contains <@), DM should be delayed
	// The DM should be scheduled for ~30 minutes from now
	if pending[0].SendAt.Before(time.Now().Add(25 * time.Minute)) {
		t.Error("DM should be delayed ~30 minutes since user was tagged")
	}
}

func TestCoordinator_QueueDMNotifications_NotTagged(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-bob"] = true

	configMgr := &mockConfigManagerWithDelay{
		mockConfigManager: newMockConfigManager(),
		delay:             30, // 30 minute delay
	}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	userMapper := &mockUserMapperNoMention{
		mappings: map[string]string{"bob": "discord-bob"},
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-nottagged",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// DM should be queued immediately (user was NOT tagged - no channel mention)
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("Expected 1 pending DM, got %d", len(pending))
	}

	// Since user was NOT tagged, DM should be immediate (within 1 second)
	if pending[0].SendAt.After(time.Now().Add(time.Second)) {
		t.Error("DM should be immediate since user was not tagged in channel")
	}
}

// mockUserMapperNoMention returns username as-is (no Discord mention format)
type mockUserMapperNoMention struct {
	mappings map[string]string
}

func (m *mockUserMapperNoMention) DiscordID(_ context.Context, githubUsername string) string {
	return m.mappings[githubUsername]
}

func (m *mockUserMapperNoMention) Mention(_ context.Context, githubUsername string) string {
	return githubUsername // No <@ prefix, so user won't be tracked as tagged
}

func TestCoordinator_ProcessChannel_NotResolved(t *testing.T) {
	discord := newMockDiscordClient()
	// Don't add channelID mapping - resolution will fail

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-noresolve",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No messages should be posted - channel not resolved
	if len(discord.postedMessages) > 0 {
		t.Errorf("Should not post when channel not resolved, got %d", len(discord.postedMessages))
	}
}

func TestCoordinator_ProcessChannel_BotNotInChannel(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	// Don't add bot to channel

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-notinchan",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No messages should be posted - bot not in channel
	if len(discord.postedMessages) > 0 {
		t.Errorf("Should not post when bot not in channel, got %d", len(discord.postedMessages))
	}
}

func TestCoordinator_GetPRLock(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	prURL := "https://github.com/owner/repo/pull/123"

	// Get lock twice for same URL - should be same lock
	lock1 := coord.prLocks.get(prURL)
	lock2 := coord.prLocks.get(prURL)

	if lock1 != lock2 {
		t.Error("prLocks.get should return same lock for same URL")
	}

	// Different URL should get different lock
	lock3 := coord.prLocks.get("https://github.com/owner/repo/pull/456")
	if lock1 == lock3 {
		t.Error("prLocks.get should return different lock for different URL")
	}
}

func TestCoordinator_ReconcilePR_UnchangedContent(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	ctx := context.Background()

	prURL := "https://github.com/testorg/testrepo/pull/1"
	updatedAt := time.Now()

	// Generate the exact content that will be produced
	// Default mock returns: Title "Test PR", Author "testuser", WorkflowState "ASSIGNED_WAITING_FOR_REVIEW"
	expectedContent := format.ChannelMessage(format.ChannelMessageParams{
		Owner:       "testorg",
		Repo:        "testrepo",
		Number:      1,
		Title:       "Test PR",
		Author:      "testuser",
		State:       format.StateNeedsReview, // ASSIGNED_WAITING_FOR_REVIEW maps to StateNeedsReview
		ActionUsers: nil,
		PRURL:       prURL,
		ChannelName: "testrepo",
	})

	threadInfo := state.ThreadInfo{
		MessageID:   "existing-msg",
		ChannelID:   "chan-testrepo",
		ChannelType: "text",
		LastState:   "needs_review",
		MessageText: expectedContent,
	}
	if err := store.SaveThread(ctx, "testorg", "testrepo", 1, "chan-testrepo", threadInfo); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	searcher := &mockPRSearcher{
		openPRs: []PRSearchResult{
			{
				URL:       prURL,
				UpdatedAt: updatedAt,
			},
		},
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord:  discord,
		Config:   configMgr,
		Store:    store,
		Turn:     turn,
		Org:      "testorg",
		Searcher: searcher,
	})

	coord.PollAndReconcile(ctx)

	// Polls should always process PRs (to catch check status changes),
	// but content comparison prevents unnecessary Discord API calls.
	// No new message should be posted since content is unchanged.
	if len(discord.postedMessages) > 0 {
		t.Errorf("Should not post new message when content unchanged, got %d messages", len(discord.postedMessages))
	}

	// No update should be sent since content matches
	if len(discord.updatedMessages) > 0 {
		t.Errorf("Should not update message when content unchanged, got %d updates", len(discord.updatedMessages))
	}
}

func TestCoordinator_ReconcilePR_CheckStatusChanged(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	ctx := context.Background()

	prURL := "https://github.com/testorg/testrepo/pull/1"
	updatedAt := time.Now()

	// Set up Turn API to return "tests_broken" state
	turn.responses[prURL] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "testuser",
			State:  "open",
		},
		Analysis: Analysis{
			WorkflowState: "PUBLISHED_WAITING_FOR_TESTS",
			Checks: Checks{
				Failing: 1, // Tests are now failing
			},
		},
	}

	// Create existing message with "tests_running" state
	// Simulates: PR was running tests, now they failed
	oldContent := "🧪 [testrepo#1](https://github.com/testorg/testrepo/pull/1?st=tests_running) · Test PR · testuser"
	threadInfo := state.ThreadInfo{
		MessageID:   "existing-msg",
		ChannelID:   "chan-testrepo",
		ChannelType: "text",
		LastState:   "tests_running",
		MessageText: oldContent,
	}
	if err := store.SaveThread(ctx, "testorg", "testrepo", 1, "chan-testrepo", threadInfo); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	searcher := &mockPRSearcher{
		openPRs: []PRSearchResult{
			{
				URL:       prURL,
				UpdatedAt: updatedAt,
			},
		},
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord:  discord,
		Config:   configMgr,
		Store:    store,
		Turn:     turn,
		Org:      "testorg",
		Searcher: searcher,
	})

	coord.PollAndReconcile(ctx)

	// This is the bug fix: polls should detect check status changes
	// even when PR's updated_at hasn't changed, and update the message.
	if len(discord.updatedMessages) != 1 {
		t.Errorf("Should update message when check status changes, got %d updates", len(discord.updatedMessages))
	}

	if len(discord.updatedMessages) > 0 {
		updated := discord.updatedMessages[0]
		// Verify state changed from tests_running (🧪) to tests_broken (🪳)
		if !contains(updated.text, "🪳") {
			t.Errorf("Updated message should show tests_broken emoji (🪳), got: %s", updated.text)
		}
		if !contains(updated.text, "st=tests_broken") {
			t.Errorf("Updated message should have tests_broken state, got: %s", updated.text)
		}
	}
}

func contains(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > len(substr) && (s[:len(substr)] == substr || s[len(s)-len(substr):] == substr || containsHelper(s, substr)))
}

func containsHelper(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

func TestCoordinator_ProcessEvent_ContextCanceled(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // Cancel immediately

	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-canceled",
	}

	// Should return without processing when context is canceled
	coord.ProcessEvent(ctx, event)
	// Give a moment for goroutine to potentially start
	time.Sleep(10 * time.Millisecond)
	coord.Wait()

	// No messages should be posted
	if len(discord.postedMessages) > 0 {
		t.Errorf("Should not post when context canceled, got %d", len(discord.postedMessages))
	}
}

func TestCoordinator_ProcessEvent_ProcessChannelError(t *testing.T) {
	discord := &mockDiscordClientWithError{
		mockDiscordClient: newMockDiscordClient(),
		postErr:           context.DeadlineExceeded,
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-posterr",
	}

	// Should not panic on error
	coord.ProcessEvent(ctx, event)
	coord.Wait()
}

type mockDiscordClientWithError struct {
	*mockDiscordClient

	postErr   error
	updateErr error
}

func (m *mockDiscordClientWithError) PostMessage(_ context.Context, channelID, text string) (string, error) {
	if m.postErr != nil {
		return "", m.postErr
	}
	m.postedMessages = append(m.postedMessages, postedMessage{channelID, text})
	return "msg-" + channelID, nil
}

func (m *mockDiscordClientWithError) UpdateMessage(_ context.Context, _, _, _ string) error {
	return m.updateErr
}

func TestCoordinator_TextChannel_UpdateFails_CreateNew(t *testing.T) {
	discord := &mockDiscordClientWithError{
		mockDiscordClient: newMockDiscordClient(),
		updateErr:         context.DeadlineExceeded, // Update fails
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = false

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	// Pre-save message info
	ctx := context.Background()
	threadInfo := state.ThreadInfo{
		MessageID:   "existing-msg",
		ChannelID:   "chan-testrepo",
		ChannelType: "text",
	}
	if err := store.SaveThread(ctx, "testorg", "testrepo", 42, "chan-testrepo", threadInfo); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-updatefail",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should have fallen back to creating new message
	if len(discord.postedMessages) != 1 {
		t.Errorf("Should fall back to creating new message, got %d", len(discord.postedMessages))
	}
}

func TestCoordinator_ForumChannel_UpdateFails_CreateNew(t *testing.T) {
	discord := &mockDiscordClientWithForumError{
		mockDiscordClient: newMockDiscordClient(),
		updateForumErr:    context.DeadlineExceeded, // Update fails
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	// Pre-save thread info
	ctx := context.Background()
	threadInfo := state.ThreadInfo{
		ThreadID:    "existing-thread",
		MessageID:   "existing-msg",
		ChannelID:   "chan-testrepo",
		ChannelType: "forum",
	}
	if err := store.SaveThread(ctx, "testorg", "testrepo", 42, "chan-testrepo", threadInfo); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-forumfail",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should have fallen back to creating new thread
	if len(discord.forumThreads) != 1 {
		t.Errorf("Should fall back to creating new thread, got %d", len(discord.forumThreads))
	}
}

type mockDiscordClientWithForumError struct {
	*mockDiscordClient

	updateForumErr error
}

func (m *mockDiscordClientWithForumError) UpdateForumPost(_ context.Context, _, _, _, _ string) error {
	return m.updateForumErr
}

func TestCoordinator_ForumChannel_Closed_Archive(t *testing.T) {
	discord := &mockDiscordClientWithArchive{
		mockDiscordClient: newMockDiscordClient(),
		archived:          make([]string, 0),
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
			Closed: true, // Closed PR should trigger archive
		},
	}

	// Pre-save thread info
	ctx := context.Background()
	threadInfo := state.ThreadInfo{
		ThreadID:    "existing-thread",
		MessageID:   "existing-msg",
		ChannelID:   "chan-testrepo",
		ChannelType: "forum",
	}
	if err := store.SaveThread(ctx, "testorg", "testrepo", 42, "chan-testrepo", threadInfo); err != nil {
		t.Fatalf("SaveThread() error = %v", err)
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-closed-archive",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should have archived the thread
	if len(discord.archived) != 1 {
		t.Errorf("Expected 1 archived thread for closed PR, got %d", len(discord.archived))
	}
}

func TestCoordinator_ForumChannel_CreateNewFails(t *testing.T) {
	discord := &mockDiscordClientWithForumCreateError{
		mockDiscordClient: newMockDiscordClient(),
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-forum-create-fail",
	}

	// Should not panic on error
	coord.ProcessEvent(ctx, event)
	coord.Wait()
}

type mockDiscordClientWithForumCreateError struct {
	*mockDiscordClient
}

func (m *mockDiscordClientWithForumCreateError) PostForumThread(_ context.Context, _, _, _ string) (threadID, messageID string, err error) {
	return "", "", context.DeadlineExceeded
}

func TestCoordinator_TextChannel_CreateNewFails(t *testing.T) {
	discord := &mockDiscordClientWithError{
		mockDiscordClient: newMockDiscordClient(),
		postErr:           context.DeadlineExceeded,
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = false

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-text-create-fail",
	}

	// Should not panic on error
	coord.ProcessEvent(ctx, event)
	coord.Wait()
}

func TestCoordinator_ReconcilePR_ProcessError(t *testing.T) {
	discord := newMockDiscordClient()
	// No channel set up, so processing will return early

	configMgr := newMockConfigManager()
	configMgr.channels["testorg:testrepo"] = []string{} // No channels
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	searcher := &mockPRSearcher{
		openPRs: []PRSearchResult{
			{
				URL:       "https://github.com/testorg/testrepo/pull/1",
				UpdatedAt: time.Now(),
			},
		},
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord:  discord,
		Config:   configMgr,
		Store:    store,
		Turn:     turn,
		Org:      "testorg",
		Searcher: searcher,
	})

	ctx := context.Background()
	coord.PollAndReconcile(ctx)

	// Should complete without error (poll key should still be marked)
}

func TestCoordinator_MultipleChannels(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["channel1"] = "chan-1"
	discord.channelIDs["channel2"] = "chan-2"
	discord.botInChannel["chan-1"] = true
	discord.botInChannel["chan-2"] = true

	configMgr := newMockConfigManager()
	configMgr.channels["testorg:testrepo"] = []string{"channel1", "channel2"}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-multi",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should post to both channels
	if len(discord.postedMessages) != 2 {
		t.Errorf("Expected 2 posted messages (to both channels), got %d", len(discord.postedMessages))
	}
}

func TestCoordinator_BuildActionUsers_NoUserMapper(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: nil, // No user mapper
		Org:        "testorg",
	})

	checkResp := &CheckResponse{
		Analysis: Analysis{
			NextAction: map[string]Action{
				"alice": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	ctx := context.Background()
	users := coord.buildActionUsers(ctx, checkResp)

	if len(users) != 1 {
		t.Fatalf("Expected 1 user, got %d", len(users))
	}
	// Without user mapper, mention should be the username itself
	if users[0].Mention != "alice" {
		t.Errorf("Mention = %q, want %q", users[0].Mention, "alice")
	}
}

func TestCoordinator_QueueDMNotifications_NoUserMapper(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: nil, // No user mapper
		Org:        "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-no-mapper",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No DMs should be queued without user mapper
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending DMs without user mapper, got %d", len(pending))
	}
}

func TestCoordinator_GetDMLock(t *testing.T) {
	discord := newMockDiscordClient()
	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	prURL := "https://github.com/owner/repo/pull/123"
	userID := "discord-123"

	// Get lock twice for same user+PR - should be same lock
	lock1 := coord.dmLocks.get(userID + ":" + prURL)
	lock2 := coord.dmLocks.get(userID + ":" + prURL)

	if lock1 != lock2 {
		t.Error("dmLocks.get should return same lock for same user+PR")
	}

	// Different user, same PR should get different lock
	lock3 := coord.dmLocks.get("discord-456:" + prURL)
	if lock1 == lock3 {
		t.Error("dmLocks.get should return different lock for different user")
	}

	// Same user, different PR should get different lock
	lock4 := coord.dmLocks.get(userID + ":https://github.com/owner/repo/pull/456")
	if lock1 == lock4 {
		t.Error("dmLocks.get should return different lock for different PR")
	}
}

func TestCoordinator_UpdateAllDMsForClosedPR(t *testing.T) {
	discord := &mockDiscordClientWithDMUpdate{
		mockDiscordClient: newMockDiscordClient(),
		updatedDMs:        make([]updatedDM, 0),
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-alice"] = true
	discord.usersInGuild["discord-bob"] = true

	configMgr := &mockConfigManagerWithDelay{
		mockConfigManager: newMockConfigManager(),
		delay:             30,
	}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	userMapper := newMockUserMapper()
	userMapper.mappings["alice"] = "discord-alice"
	userMapper.mappings["bob"] = "discord-bob"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	// Pre-save DM info for both alice and bob (simulating they received DMs earlier)
	if err := store.SaveDMInfo(ctx, "discord-alice", prURL, state.DMInfo{
		ChannelID:   "dm-chan-alice",
		MessageID:   "dm-msg-alice",
		MessageText: "Old message for alice",
		LastState:   "review",
		SentAt:      time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveDMInfo() error = %v", err)
	}
	if err := store.SaveDMInfo(ctx, "discord-bob", prURL, state.DMInfo{
		ChannelID:   "dm-chan-bob",
		MessageID:   "dm-msg-bob",
		MessageText: "Old message for bob",
		LastState:   "review",
		SentAt:      time.Now().Add(-time.Hour),
	}); err != nil {
		t.Fatalf("SaveDMInfo() error = %v", err)
	}

	// Now process a merge event (only alice has next action but both should get DM updates)
	turn.responses[prURL] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "charlie",
			Merged: true,
		},
		Analysis: Analysis{
			NextAction: map[string]Action{
				"alice": {Kind: "none", Reason: "merged"},
			},
		},
	}

	event := SprinklerEvent{
		URL:        prURL,
		Type:       "push",
		DeliveryID: "delivery-merged-update",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Both alice and bob should have their DMs updated
	if len(discord.updatedDMs) != 2 {
		t.Errorf("Expected 2 updated DMs (alice and bob), got %d", len(discord.updatedDMs))
	}

	// Verify both users' DM info was updated with merged state
	aliceInfo, _ := store.DMInfo(ctx, "discord-alice", prURL)
	if aliceInfo.LastState != "merged" {
		t.Errorf("alice LastState = %q, want merged", aliceInfo.LastState)
	}

	bobInfo, _ := store.DMInfo(ctx, "discord-bob", prURL)
	if bobInfo.LastState != "merged" {
		t.Errorf("bob LastState = %q, want merged", bobInfo.LastState)
	}
}

type updatedDM struct {
	channelID string
	messageID string
	text      string
}

type mockDiscordClientWithDMUpdate struct {
	*mockDiscordClient

	updatedDMs []updatedDM
}

func (m *mockDiscordClientWithDMUpdate) UpdateDM(_ context.Context, channelID, messageID, text string) error {
	m.updatedDMs = append(m.updatedDMs, updatedDM{channelID, messageID, text})
	return nil
}

func TestCoordinator_CancelPendingDMsOnClose(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-bob"] = true

	configMgr := &mockConfigManagerWithDelay{
		mockConfigManager: newMockConfigManager(),
		delay:             30,
	}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	userMapper := newMockUserMapper()
	userMapper.mappings["bob"] = "discord-bob"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	// First, process an open PR to queue a DM
	turn.responses[prURL] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	event1 := SprinklerEvent{
		URL:        prURL,
		Type:       "push",
		DeliveryID: "delivery-open",
	}

	coord.ProcessEvent(ctx, event1)
	coord.Wait()

	// Verify DM was queued
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("Expected 1 pending DM after open PR, got %d", len(pending))
	}

	// Now merge the PR
	turn.responses[prURL] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
			Merged: true,
		},
		Analysis: Analysis{
			NextAction: map[string]Action{},
		},
	}

	event2 := SprinklerEvent{
		URL:        prURL,
		Type:       "push",
		DeliveryID: "delivery-merged",
	}

	coord.ProcessEvent(ctx, event2)
	coord.Wait()

	// Pending DM should be cancelled
	pending, err = store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending DMs after merge, got %d", len(pending))
	}
}

func TestCoordinator_QueuedDMUpdate(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-bob"] = true

	configMgr := &mockConfigManagerWithDelay{
		mockConfigManager: newMockConfigManager(),
		delay:             30,
	}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	userMapper := newMockUserMapper()
	userMapper.mappings["bob"] = "discord-bob"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	// First event: needs review
	turn.responses[prURL] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	event1 := SprinklerEvent{
		URL:        prURL,
		Type:       "push",
		DeliveryID: "delivery-review",
	}

	coord.ProcessEvent(ctx, event1)
	coord.Wait()

	// Verify initial queued DM
	pending1, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending1) != 1 {
		t.Fatalf("Expected 1 pending DM, got %d", len(pending1))
	}
	initialID := pending1[0].ID
	initialMsg := pending1[0].MessageText

	// Second event: state changes to fix_tests
	turn.responses[prURL] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "TESTS_FAILING",
			Checks:        Checks{Failing: 2},
			NextAction: map[string]Action{
				"bob": {Kind: "fix_tests", Reason: "tests failing"},
			},
		},
	}

	event2 := SprinklerEvent{
		URL:        prURL,
		Type:       "push",
		DeliveryID: "delivery-fix-tests",
	}

	coord.ProcessEvent(ctx, event2)
	coord.Wait()

	// Should still have 1 pending DM but with new message content
	pending2, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending2) != 1 {
		t.Fatalf("Expected 1 pending DM after state change, got %d", len(pending2))
	}

	// The old queued DM should be replaced (different ID)
	if pending2[0].ID == initialID {
		t.Error("Queued DM should be replaced with new one")
	}

	// Message content should be different
	if pending2[0].MessageText == initialMsg {
		t.Error("Message text should be updated for state change")
	}
}

func TestCoordinator_CrossInstanceRacePreventionForum(t *testing.T) {
	discord := &mockDiscordClientWithFindThread{
		mockDiscordClient: newMockDiscordClient(),
		foundThreadID:     "existing-thread-123",
		foundMsgID:        "existing-msg-456",
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = true

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-cross-instance",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should not create new thread since one was found
	if len(discord.forumThreads) != 0 {
		t.Errorf("Should not create new thread when existing found, got %d", len(discord.forumThreads))
	}

	// Verify thread info was saved
	info, exists := store.Thread(ctx, "testorg", "testrepo", 42, "chan-testrepo")
	if !exists {
		t.Error("Thread info should be saved")
	}
	if info.ThreadID != "existing-thread-123" {
		t.Errorf("ThreadID = %q, want existing-thread-123", info.ThreadID)
	}
}

type mockDiscordClientWithFindThread struct {
	*mockDiscordClient

	foundThreadID string
	foundMsgID    string
}

func (m *mockDiscordClientWithFindThread) FindForumThread(_ context.Context, _, _ string) (threadID, messageID string, found bool) {
	if m.foundThreadID != "" {
		return m.foundThreadID, m.foundMsgID, true
	}
	return "", "", false
}

func TestCoordinator_CrossInstanceRacePreventionText(t *testing.T) {
	discord := &mockDiscordClientWithFindMessage{
		mockDiscordClient: newMockDiscordClient(),
		foundMsgID:        "existing-msg-789",
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = false

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()
	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-cross-instance-text",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should not create new message since one was found
	if len(discord.postedMessages) != 0 {
		t.Errorf("Should not create new message when existing found, got %d", len(discord.postedMessages))
	}

	// Verify message info was saved
	info, exists := store.Thread(ctx, "testorg", "testrepo", 42, "chan-testrepo")
	if !exists {
		t.Error("Message info should be saved")
	}
	if info.MessageID != "existing-msg-789" {
		t.Errorf("MessageID = %q, want existing-msg-789", info.MessageID)
	}
}

type mockDiscordClientWithFindMessage struct {
	*mockDiscordClient

	foundMsgID string
}

func (m *mockDiscordClientWithFindMessage) FindChannelMessage(_ context.Context, _, _ string) (string, bool) {
	if m.foundMsgID != "" {
		return m.foundMsgID, true
	}
	return "", false
}

func TestCoordinator_DMHistorySearchFallback(t *testing.T) {
	discord := &mockDiscordClientWithFindDM{
		mockDiscordClient: newMockDiscordClient(),
		foundChannelID:    "dm-chan-found",
		foundMsgID:        "dm-msg-found",
		updatedDMs:        make([]updatedDM, 0),
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-bob"] = true

	configMgr := &mockConfigManagerWithDelay{
		mockConfigManager: newMockConfigManager(),
		delay:             30,
	}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	userMapper := newMockUserMapper()
	userMapper.mappings["bob"] = "discord-bob"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	// Note: No DMInfo in store - simulating restart scenario

	event := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-dm-fallback",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// Should have updated the found DM, not created new
	if len(discord.updatedDMs) != 1 {
		t.Errorf("Expected 1 DM update (from history search), got %d", len(discord.updatedDMs))
	}

	// No new DMs should be queued
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending DMs (should update existing), got %d", len(pending))
	}
}

type mockDiscordClientWithFindDM struct {
	*mockDiscordClient

	foundChannelID string
	foundMsgID     string
	updatedDMs     []updatedDM
}

func (m *mockDiscordClientWithFindDM) FindDMForPR(_ context.Context, _, _ string) (channelID, messageID string, found bool) {
	if m.foundChannelID != "" && m.foundMsgID != "" {
		return m.foundChannelID, m.foundMsgID, true
	}
	return "", "", false
}

func (m *mockDiscordClientWithFindDM) UpdateDM(_ context.Context, channelID, messageID, text string) error {
	m.updatedDMs = append(m.updatedDMs, updatedDM{channelID, messageID, text})
	return nil
}

func TestCoordinator_MessageContentComparisonSkipsUpdate(t *testing.T) {
	discord := newMockDiscordClient()
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.forumChannels["chan-testrepo"] = false

	configMgr := newMockConfigManager()
	store := state.NewMemoryStore()
	turn := newMockTurnClient()
	turn.responses["https://github.com/testorg/testrepo/pull/42"] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
			State:  "open",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
		},
	}

	coord := NewCoordinator(CoordinatorConfig{
		Discord: discord,
		Config:  configMgr,
		Store:   store,
		Turn:    turn,
		Org:     "testorg",
	})

	ctx := context.Background()

	// First event - creates message
	event1 := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-content-1",
	}
	coord.ProcessEvent(ctx, event1)
	coord.Wait()

	// Verify message was created
	if len(discord.postedMessages) != 1 {
		t.Fatalf("Expected 1 posted message, got %d", len(discord.postedMessages))
	}

	// Second event - same content, should skip update
	event2 := SprinklerEvent{
		URL:        "https://github.com/testorg/testrepo/pull/42",
		Type:       "push",
		DeliveryID: "delivery-content-2",
	}
	coord.ProcessEvent(ctx, event2)
	coord.Wait()

	// Should not post new message or update (content unchanged)
	if len(discord.postedMessages) != 1 {
		t.Errorf("Should not post new message for unchanged content, got %d", len(discord.postedMessages))
	}
	if len(discord.updatedMessages) != 0 {
		t.Errorf("Should not update message for unchanged content, got %d", len(discord.updatedMessages))
	}
}

func TestCoordinator_DMIdempotency(t *testing.T) {
	discord := &mockDiscordClientWithDMUpdate{
		mockDiscordClient: newMockDiscordClient(),
		updatedDMs:        make([]updatedDM, 0),
	}
	discord.channelIDs["testrepo"] = "chan-testrepo"
	discord.botInChannel["chan-testrepo"] = true
	discord.usersInGuild["discord-bob"] = true

	configMgr := &mockConfigManagerWithDelay{
		mockConfigManager: newMockConfigManager(),
		delay:             30,
	}
	store := state.NewMemoryStore()
	turn := newMockTurnClient()

	userMapper := newMockUserMapper()
	userMapper.mappings["bob"] = "discord-bob"

	coord := NewCoordinator(CoordinatorConfig{
		Discord:    discord,
		Config:     configMgr,
		Store:      store,
		Turn:       turn,
		UserMapper: userMapper,
		Org:        "testorg",
	})

	ctx := context.Background()
	prURL := "https://github.com/testorg/testrepo/pull/42"

	// Pre-save DM info with "needs_review" state (must match format.StateNeedsReview)
	if err := store.SaveDMInfo(ctx, "discord-bob", prURL, state.DMInfo{
		ChannelID:   "dm-chan-bob",
		MessageID:   "dm-msg-bob",
		MessageText: "Review needed",
		LastState:   "needs_review",
		SentAt:      time.Now().Add(-time.Minute),
	}); err != nil {
		t.Fatalf("SaveDMInfo() error = %v", err)
	}

	// Process event with same state
	turn.responses[prURL] = &CheckResponse{
		PullRequest: PRInfo{
			Title:  "Test PR",
			Author: "alice",
		},
		Analysis: Analysis{
			WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW",
			NextAction: map[string]Action{
				"bob": {Kind: "review", Reason: "needs review"},
			},
		},
	}

	event := SprinklerEvent{
		URL:        prURL,
		Type:       "push",
		DeliveryID: "delivery-same-state",
	}

	coord.ProcessEvent(ctx, event)
	coord.Wait()

	// No DM updates should have been made (state unchanged)
	if len(discord.updatedDMs) != 0 {
		t.Errorf("Expected 0 DM updates for unchanged state, got %d", len(discord.updatedDMs))
	}

	// No new pending DMs should be queued
	pending, err := store.PendingDMs(ctx, time.Now().Add(time.Hour))
	if err != nil {
		t.Fatalf("PendingDMs() error = %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("Expected 0 pending DMs for unchanged state, got %d", len(pending))
	}
}
