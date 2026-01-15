# reviewGOOSE:Discord

The Discord integration for [reviewGOOSE](https://codegroove.dev/reviewgoose/) — know instantly when you're blocking a PR.

**reviewGOOSE:Discord** tracks GitHub pull requests and notifies reviewers when it's their turn. Works alongside [reviewGOOSE:Desktop](https://github.com/codeGROOVE-dev/goose) for a complete PR tracking experience.

## Features

- Creates Discord threads for new PRs (forum channels) or posts in text channels
- Smart notifications: Delays DMs if user already notified in channel
- Channel auto-discovery: repos automatically map to same-named channels
- Configurable notification settings via YAML
- Daily reports between 6-11:30am local time
- Reliable delivery with deduplication

## Quick Start

### Prerequisites

- GitHub organization admin access
- Discord server admin access
- Discord Developer Mode enabled (Settings → Advanced → Developer Mode)

### 1. Add the Bot to Your Server

[Add reviewGOOSE to Discord](https://discord.com/oauth2/authorize?client_id=1461368540190871831&permissions=2147485696&scope=bot%20applications.commands)

### 2. Install the GitHub App

Install the [reviewGOOSE GitHub App](https://github.com/apps/reviewgoose) on your organization.

### 3. Get Your Discord IDs

**Enable Developer Mode** (if not already enabled):
1. Open Discord Settings (gear icon)
2. Go to Advanced (under App Settings)
3. Enable "Developer Mode"

**Get Server ID**: Right-click your server name → Copy Server ID
**Get User IDs**: Right-click any username → Copy User ID

### 4. Create Configuration Repository

Create a repository named `.codeGROOVE` in your GitHub organization.

### 5. Add Configuration File

Create `.codeGROOVE/discord.yaml`:

```yaml
global:
  guild_id: "YOUR_DISCORD_SERVER_ID"

users:
  github-username: "discord-user-id"
  another-user: "discord-user-id"
```

Done! The bot will post PR notifications to channels matching your repository names (e.g., `api` repo → `#api` channel).

## Configuration

Full configuration options for `.codeGROOVE/discord.yaml`:

```yaml
global:
  guild_id: "1234567890123456789"
  reminder_dm_delay: 65  # Minutes to wait before sending DM (default: 65, 0 = disabled)

users:
  alice: "111111111111111111"  # GitHub username → Discord user ID
  bob: "222222222222222222"
  # Unmapped users: bot attempts username match in guild

channels:
  # Route all repos to one channel
  pull-requests:
    repos:
      - "*"

  # Route specific repos with custom DM delay
  backend:
    repos:
      - api
      - db
    reminder_dm_delay: 30

  # Disable notifications for a repo
  noisy-repo:
    mute: true
```

## How It Works

**Channel Routing**
- Default: repos auto-map to same-named channels (`api` repo → `#api` channel)
- Override: Add repo to a channel's `repos:` list to route elsewhere
- Wildcard: Use `repos: ["*"]` to route all repos to one channel
- Mute: Set `mute: true` on a channel to disable notifications

**Channel Types**
- Forum channels: Each PR gets its own thread (recommended)
- Text channels: PR updates appear as regular messages

## User Mapping

The bot maps GitHub → Discord users in this order:

1. Explicit mapping in `users:` section
2. Username match (searches Discord guild for matching username)
3. Fallback (mentions GitHub username as plain text)

Add explicit mappings for users whose Discord and GitHub usernames differ.

## Slash Commands

- `/goose status` - Show bot connection status
- `/goose report` - Get your personal PR report
- `/goose dashboard` - Link to web dashboard
- `/goose help` - Show help

## Notification Behavior

- **Channel mentions**: DMs delayed by `reminder_dm_delay` (default: 65 min)
- **No channel access**: Immediate DM to user
- **Daily reports**: 6-11:30am local time if user has pending PRs
- **Anti-spam**: Rate limiting prevents notification floods

## Troubleshooting

**Bot doesn't respond to commands**
- Verify bot has correct permissions
- Try removing and re-adding the bot

**No notifications for my org**
- Verify GitHub App is installed on your org
- Check `.codeGROOVE/discord.yaml` exists in `.codeGROOVE` repo
- Verify `guild_id` matches your Discord server ID

**Messages not appearing in channels**
- Check channel name matches repo name or is configured in yaml
- Verify bot has "Send Messages" permission
- For forum channels: verify bot has "Create Public Threads" permission

**DMs not working**
- User must share a server with the bot
- User must have DMs enabled from server members
- Check `reminder_dm_delay` isn't 0 (disabled)

## Self-Hosting

See [DEPLOYMENT.md](DEPLOYMENT.md) for instructions on deploying your own instance.
