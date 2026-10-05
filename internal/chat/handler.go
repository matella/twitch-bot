// Package chat contient la logique du bot, indépendante de Twitch et de Spotify :
// elle reçoit une ligne de chat et renvoie la réponse à envoyer (ou "").
package chat

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
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
	ID       string
	Name     string
	Artist   string // artiste principal
	Artists  []string
	Duration time.Duration
	Explicit bool
}

// Playback est l'état de lecture : titre en cours et titres à venir.
type Playback struct {
	Current *Track // nil si rien ne joue
	Queue   []Track
}

// User est l'auteur d'un message.
type User struct {
	Name  string      // login Twitch, en minuscules
	Level model.Level // rôle déduit des badges
}

// Store est la persistance dont le bot a besoin.
type Store interface {
	ListCommands(ctx context.Context) ([]model.Command, error)
	AddCommand(ctx context.Context, c model.Command) error
	UpdateCommand(ctx context.Context, c model.Command) error
	DeleteCommand(ctx context.Context, name string) error
	IncrementUse(ctx context.Context, name string) (int64, error)
	AddSongRequest(ctx context.Context, r model.SongRequest) error
	RecentSongRequests(ctx context.Context, limit int) ([]model.SongRequest, error)
	GetMusicSettings(ctx context.Context) (model.MusicSettings, error)
}

// Music est le service de musique (Spotify).
type Music interface {
	Connected() bool
	Find(ctx context.Context, query string) (*Track, error)
	Queue(ctx context.Context, trackID string) error
	Playback(ctx context.Context) (*Playback, error)
	Skip(ctx context.Context) error
}

// Options configure le Handler.
type Options struct {
	Channel      string
	SongCooldown time.Duration
	Now          func() time.Time // injectable pour les tests
	Intn         func(int) int    // idem, pour {random} et {pick:...}
}

// Handler traite les messages du chat.
type Handler struct {
	store Store
	music Music // peut être nil si Spotify n'est pas configuré
	opts  Options

	mu       sync.Mutex
	lastUsed map[string]time.Time // délais : clé « cmd:nom », « ucmd:nom:user », « song:user »…

	cacheMu   sync.Mutex
	cacheAt   time.Time
	cacheList []model.Command
}

const (
	// maxQueryRunes limite la longueur d'une recherche de titre.
	maxQueryRunes = 100
	// infoCooldown est le délai global de !queue, !np et !help : sans lui, un spectateur
	// ferait répondre le bot en boucle (et Twitch bride les bots trop bavards).
	infoCooldown = 8 * time.Second
	// cacheTTL est la durée pendant laquelle la liste des commandes est gardée en mémoire.
	cacheTTL = 3 * time.Second
	// manageLevel est le rôle requis pour !addcmd, !editcmd et !delcmd.
	manageLevel = model.LevelModerator
	// historyScan est le nombre de demandes récentes examinées pour retrouver celles en attente.
	historyScan = 100
)

// New crée un Handler. music peut être nil.
func New(store Store, music Music, opts Options) *Handler {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Intn == nil {
		opts.Intn = rand.IntN
	}
	return &Handler{store: store, music: music, opts: opts, lastUsed: make(map[string]time.Time)}
}

// InvalidateCommands force le rechargement des commandes (après une modification depuis l'administration).
func (h *Handler) InvalidateCommands() {
	h.cacheMu.Lock()
	h.cacheAt = time.Time{}
	h.cacheMu.Unlock()
}

// Handle traite une ligne de chat et renvoie le message à publier, ou "" s'il n'y a rien à répondre.
func (h *Handler) Handle(ctx context.Context, u User, text string) string {
	name, args, ok := commands.Parse(text)
	if !ok {
		return ""
	}
	var reply string
	switch name {
	case "queue":
		reply = h.limited("queue", func() string { return h.queue(ctx) })
	case "np", "currentsong":
		reply = h.limited("np", func() string { return h.nowPlaying(ctx) })
	case "help":
		reply = h.limited("help", func() string { return h.help(ctx, u) })
	case "skip":
		reply = h.skip(ctx, u)
	case "addcmd", "editcmd", "delcmd":
		reply = h.manage(ctx, u, name, args)
	default:
		// Le nom de la commande de demande de musique est configurable depuis
		// l'administration : il est donc résolu ici, avant de retomber sur les
		// commandes personnalisées.
		if ms := h.settings(ctx); ms.IsRequestCommand(name) {
			reply = h.songRequest(ctx, u, args, ms)
		} else {
			reply = h.custom(ctx, u, name, args)
		}
	}
	return commands.SanitizeOutgoing(reply)
}

// ---- délais ----

type cooldown struct {
	key string
	d   time.Duration
}

// reserve vérifie les délais puis les consomme, en une seule étape atomique
// (deux messages simultanés ne peuvent pas passer tous les deux). Renvoie le temps
// restant avant autorisation, ou 0 si les délais ont été consommés.
func (h *Handler) reserve(cds ...cooldown) time.Duration {
	h.mu.Lock()
	defer h.mu.Unlock()
	now := h.opts.Now()
	var wait time.Duration
	for _, c := range cds {
		if c.d <= 0 {
			continue
		}
		if last, ok := h.lastUsed[c.key]; ok {
			wait = max(wait, c.d-now.Sub(last))
		}
	}
	if wait > 0 {
		return wait
	}
	for _, c := range cds {
		if c.d > 0 {
			h.lastUsed[c.key] = now
		}
	}
	if len(h.lastUsed) > 2000 {
		for k, t := range h.lastUsed {
			if now.Sub(t) > time.Hour {
				delete(h.lastUsed, k)
			}
		}
	}
	return 0
}

// release annule un délai consommé (la demande n'a finalement rien coûté).
func (h *Handler) release(key string) {
	h.mu.Lock()
	delete(h.lastUsed, key)
	h.mu.Unlock()
}

// limited exécute f sauf si la commande est en délai global (silence dans ce cas).
func (h *Handler) limited(name string, f func() string) string {
	if h.reserve(cooldown{"cmd:" + name, infoCooldown}) > 0 {
		return ""
	}
	return f()
}

// ---- commandes personnalisées ----

func (h *Handler) commandList(ctx context.Context) ([]model.Command, error) {
	h.cacheMu.Lock()
	defer h.cacheMu.Unlock()
	if !h.cacheAt.IsZero() && h.opts.Now().Sub(h.cacheAt) < cacheTTL {
		return h.cacheList, nil
	}
	list, err := h.store.ListCommands(ctx)
	if err != nil {
		return nil, err
	}
	h.cacheList, h.cacheAt = list, h.opts.Now()
	return list, nil
}

// lookup trouve une commande par son nom ou par un de ses alias.
func (h *Handler) lookup(ctx context.Context, name string) (*model.Command, error) {
	list, err := h.commandList(ctx)
	if err != nil {
		return nil, err
	}
	for i := range list {
		if list[i].Name == name {
			return &list[i], nil
		}
	}
	for i := range list {
		for _, a := range list[i].Aliases {
			if a == name {
				return &list[i], nil
			}
		}
	}
	return nil, model.ErrNotFound
}

func (h *Handler) custom(ctx context.Context, u User, name, args string) string {
	cmd, err := h.lookup(ctx, name)
	if err != nil {
		if !errors.Is(err, model.ErrNotFound) {
			slog.Error("lecture de la commande", "name", name, "err", err)
		}
		return ""
	}
	if !cmd.Enabled || u.Level < cmd.Permission {
		return ""
	}
	if h.reserve(
		cooldown{"cmd:" + cmd.Name, time.Duration(cmd.CooldownSeconds) * time.Second},
		cooldown{"ucmd:" + cmd.Name + ":" + u.Name, time.Duration(cmd.UserCooldownSeconds) * time.Second},
	) > 0 {
		return ""
	}
	count, err := h.store.IncrementUse(ctx, cmd.Name)
	if err != nil {
		slog.Warn("compteur de la commande", "name", cmd.Name, "err", err)
		count = cmd.UseCount + 1
	}
	return commands.Render(cmd.Response, commands.Vars{
		User: u.Name, Channel: h.opts.Channel, Args: args, Count: count, Intn: h.opts.Intn,
	})
}

func (h *Handler) help(ctx context.Context, u User) string {
	msg := "Commandes : !" + h.settings(ctx).RequestCommand + " <titre ou lien Spotify>, !queue, !np"
	cmds, err := h.commandList(ctx)
	if err != nil {
		slog.Error("liste des commandes", "err", err)
		return msg
	}
	for _, c := range cmds {
		if c.Enabled && u.Level >= c.Permission {
			msg += ", !" + c.Name
		}
	}
	return msg
}

// manage gère !addcmd, !editcmd et !delcmd (modérateurs).
func (h *Handler) manage(ctx context.Context, u User, verb, args string) string {
	if u.Level < manageLevel {
		return ""
	}
	fields := strings.Fields(args)
	if len(fields) == 0 {
		if verb == "delcmd" {
			return "Utilisation : !delcmd <nom>"
		}
		return fmt.Sprintf("Utilisation : !%s <nom> <réponse>", verb)
	}
	name := commands.NormalizeName(fields[0])
	response := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(args), fields[0]))
	defer h.InvalidateCommands()

	switch verb {
	case "delcmd":
		switch err := h.store.DeleteCommand(ctx, name); {
		case errors.Is(err, model.ErrNotFound):
			return fmt.Sprintf("!%s n'existe pas.", name)
		case err != nil:
			slog.Error("suppression de la commande", "name", name, "err", err)
			return "Erreur interne, réessaie plus tard."
		}
		slog.Info("commande supprimée depuis le chat", "name", name, "by", u.Name)
		return fmt.Sprintf("!%s supprimée.", name)

	case "addcmd":
		cmd, err := commands.New(name, response)
		if err != nil {
			return "Impossible : " + err.Error()
		}
		if _, err := h.lookup(ctx, cmd.Name); err == nil {
			return fmt.Sprintf("!%s existe déjà (utilise !editcmd).", cmd.Name)
		}
		switch err := h.store.AddCommand(ctx, cmd); {
		case errors.Is(err, model.ErrExists):
			return fmt.Sprintf("!%s existe déjà (utilise !editcmd).", cmd.Name)
		case err != nil:
			slog.Error("création de la commande", "name", cmd.Name, "err", err)
			return "Erreur interne, réessaie plus tard."
		}
		slog.Info("commande créée depuis le chat", "name", cmd.Name, "by", u.Name)
		return fmt.Sprintf("!%s ajoutée.", cmd.Name)

	default: // editcmd
		list, err := h.store.ListCommands(ctx) // lecture directe : pas de cache avant une écriture
		if err != nil {
			slog.Error("liste des commandes", "err", err)
			return "Erreur interne, réessaie plus tard."
		}
		var cmd *model.Command
		for i := range list {
			if list[i].Name == name {
				cmd = &list[i]
			}
		}
		if cmd == nil {
			return fmt.Sprintf("!%s n'existe pas (utilise !addcmd).", name)
		}
		if err := commands.ValidateResponse(response); err != nil {
			return "Impossible : " + err.Error()
		}
		cmd.Response = strings.TrimSpace(response)
		if err := h.store.UpdateCommand(ctx, *cmd); err != nil {
			slog.Error("mise à jour de la commande", "name", name, "err", err)
			return "Erreur interne, réessaie plus tard."
		}
		slog.Info("commande modifiée depuis le chat", "name", name, "by", u.Name)
		return fmt.Sprintf("!%s modifiée.", name)
	}
}

// ---- musique ----

func (h *Handler) musicReady() bool { return h.music != nil && h.music.Connected() }

func (h *Handler) settings(ctx context.Context) model.MusicSettings {
	ms, err := h.store.GetMusicSettings(ctx)
	if err != nil {
		slog.Error("réglages de la musique", "err", err)
		return model.DefaultMusicSettings()
	}
	return ms
}

// pendingRequests renvoie les demandes du bot encore dans la file Spotify, dans l'ordre de lecture.
func (h *Handler) pendingRequests(ctx context.Context, pb *Playback) []model.SongRequest {
	reqs, err := h.store.RecentSongRequests(ctx, historyScan)
	if err != nil {
		slog.Error("lecture des demandes", "err", err)
		return nil
	}
	byTrack := map[string]model.SongRequest{} // la plus récente d'abord : on garde la première
	for _, r := range reqs {
		if _, ok := byTrack[r.TrackID]; !ok && r.Status == model.StatusQueued {
			byTrack[r.TrackID] = r
		}
	}
	var out []model.SongRequest
	for _, t := range pb.Queue {
		if r, ok := byTrack[t.ID]; ok {
			out = append(out, r)
		}
	}
	return out
}

func (h *Handler) queue(ctx context.Context) string {
	if h.musicReady() {
		pb, err := h.music.Playback(ctx)
		if err == nil {
			pending := h.pendingRequests(ctx, pb)
			if len(pending) == 0 {
				return "Aucune demande en attente."
			}
			if len(pending) > 5 {
				pending = pending[:5]
			}
			return "À venir : " + joinRequests(pending)
		}
		slog.Warn("file Spotify indisponible, repli sur l'historique", "err", err)
	}
	reqs, err := h.store.RecentSongRequests(ctx, 5)
	if err != nil {
		slog.Error("lecture des demandes", "err", err)
		return "Erreur interne, réessaie plus tard."
	}
	var queued []model.SongRequest
	for _, r := range reqs {
		if r.Status == model.StatusQueued {
			queued = append(queued, r)
		}
	}
	if len(queued) == 0 {
		return "Aucune demande récente."
	}
	return "Dernières demandes : " + joinRequests(queued)
}

func joinRequests(reqs []model.SongRequest) string {
	parts := make([]string, len(reqs))
	for i, r := range reqs {
		parts[i] = fmt.Sprintf("%s - %s (%s)", r.Artist, r.TrackName, r.RequestedBy)
	}
	return strings.Join(parts, " | ")
}

func (h *Handler) nowPlaying(ctx context.Context) string {
	if !h.musicReady() {
		return "Les demandes de musique ne sont pas disponibles pour le moment."
	}
	pb, err := h.music.Playback(ctx)
	if err != nil {
		slog.Error("lecture de l'état Spotify", "err", err)
		return "Erreur Spotify, réessaie plus tard."
	}
	if pb.Current == nil {
		return "Aucun titre en cours de lecture."
	}
	return fmt.Sprintf("En cours : %s - %s", pb.Current.Artist, pb.Current.Name)
}

func (h *Handler) skip(ctx context.Context, u User) string {
	ms := h.settings(ctx)
	if u.Level < ms.SkipLevel {
		return ""
	}
	if !h.musicReady() {
		return "Les demandes de musique ne sont pas disponibles pour le moment."
	}
	if h.reserve(cooldown{"cmd:skip", 3 * time.Second}) > 0 {
		return ""
	}
	if err := h.music.Skip(ctx); err != nil {
		if errors.Is(err, ErrNoDevice) {
			return "Impossible de passer le titre : aucun lecteur Spotify actif."
		}
		slog.Error("skip spotify", "err", err)
		return "Erreur Spotify, réessaie plus tard."
	}
	slog.Info("titre passé", "by", u.Name)
	return "Titre passé."
}

// refusal renvoie la raison pour laquelle un titre est refusé par les réglages, ou "".
func refusal(t *Track, ms model.MusicSettings) string {
	if ms.BlockExplicit && t.Explicit {
		return "les titres explicites sont refusés"
	}
	if ms.MaxDurationSeconds > 0 && t.Duration > time.Duration(ms.MaxDurationSeconds)*time.Second {
		return fmt.Sprintf("titre trop long (%d min max)", (ms.MaxDurationSeconds+59)/60)
	}
	hay := strings.ToLower(t.Name + " " + strings.Join(t.Artists, " "))
	if t.Artist != "" && len(t.Artists) == 0 {
		hay += " " + strings.ToLower(t.Artist)
	}
	for _, term := range ms.Blocklist {
		if strings.Contains(hay, term) {
			return "ce titre ou cet artiste n'est pas autorisé"
		}
	}
	return ""
}

func (h *Handler) songRequest(ctx context.Context, u User, query string, ms model.MusicSettings) string {
	if !h.musicReady() {
		return "Les demandes de musique ne sont pas disponibles pour le moment."
	}
	if u.Level < ms.RequestLevel {
		return fmt.Sprintf("@%s les demandes sont réservées aux %s.", u.Name, ms.RequestLevel.Label())
	}
	if query == "" {
		return "Utilisation : !" + ms.RequestCommand + " <titre ou lien Spotify>"
	}
	if utf8.RuneCountInString(query) > maxQueryRunes {
		return fmt.Sprintf("Requête trop longue (%d caractères max).", maxQueryRunes)
	}

	// Le délai est consommé avant d'appeler Spotify : les demandes simultanées d'un même
	// spectateur ne passent pas, et les échecs ne permettent pas de marteler l'API.
	key := "song:" + u.Name
	if left := h.reserve(cooldown{key, h.opts.SongCooldown}); left > 0 {
		return fmt.Sprintf("@%s patiente encore %ds avant une nouvelle demande.", u.Name, int(left.Seconds())+1)
	}

	track, err := h.music.Find(ctx, query)
	switch {
	case errors.Is(err, ErrNoResult):
		h.release(key) // une faute de frappe ne doit pas coûter le délai
		return fmt.Sprintf("Aucun résultat pour « %s ».", query)
	case err != nil:
		slog.Error("recherche spotify", "err", err)
		return "Erreur Spotify, réessaie plus tard."
	}

	if why := refusal(track, ms); why != "" {
		h.release(key)
		return fmt.Sprintf("@%s impossible : %s.", u.Name, why)
	}
	if why := h.queueLimit(ctx, u, track, ms); why != "" {
		h.release(key)
		return fmt.Sprintf("@%s %s", u.Name, why)
	}

	req := model.SongRequest{TrackID: track.ID, TrackName: track.Name, Artist: track.Artist, RequestedBy: u.Name}
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
	return fmt.Sprintf("@%s ✓ %s - %s ajouté à la file !", u.Name, track.Artist, track.Name)
}

// queueLimit applique les règles qui dépendent de l'état de la file : doublons et plafonds.
// Si l'état de lecture est illisible, les règles sont ignorées plutôt que de bloquer toutes les demandes.
func (h *Handler) queueLimit(ctx context.Context, u User, t *Track, ms model.MusicSettings) string {
	pb, err := h.music.Playback(ctx)
	if err != nil {
		slog.Warn("état de lecture Spotify illisible : doublons et plafonds ignorés", "err", err)
		return ""
	}
	if pb.Current != nil && pb.Current.ID == t.ID {
		return "ce titre est en train de jouer."
	}
	for _, q := range pb.Queue {
		if q.ID == t.ID {
			return "ce titre est déjà dans la file."
		}
	}
	if ms.MaxPending == 0 && ms.MaxPerUser == 0 {
		return ""
	}
	pending := h.pendingRequests(ctx, pb)
	if ms.MaxPending > 0 && len(pending) >= ms.MaxPending {
		return "la file de demandes est pleine, réessaie plus tard."
	}
	if ms.MaxPerUser > 0 {
		n := 0
		for _, r := range pending {
			if r.RequestedBy == u.Name {
				n++
			}
		}
		if n >= ms.MaxPerUser {
			return fmt.Sprintf("tu as déjà %d demande(s) en attente.", n)
		}
	}
	return ""
}

func (h *Handler) record(ctx context.Context, r model.SongRequest) {
	if err := h.store.AddSongRequest(ctx, r); err != nil {
		slog.Error("enregistrement de la demande", "err", err)
	}
}
