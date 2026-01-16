package format

import (
	"strings"
	"testing"
)

func TestStateEmoji(t *testing.T) {
	tests := []struct {
		state PRState
		want  string
	}{
		{StateNewlyPublished, EmojiNewlyPublished},
		{StateDraft, EmojiDraft},
		{StateTestsRunning, EmojiTestsRunning},
		{StateTestsBroken, EmojiTestsBroken},
		{StateAwaitingAssign, EmojiAwaitingAssign},
		{StateNeedsReview, EmojiNeedsReview},
		{StateChanges, EmojiChanges},
		{StateApproved, EmojiApproved},
		{StateMerged, EmojiMerged},
		{StateClosed, EmojiClosed},
		{StateConflict, EmojiConflict},
		{StateUnknown, EmojiUnknown},
		{"invalid", EmojiUnknown},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			got := StateEmoji(tt.state)
			if got != tt.want {
				t.Errorf("StateEmoji(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestStateText(t *testing.T) {
	tests := []struct {
		state PRState
		want  string
	}{
		{StateTestsRunning, "tests pending"},
		{StateTestsBroken, "tests failing"},
		{StateNeedsReview, "needs review"},
		{StateAwaitingAssign, "awaiting assignment"},
		{StateChanges, "changes requested"},
		{StateConflict, "merge conflict"},
		// States without text labels
		{StateNewlyPublished, ""},
		{StateDraft, ""},
		{StateApproved, ""},
		{StateMerged, ""},
		{StateClosed, ""},
		{StateUnknown, ""},
	}

	for _, tt := range tests {
		t.Run(string(tt.state), func(t *testing.T) {
			got := StateText(tt.state)
			if got != tt.want {
				t.Errorf("StateText(%q) = %q, want %q", tt.state, got, tt.want)
			}
		})
	}
}

func TestChannelMessage(t *testing.T) {
	tests := []struct {
		name   string
		params ChannelMessageParams
		want   []string // substrings that must be present
	}{
		{
			name: "basic message",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "repo",
				Number: 123,
				Title:  "Fix the bug",
				Author: "alice",
				State:  StateNeedsReview,
				PRURL:  "https://github.com/org/repo/pull/123",
			},
			want: []string{
				EmojiNeedsReview,
				"[repo#123]",
				" · Fix the bug", // Dot delimiter before title
				" · alice",       // Dot delimiter before author
			},
		},
		{
			name: "tests pending state",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "turnserver",
				Number: 18,
				Title:  "Add GitLab/GitTea support",
				Author: "tstromberg",
				State:  StateTestsRunning,
				PRURL:  "https://github.com/org/turnserver/pull/18",
			},
			want: []string{
				EmojiTestsRunning,
				"[turnserver#18]",
				" · Add GitLab/GitTea support", // Dot delimiter before title
				" · tstromberg",                // Dot delimiter before author
				" • tests pending",             // State text with bullet delimiter
			},
		},
		{
			name: "tests failing state",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "repo",
				Number: 99,
				Title:  "Breaking change",
				Author: "bob",
				State:  StateTestsBroken,
				PRURL:  "https://github.com/org/repo/pull/99",
			},
			want: []string{
				EmojiTestsBroken,
				" · Breaking change",
				" · bob",
				" • tests failing", // State text shown when no action users
			},
		},
		{
			name: "merge conflict state",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "repo",
				Number: 50,
				Title:  "Outdated branch",
				Author: "charlie",
				State:  StateConflict,
				PRURL:  "https://github.com/org/repo/pull/50",
			},
			want: []string{
				EmojiConflict,
				" · Outdated branch",
				" · charlie",
				" • merge conflict", // State text shown when no action users
			},
		},
		{
			name: "with action users (no state text shown)",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "repo",
				Number: 42,
				Title:  "New feature",
				Author: "bob",
				State:  StateChanges,
				PRURL:  "https://github.com/org/repo/pull/42",
				ActionUsers: []ActionUser{
					{Username: "charlie", Mention: "<@123>", Action: "review"},
				},
			},
			want: []string{
				EmojiChanges,
				" · New feature",
				" · bob",
				" • **review** → <@123>", // Action users only (no state text - matches Slacker)
			},
		},
		{
			name: "state without text label (approved)",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "repo",
				Number: 10,
				Title:  "Ready to merge",
				Author: "dave",
				State:  StateApproved,
				PRURL:  "https://github.com/org/repo/pull/10",
			},
			want: []string{
				EmojiApproved,
				" · Ready to merge",
				" · dave",
			},
		},
		{
			name: "action users without state text",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "repo",
				Number: 11,
				Title:  "Approved PR",
				Author: "eve",
				State:  StateApproved, // No state text for approved
				PRURL:  "https://github.com/org/repo/pull/11",
				ActionUsers: []ActionUser{
					{Username: "alice", Mention: "<@1>", Action: "merge"},
				},
			},
			want: []string{
				EmojiApproved,
				" · Approved PR",
				" · eve",
				" • **merge** → <@1>", // Action users with bullet (no state text)
			},
		},
		{
			name: "multiple action users grouped by action",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "repo",
				Number: 1,
				Title:  "Test",
				Author: "author",
				State:  StateNeedsReview,
				PRURL:  "https://github.com/org/repo/pull/1",
				ActionUsers: []ActionUser{
					{Username: "a", Mention: "<@1>", Action: "review"},
					{Username: "b", Mention: "<@2>", Action: "review"},
				},
			},
			want: []string{
				"<@1>",
				"<@2>",
				"**review**",
				", ", // Users with same action are comma-separated
			},
		},
		{
			name: "long title truncated",
			params: ChannelMessageParams{
				Owner:  "org",
				Repo:   "repo",
				Number: 1,
				Title:  strings.Repeat("a", 100),
				Author: "author",
				State:  StateDraft,
				PRURL:  "https://github.com/org/repo/pull/1",
			},
			want: []string{
				"...",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ChannelMessage(tt.params)
			for _, substr := range tt.want {
				if !strings.Contains(got, substr) {
					t.Errorf("ChannelMessage() = %q, want to contain %q", got, substr)
				}
			}
		})
	}
}

func TestForumThreadTitle(t *testing.T) {
	tests := []struct {
		name   string
		repo   string
		number int
		title  string
		want   string
	}{
		{
			name:   "short title",
			repo:   "myrepo",
			number: 42,
			title:  "Fix bug",
			want:   "[myrepo#42] Fix bug",
		},
		{
			name:   "title at limit",
			repo:   "repo",
			number: 1,
			title:  strings.Repeat("a", 90),
			want:   "[repo#1] " + strings.Repeat("a", 90),
		},
		{
			name:   "very long title truncated",
			repo:   "repo",
			number: 123,
			title:  strings.Repeat("x", 200),
			want:   "[repo#123] ",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ForumThreadTitle(tt.repo, tt.number, tt.title)
			if len(got) > 100 {
				t.Errorf("ForumThreadTitle() length = %d, want <= 100", len(got))
			}
			if !strings.HasPrefix(got, tt.want[:min(len(got), len(tt.want))]) {
				t.Errorf("ForumThreadTitle() = %q, want prefix %q", got, tt.want)
			}
		})
	}
}

func TestDMMessage(t *testing.T) {
	params := ChannelMessageParams{
		Owner:  "org",
		Repo:   "repo",
		Number: 99,
		Title:  "Important PR",
		Author: "dave",
		State:  StateApproved,
		PRURL:  "https://github.com/org/repo/pull/99",
	}

	t.Run("with action", func(t *testing.T) {
		got := DMMessage(params, "Review needed")
		if !strings.Contains(got, "**Review needed**") {
			t.Errorf("DMMessage() = %q, want to contain bold action", got)
		}
		if !strings.Contains(got, "org/repo#99") {
			t.Errorf("DMMessage() = %q, want to contain full PR ref", got)
		}
	})

	t.Run("without action", func(t *testing.T) {
		got := DMMessage(params, "")
		if strings.Contains(got, "**") {
			t.Errorf("DMMessage() = %q, should not contain bold markers", got)
		}
	})
}

func TestStateFromAnalysis(t *testing.T) {
	tests := []struct {
		name   string
		params StateAnalysisParams
		want   PRState
	}{
		// Terminal states
		{"merged PR", StateAnalysisParams{Merged: true}, StateMerged},
		{"closed PR", StateAnalysisParams{Closed: true}, StateClosed},

		// Draft shows as tests running (hourglass) to match slacker
		{"draft PR", StateAnalysisParams{Draft: true}, StateTestsRunning},

		// Merge conflicts
		{"merge conflict", StateAnalysisParams{MergeConflict: true}, StateConflict},

		// Check states
		{"failing checks", StateAnalysisParams{ChecksFailing: 2}, StateTestsBroken},
		{"pending checks", StateAnalysisParams{ChecksPending: 3}, StateTestsRunning},
		{"waiting checks", StateAnalysisParams{ChecksWaiting: 1}, StateTestsRunning},
		{"pending and waiting", StateAnalysisParams{ChecksPending: 2, ChecksWaiting: 1}, StateTestsRunning},

		// Approved states (matches slacker logic)
		{"approved no comments", StateAnalysisParams{Approved: true}, StateApproved},
		{"approved with unresolved comments", StateAnalysisParams{Approved: true, UnresolvedComments: 2}, StateChanges},

		// Workflow state fallbacks
		{"newly published", StateAnalysisParams{WorkflowState: "NEWLY_PUBLISHED"}, StateNewlyPublished},
		{"waiting for assignment", StateAnalysisParams{WorkflowState: "TESTED_WAITING_FOR_ASSIGNMENT"}, StateAwaitingAssign},
		{"waiting for review", StateAnalysisParams{WorkflowState: "ASSIGNED_WAITING_FOR_REVIEW"}, StateNeedsReview},
		{"waiting for approval", StateAnalysisParams{WorkflowState: "REFINED_WAITING_FOR_APPROVAL"}, StateNeedsReview},
		{"needs refinement", StateAnalysisParams{WorkflowState: "REVIEWED_NEEDS_REFINEMENT"}, StateChanges},

		// Default to awaiting review (matches slacker)
		{"unknown workflow", StateAnalysisParams{WorkflowState: "SOMETHING_ELSE"}, StateNeedsReview},
		{"empty workflow", StateAnalysisParams{}, StateNeedsReview},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := StateFromAnalysis(tt.params)
			if got != tt.want {
				t.Errorf("StateFromAnalysis() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestActionLabel(t *testing.T) {
	tests := []struct {
		action string
		want   string
	}{
		{"review", "review"},
		{"re_review", "re review"},
		{"approve", "approve"},
		{"resolve_comments", "resolve comments"},
		{"fix_tests", "fix tests"},
		{"fix_conflict", "fix conflict"},
		{"merge", "merge"},
		{"publish_draft", "publish draft"},
		{"request_reviewers", "request reviewers"},
		{"unknown_action", "unknown action"},
		{"", ""},
	}

	for _, tt := range tests {
		t.Run(tt.action, func(t *testing.T) {
			got := ActionLabel(tt.action)
			if got != tt.want {
				t.Errorf("ActionLabel(%q) = %q, want %q", tt.action, got, tt.want)
			}
		})
	}
}

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
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Truncate(tt.input, tt.maxLen)
			if got != tt.want {
				t.Errorf("Truncate(%q, %d) = %q, want %q", tt.input, tt.maxLen, got, tt.want)
			}
			if len(got) > tt.maxLen {
				t.Errorf("Truncate(%q, %d) length = %d, want <= %d", tt.input, tt.maxLen, len(got), tt.maxLen)
			}
		})
	}
}
