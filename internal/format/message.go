// Package format provides PR message formatting for Discord.
package format

import (
	"fmt"
	"strings"
)

// PR state emoji mappings.
const (
	EmojiNewlyPublished = "\U0001F195"   // 🆕 Newly published
	EmojiDraft          = "\U0001F6A7"   // 🚧 Draft (construction)
	EmojiTestsRunning   = "\U0001F9EA"   // 🧪 Tests running
	EmojiTestsBroken    = "\U0001FAB3"   // 🪳 Tests failing (cockroach)
	EmojiAwaitingAssign = "\U0001F937"   // 🤷 Awaiting assignment
	EmojiNeedsReview    = "\u23F3"       // ⏳ Needs review (hourglass)
	EmojiChanges        = "\U0001FA9A"   // 🪚 Changes requested (saw)
	EmojiApproved       = "\u2705"       // ✅ Approved
	EmojiMerged         = "\U0001F680"   // 🚀 Merged
	EmojiClosed         = "\u274C"       // ❌ Closed
	EmojiConflict       = "\u26A0\uFE0F" // ⚠️ Merge conflict
	EmojiUnknown        = "\U0001F4EF"   // 📯 Unknown state (postal horn)
)

// PRState represents the simplified state of a PR for formatting.
type PRState string

// PR state constants.
const (
	StateNewlyPublished PRState = "newly_published"
	StateDraft          PRState = "draft"
	StateTestsRunning   PRState = "tests_running"
	StateTestsBroken    PRState = "tests_broken"
	StateAwaitingAssign PRState = "awaiting_assignment"
	StateNeedsReview    PRState = "needs_review"
	StateChanges        PRState = "changes_requested"
	StateApproved       PRState = "approved"
	StateMerged         PRState = "merged"
	StateClosed         PRState = "closed"
	StateConflict       PRState = "conflict"
	StateUnknown        PRState = "unknown"
)

// StateEmoji returns the emoji for a PR state.
func StateEmoji(state PRState) string {
	switch state {
	case StateNewlyPublished:
		return EmojiNewlyPublished
	case StateDraft:
		return EmojiDraft
	case StateTestsRunning:
		return EmojiTestsRunning
	case StateTestsBroken:
		return EmojiTestsBroken
	case StateAwaitingAssign:
		return EmojiAwaitingAssign
	case StateNeedsReview:
		return EmojiNeedsReview
	case StateChanges:
		return EmojiChanges
	case StateApproved:
		return EmojiApproved
	case StateMerged:
		return EmojiMerged
	case StateClosed:
		return EmojiClosed
	case StateConflict:
		return EmojiConflict
	default:
		return EmojiUnknown
	}
}

// StateParam returns the URL state parameter suffix for a PR state.
func StateParam(state PRState) string {
	return "?st=" + string(state)
}

// StateText returns the human-readable text label for a PR state.
// Returns empty string for states that don't need text labels.
func StateText(state PRState) string {
	switch state {
	case StateTestsRunning:
		return "tests pending"
	case StateTestsBroken:
		return "tests failing"
	case StateNeedsReview:
		return "needs review"
	case StateAwaitingAssign:
		return "awaiting assignment"
	case StateChanges:
		return "changes requested"
	case StateConflict:
		return "merge conflict"
	default:
		return ""
	}
}

// ChannelMessageParams contains parameters for formatting a channel message.
type ChannelMessageParams struct {
	ActionUsers []ActionUser // Users who need to take action
	Owner       string
	Repo        string
	Title       string
	Author      string
	State       PRState
	PRURL       string
	ChannelName string // If provided and matches Repo (case-insensitive), use short form #123
	Number      int
}

// ActionUser represents a user who needs to take action.
type ActionUser struct {
	Username string
	Mention  string // Discord mention format or plain username
	Action   string // e.g., "review", "approve", "fix tests"
}

// ChannelMessage formats a PR notification for a text channel.
func ChannelMessage(p ChannelMessageParams) string {
	emoji := StateEmoji(p.State)

	// Format: emoji [repo#123](url?st=state) · Title · author • action → @users
	var sb strings.Builder

	sb.WriteString(emoji)
	sb.WriteString(" ")

	// PR link with state param - use short form #123 if channel matches repo
	prRef := fmt.Sprintf("%s#%d", p.Repo, p.Number)
	if p.ChannelName != "" && strings.EqualFold(p.ChannelName, p.Repo) {
		prRef = fmt.Sprintf("#%d", p.Number)
	}
	sb.WriteString(fmt.Sprintf("[%s](%s%s)", prRef, p.PRURL, StateParam(p.State)))

	// Title with dot delimiter
	sb.WriteString(" · ")
	sb.WriteString(Truncate(p.Title, 60))

	// Author
	sb.WriteString(" · ")
	sb.WriteString(p.Author)

	// Action users - group by action
	actionSuffix := ActionGroups(p.ActionUsers)
	if actionSuffix != "" {
		// If there are action users, show them directly with bullet separator
		// (matching Slacker behavior - no state text when actions are present)
		sb.WriteString(" • ")
		sb.WriteString(actionSuffix)
	} else {
		// Only show state text if no action users are present
		// State text provides context when there's no specific action to take
		stateText := StateText(p.State)
		if stateText != "" {
			sb.WriteString(" • ")
			sb.WriteString(stateText)
		}
	}

	return sb.String()
}

// ActionGroups groups users by action and formats them.
// Returns format like: "**review** → @alice, @bob; **approve** → @charlie".
func ActionGroups(users []ActionUser) string {
	if len(users) == 0 {
		return ""
	}

	// Group users by action
	actionGroups := make(map[string][]string)
	for _, au := range users {
		actionGroups[au.Action] = append(actionGroups[au.Action], au.Mention)
	}

	// Format each group
	var parts []string
	for action, mentions := range actionGroups {
		if action == "" {
			continue
		}
		userList := strings.Join(mentions, ", ")
		parts = append(parts, fmt.Sprintf("**%s** → %s", action, userList))
	}

	// Join with semicolons (commas used between users)
	return strings.Join(parts, "; ")
}

// ForumThreadTitle formats the title for a forum thread.
func ForumThreadTitle(repo string, number int, title string) string {
	// [repo#123] Title (truncated to fit Discord's 100 char limit)
	prefix := fmt.Sprintf("[%s#%d] ", repo, number)
	maxTitleLen := 100 - len(prefix)
	return prefix + Truncate(title, maxTitleLen)
}

// ForumThreadContent formats the content for a forum thread starter message.
func ForumThreadContent(p ChannelMessageParams) string {
	return ChannelMessage(p)
}

// DMMessage formats a DM notification.
func DMMessage(p ChannelMessageParams, action string) string {
	emoji := StateEmoji(p.State)

	var sb strings.Builder
	sb.WriteString(emoji)
	sb.WriteString(" ")

	// Action prompt
	if action != "" {
		sb.WriteString("**")
		sb.WriteString(action)
		sb.WriteString("**: ")
	}

	// PR link
	sb.WriteString(fmt.Sprintf("[%s/%s#%d](%s)", p.Owner, p.Repo, p.Number, p.PRURL))
	sb.WriteString(" ")
	sb.WriteString(p.Title)

	// Author
	sb.WriteString(" by ")
	sb.WriteString(p.Author)

	return sb.String()
}

// StateAnalysisParams contains parameters for StateFromAnalysis.
type StateAnalysisParams struct {
	Merged             bool
	Closed             bool
	Draft              bool
	MergeConflict      bool
	Approved           bool
	ChecksFailing      int
	ChecksPending      int
	ChecksWaiting      int
	UnresolvedComments int
	WorkflowState      string
}

// StateFromAnalysis determines PRState from Turn API analysis.
// This matches slacker's extractStateFromTurnclient logic.
func StateFromAnalysis(p StateAnalysisParams) PRState {
	// Check if PR is merged (most direct way)
	if p.Merged {
		return StateMerged
	}

	// Check if PR is closed but not merged
	if p.Closed {
		return StateClosed
	}

	// Draft PRs show as tests running (hourglass)
	if p.Draft {
		return StateTestsRunning
	}

	// Merge conflicts take priority over other states
	if p.MergeConflict {
		return StateConflict
	}

	// Tests failing
	if p.ChecksFailing > 0 {
		return StateTestsBroken
	}

	// Tests pending or waiting (deployment gates)
	if p.ChecksPending > 0 || p.ChecksWaiting > 0 {
		return StateTestsRunning
	}

	// Approved with unresolved comments = changes requested
	if p.Approved {
		if p.UnresolvedComments > 0 {
			return StateChanges
		}
		return StateApproved
	}

	// Fallback to workflow state for more nuanced states
	switch p.WorkflowState {
	case "NEWLY_PUBLISHED":
		return StateNewlyPublished
	case "TESTED_WAITING_FOR_ASSIGNMENT":
		return StateAwaitingAssign
	case "REVIEWED_NEEDS_REFINEMENT":
		return StateChanges
	default:
		// Includes ASSIGNED_WAITING_FOR_REVIEW, REFINED_WAITING_FOR_APPROVAL, and unknown states
		return StateNeedsReview
	}
}

// ActionLabel returns a human-readable label for an action.
// Converts snake_case to space-separated words (matching Slacker format).
func ActionLabel(action string) string {
	return strings.ReplaceAll(action, "_", " ")
}

// Truncate truncates a string to maxLen, adding "..." if truncated.
func Truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	if maxLen <= 3 {
		return s[:maxLen]
	}
	return s[:maxLen-3] + "..."
}
