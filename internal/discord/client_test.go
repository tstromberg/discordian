package discord

import (
	"context"
	"strings"
	"testing"

	"github.com/bwmarrin/discordgo"
)

// findUserInMembers tests the matching logic without Discord API calls.
// This mirrors the logic in LookupUserByUsername but operates on a slice of members.
func findUserInMembers(username string, members []*discordgo.Member) (userID, matchType string) {
	// Skip empty usernames
	if username == "" {
		return "", ""
	}

	// Tier 1: Exact match (Username takes precedence over GlobalName)
	for _, member := range members {
		if member.User.Username == username {
			return member.User.ID, "username"
		}
	}
	for _, member := range members {
		if member.User.GlobalName == username {
			return member.User.ID, "global_name"
		}
	}

	// Tier 2: Case-insensitive match (Username takes precedence over GlobalName)
	for _, member := range members {
		if strings.EqualFold(member.User.Username, username) {
			return member.User.ID, "username_case_insensitive"
		}
	}
	for _, member := range members {
		if strings.EqualFold(member.User.GlobalName, username) {
			return member.User.ID, "global_name_case_insensitive"
		}
	}

	lowerUsername := strings.ToLower(username)

	// Tier 3: Prefix match (only if unambiguous)
	type prefixMatch struct {
		member    *discordgo.Member
		matchType string
	}
	var matches []prefixMatch

	for _, member := range members {
		if strings.HasPrefix(strings.ToLower(member.User.Username), lowerUsername) {
			matches = append(matches, prefixMatch{member: member, matchType: "username_prefix"})
		} else if strings.HasPrefix(strings.ToLower(member.User.GlobalName), lowerUsername) {
			matches = append(matches, prefixMatch{member: member, matchType: "global_name_prefix"})
		}
	}

	if len(matches) == 1 {
		return matches[0].member.User.ID, matches[0].matchType
	}

	return "", ""
}

func TestFindUserInMembers(t *testing.T) {
	tests := []struct {
		name          string
		username      string
		members       []*discordgo.Member
		wantID        string
		wantMatchType string
	}{
		{
			name:     "exact username match",
			username: "alice",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "111",
			wantMatchType: "username",
		},
		{
			name:     "exact global name match",
			username: "Alice Smith",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "111",
			wantMatchType: "global_name",
		},
		{
			name:     "case-insensitive username match",
			username: "ALICE",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "111",
			wantMatchType: "username_case_insensitive",
		},
		{
			name:     "case-insensitive global name match",
			username: "alice smith",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "111",
			wantMatchType: "global_name_case_insensitive",
		},
		{
			name:     "prefix match - username - unambiguous",
			username: "ali",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "111",
			wantMatchType: "username_prefix",
		},
		{
			name:     "prefix match - global name - unambiguous",
			username: "Alice S",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "111",
			wantMatchType: "global_name_prefix",
		},
		{
			name:     "prefix match - ambiguous - should fail",
			username: "al",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "alex", GlobalName: "Alex Jones"}},
				{User: &discordgo.User{ID: "333", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "",
			wantMatchType: "",
		},
		{
			name:     "no match",
			username: "charlie",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "",
			wantMatchType: "",
		},
		{
			name:     "exact match takes precedence over prefix",
			username: "alice",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "alicejones", GlobalName: "Alice Jones"}},
			},
			wantID:        "111",
			wantMatchType: "username",
		},
		{
			name:     "case-insensitive takes precedence over prefix",
			username: "ALICE",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "ALICEJONES", GlobalName: "Alice Jones"}},
			},
			wantID:        "111",
			wantMatchType: "username_case_insensitive",
		},
		{
			name:     "empty username - should not match empty fields",
			username: "",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "", GlobalName: ""}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "",
			wantMatchType: "",
		},
		{
			name:     "prefix match with case insensitivity",
			username: "ALI",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "Alice Smith"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Bob Jones"}},
			},
			wantID:        "111",
			wantMatchType: "username_prefix",
		},
		{
			name:     "username match preferred over global name when both match",
			username: "bob",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "111", Username: "alice", GlobalName: "bob"}},
				{User: &discordgo.User{ID: "222", Username: "bob", GlobalName: "Robert"}},
			},
			wantID:        "222",
			wantMatchType: "username",
		},
		{
			name:     "real world case - thomstrom exact match",
			username: "thomstrom",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "123456", Username: "thomstrom", GlobalName: "thomstrom"}},
				{User: &discordgo.User{ID: "789012", Username: "alice", GlobalName: "Alice"}},
			},
			wantID:        "123456",
			wantMatchType: "username",
		},
		{
			name:     "real world case - THOMSTROM case insensitive",
			username: "THOMSTROM",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "123456", Username: "thomstrom", GlobalName: "thomstrom"}},
				{User: &discordgo.User{ID: "789012", Username: "alice", GlobalName: "Alice"}},
			},
			wantID:        "123456",
			wantMatchType: "username_case_insensitive",
		},
		{
			name:     "real world case - thom prefix",
			username: "thom",
			members: []*discordgo.Member{
				{User: &discordgo.User{ID: "123456", Username: "thomstrom", GlobalName: "Thomas S"}},
				{User: &discordgo.User{ID: "789012", Username: "alice", GlobalName: "Alice"}},
			},
			wantID:        "123456",
			wantMatchType: "username_prefix",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			gotID, gotMatchType := findUserInMembers(tt.username, tt.members)

			if gotID != tt.wantID {
				t.Errorf("findUserInMembers() ID = %v, want %v", gotID, tt.wantID)
			}

			if gotMatchType != tt.wantMatchType {
				t.Errorf("findUserInMembers() matchType = %v, want %v", gotMatchType, tt.wantMatchType)
			}
		})
	}
}

// TestClient_SetGuildID tests setting the guild ID.
func TestClient_SetGuildID(t *testing.T) {
	client := &Client{}

	client.SetGuildID("test-guild-123")

	if client.guildID != "test-guild-123" {
		t.Errorf("SetGuildID() guildID = %q, want %q", client.guildID, "test-guild-123")
	}
}

// TestClient_GuildID tests getting the guild ID.
func TestClient_GuildID(t *testing.T) {
	client := &Client{guildID: "test-guild-456"}

	got := client.GuildID()
	if got != "test-guild-456" {
		t.Errorf("GuildID() = %q, want %q", got, "test-guild-456")
	}
}

// TestClient_Session tests getting the session.
func TestClient_Session(t *testing.T) {
	mockSession := &discordgo.Session{}
	client := &Client{session: mockSession}

	got := client.Session()
	if got != mockSession {
		t.Error("Session() should return the same session")
	}
}

// TestIsAllDigits tests the isAllDigits helper function.
func TestIsAllDigits(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  bool
	}{
		{"all digits", "123456", true},
		{"has letters", "123abc", false},
		{"has spaces", "123 456", false},
		{"has special chars", "123-456", false},
		{"empty string", "", false}, // Empty string is not all digits
		{"single digit", "5", true},
		{"single letter", "a", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isAllDigits(tt.input)
			if got != tt.want {
				t.Errorf("isAllDigits(%q) = %v, want %v", tt.input, got, tt.want)
			}
		})
	}
}

// TestClient_ResolveChannelID_LooksLikeID tests ResolveChannelID when input looks like an ID.
func TestClient_ResolveChannelID_LooksLikeID(t *testing.T) {
	client := &Client{
		guildID:      "test-guild",
		channelCache: make(map[string]string),
	}

	// 20-character numeric string should be returned as-is (looks like a Discord ID)
	channelID := "12345678901234567890"
	got := client.ResolveChannelID(context.Background(), channelID)

	if got != channelID {
		t.Errorf("ResolveChannelID(%q) = %q, want %q", channelID, got, channelID)
	}
}

// TestClient_ResolveChannelID_CacheHit tests ResolveChannelID with cached channel.
func TestClient_ResolveChannelID_CacheHit(t *testing.T) {
	client := &Client{
		guildID: "test-guild",
		channelCache: map[string]string{
			"general": "111222333444555666",
		},
	}

	got := client.ResolveChannelID(context.Background(), "general")
	want := "111222333444555666"

	if got != want {
		t.Errorf("ResolveChannelID(\"general\") = %q, want %q", got, want)
	}
}

// TestClient_ResolveChannelID_NoGuildID tests ResolveChannelID when no guild ID is set.
func TestClient_ResolveChannelID_NoGuildID(t *testing.T) {
	client := &Client{
		guildID:      "",
		channelCache: make(map[string]string),
	}

	channelName := "general"
	got := client.ResolveChannelID(context.Background(), channelName)

	// Should return the input unchanged when no guild ID is set
	if got != channelName {
		t.Errorf("ResolveChannelID(%q) = %q, want %q", channelName, got, channelName)
	}
}

// TestClient_ChannelType_CacheHit tests ChannelType with cached channel type.
func TestClient_ChannelType_CacheHit(t *testing.T) {
	client := &Client{
		channelTypeCache: map[string]discordgo.ChannelType{
			"123456": discordgo.ChannelTypeGuildText,
		},
	}

	got, err := client.ChannelType(context.Background(), "123456")
	if err != nil {
		t.Fatalf("ChannelType() error = %v, want nil", err)
	}

	if got != discordgo.ChannelTypeGuildText {
		t.Errorf("ChannelType() = %v, want %v", got, discordgo.ChannelTypeGuildText)
	}
}

// TestClient_IsBotInChannel_NilState tests IsBotInChannel when session state is nil.
func TestClient_IsBotInChannel_NilState(t *testing.T) {
	client := &Client{
		session: &discordgo.Session{
			State: nil,
		},
	}

	got := client.IsBotInChannel(context.Background(), "some-channel-id")
	if got {
		t.Error("IsBotInChannel() = true, want false when session.State is nil")
	}
}

// TestClient_IsBotInChannel_NilUser tests IsBotInChannel when user is nil.
func TestClient_IsBotInChannel_NilUser(t *testing.T) {
	state := discordgo.NewState()
	state.User = nil

	client := &Client{
		session: &discordgo.Session{
			State: state,
		},
	}

	got := client.IsBotInChannel(context.Background(), "some-channel-id")
	if got {
		t.Error("IsBotInChannel() = true, want false when session.State.User is nil")
	}
}

// TestClient_IsUserInGuild_NoGuildID tests IsUserInGuild when no guild ID is set.
func TestClient_IsUserInGuild_NoGuildID(t *testing.T) {
	client := &Client{
		guildID: "",
		session: &discordgo.Session{},
	}

	got := client.IsUserInGuild(context.Background(), "user-123")
	if got {
		t.Error("IsUserInGuild() = true, want false when no guild ID is set")
	}
}

// TestClient_IsUserActive_NoGuildID tests IsUserActive when no guild ID is set.
func TestClient_IsUserActive_NoGuildID(t *testing.T) {
	client := &Client{
		guildID: "",
		session: &discordgo.Session{},
	}

	got := client.IsUserActive(context.Background(), "user-123")
	if got {
		t.Error("IsUserActive() = true, want false when no guild ID is set")
	}
}

// TestClient_GuildInfo_NoGuildID tests GuildInfo when no guild ID is set.
func TestClient_GuildInfo_NoGuildID(t *testing.T) {
	client := &Client{
		guildID: "",
		session: &discordgo.Session{},
	}

	_, err := client.GuildInfo(context.Background())
	if err == nil {
		t.Error("GuildInfo() error = nil, want error when no guild ID is set")
	}
	if err != nil && err.Error() != "no guild ID set" {
		t.Errorf("GuildInfo() error = %q, want %q", err.Error(), "no guild ID set")
	}
}

// TestClient_BotInfo_NilState tests BotInfo when session state is nil.
func TestClient_BotInfo_NilState(t *testing.T) {
	client := &Client{
		session: &discordgo.Session{
			State: nil,
		},
	}

	_, err := client.BotInfo(context.Background())
	if err == nil {
		t.Error("BotInfo() error = nil, want error when session.State is nil")
	}
	if err != nil && err.Error() != "bot user not available" {
		t.Errorf("BotInfo() error = %q, want %q", err.Error(), "bot user not available")
	}
}

// TestClient_BotInfo_NilUser tests BotInfo when user is nil.
func TestClient_BotInfo_NilUser(t *testing.T) {
	state := discordgo.NewState()
	state.User = nil

	client := &Client{
		session: &discordgo.Session{
			State: state,
		},
	}

	_, err := client.BotInfo(context.Background())
	if err == nil {
		t.Error("BotInfo() error = nil, want error when session.State.User is nil")
	}
	if err != nil && err.Error() != "bot user not available" {
		t.Errorf("BotInfo() error = %q, want %q", err.Error(), "bot user not available")
	}
}
