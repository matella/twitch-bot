// Package bot relie le chat Twitch (IRC) au Handler de commandes.
//
// Le bot ne s'arrête jamais sur une erreur de connexion (jeton refusé, coupure réseau) :
// il réessaie avec un délai croissant et l'état reste visible dans l'administration.
package bot

import (
	"context"
	"errors"
	"log/slog"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	twitch "github.com/gempir/go-twitch-irc/v4"

	"github.com/matella/twitch-bot/internal/chat"
	"github.com/matella/twitch-bot/internal/model"
)

const (
	// maxInFlight borne le nombre de commandes traitées en parallèle :
	// au-delà, les messages sont ignorés plutôt que d'empiler des goroutines.
	maxInFlight = 8
	// Twitch bride un compte non modérateur à 20 messages par 30 secondes : on reste en dessous.
	sendBurst  = 15
	sendWindow = 30 * time.Second
	// activityTimeout : sans aucun signe de vie de Twitch pendant ce délai, le bot se déclare hors ligne.
	// Le client envoie un PING toutes les 15 s, donc un silence prolongé signale une coupure.
	activityTimeout = 90 * time.Second
	// tokenRefreshEvery : fréquence de vérification du jeton sur une connexion ouverte.
	tokenRefreshEvery = 5 * time.Minute
)

// Credentials fournit le pseudo et le mot de passe IRC (« oauth:... ») du bot.
// Elle est interrogée à chaque connexion : un jeton renouvelé est ainsi pris en compte.
type Credentials interface {
	Credentials(ctx context.Context) (user, pass string, err error)
}

// StaticCredentials est un couple fixe (jeton collé dans la configuration).
type StaticCredentials struct{ User, Pass string }

func (s StaticCredentials) Credentials(context.Context) (string, string, error) {
	return s.User, s.Pass, nil
}

// Bot est le client Twitch.
type Bot struct {
	channel string
	creds   Credentials
	handler *chat.Handler
	limiter *window

	sem chan struct{}

	lastActivity atomic.Int64 // UnixNano du dernier signe de vie ; 0 = pas connecté
	mu           sync.Mutex
	lastErr      string
	cancelConn   context.CancelFunc // coupe la connexion en cours (Reconnect)
}

// New crée le bot.
func New(channel string, creds Credentials, handler *chat.Handler) *Bot {
	return &Bot{
		channel: channel,
		creds:   creds,
		handler: handler,
		limiter: newWindow(sendBurst, sendWindow),
		sem:     make(chan struct{}, maxInFlight),
	}
}

// Connected indique si la connexion à Twitch est active.
func (b *Bot) Connected() bool {
	last := b.lastActivity.Load()
	return last != 0 && time.Since(time.Unix(0, last)) < activityTimeout
}

// LastError renvoie la dernière raison d'échec de connexion (vide si tout va bien).
func (b *Bot) LastError() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.lastErr
}

func (b *Bot) setErr(msg string) {
	b.mu.Lock()
	b.lastErr = msg
	b.mu.Unlock()
}

func (b *Bot) touch() { b.lastActivity.Store(time.Now().UnixNano()) }

// Reconnect coupe la connexion en cours ; Run se reconnecte aussitôt avec les identifiants actuels.
func (b *Bot) Reconnect() {
	b.mu.Lock()
	cancel := b.cancelConn
	b.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// Run se connecte et bloque jusqu'à l'annulation de ctx. Les erreurs de connexion ne sont
// jamais fatales : elles sont journalisées puis la connexion est retentée.
func (b *Bot) Run(ctx context.Context) error {
	const minWait, maxWait = 5 * time.Second, 2 * time.Minute
	wait := minWait
	for ctx.Err() == nil {
		user, pass, err := b.creds.Credentials(ctx)
		if err != nil {
			b.setErr(err.Error())
			if !sleep(ctx, 10*time.Second) {
				break
			}
			continue
		}

		started := time.Now()
		err = b.session(ctx, user, pass)
		b.lastActivity.Store(0)
		if ctx.Err() != nil {
			break
		}
		if err != nil {
			b.setErr(err.Error())
			slog.Warn("connexion Twitch perdue", "err", err, "retry_in", wait)
		}
		if time.Since(started) > time.Minute {
			wait = minWait // la connexion avait tenu : on repart de zéro
		}
		if !sleep(ctx, wait) {
			break
		}
		wait = min(wait*2, maxWait)
	}
	return nil
}

func sleep(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

// session ouvre une connexion et la garde jusqu'à une erreur, un Reconnect ou l'arrêt.
func (b *Bot) session(ctx context.Context, user, pass string) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	b.mu.Lock()
	b.cancelConn = cancel
	b.mu.Unlock()

	client := twitch.NewClient(user, pass)
	client.SendPings = true
	client.IdlePingInterval = 15 * time.Second
	self := strings.ToLower(user)

	client.OnConnect(func() {
		b.touch()
		b.setErr("")
		slog.Info("connecté à Twitch", "channel", b.channel, "as", self)
	})
	client.OnPongMessage(func(twitch.PongMessage) { b.touch() })
	client.OnPingMessage(func(twitch.PingMessage) { b.touch() })
	client.OnReconnectMessage(func(twitch.ReconnectMessage) {
		b.lastActivity.Store(0)
		slog.Info("Twitch demande une reconnexion")
	})
	client.OnNoticeMessage(func(m twitch.NoticeMessage) {
		// msg_duplicate, msg_ratelimit, msg_banned… : sans cela les échecs d'envoi sont invisibles.
		slog.Warn("notice Twitch", "id", m.MsgID, "text", m.Message)
	})
	client.OnPrivateMessage(func(m twitch.PrivateMessage) {
		b.touch()
		b.onMessage(client, self, m)
	})
	client.Join(b.channel)

	errc := make(chan error, 1)
	go func() { errc <- client.Connect() }()

	refresh := time.NewTicker(tokenRefreshEvery)
	defer refresh.Stop()
	for {
		select {
		case <-ctx.Done():
			_ = client.Disconnect()
			select {
			case <-errc:
			case <-time.After(5 * time.Second):
			}
			return nil
		case err := <-errc:
			if errors.Is(err, twitch.ErrClientDisconnected) {
				return nil
			}
			return err
		case <-refresh.C:
			// La bibliothèque se reconnecte seule après une coupure, avec le dernier jeton fourni.
			if _, p, err := b.creds.Credentials(ctx); err == nil {
				client.SetIRCToken(p)
			} else {
				slog.Warn("renouvellement du jeton Twitch", "err", err)
			}
		}
	}
}

// levelOf déduit le rôle d'un spectateur de ses badges.
func levelOf(login, channel string, badges map[string]int) model.Level {
	// Seule la présence compte : la valeur est une version (ex. subscriber/0 = abonné de moins d'un mois).
	has := func(k string) bool { _, ok := badges[k]; return ok }
	switch {
	case login == strings.ToLower(channel) || has("broadcaster"):
		return model.LevelBroadcaster
	case has("moderator"):
		return model.LevelModerator
	case has("vip"):
		return model.LevelVIP
	case has("subscriber") || has("founder"):
		return model.LevelSubscriber
	}
	return model.LevelEveryone
}

func (b *Bot) onMessage(client *twitch.Client, self string, m twitch.PrivateMessage) {
	login := strings.ToLower(m.User.Name)
	if login == self || !strings.HasPrefix(m.Message, "!") {
		return
	}
	select {
	case b.sem <- struct{}{}:
	default:
		slog.Warn("trop de commandes simultanées, message ignoré", "user", login)
		return
	}
	u := chat.User{Name: login, Level: levelOf(login, b.channel, m.User.Badges)}
	go func() {
		defer func() { <-b.sem }()
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		reply := b.handler.Handle(ctx, u, m.Message)
		if reply == "" {
			return
		}
		if !b.limiter.allow(time.Now()) {
			slog.Warn("limite d'envoi atteinte, réponse abandonnée", "user", login)
			return
		}
		client.Say(b.channel, reply)
	}()
}

// window est un limiteur « fenêtre glissante » : au plus max événements par période.
type window struct {
	mu    sync.Mutex
	max   int
	per   time.Duration
	times []time.Time
}

func newWindow(max int, per time.Duration) *window { return &window{max: max, per: per} }

func (w *window) allow(now time.Time) bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	keep := w.times[:0]
	for _, t := range w.times {
		if now.Sub(t) < w.per {
			keep = append(keep, t)
		}
	}
	w.times = keep
	if len(w.times) >= w.max {
		return false
	}
	w.times = append(w.times, now)
	return true
}
