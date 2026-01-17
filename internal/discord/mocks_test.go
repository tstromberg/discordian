package discord

import (
	"fmt"
	"sync"

	"github.com/bwmarrin/discordgo"
)

// MockSession is a programmable mock for discordgo.Session
type MockSession struct {
	// Programmable responses
	OpenError                error
	CloseError               error
	MessageSendError         error
	MessageEditError         error
	UserChannelError         error
	GuildMembersError        error
	ChannelError             error
	GuildChannelsError       error
	MessagesError            error
	ThreadsActiveError       error
	ApplicationCommandsError error
	InteractionResponseError error

	// Storage for tracking calls
	SentMessages    []*sentMessage
	EditedMessages  []*editedMessage
	CreatedChannels []string
	Interactions    []*discordgo.InteractionResponse

	// Mock data
	Channels      map[string]*discordgo.Channel
	Members       map[string][]*discordgo.Member
	Messages      map[string][]*discordgo.Message
	ActiveThreads []*discordgo.Channel
	Commands      []*discordgo.ApplicationCommand

	mu sync.Mutex
}

type sentMessage struct {
	ChannelID string
	Content   string
	Embed     *discordgo.MessageEmbed
}

type editedMessage struct {
	ChannelID string
	MessageID string
	Content   string
	Embed     *discordgo.MessageEmbed
}

func NewMockSession() *MockSession {
	return &MockSession{
		SentMessages:   make([]*sentMessage, 0),
		EditedMessages: make([]*editedMessage, 0),
		Channels:       make(map[string]*discordgo.Channel),
		Members:        make(map[string][]*discordgo.Member),
		Messages:       make(map[string][]*discordgo.Message),
		Commands:       make([]*discordgo.ApplicationCommand, 0),
	}
}

func (m *MockSession) Open() error {
	return m.OpenError
}

func (m *MockSession) Close() error {
	return m.CloseError
}

func (m *MockSession) ChannelMessageSend(channelID string, content string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	if m.MessageSendError != nil {
		return nil, m.MessageSendError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.SentMessages = append(m.SentMessages, &sentMessage{
		ChannelID: channelID,
		Content:   content,
	})

	msgID := fmt.Sprintf("msg-%d", len(m.SentMessages))
	return &discordgo.Message{
		ID:        msgID,
		ChannelID: channelID,
		Content:   content,
	}, nil
}

func (m *MockSession) ChannelMessageSendEmbed(channelID string, embed *discordgo.MessageEmbed, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	if m.MessageSendError != nil {
		return nil, m.MessageSendError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.SentMessages = append(m.SentMessages, &sentMessage{
		ChannelID: channelID,
		Embed:     embed,
	})

	msgID := fmt.Sprintf("msg-%d", len(m.SentMessages))
	return &discordgo.Message{
		ID:        msgID,
		ChannelID: channelID,
		Embeds:    []*discordgo.MessageEmbed{embed},
	}, nil
}

func (m *MockSession) ChannelMessageEdit(channelID, messageID string, content string, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	if m.MessageEditError != nil {
		return nil, m.MessageEditError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.EditedMessages = append(m.EditedMessages, &editedMessage{
		ChannelID: channelID,
		MessageID: messageID,
		Content:   content,
	})

	return &discordgo.Message{
		ID:        messageID,
		ChannelID: channelID,
		Content:   content,
	}, nil
}

func (m *MockSession) ChannelMessageEditEmbed(channelID, messageID string, embed *discordgo.MessageEmbed, options ...discordgo.RequestOption) (*discordgo.Message, error) {
	if m.MessageEditError != nil {
		return nil, m.MessageEditError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.EditedMessages = append(m.EditedMessages, &editedMessage{
		ChannelID: channelID,
		MessageID: messageID,
		Embed:     embed,
	})

	return &discordgo.Message{
		ID:        messageID,
		ChannelID: channelID,
		Embeds:    []*discordgo.MessageEmbed{embed},
	}, nil
}

func (m *MockSession) UserChannelCreate(recipientID string, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	if m.UserChannelError != nil {
		return nil, m.UserChannelError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	channelID := fmt.Sprintf("dm-%s", recipientID)
	m.CreatedChannels = append(m.CreatedChannels, channelID)

	return &discordgo.Channel{
		ID:   channelID,
		Type: discordgo.ChannelTypeDM,
	}, nil
}

func (m *MockSession) GuildMembers(guildID string, after string, limit int, options ...discordgo.RequestOption) ([]*discordgo.Member, error) {
	if m.GuildMembersError != nil {
		return nil, m.GuildMembersError
	}

	if members, ok := m.Members[guildID]; ok {
		return members, nil
	}

	return []*discordgo.Member{}, nil
}

func (m *MockSession) Channel(channelID string, options ...discordgo.RequestOption) (*discordgo.Channel, error) {
	if m.ChannelError != nil {
		return nil, m.ChannelError
	}

	if channel, ok := m.Channels[channelID]; ok {
		return channel, nil
	}

	return nil, fmt.Errorf("channel not found")
}

func (m *MockSession) GuildChannels(guildID string, options ...discordgo.RequestOption) ([]*discordgo.Channel, error) {
	if m.GuildChannelsError != nil {
		return nil, m.GuildChannelsError
	}

	channels := make([]*discordgo.Channel, 0)
	for _, ch := range m.Channels {
		if ch.GuildID == guildID {
			channels = append(channels, ch)
		}
	}

	return channels, nil
}

func (m *MockSession) ChannelMessages(channelID string, limit int, beforeID, afterID, aroundID string, options ...discordgo.RequestOption) ([]*discordgo.Message, error) {
	if m.MessagesError != nil {
		return nil, m.MessagesError
	}

	if messages, ok := m.Messages[channelID]; ok {
		return messages, nil
	}

	return []*discordgo.Message{}, nil
}

func (m *MockSession) ThreadsActive(guildID string, options ...discordgo.RequestOption) (*discordgo.ThreadsList, error) {
	if m.ThreadsActiveError != nil {
		return nil, m.ThreadsActiveError
	}

	return &discordgo.ThreadsList{
		Threads: m.ActiveThreads,
	}, nil
}

func (m *MockSession) ApplicationCommandBulkOverwrite(appID, guildID string, commands []*discordgo.ApplicationCommand, options ...discordgo.RequestOption) ([]*discordgo.ApplicationCommand, error) {
	if m.ApplicationCommandsError != nil {
		return nil, m.ApplicationCommandsError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.Commands = commands
	return commands, nil
}

func (m *MockSession) InteractionRespond(interaction *discordgo.Interaction, resp *discordgo.InteractionResponse, options ...discordgo.RequestOption) error {
	if m.InteractionResponseError != nil {
		return m.InteractionResponseError
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	m.Interactions = append(m.Interactions, resp)
	return nil
}

// Helper functions to set up mock data

func (m *MockSession) AddChannel(channel *discordgo.Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Channels[channel.ID] = channel
}

func (m *MockSession) AddMember(guildID string, member *discordgo.Member) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Members[guildID] = append(m.Members[guildID], member)
}

func (m *MockSession) AddMessage(channelID string, message *discordgo.Message) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.Messages[channelID] = append(m.Messages[channelID], message)
}

func (m *MockSession) AddActiveThread(thread *discordgo.Channel) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ActiveThreads = append(m.ActiveThreads, thread)
}

// NewMockChannel creates a mock Discord channel
func NewMockChannel(id, name, guildID string, channelType discordgo.ChannelType) *discordgo.Channel {
	return &discordgo.Channel{
		ID:      id,
		Name:    name,
		GuildID: guildID,
		Type:    channelType,
	}
}

// NewMockMember creates a mock Discord guild member
func NewMockMember(userID, username, globalName string) *discordgo.Member {
	return &discordgo.Member{
		User: &discordgo.User{
			ID:         userID,
			Username:   username,
			GlobalName: globalName,
		},
	}
}

// NewMockMessage creates a mock Discord message
func NewMockMessage(id, channelID, content, authorID string) *discordgo.Message {
	return &discordgo.Message{
		ID:        id,
		ChannelID: channelID,
		Content:   content,
		Author: &discordgo.User{
			ID: authorID,
		},
	}
}
