package discord

import (
	"strings"
	"testing"
	"time"

	"github.com/codeGROOVE-dev/discordian/internal/format"
)

func TestTruncate(t *testing.T) {
	tests := []struct {
		name   string
		input  string
		maxLen int
		want   string
	}{
		{"no truncation needed", "hello", 10, "hello"},
		{"exact length", "hello", 5, "hello"},
		{"needs truncation", "hello world", 8, "hello..."},
		{"very short max", "hello", 2, "he"},
		{"max 3", "hello", 3, "hel"},
		{"max 4", "hello", 4, "h..."},
		{"empty string", "", 10, ""},
		{"unicode", "hello world", 8, "hello..."},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := format.Truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
			if len(got) > tt.maxLen {
				t.Errorf("Truncate(%q, %d) length = %d, want <= %d", tt.input, tt.maxLen, len(got), tt.maxLen)
			}
		})
	}
}

func TestFormatDashboardEmbed(t *testing.T) {
	handler := &SlashCommandHandler{}

	t.Run("empty report with dashboard link", func(t *testing.T) {
		report := &PRReport{}
		embed := handler.formatDashboardEmbed(report, "https://dash.example.com", "")

		if embed.Author == nil || embed.Author.Name != "reviewGOOSE" {
			t.Error("Should have reviewGOOSE author")
		}
		if embed.Color != 0x57F287 {
			t.Errorf("Color = %x, want Discord green 0x57F287 (no PRs)", embed.Color)
		}
		// Should have dashboard link in Links field
		hasLinksField := false
		for _, field := range embed.Fields {
			if strings.Contains(field.Name, "Links") {
				hasLinksField = true
				if !strings.Contains(field.Value, "Personal") {
					t.Error("Links field should contain Personal dashboard link")
				}
			}
		}
		if !hasLinksField {
			t.Error("Should have Links field with dashboard link")
		}
	})

	t.Run("with incoming PRs", func(t *testing.T) {
		report := &PRReport{
			IncomingPRs: []PRSummary{
				{
					Repo:   "myrepo",
					Number: 42,
					Title:  "Fix the bug",
					Author: "alice",
					URL:    "https://github.com/o/myrepo/pull/42",
				},
			},
		}
		embed := handler.formatDashboardEmbed(report, "https://dash.example.com", "")

		// Should be yellow when there are PRs to review
		if embed.Color != 0xFEE75C {
			t.Errorf("Color = %x, want Discord yellow 0xFEE75C (has PRs)", embed.Color)
		}

		// Should have Reviewing field
		hasReviewingField := false
		for _, field := range embed.Fields {
			if strings.Contains(field.Name, "Reviewing") {
				hasReviewingField = true
				if !strings.Contains(field.Value, "myrepo#42") {
					t.Errorf("Field value should contain PR reference")
				}
				if !strings.Contains(field.Value, "alice") {
					t.Errorf("Field value should contain author name")
				}
			}
		}
		if !hasReviewingField {
			t.Error("Should have Reviewing field for incoming PRs")
		}
	})

	t.Run("with outgoing PRs", func(t *testing.T) {
		report := &PRReport{
			OutgoingPRs: []PRSummary{
				{
					Repo:   "myrepo",
					Number: 99,
					Title:  "New feature",
					URL:    "https://github.com/o/myrepo/pull/99",
				},
			},
		}
		embed := handler.formatDashboardEmbed(report, "https://dash.example.com", "")

		// Should have Your PRs field
		hasYourPRsField := false
		for _, field := range embed.Fields {
			if strings.Contains(field.Name, "Your PRs") {
				hasYourPRsField = true
				if !strings.Contains(field.Value, "myrepo#99") {
					t.Errorf("Field value should contain PR reference")
				}
			}
		}
		if !hasYourPRsField {
			t.Error("Should have Your PRs field for outgoing PRs")
		}
	})

	t.Run("with both sections", func(t *testing.T) {
		report := &PRReport{
			IncomingPRs: []PRSummary{
				{Repo: "repo1", Number: 1, Title: "PR1", URL: "url1", Author: "bob"},
			},
			OutgoingPRs: []PRSummary{
				{Repo: "repo2", Number: 2, Title: "PR2", URL: "url2"},
			},
		}
		embed := handler.formatDashboardEmbed(report, "https://dash.example.com", "")

		// Should have Reviewing, Your PRs, and Links fields
		if len(embed.Fields) != 3 {
			t.Fatalf("Fields = %d, want 3 (Reviewing, Your PRs, Links)", len(embed.Fields))
		}
	})

	t.Run("long title gets truncated", func(t *testing.T) {
		longTitle := strings.Repeat("x", 100)
		report := &PRReport{
			IncomingPRs: []PRSummary{
				{Repo: "repo", Number: 1, Title: longTitle, URL: "url", Author: "charlie"},
			},
		}
		embed := handler.formatDashboardEmbed(report, "https://dash.example.com", "")

		// Title should be truncated to 50 chars
		if strings.Contains(embed.Fields[0].Value, longTitle) {
			t.Error("Long title should be truncated")
		}
		if !strings.Contains(embed.Fields[0].Value, "...") {
			t.Error("Truncated title should contain ellipsis")
		}
	})

	t.Run("includes org links", func(t *testing.T) {
		report := &PRReport{}
		orgLinks := "\n\n**Organization Dashboards:**\n• myorg: [View Dashboard](https://example.com/orgs/myorg)\n"
		embed := handler.formatDashboardEmbed(report, "https://dash.example.com", orgLinks)

		// Should include org links in Links field
		hasLinksField := false
		for _, field := range embed.Fields {
			if strings.Contains(field.Name, "Links") {
				hasLinksField = true
				if !strings.Contains(field.Value, "myorg") {
					t.Error("Links field should include org links")
				}
			}
		}
		if !hasLinksField {
			t.Error("Should have Links field with org links")
		}
	})
}

func TestNewSlashCommandHandler(t *testing.T) {
	t.Run("with nil logger", func(t *testing.T) {
		handler := NewSlashCommandHandler(nil, nil)
		if handler.logger == nil {
			t.Error("logger should default to slog.Default()")
		}
		if handler.dashboardURL != "https://reviewgoose.dev" {
			t.Errorf("dashboardURL = %q, want default", handler.dashboardURL)
		}
	})
}

func TestSlashCommandHandler_SetDashboardURL(t *testing.T) {
	handler := NewSlashCommandHandler(nil, nil)

	customURL := "https://custom.example.com"
	handler.SetDashboardURL(customURL)

	if handler.dashboardURL != customURL {
		t.Errorf("dashboardURL = %q, want %q", handler.dashboardURL, customURL)
	}
}

func TestSlashCommandHandler_SetStatusGetter(t *testing.T) {
	handler := NewSlashCommandHandler(nil, nil)

	if handler.statusGetter != nil {
		t.Error("statusGetter should be nil initially")
	}

	// We can't easily test this without a mock, but we can verify the method exists
	handler.SetStatusGetter(nil)
}

func TestSlashCommandHandler_SetReportGetter(t *testing.T) {
	handler := NewSlashCommandHandler(nil, nil)

	if handler.reportGetter != nil {
		t.Error("reportGetter should be nil initially")
	}

	handler.SetReportGetter(nil)
}

func TestPRSummary_Fields(t *testing.T) {
	// Test that PRSummary has all expected fields
	pr := PRSummary{
		Repo:      "testrepo",
		Number:    123,
		Title:     "Test PR",
		Author:    "testuser",
		State:     "open",
		URL:       "https://github.com/o/r/pull/123",
		Action:    "review",
		UpdatedAt: "2024-01-15",
		IsBlocked: true,
	}

	if pr.Repo != "testrepo" {
		t.Errorf("Repo = %q, want 'testrepo'", pr.Repo)
	}
	if pr.Number != 123 {
		t.Errorf("Number = %d, want 123", pr.Number)
	}
	if !pr.IsBlocked {
		t.Error("IsBlocked should be true")
	}
}

func TestPRReport_Fields(t *testing.T) {
	report := PRReport{
		IncomingPRs: []PRSummary{{Repo: "r1"}},
		OutgoingPRs: []PRSummary{{Repo: "r2"}, {Repo: "r3"}},
		GeneratedAt: "2024-01-15",
	}

	if len(report.IncomingPRs) != 1 {
		t.Errorf("IncomingPRs len = %d, want 1", len(report.IncomingPRs))
	}
	if len(report.OutgoingPRs) != 2 {
		t.Errorf("OutgoingPRs len = %d, want 2", len(report.OutgoingPRs))
	}
}

func TestBotStatus_Fields(t *testing.T) {
	status := BotStatus{
		Connected:       true,
		ActivePRs:       10,
		PendingDMs:      5,
		ConnectedOrgs:   []string{"org1", "org2"},
		UptimeSeconds:   3600,
		LastEventTime:   "10 minutes ago",
		ConfiguredRepos: []string{"repo1"},
		WatchedChannels: []string{"channel1", "channel2"},
	}

	if !status.Connected {
		t.Error("Connected should be true")
	}
	if status.ActivePRs != 10 {
		t.Errorf("ActivePRs = %d, want 10", status.ActivePRs)
	}
	if len(status.ConnectedOrgs) != 2 {
		t.Errorf("ConnectedOrgs len = %d, want 2", len(status.ConnectedOrgs))
	}
}

func TestFormatDuration(t *testing.T) {
	tests := []struct {
		name     string
		duration string // In hours format like "72h30m"
		want     string
	}{
		{
			name:     "days hours and minutes",
			duration: "73h45m",
			want:     "3d 1h 45m",
		},
		{
			name:     "only days and hours",
			duration: "48h0m",
			want:     "2d 0h 0m",
		},
		{
			name:     "only hours and minutes",
			duration: "5h30m",
			want:     "5h 30m",
		},
		{
			name:     "only minutes",
			duration: "45m",
			want:     "45m",
		},
		{
			name:     "zero duration",
			duration: "0m",
			want:     "0m",
		},
		{
			name:     "exactly 1 day",
			duration: "24h0m",
			want:     "1d 0h 0m",
		},
		{
			name:     "exactly 1 hour",
			duration: "1h0m",
			want:     "1h 0m",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d, err := time.ParseDuration(tt.duration)
			if err != nil {
				t.Fatalf("Failed to parse duration %q: %v", tt.duration, err)
			}
			got := formatDuration(d)
			if got != tt.want {
				t.Errorf("formatDuration(%v) = %q, want %q", d, got, tt.want)
			}
		})
	}
}

func TestFormatUserMappingsEmbed(t *testing.T) {
	handler := &SlashCommandHandler{}

	t.Run("empty mappings", func(t *testing.T) {
		mappings := &UserMappings{}
		embed := handler.formatUserMappingsEmbed(mappings)

		if embed.Author == nil || embed.Author.Name != "User Mappings" {
			t.Error("Should have User Mappings author")
		}
		if embed.Color != 0x5865F2 {
			t.Errorf("Color = %x, want Discord blurple 0x5865F2", embed.Color)
		}
		if embed.Description != "No user mappings found." {
			t.Errorf("Description = %q, want 'No user mappings found.'", embed.Description)
		}
	})

	t.Run("with config mappings", func(t *testing.T) {
		mappings := &UserMappings{
			ConfigMappings: []UserMapping{
				{
					GitHubUsername: "alice",
					DiscordUserID:  "123456789",
					Org:            "myorg",
				},
				{
					GitHubUsername: "bob",
					DiscordUserID:  "987654321",
					Org:            "",
				},
			},
		}
		embed := handler.formatUserMappingsEmbed(mappings)

		// Should have Config field
		hasConfigField := false
		for _, field := range embed.Fields {
			if strings.Contains(field.Name, "Config") && strings.Contains(field.Name, "2") {
				hasConfigField = true
				if !strings.Contains(field.Value, "alice") {
					t.Error("Field value should contain alice")
				}
				if !strings.Contains(field.Value, "bob") {
					t.Error("Field value should contain bob")
				}
				if !strings.Contains(field.Value, "123456789") {
					t.Error("Field value should contain Discord ID")
				}
				if !strings.Contains(field.Value, "myorg") {
					t.Error("Field value should contain org for alice")
				}
			}
		}
		if !hasConfigField {
			t.Error("Should have Config field")
		}
	})

	t.Run("with discovered mappings", func(t *testing.T) {
		mappings := &UserMappings{
			DiscoveredMappings: []UserMapping{
				{
					GitHubUsername: "charlie",
					DiscordUserID:  "111222333",
					Org:            "testorg",
				},
			},
		}
		embed := handler.formatUserMappingsEmbed(mappings)

		// Should have Discovered field
		hasDiscoveredField := false
		for _, field := range embed.Fields {
			if strings.Contains(field.Name, "Discovered") && strings.Contains(field.Name, "1") {
				hasDiscoveredField = true
				if !strings.Contains(field.Value, "charlie") {
					t.Error("Field value should contain charlie")
				}
				if !strings.Contains(field.Value, "testorg") {
					t.Error("Field value should contain org")
				}
			}
		}
		if !hasDiscoveredField {
			t.Error("Should have Discovered field")
		}
	})

	t.Run("with both types of mappings", func(t *testing.T) {
		mappings := &UserMappings{
			ConfigMappings: []UserMapping{
				{GitHubUsername: "user1", DiscordUserID: "111", Org: "org1"},
			},
			DiscoveredMappings: []UserMapping{
				{GitHubUsername: "user2", DiscordUserID: "222", Org: "org2"},
			},
		}
		embed := handler.formatUserMappingsEmbed(mappings)

		// Should have both Config and Discovered fields
		if len(embed.Fields) != 2 {
			t.Fatalf("Fields = %d, want 2 (Config, Discovered)", len(embed.Fields))
		}
	})
}

func TestFormatChannelMappingsEmbed(t *testing.T) {
	handler := &SlashCommandHandler{}

	t.Run("empty mappings", func(t *testing.T) {
		mappings := &ChannelMappings{}
		embed := handler.formatChannelMappingsEmbed(mappings)

		if embed.Author == nil || embed.Author.Name != "Channel Mappings" {
			t.Error("Should have Channel Mappings author")
		}
		if embed.Color != 0x5865F2 {
			t.Errorf("Color = %x, want Discord blurple 0x5865F2", embed.Color)
		}
		if embed.Description != "No channel mappings found." {
			t.Errorf("Description = %q, want 'No channel mappings found.'", embed.Description)
		}
	})

	t.Run("with single org mappings", func(t *testing.T) {
		mappings := &ChannelMappings{
			RepoMappings: []RepoChannelMapping{
				{
					Org:      "myorg",
					Repo:     "repo1",
					Channels: []string{"channel123", "channel456"},
				},
				{
					Org:      "myorg",
					Repo:     "repo2",
					Channels: []string{"channel789"},
				},
			},
		}
		embed := handler.formatChannelMappingsEmbed(mappings)

		// Should have field for myorg
		hasOrgField := false
		for _, field := range embed.Fields {
			if strings.Contains(field.Name, "myorg") && strings.Contains(field.Name, "2 repos") {
				hasOrgField = true
				if !strings.Contains(field.Value, "repo1") {
					t.Error("Field value should contain repo1")
				}
				if !strings.Contains(field.Value, "repo2") {
					t.Error("Field value should contain repo2")
				}
				// Discord channel mentions should be formatted as <#ID>
				if !strings.Contains(field.Value, "<#channel123>") {
					t.Error("Field value should contain formatted channel mention")
				}
			}
		}
		if !hasOrgField {
			t.Error("Should have field for myorg")
		}
	})

	t.Run("with multiple orgs", func(t *testing.T) {
		mappings := &ChannelMappings{
			RepoMappings: []RepoChannelMapping{
				{Org: "org1", Repo: "repo1", Channels: []string{"ch1"}},
				{Org: "org2", Repo: "repo2", Channels: []string{"ch2"}},
			},
		}
		embed := handler.formatChannelMappingsEmbed(mappings)

		// Should have fields for both orgs
		if len(embed.Fields) != 2 {
			t.Fatalf("Fields = %d, want 2 (one per org)", len(embed.Fields))
		}
	})

	t.Run("groups repos by org", func(t *testing.T) {
		mappings := &ChannelMappings{
			RepoMappings: []RepoChannelMapping{
				{Org: "org1", Repo: "repo1", Channels: []string{"ch1"}},
				{Org: "org1", Repo: "repo2", Channels: []string{"ch2"}},
				{Org: "org1", Repo: "repo3", Channels: []string{"ch3"}},
			},
		}
		embed := handler.formatChannelMappingsEmbed(mappings)

		// Should group all repos under single org field
		if len(embed.Fields) != 1 {
			t.Fatalf("Fields = %d, want 1 (all repos under org1)", len(embed.Fields))
		}

		field := embed.Fields[0]
		if !strings.Contains(field.Name, "3 repos") {
			t.Errorf("Field name should contain '3 repos', got %q", field.Name)
		}
	})
}
