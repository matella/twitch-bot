// Package bot relie le chat Twitch (IRC) au Handler de commandes.
package bot

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync/atomic"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v4"

	"github.com/matella/twitch-bot/internal/chat"
)

// maxInFlight borne le nombre de commandes traitées en parallèle :
// au-delà, les messages sont ignorés plutôt que d'empiler des goroutines.
const maxInFlight = 8

// Bot est le client Twitch.
type Bot struct {
	client  *twitch.Client
	channel string
	self    string
	handler *chat.Handler

	connected atomic.Bool
	sem       chan struct{}
}

// New crée le bot. token doit être de la forme "oauth:...".
func New(channel, username, token string, handler *chat.Handler) *Bot {
	return &Bot{
		client:  twitch.NewClient(username, token),
		channel: channel,
		self:    strings.ToLower(username),
		handler: handler,
		sem:     make(chan struct{}, maxInFlight),
	}
}

// Connected indique si le bot s'est connecté à Twitch (dernière connexion réussie ;
// la bibliothèque se reconnecte seule en cas de coupure).
func (b *Bot) Connected() bool { return b.connected.Load() }

// Run se connecte et bloque jusqu'à l'annulation de ctx ou une erreur fatale
// (par exemple un jeton refusé).
func (b *Bot) Run(ctx context.Context) error {
	b.client.OnConnect(func() {
		b.connected.Store(true)
		slog.Info("connecté à Twitch", "channel", b.channel)
	})
	b.client.OnPrivateMessage(b.onMessage)
	b.client.Join(b.channel)

	errc := make(chan error, 1)
	go func() { errc <- b.client.Connect() }()

	select {
	case <-ctx.Done():
		_ = b.client.Disconnect()
		select {
		case <-errc:
		case <-time.After(5 * time.Second):
		}
		b.connected.Store(false)
		return nil
	case err := <-errc:
		b.connected.Store(false)
		if errors.Is(err, twitch.ErrClientDisconnected) {
			return nil
		}
		return err
	}
}

func (b *Bot) onMessage(m twitch.PrivateMessage) {
	if strings.EqualFold(m.User.Name, b.self) || !strings.HasPrefix(m.Message, "!") {
		return
	}
	select {
	case b.sem <- struct{}{}:
	default:
		slog.Warn("trop de commandes simultanées, message ignoré", "user", m.User.Name)
		return
	}
	go func() {
		defer func() { <-b.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		if reply := b.handler.Handle(ctx, strings.ToLower(m.User.Name), m.Message); reply != "" {
			b.client.Say(b.channel, reply)
		}
	}()
}
