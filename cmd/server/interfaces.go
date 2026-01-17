package main

import (
	"context"

	"github.com/codeGROOVE-dev/discordian/internal/discord"
	"github.com/codeGROOVE-dev/discordian/internal/github"
)

// GitHubManager defines GitHub operations needed by the server.
type GitHubManager interface {
	RefreshInstallations(ctx context.Context) error
	AllOrgs() []string
	ClientForOrg(org string) (*github.OrgClient, bool)
	AppClient() *github.AppClient
}

// DiscordGuildManager defines Discord guild management operations.
type DiscordGuildManager interface {
	RegisterClient(guildID string, client *discord.Client)
}
