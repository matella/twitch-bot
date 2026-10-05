// Package server expose l'interface d'administration (API JSON + page web).
//
// Toutes les routes, sauf /api/health, exigent une authentification HTTP Basic.
// Le stockage, Spotify, Twitch et le bot sont injectés via des interfaces.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"log/slog"
	"mime"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/matella/twitch-bot/internal/chat"
	"github.com/matella/twitch-bot/internal/commands"
	"github.com/matella/twitch-bot/internal/model"
)

// Store est la persistance utilisée par l'API.
type Store interface {
	ListCommands(ctx context.Context) ([]model.Command, error)
	AddCommand(ctx context.Context, c model.Command) error
	UpdateCommand(ctx context.Context, c model.Command) error
	DeleteCommand(ctx context.Context, name string) error
	RecentSongRequests(ctx context.Context, limit int) ([]model.SongRequest, error)
	DeleteSongRequest(ctx context.Context, id int64) error
	ClearSongRequests(ctx context.Context) error
	GetMusicSettings(ctx context.Context) (model.MusicSettings, error)
	SetMusicSettings(ctx context.Context, s model.MusicSettings) error
}

// Account est la connexion à un compte externe (Spotify, Twitch) pilotée depuis l'administration.
type Account interface {
	AuthURL(state string) string
	Exchange(ctx context.Context, code string) error
	Connected() bool
	Account() string
	Disconnect(ctx context.Context) error
}

// Spotify gère le compte Spotify du streamer et sa file de lecture. Peut être nil.
type Spotify interface {
	Account
	Playback(ctx context.Context) (*chat.Playback, error)
	Skip(ctx context.Context) error
}

// Twitch gère le compte Twitch du bot. Peut être nil (jeton fixe dans la configuration).
type Twitch interface {
	Account
}

// Bot expose l'état de la connexion à Twitch. Peut être nil.
type Bot interface {
	Connected() bool
	LastError() string
	Reconnect()
}

// Options configure le serveur.
type Options struct {
	Channel       string
	AdminUser     string
	AdminPassword string
	Assets        fs.FS // contenu statique (index.html, app.js, style.css)
	// TrustProxy : croire X-Forwarded-Proto / X-Forwarded-For (à n'activer que derrière un reverse proxy).
	TrustProxy bool
	// OnCommandsChanged est appelée après chaque modification des commandes (pour vider le cache du bot).
	OnCommandsChanged func()
	Now               func() time.Time // injectable pour les tests
}

type server struct {
	store   Store
	spotify Spotify
	twitch  Twitch
	bot     Bot
	opts    Options
	fails   *failLimiter

	userHash [sha256.Size]byte
	passHash [sha256.Size]byte
}

// New construit le handler HTTP complet.
func New(o Options, st Store, sp Spotify, tw Twitch, bot Bot) http.Handler {
	if o.Now == nil {
		o.Now = time.Now
	}
	s := &server{store: st, spotify: sp, twitch: tw, bot: bot, opts: o, fails: newFailLimiter(o.Now)}
	s.userHash = sha256.Sum256([]byte(o.AdminUser))
	s.passHash = sha256.Sum256([]byte(o.AdminPassword))

	api := http.NewServeMux()
	api.HandleFunc("GET /api/status", s.handleStatus)
	api.HandleFunc("GET /api/commands", s.listCommands)
	api.HandleFunc("POST /api/commands", s.createCommand)
	api.HandleFunc("PUT /api/commands/{name}", s.updateCommand)
	api.HandleFunc("DELETE /api/commands/{name}", s.deleteCommand)
	api.HandleFunc("GET /api/requests", s.listRequests)
	api.HandleFunc("DELETE /api/requests", s.clearRequests)
	api.HandleFunc("DELETE /api/requests/{id}", s.deleteRequest)
	api.HandleFunc("GET /api/music-settings", s.getMusicSettings)
	api.HandleFunc("PUT /api/music-settings", s.putMusicSettings)
	api.HandleFunc("GET /api/spotify/queue", s.spotifyQueue)
	api.HandleFunc("POST /api/spotify/skip", s.spotifySkip)

	spotifyFlow := s.oauthFlow("spotify", "Spotify n'est pas configuré (SPOTIFY_ID / SPOTIFY_SECRET)", nil)
	twitchFlow := s.oauthFlow("twitch", "La connexion Twitch n'est pas configurée (TWITCH_CLIENT_ID / TWITCH_CLIENT_SECRET)",
		func() {
			if s.bot != nil {
				s.bot.Reconnect() // reprend aussitôt avec le nouveau compte
			}
		})
	if s.spotify != nil {
		spotifyFlow.account = s.spotify
	}
	if s.twitch != nil {
		twitchFlow.account = s.twitch
	}
	api.HandleFunc("POST /api/spotify/disconnect", spotifyFlow.disconnect)
	api.HandleFunc("GET /auth/spotify/login", spotifyFlow.login)
	api.HandleFunc("GET /auth/spotify/callback", spotifyFlow.callback)
	api.HandleFunc("POST /api/twitch/disconnect", twitchFlow.disconnect)
	api.HandleFunc("GET /auth/twitch/login", twitchFlow.login)
	api.HandleFunc("GET /auth/twitch/callback", twitchFlow.callback)
	api.Handle("GET /", http.FileServerFS(o.Assets))

	root := http.NewServeMux()
	root.HandleFunc("GET /api/health", func(w http.ResponseWriter, _ *http.Request) {
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	})
	root.Handle("/", s.headers(s.auth(s.csrf(api))))
	return root
}

// ---- middlewares ----

func (s *server) headers(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Referrer-Policy", "no-referrer")
		h.Set("Content-Security-Policy", "default-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		next.ServeHTTP(w, r)
	})
}

// clientKey identifie l'appelant pour la limitation des tentatives de connexion.
func (s *server) clientKey(r *http.Request) string {
	if s.opts.TrustProxy {
		// Le dernier élément est celui ajouté par notre propre proxy ; les précédents sont falsifiables.
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			return strings.TrimSpace(parts[len(parts)-1])
		}
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := s.clientKey(r)
		if wait := s.fails.blockedFor(key); wait > 0 {
			w.Header().Set("Retry-After", strconv.Itoa(int(wait.Seconds())+1))
			writeErr(w, http.StatusTooManyRequests, "trop de tentatives, réessaie plus tard")
			return
		}
		u, p, ok := r.BasicAuth()
		uh := sha256.Sum256([]byte(u))
		ph := sha256.Sum256([]byte(p))
		// Les deux comparaisons sont toujours évaluées (temps constant, longueurs égales après hachage).
		userOK := subtle.ConstantTimeCompare(uh[:], s.userHash[:])
		passOK := subtle.ConstantTimeCompare(ph[:], s.passHash[:])
		if !ok || userOK&passOK != 1 {
			// Une requête sans identifiants est le premier échange normal d'un navigateur : pas une erreur.
			if ok {
				s.fails.fail(key)
			}
			w.Header().Set("WWW-Authenticate", `Basic realm="twitch-bot", charset="UTF-8"`)
			writeErr(w, http.StatusUnauthorized, "authentification requise")
			return
		}
		s.fails.reset(key)
		next.ServeHTTP(w, r)
	})
}

// csrf bloque les requêtes modifiantes venant d'un autre site : le navigateur renvoie
// automatiquement les identifiants Basic, une page tierce pourrait sinon agir à ta place.
func (s *server) csrf(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if sf := r.Header.Get("Sec-Fetch-Site"); sf != "" && sf != "same-origin" && sf != "none" {
				writeErr(w, http.StatusForbidden, "requête inter-sites refusée")
				return
			}
			if origin := r.Header.Get("Origin"); origin != "" {
				if u, err := url.Parse(origin); err != nil || u.Host != r.Host {
					writeErr(w, http.StatusForbidden, "origine refusée")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// failLimiter bloque temporairement un appelant après trop d'échecs d'authentification.
type failLimiter struct {
	now func() time.Time
	mu  sync.Mutex
	m   map[string]*failEntry
}

type failEntry struct {
	count   int
	first   time.Time
	blocked time.Time
}

const (
	maxFails   = 10
	failWindow = 10 * time.Minute
	blockFor   = 10 * time.Minute
)

func newFailLimiter(now func() time.Time) *failLimiter {
	return &failLimiter{now: now, m: map[string]*failEntry{}}
}

func (l *failLimiter) blockedFor(key string) time.Duration {
	l.mu.Lock()
	defer l.mu.Unlock()
	if e, ok := l.m[key]; ok {
		if left := e.blocked.Sub(l.now()); left > 0 {
			return left
		}
	}
	return 0
}

func (l *failLimiter) fail(key string) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := l.now()
	e := l.m[key]
	if e == nil || now.Sub(e.first) > failWindow {
		e = &failEntry{first: now}
		l.m[key] = e
	}
	e.count++
	if e.count >= maxFails {
		e.blocked = now.Add(blockFor)
		e.count, e.first = 0, now
	}
	if len(l.m) > 1000 {
		for k, v := range l.m {
			if now.Sub(v.first) > failWindow && now.After(v.blocked) {
				delete(l.m, k)
			}
		}
	}
}

func (l *failLimiter) reset(key string) {
	l.mu.Lock()
	delete(l.m, key)
	l.mu.Unlock()
}

// ---- helpers JSON ----

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	if v == nil {
		return
	}
	if err := json.NewEncoder(w).Encode(v); err != nil {
		slog.Warn("écriture de la réponse", "err", err)
	}
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

func serverErr(w http.ResponseWriter, what string, err error) {
	slog.Error(what, "err", err)
	writeErr(w, http.StatusInternalServerError, "erreur interne")
}

// decode lit un corps JSON. Exiger application/json force un « preflight » CORS
// pour toute requête inter-sites, ce qui est un second rempart contre le CSRF.
func decode(w http.ResponseWriter, r *http.Request, dst any) bool {
	mt, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
	if mt != "application/json" {
		writeErr(w, http.StatusUnsupportedMediaType, "Content-Type application/json requis")
		return false
	}
	r.Body = http.MaxBytesReader(w, r.Body, 16<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(dst); err != nil {
		writeErr(w, http.StatusBadRequest, "JSON invalide")
		return false
	}
	return true
}

// ---- statut ----

type accountStatus struct {
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected"`
	Account    string `json:"account,omitempty"`
}

type statusResponse struct {
	Channel      string        `json:"channel"`
	BotConnected bool          `json:"bot_connected"`
	BotError     string        `json:"bot_error,omitempty"`
	Twitch       accountStatus `json:"twitch"`
	Spotify      accountStatus `json:"spotify"`
}

func statusOf(a Account) accountStatus {
	st := accountStatus{Configured: true, Connected: a.Connected()}
	if st.Connected {
		st.Account = a.Account()
	}
	return st
}

func (s *server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	resp := statusResponse{Channel: s.opts.Channel}
	if s.bot != nil {
		resp.BotConnected = s.bot.Connected()
		if !resp.BotConnected {
			resp.BotError = s.bot.LastError()
		}
	}
	if s.twitch != nil {
		resp.Twitch = statusOf(s.twitch)
	}
	if s.spotify != nil {
		resp.Spotify = statusOf(s.spotify)
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- commandes ----

type commandBody struct {
	Name                string   `json:"name"`
	Response            string   `json:"response"`
	CooldownSeconds     *int     `json:"cooldown_seconds"`
	UserCooldownSeconds *int     `json:"user_cooldown_seconds"`
	Permission          string   `json:"permission"`
	Enabled             *bool    `json:"enabled"`
	Aliases             []string `json:"aliases"`
}

// toCommand valide le corps et construit la commande. name est déjà normalisé.
func (b commandBody) toCommand(name string) (model.Command, error) {
	cmd, err := commands.New(name, b.Response)
	if err != nil {
		return model.Command{}, err
	}
	if b.CooldownSeconds != nil {
		cmd.CooldownSeconds = *b.CooldownSeconds
	}
	if b.UserCooldownSeconds != nil {
		cmd.UserCooldownSeconds = *b.UserCooldownSeconds
	}
	for _, cd := range []int{cmd.CooldownSeconds, cmd.UserCooldownSeconds} {
		if err := commands.ValidateCooldown(cd); err != nil {
			return model.Command{}, err
		}
	}
	if cmd.Permission, err = model.ParseLevel(b.Permission); err != nil {
		return model.Command{}, err
	}
	if b.Enabled != nil {
		cmd.Enabled = *b.Enabled
	}
	if cmd.Aliases, err = commands.NormalizeAliases(cmd.Name, b.Aliases); err != nil {
		return model.Command{}, err
	}
	return cmd, nil
}

// conflict renvoie la commande qui utilise déjà le nom ou un alias de cmd (hors cmd elle-même).
func conflict(cmd model.Command, all []model.Command) (string, bool) {
	taken := map[string]string{} // nom ou alias -> commande propriétaire
	for _, c := range all {
		if c.Name == cmd.Name {
			continue
		}
		taken[c.Name] = c.Name
		for _, a := range c.Aliases {
			taken[a] = c.Name
		}
	}
	for _, n := range append([]string{cmd.Name}, cmd.Aliases...) {
		if owner, ok := taken[n]; ok {
			return fmt.Sprintf("« %s » est déjà utilisé par !%s", n, owner), true
		}
	}
	return "", false
}

// requestConflict renvoie un message si cmd empiète sur la commande de demande de musique.
func requestConflict(cmd model.Command, ms model.MusicSettings) (string, bool) {
	taken := map[string]bool{}
	for _, n := range ms.RequestNames() {
		if n != "" {
			taken[n] = true
		}
	}
	for _, n := range append([]string{cmd.Name}, cmd.Aliases...) {
		if taken[n] {
			return fmt.Sprintf("« %s » est utilisé par la commande de demande de musique (!%s)", n, ms.RequestCommand), true
		}
	}
	return "", false
}

// requestShadows renvoie un message si la commande de demande masquerait une commande personnalisée.
func requestShadows(ms model.MusicSettings, all []model.Command) (string, bool) {
	owner := map[string]string{}
	for _, c := range all {
		owner[c.Name] = c.Name
		for _, a := range c.Aliases {
			owner[a] = c.Name
		}
	}
	for _, n := range ms.RequestNames() {
		if n == "" {
			continue
		}
		if o, ok := owner[n]; ok {
			return fmt.Sprintf("« %s » est déjà utilisé par la commande personnalisée !%s", n, o), true
		}
	}
	return "", false
}

func (s *server) commandsChanged() {
	if s.opts.OnCommandsChanged != nil {
		s.opts.OnCommandsChanged()
	}
}

func (s *server) listCommands(w http.ResponseWriter, r *http.Request) {
	cmds, err := s.store.ListCommands(r.Context())
	if err != nil {
		serverErr(w, "liste des commandes", err)
		return
	}
	if cmds == nil {
		cmds = []model.Command{}
	}
	writeJSON(w, http.StatusOK, cmds)
}

func (s *server) createCommand(w http.ResponseWriter, r *http.Request) {
	var body commandBody
	if !decode(w, r, &body) {
		return
	}
	cmd, err := body.toCommand(commands.NormalizeName(body.Name))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	all, err := s.store.ListCommands(r.Context())
	if err != nil {
		serverErr(w, "création de la commande", err)
		return
	}
	if msg, bad := conflict(cmd, all); bad {
		writeErr(w, http.StatusConflict, msg)
		return
	}
	ms, err := s.store.GetMusicSettings(r.Context())
	if err != nil {
		serverErr(w, "création de la commande", err)
		return
	}
	if msg, bad := requestConflict(cmd, ms); bad {
		writeErr(w, http.StatusConflict, msg)
		return
	}
	switch err := s.store.AddCommand(r.Context(), cmd); {
	case errors.Is(err, model.ErrExists):
		writeErr(w, http.StatusConflict, "cette commande existe déjà")
	case err != nil:
		serverErr(w, "création de la commande", err)
	default:
		slog.Info("commande créée", "name", cmd.Name)
		s.commandsChanged()
		writeJSON(w, http.StatusCreated, cmd)
	}
}

func (s *server) updateCommand(w http.ResponseWriter, r *http.Request) {
	var body commandBody
	if !decode(w, r, &body) {
		return
	}
	cmd, err := body.toCommand(commands.NormalizeName(r.PathValue("name")))
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	all, err := s.store.ListCommands(r.Context())
	if err != nil {
		serverErr(w, "mise à jour de la commande", err)
		return
	}
	if msg, bad := conflict(cmd, all); bad {
		writeErr(w, http.StatusConflict, msg)
		return
	}
	ms, err := s.store.GetMusicSettings(r.Context())
	if err != nil {
		serverErr(w, "mise à jour de la commande", err)
		return
	}
	if msg, bad := requestConflict(cmd, ms); bad {
		writeErr(w, http.StatusConflict, msg)
		return
	}
	switch err := s.store.UpdateCommand(r.Context(), cmd); {
	case errors.Is(err, model.ErrNotFound):
		writeErr(w, http.StatusNotFound, "commande introuvable")
	case err != nil:
		serverErr(w, "mise à jour de la commande", err)
	default:
		slog.Info("commande modifiée", "name", cmd.Name)
		s.commandsChanged()
		writeJSON(w, http.StatusOK, cmd)
	}
}

func (s *server) deleteCommand(w http.ResponseWriter, r *http.Request) {
	name := commands.NormalizeName(r.PathValue("name"))
	switch err := s.store.DeleteCommand(r.Context(), name); {
	case errors.Is(err, model.ErrNotFound):
		writeErr(w, http.StatusNotFound, "commande introuvable")
	case err != nil:
		serverErr(w, "suppression de la commande", err)
	default:
		slog.Info("commande supprimée", "name", name)
		s.commandsChanged()
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// ---- historique des demandes ----

func (s *server) listRequests(w http.ResponseWriter, r *http.Request) {
	limit := 50
	if v := r.URL.Query().Get("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 200 {
			writeErr(w, http.StatusBadRequest, "limit invalide (1 à 200)")
			return
		}
		limit = n
	}
	reqs, err := s.store.RecentSongRequests(r.Context(), limit)
	if err != nil {
		serverErr(w, "liste des demandes", err)
		return
	}
	if reqs == nil {
		reqs = []model.SongRequest{}
	}
	writeJSON(w, http.StatusOK, reqs)
}

func (s *server) deleteRequest(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(r.PathValue("id"), 10, 64)
	if err != nil {
		writeErr(w, http.StatusBadRequest, "identifiant invalide")
		return
	}
	switch err := s.store.DeleteSongRequest(r.Context(), id); {
	case errors.Is(err, model.ErrNotFound):
		writeErr(w, http.StatusNotFound, "demande introuvable")
	case err != nil:
		serverErr(w, "suppression de la demande", err)
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

func (s *server) clearRequests(w http.ResponseWriter, r *http.Request) {
	if err := s.store.ClearSongRequests(r.Context()); err != nil {
		serverErr(w, "vidage de l'historique", err)
		return
	}
	slog.Info("historique des demandes vidé")
	writeJSON(w, http.StatusOK, map[string]string{"status": "cleared"})
}

// ---- réglages de la musique ----

func (s *server) getMusicSettings(w http.ResponseWriter, r *http.Request) {
	ms, err := s.store.GetMusicSettings(r.Context())
	if err != nil {
		serverErr(w, "lecture des réglages de la musique", err)
		return
	}
	writeJSON(w, http.StatusOK, ms)
}

func (s *server) putMusicSettings(w http.ResponseWriter, r *http.Request) {
	var ms model.MusicSettings
	if !decode(w, r, &ms) {
		return
	}
	name, aliases, err := commands.NormalizeRequestCommand(ms.RequestCommand, ms.RequestAliases)
	if err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	ms.RequestCommand, ms.RequestAliases = name, aliases
	if err := ms.Normalize(); err != nil {
		writeErr(w, http.StatusBadRequest, err.Error())
		return
	}
	all, err := s.store.ListCommands(r.Context())
	if err != nil {
		serverErr(w, "enregistrement des réglages de la musique", err)
		return
	}
	if msg, bad := requestShadows(ms, all); bad {
		writeErr(w, http.StatusConflict, msg)
		return
	}
	if err := s.store.SetMusicSettings(r.Context(), ms); err != nil {
		serverErr(w, "enregistrement des réglages de la musique", err)
		return
	}
	slog.Info("réglages de la musique modifiés")
	writeJSON(w, http.StatusOK, ms)
}

// ---- file Spotify ----

type trackDTO struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Artist          string `json:"artist"`
	DurationSeconds int    `json:"duration_seconds"`
	Explicit        bool   `json:"explicit"`
}

func toDTO(t chat.Track) trackDTO {
	return trackDTO{ID: t.ID, Name: t.Name, Artist: t.Artist, DurationSeconds: int(t.Duration.Seconds()), Explicit: t.Explicit}
}

// spotifyErr traduit une erreur du lecteur en réponse HTTP.
func spotifyErr(w http.ResponseWriter, what string, err error) {
	switch {
	case errors.Is(err, chat.ErrNotConnected):
		writeErr(w, http.StatusConflict, "Spotify n'est pas connecté")
	case errors.Is(err, chat.ErrNoDevice):
		writeErr(w, http.StatusConflict, "aucun lecteur Spotify actif")
	default:
		slog.Error(what, "err", err)
		writeErr(w, http.StatusBadGateway, "erreur Spotify")
	}
}

func (s *server) spotifyQueue(w http.ResponseWriter, r *http.Request) {
	if s.spotify == nil {
		writeErr(w, http.StatusNotFound, "Spotify n'est pas configuré (SPOTIFY_ID / SPOTIFY_SECRET)")
		return
	}
	pb, err := s.spotify.Playback(r.Context())
	if err != nil {
		spotifyErr(w, "lecture de la file Spotify", err)
		return
	}
	resp := struct {
		Current *trackDTO  `json:"current"`
		Queue   []trackDTO `json:"queue"`
	}{Queue: make([]trackDTO, 0, len(pb.Queue))}
	if pb.Current != nil {
		c := toDTO(*pb.Current)
		resp.Current = &c
	}
	for _, t := range pb.Queue {
		resp.Queue = append(resp.Queue, toDTO(t))
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *server) spotifySkip(w http.ResponseWriter, r *http.Request) {
	if s.spotify == nil {
		writeErr(w, http.StatusNotFound, "Spotify n'est pas configuré (SPOTIFY_ID / SPOTIFY_SECRET)")
		return
	}
	if err := s.spotify.Skip(r.Context()); err != nil {
		spotifyErr(w, "skip Spotify", err)
		return
	}
	slog.Info("titre passé depuis l'administration")
	writeJSON(w, http.StatusOK, map[string]string{"status": "skipped"})
}

// ---- connexion OAuth (Spotify, Twitch) ----

// oauth regroupe les routes de connexion d'un compte externe.
type oauth struct {
	s             *server
	name          string // "spotify" ou "twitch" : nom de route, de cookie et de paramètre de retour
	notConfigured string
	account       Account // nil si non configuré
	onChange      func()  // appelée après une connexion ou une déconnexion réussie
}

func (s *server) oauthFlow(name, notConfigured string, onChange func()) *oauth {
	return &oauth{s: s, name: name, notConfigured: notConfigured, onChange: onChange}
}

func (o *oauth) cookie(r *http.Request, value string, maxAge int) *http.Cookie {
	return &http.Cookie{
		Name: "oauth_state_" + o.name, Value: value, Path: "/auth/" + o.name, MaxAge: maxAge,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: o.s.isHTTPS(r),
	}
}

func (o *oauth) require(w http.ResponseWriter) bool {
	if o.account == nil {
		writeErr(w, http.StatusNotFound, o.notConfigured)
		return false
	}
	return true
}

func (s *server) isHTTPS(r *http.Request) bool {
	return r.TLS != nil || (s.opts.TrustProxy && r.Header.Get("X-Forwarded-Proto") == "https")
}

func (o *oauth) login(w http.ResponseWriter, r *http.Request) {
	if !o.require(w) {
		return
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		serverErr(w, "génération du state OAuth", err)
		return
	}
	state := hex.EncodeToString(b[:])
	http.SetCookie(w, o.cookie(r, state, 600))
	http.Redirect(w, r, o.account.AuthURL(state), http.StatusFound)
}

func (o *oauth) callback(w http.ResponseWriter, r *http.Request) {
	if !o.require(w) {
		return
	}
	q := r.URL.Query()
	c, err := r.Cookie("oauth_state_" + o.name)
	if err != nil || c.Value == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(q.Get("state"))) != 1 {
		writeErr(w, http.StatusBadRequest, "paramètre state invalide, relance la connexion depuis la page d'administration")
		return
	}
	http.SetCookie(w, o.cookie(r, "", -1))
	back := func(result string) { http.Redirect(w, r, "/?"+o.name+"="+result, http.StatusFound) }
	if q.Get("error") != "" {
		back("denied")
		return
	}
	code := q.Get("code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "code manquant")
		return
	}
	if err := o.account.Exchange(r.Context(), code); err != nil {
		slog.Error("échange du code OAuth", "provider", o.name, "err", err)
		back("error")
		return
	}
	slog.Info("compte connecté", "provider", o.name)
	if o.onChange != nil {
		o.onChange()
	}
	back("connected")
}

func (o *oauth) disconnect(w http.ResponseWriter, r *http.Request) {
	if !o.require(w) {
		return
	}
	if err := o.account.Disconnect(r.Context()); err != nil {
		serverErr(w, "déconnexion du compte "+o.name, err)
		return
	}
	slog.Info("compte déconnecté", "provider", o.name)
	if o.onChange != nil {
		o.onChange()
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}
