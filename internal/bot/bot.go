package bot

import (
	"fmt"
	"log"
	"strings"
	"sync"

	"github.com/gempir/go-twitch-irc/v3"
	"github.com/matella/twitch-bot/internal/db"
	"github.com/matella/twitch-bot/internal/spotify"
)

// Bot represents the Twitch bot
type Bot struct {
	client   *twitch.Client
	channel  string
	database *db.DB
	spotify  *spotify.Client
	mu       sync.RWMutex
	running  bool
}

// New creates a new bot instance
func New(channel, username, token string, database *db.DB, spotify *spotify.Client) *Bot {
	client := twitch.NewClient(username, token)
	return &Bot{
		client:   client,
		channel:  channel,
		database: database,
		spotify:  spotify,
	}
}

// Connect connects the bot to Twitch and starts listening
func (b *Bot) Connect() error {
	b.mu.Lock()
	b.running = true
	b.mu.Unlock()

	// Set up message handler
	b.client.OnPrivateMessage(func(message twitch.PrivateMessage) {
		b.handleMessage(message)
	})

	// Set up join handler
	b.client.OnUserJoinMessage(func(message twitch.UserJoinMessage) {
		log.Printf("Joined channel: %s", message.Channel)
		b.database.LogEvent("bot_join", message.Channel)
	})

	// Join channel
	b.client.Join(b.channel)

	// Connect and block until connection is closed
	log.Printf("Connecting to Twitch channel: %s", b.channel)
	return b.client.Connect()
}

// Disconnect gracefully disconnects the bot
func (b *Bot) Disconnect() {
	b.mu.Lock()
	b.running = false
	b.mu.Unlock()

	b.client.Disconnect()
	log.Println("Bot disconnected")
}

// handleMessage processes incoming chat messages
func (b *Bot) handleMessage(message twitch.PrivateMessage) {
	// Ignore bot's own messages
	if message.User.Name == b.client.GetLatestUsername() {
		return
	}

	text := message.Message
	log.Printf("[%s] %s: %s", message.Channel, message.User.Name, text)

	// Log message
	b.database.LogEvent("chat_message", fmt.Sprintf("%s: %s", message.User.Name, text))

	// Check if it's a command (starts with !)
	if !strings.HasPrefix(text, "!") {
		return
	}

	// Parse command
	parts := strings.Fields(text)
	commandName := strings.ToLower(strings.TrimPrefix(parts[0], "!"))
	args := parts[1:]

	b.handleCommand(message, commandName, args)
}

// handleCommand processes bot commands
func (b *Bot) handleCommand(message twitch.PrivateMessage, command string, args []string) {
	switch command {
	case "song":
		b.handleSongRequest(message, args)
	case "queue":
		b.handleQueueCommand(message)
	case "help":
		b.sendMessage("Available commands: !song <search>, !queue, !help")
	default:
		// Check database for custom commands
		cmd, err := b.database.GetCommand(command)
		if err == nil && cmd != nil {
			b.sendMessage(cmd.Response)
		}
	}
}

// handleSongRequest processes song requests
func (b *Bot) handleSongRequest(message twitch.PrivateMessage, args []string) {
	if b.spotify == nil {
		b.sendMessage("Spotify integration not configured")
		return
	}

	if len(args) == 0 {
		b.sendMessage("Usage: !song <artist> <song>")
		return
	}

	query := strings.Join(args, " ")
	track, err := b.spotify.SearchTrack(query)
	if err != nil {
		log.Printf("Spotify search error: %v", err)
		b.sendMessage("Error searching for song")
		return
	}

	if track == nil {
		b.sendMessage(fmt.Sprintf("No results for: %s", query))
		return
	}

	// Add to queue
	err = b.database.AddSongToQueue(
		track.URI,
		track.Name,
		track.Artist,
		message.User.Name,
	)
	if err != nil {
		log.Printf("Database error: %v", err)
		b.sendMessage("Error adding song to queue")
		return
	}

	b.sendMessage(fmt.Sprintf("Added: %s - %s", track.Artist, track.Name))
	b.database.LogEvent("song_request", fmt.Sprintf("%s requested %s", message.User.Name, track.Name))
}

// handleQueueCommand shows current queue
func (b *Bot) handleQueueCommand(message twitch.PrivateMessage) {
	songs, err := b.database.GetQueue(5)
	if err != nil {
		b.sendMessage("Error fetching queue")
		return
	}

	if len(songs) == 0 {
		b.sendMessage("Queue is empty")
		return
	}

	queueMsg := "Now queued: "
	for i, song := range songs {
		if i > 0 {
			queueMsg += " | "
		}
		queueMsg += fmt.Sprintf("%s - %s", song.ArtistName, song.TrackName)
	}

	// Twitch has message length limits, truncate if needed
	if len(queueMsg) > 500 {
		queueMsg = queueMsg[:497] + "..."
	}

	b.sendMessage(queueMsg)
}

// sendMessage sends a message to the channel
func (b *Bot) sendMessage(message string) {
	b.client.Say(b.channel, message)
	log.Printf("Sent to %s: %s", b.channel, message)
}

// IsRunning returns whether the bot is currently running
func (b *Bot) IsRunning() bool {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.running
}
