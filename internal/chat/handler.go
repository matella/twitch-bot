// Package chat contient la logique du bot, indépendante de Twitch et de Spotify :
// elle reçoit une ligne de chat et renvoie la réponse à envoyer (ou "").
package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/matella/twitch-bot/internal/commands"
	"github.com/matella/twitch-bot/internal/model"
)

// Erreurs que doit renvoyer une implémentation de Music.
var (
	ErrNoResult     = errors.New("aucun résultat")
	ErrNotConnected = errors.New("spotify non connecté")
	ErrNoDevice     = errors.New("aucun lecteur spotify actif")
)

// Track est un titre trouvé sur Spotify.
type Track struct {
	ID     string
	Name   string
	Artist string
}

// Store est la persistance dont le bot a besoin.
type Store interface {
	GetCommand(ctx context.Context, name string) (*model.Command, error)
	ListCommands(ctx context.Context) ([]model.Command, error)
	AddSongRequest(ctx context.Context, r model.SongRequest) error
	RecentSongRequests(ctx context.Context, limit int) ([]model.SongRequest, error)
}

// Music est le service de musique (Spotify).
type Music interface {
	Connected() bool
	Find(ctx context.Context, query string) (*Track, error)
	Queue(ctx context.Context, trackID string) error
}

// Options configure le Handler.
type Options struct {
	Channel      string
	SongCooldown time.Duration
	Now          func() time.Time // injectable pour les tests
}

// Handler traite les messages du chat.
type Handler struct {
	store Store
	music Music // peut être nil si Spotify n'est pas configuré
	opts  Options

	mu       sync.Mutex
	lastCmd  map[string]time.Time // délai global par commande
	lastSong map[string]time.Time // délai par spectateur
}

// maxQueryRunes limite la longueur d'une recherche de titre.
const maxQueryRunes = 100

// New crée un Handler. music peut être nil.
func New(store Store, music Music, opts Options) *Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	return &Handler{
		store:    store,
		music:    music,
		opts:     opts,
		lastCmd:  make(map[string]time.Time),
		lastSong: make(map[string]time.Time),
	}
}

// Handle traite une ligne de chat envoyée par user (login Twitch, en minuscules)
// et renvoie le message à publier, ou "" s'il n'y a rien à répondre.
func (h *Handler) Handle(ctx context.Context, user, text string) string {
	name, args, ok := commands.Parse(text)
	if !ok {
		return ""
	}
	var reply string
	switch name {
	case "song", "sr":
		reply = h.songRequest(ctx, user, args)
	case "queue":
		reply = h.recent(ctx)
	case "help":
		reply = h.help(ctx)
	default:
		reply = h.custom(ctx, name, user, args)
	}
	return commands.SanitizeOutgoing(reply)
}

func (h *Handler) custom(ctx context.Context, name, user, args string) string {
	cmd, err := h.store.GetCommand(ctx, name)
	if err != nil {
		if !errors.Is(err, model.ErrNotFound) {
			slog.Error("lecture de la commande", "name", name, "err", err)
		}
		return ""
	}
	cd := time.Duration(cmd.CooldownSeconds) * time.Second
	if h.cooldownLeft(h.lastCmd, name, cd) > 0 {
		return ""
	}
	h.touch(h.lastCmd, name)
	return commands.Render(cmd.Response, user, h.opts.Channel, args)
}

func (h *Handler) help(ctx context.Context) string {
	msg := "Commandes : !song <titre ou lien Spotify>, !queue"
	cmds, err := h.store.ListCommands(ctx)
	if err != nil {
		slog.Error("liste des commandes", "err", err)
		return msg
	}
	for _, c := range cmds {
		msg += ", !" + c.Name
	}
	return msg
}

func (h *Handler) recent(ctx context.Context) string {
	reqs, err := h.store.RecentSongRequests(ctx, 5)
	if err != nil {
		slog.Error("lecture des demandes", "err", err)
		return "Erreur interne, réessaie plus tard."
	}
	var parts []string
	for _, r := range reqs {
		if r.Status == model.StatusQueued {
			parts = append(parts, fmt.Sprintf("%s - %s (%s)", r.Artist, r.TrackName, r.RequestedBy))
		}
	}
	if len(parts) == 0 {
		return "Aucune demande récente."
	}
	return "Dernières demandes : " + strings.Join(parts, " | ")
}

func (h *Handler) songRequest(ctx context.Context, user, query string) string {
	if h.music == nil || !h.music.Connected() {
		return "Les demandes de musique ne sont pas disponibles pour le moment."
	}
	if query == "" {
		return "Utilisation : !song <titre ou lien Spotify>"
	}
	if utf8.RuneCountInString(query) > maxQueryRunes {
		return fmt.Sprintf("Requête trop longue (%d caractères max).", maxQueryRunes)
	}
	if left := h.cooldownLeft(h.lastSong, user, h.opts.SongCooldown); left > 0 {
		return fmt.Sprintf("@%s patiente encore %ds avant une nouvelle demande.", user, int(left.Seconds())+1)
	}

	track, err := h.music.Find(ctx, query)
	switch {
	case errors.Is(err, ErrNoResult):
		return fmt.Sprintf("Aucun résultat pour « %s ».", query)
	case err != nil:
		slog.Error("recherche spotify", "err", err)
		return "Erreur Spotify, réessaie plus tard."
	}

	req := model.SongRequest{TrackID: track.ID, TrackName: track.Name, Artist: track.Artist, RequestedBy: user}
	if err := h.music.Queue(ctx, track.ID); err != nil {
		req.Status = model.StatusFailed
		h.record(ctx, req)
		if errors.Is(err, ErrNoDevice) {
			return "Impossible d'ajouter le titre : aucun lecteur Spotify actif."
		}
		slog.Error("ajout à la file spotify", "err", err)
		return "Erreur Spotify, réessaie plus tard."
	}

	req.Status = model.StatusQueued
	h.record(ctx, req)
	h.touch(h.lastSong, user)
	return fmt.Sprintf("@%s ✓ %s - %s ajouté à la file !", user, track.Artist, track.Name)
}

func (h *Handler) record(ctx context.Context, r model.SongRequest) {
	if err := h.store.AddSongRequest(ctx, r); err != nil {
		slog.Error("enregistrement de la demande", "err", err)
	}
}

// cooldownLeft renvoie le temps restant avant que key puisse réutiliser m.
func (h *Handler) cooldownLeft(m map[string]time.Time, key string, d time.Duration) time.Duration {
	if d <= 0 {
		return 0
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if last, ok := m[key]; ok {
		if left := d - h.opts.Now().Sub(last); left > 0 {
			return left
		}
	}
	return 0
}

// touch enregistre l'utilisation de key et purge les entrées anciennes.
func (h *Handler) touch(m map[string]time.Time, key string) {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.opts.Now()
	m[key] = now
	if len(m) > 2000 {
		for k, t := range m {
			if now.Sub(t) > time.Hour {
				delete(m, k)
			}
		}
	}
}
