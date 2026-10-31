package overlay

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/json"
	"fmt"
	"io/fs"
	"net/http"
	"time"
)

// csp : la page n'embarque que ses propres scripts et les images de badges de Twitch.
const csp = "default-src 'none'; script-src 'self'; style-src 'self'; img-src https://static-cdn.jtvnw.net; " +
	"connect-src 'self'; base-uri 'none'; frame-ancestors 'none'"

// assetTypes liste les fichiers de la page accessibles sans jeton (ils ne contiennent aucune donnée).
var assetTypes = map[string]string{
	"overlay.css": "text/css; charset=utf-8",
	"overlay.js":  "text/javascript; charset=utf-8",
}

// Handler sert la page d'incrustation, son flux de messages et le catalogue des badges.
// Les données exigent le jeton secret (?token=…), car une source navigateur d'OBS ne peut pas
// envoyer d'identifiants HTTP Basic.
type Handler struct {
	tokenHash [sha256.Size]byte
	hub       *Hub
	catalog   *Catalog
	assets    fs.FS
	mux       *http.ServeMux
}

// NewHandler construit l'incrustation. assets contient overlay.html, overlay.js et overlay.css.
func NewHandler(token string, assets fs.FS, hub *Hub, catalog *Catalog) *Handler {
	h := &Handler{
		tokenHash: sha256.Sum256([]byte(token)),
		hub:       hub,
		catalog:   catalog,
		assets:    assets,
		mux:       http.NewServeMux(),
	}
	h.mux.HandleFunc("GET /overlay/chat", h.guard(h.page))
	h.mux.HandleFunc("GET /overlay/events", h.guard(h.events))
	h.mux.HandleFunc("GET /overlay/badges", h.guard(h.badges))
	h.mux.HandleFunc("GET /overlay/assets/{file}", h.asset)
	return h
}

// ServeHTTP applique les en-têtes de sécurité puis route la requête.
// Referrer-Policy: no-referrer empêche le jeton de fuir vers le CDN des badges.
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	hd := w.Header()
	hd.Set("Cache-Control", "no-store")
	hd.Set("X-Content-Type-Options", "nosniff")
	hd.Set("Referrer-Policy", "no-referrer")
	hd.Set("Content-Security-Policy", csp)
	h.mux.ServeHTTP(w, r)
}

// guard refuse toute requête sans le bon jeton.
func (h *Handler) guard(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sum := sha256.Sum256([]byte(r.URL.Query().Get("token")))
		if subtle.ConstantTimeCompare(sum[:], h.tokenHash[:]) != 1 {
			http.Error(w, "jeton manquant ou invalide", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (h *Handler) page(w http.ResponseWriter, _ *http.Request) {
	h.serveFile(w, "overlay.html", "text/html; charset=utf-8")
}

func (h *Handler) asset(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("file")
	ctype, ok := assetTypes[name]
	if !ok {
		http.NotFound(w, r)
		return
	}
	h.serveFile(w, name, ctype)
}

func (h *Handler) serveFile(w http.ResponseWriter, name, ctype string) {
	data, err := fs.ReadFile(h.assets, name)
	if err != nil {
		http.Error(w, "fichier introuvable", http.StatusNotFound)
		return
	}
	w.Header().Set("Content-Type", ctype)
	_, _ = w.Write(data)
}

func (h *Handler) badges(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	_ = json.NewEncoder(w).Encode(h.catalog.All())
}

// events diffuse les messages au format Server-Sent Events, jusqu'à la fermeture de la connexion.
func (h *Handler) events(w http.ResponseWriter, r *http.Request) {
	// Le délai d'écriture du serveur (30 s) couperait le flux : on le lève pour cette seule connexion.
	rc := http.NewResponseController(w)
	_ = rc.SetWriteDeadline(time.Time{})
	w.Header().Set("Content-Type", "text/event-stream")

	ch, unsubscribe := h.hub.Subscribe()
	defer unsubscribe()
	ping := time.NewTicker(20 * time.Second) // commentaire périodique : garde la connexion ouverte
	defer ping.Stop()

	if rc.Flush() != nil {
		return
	}
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			if _, err := fmt.Fprint(w, ": ping\n\n"); err != nil {
				return
			}
		case m := <-ch:
			data, err := json.Marshal(m)
			if err != nil {
				return
			}
			if _, err := fmt.Fprintf(w, "data: %s\n\n", data); err != nil {
				return
			}
		}
		if rc.Flush() != nil {
			return
		}
	}
}
