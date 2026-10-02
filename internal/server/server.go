// Package server expose l'interface d'administration (API JSON + page web).
//
// Toutes les routes, sauf /api/health, exigent une authentification HTTP Basic.
// Le paquet ne dépend que de la bibliothèque standard : le stockage, Spotify et
// le bot sont injectés via des interfaces.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"strconv"
	"strings"

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
}

// Spotify gère la connexion du compte Spotify du streamer. Peut être nil.
type Spotify interface {
	AuthURL(state string) string
	Exchange(ctx context.Context, code string) error
	Connected() bool
	Account() string
	Disconnect(ctx context.Context) error
}

// Bot expose l'état de la connexion à Twitch.
type Bot interface {
	Connected() bool
}

// Options configure le serveur.
type Options struct {
	Channel       string
	AdminUser     string
	AdminPassword string
	Assets        fs.FS // contenu statique (index.html, app.js, style.css)
}

type server struct {
	store   Store
	spotify Spotify
	bot     Bot
	opts    Options

	userHash [sha256.Size]byte
	passHash [sha256.Size]byte
}

const stateCookie = "spotify_state"

// New construit le handler HTTP complet.
func New(o Options, st Store, sp Spotify, bot Bot) http.Handler {
	s := &server{store: st, spotify: sp, bot: bot, opts: o}
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
	api.HandleFunc("POST /api/spotify/disconnect", s.spotifyDisconnect)
	api.HandleFunc("GET /auth/spotify/login", s.spotifyLogin)
	api.HandleFunc("GET /auth/spotify/callback", s.spotifyCallback)
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

func (s *server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u, p, ok := r.BasicAuth()
		uh := sha256.Sum256([]byte(u))
		ph := sha256.Sum256([]byte(p))
		// Les deux comparaisons sont toujours évaluées (temps constant, longueurs égales après hachage).
		userOK := subtle.ConstantTimeCompare(uh[:], s.userHash[:])
		passOK := subtle.ConstantTimeCompare(ph[:], s.passHash[:])
		if !ok || userOK&passOK != 1 {
			w.Header().Set("WWW-Authenticate", `Basic realm="twitch-bot", charset="UTF-8"`)
			writeErr(w, http.StatusUnauthorized, "authentification requise")
			return
		}
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

type spotifyStatus struct {
	Configured bool   `json:"configured"`
	Connected  bool   `json:"connected"`
	Account    string `json:"account,omitempty"`
}

type statusResponse struct {
	Channel      string        `json:"channel"`
	BotConnected bool          `json:"bot_connected"`
	Spotify      spotifyStatus `json:"spotify"`
}

func (s *server) handleStatus(w http.ResponseWriter, _ *http.Request) {
	resp := statusResponse{Channel: s.opts.Channel, BotConnected: s.bot != nil && s.bot.Connected()}
	if s.spotify != nil {
		resp.Spotify = spotifyStatus{Configured: true, Connected: s.spotify.Connected()}
		if resp.Spotify.Connected {
			resp.Spotify.Account = s.spotify.Account()
		}
	}
	writeJSON(w, http.StatusOK, resp)
}

// ---- commandes ----

type commandBody struct {
	Name            string `json:"name"`
	Response        string `json:"response"`
	CooldownSeconds *int   `json:"cooldown_seconds"`
}

// toCommand valide le corps et construit la commande. name est déjà normalisé.
func (b commandBody) toCommand(name string) (model.Command, error) {
	if err := commands.ValidateName(name); err != nil {
		return model.Command{}, err
	}
	if err := commands.ValidateResponse(b.Response); err != nil {
		return model.Command{}, err
	}
	cd := commands.DefaultCooldownSeconds
	if b.CooldownSeconds != nil {
		cd = *b.CooldownSeconds
	}
	if err := commands.ValidateCooldown(cd); err != nil {
		return model.Command{}, err
	}
	return model.Command{Name: name, Response: strings.TrimSpace(b.Response), CooldownSeconds: cd}, nil
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
	switch err := s.store.AddCommand(r.Context(), cmd); {
	case errors.Is(err, model.ErrExists):
		writeErr(w, http.StatusConflict, "cette commande existe déjà")
	case err != nil:
		serverErr(w, "création de la commande", err)
	default:
		slog.Info("commande créée", "name", cmd.Name)
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
	switch err := s.store.UpdateCommand(r.Context(), cmd); {
	case errors.Is(err, model.ErrNotFound):
		writeErr(w, http.StatusNotFound, "commande introuvable")
	case err != nil:
		serverErr(w, "mise à jour de la commande", err)
	default:
		slog.Info("commande modifiée", "name", cmd.Name)
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

// ---- Spotify ----

func (s *server) requireSpotify(w http.ResponseWriter) bool {
	if s.spotify == nil {
		writeErr(w, http.StatusNotFound, "Spotify n'est pas configuré (SPOTIFY_ID / SPOTIFY_SECRET)")
		return false
	}
	return true
}

func isHTTPS(r *http.Request) bool {
	return r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https"
}

func (s *server) spotifyLogin(w http.ResponseWriter, r *http.Request) {
	if !s.requireSpotify(w) {
		return
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		serverErr(w, "génération du state OAuth", err)
		return
	}
	state := hex.EncodeToString(b[:])
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: state, Path: "/auth/spotify", MaxAge: 600,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r),
	})
	http.Redirect(w, r, s.spotify.AuthURL(state), http.StatusFound)
}

func (s *server) spotifyCallback(w http.ResponseWriter, r *http.Request) {
	if !s.requireSpotify(w) {
		return
	}
	q := r.URL.Query()
	c, err := r.Cookie(stateCookie)
	if err != nil || c.Value == "" || subtle.ConstantTimeCompare([]byte(c.Value), []byte(q.Get("state"))) != 1 {
		writeErr(w, http.StatusBadRequest, "paramètre state invalide, relance la connexion depuis la page d'administration")
		return
	}
	http.SetCookie(w, &http.Cookie{
		Name: stateCookie, Value: "", Path: "/auth/spotify", MaxAge: -1,
		HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: isHTTPS(r),
	})
	if q.Get("error") != "" {
		http.Redirect(w, r, "/?spotify=denied", http.StatusFound)
		return
	}
	code := q.Get("code")
	if code == "" {
		writeErr(w, http.StatusBadRequest, "code manquant")
		return
	}
	if err := s.spotify.Exchange(r.Context(), code); err != nil {
		slog.Error("échange du code Spotify", "err", err)
		http.Redirect(w, r, "/?spotify=error", http.StatusFound)
		return
	}
	slog.Info("compte Spotify connecté")
	http.Redirect(w, r, "/?spotify=connected", http.StatusFound)
}

func (s *server) spotifyDisconnect(w http.ResponseWriter, r *http.Request) {
	if !s.requireSpotify(w) {
		return
	}
	if err := s.spotify.Disconnect(r.Context()); err != nil {
		serverErr(w, "déconnexion de Spotify", err)
		return
	}
	slog.Info("compte Spotify déconnecté")
	writeJSON(w, http.StatusOK, map[string]string{"status": "disconnected"})
}
