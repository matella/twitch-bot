// Package oauthx contient ce que partagent les connexions OAuth (Spotify, Twitch) :
// conservation du jeton en base et détection d'un jeton définitivement refusé.
package oauthx

import (
	"context"
	"encoding/json"
	"errors"
	"log/slog"
	"sync"

	"golang.org/x/oauth2"

	"github.com/matella/twitch-bot/internal/model"
)

// Settings est le stockage clé/valeur utilisé pour conserver le jeton.
type Settings interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	DeleteSetting(ctx context.Context, key string) error
}

// Load lit le jeton enregistré. found est faux s'il n'y en a pas ou s'il est illisible.
func Load(ctx context.Context, s Settings, key string) (tok *oauth2.Token, found bool, err error) {
	raw, err := s.GetSetting(ctx, key)
	switch {
	case errors.Is(err, model.ErrNotFound):
		return nil, false, nil
	case err != nil:
		return nil, false, err
	}
	var t oauth2.Token
	if err := json.Unmarshal([]byte(raw), &t); err != nil {
		slog.Warn("jeton illisible, reconnexion nécessaire", "key", key, "err", err)
		return nil, false, nil
	}
	return &t, true, nil
}

// Save enregistre le jeton (les erreurs sont journalisées : le jeton reste valable en mémoire).
func Save(ctx context.Context, s Settings, key string, tok *oauth2.Token) {
	b, err := json.Marshal(tok)
	if err != nil {
		slog.Error("sérialisation du jeton", "key", key, "err", err)
		return
	}
	if err := s.SetSetting(ctx, key, string(b)); err != nil {
		slog.Error("enregistrement du jeton", "key", key, "err", err)
	}
}

// IsAuthRevoked indique si l'erreur vient d'un refus définitif du fournisseur lors d'un
// renouvellement (jeton révoqué ou expiré) : seule une nouvelle connexion y remédie.
func IsAuthRevoked(err error) bool {
	var re *oauth2.RetrieveError
	if !errors.As(err, &re) {
		return false
	}
	return re.Response != nil && (re.Response.StatusCode == 400 || re.Response.StatusCode == 401)
}

// PersistingSource réécrit en base chaque nouveau jeton d'accès obtenu par renouvellement
// (le fournisseur peut aussi changer le refresh token).
type PersistingSource struct {
	src  oauth2.TokenSource
	save func(*oauth2.Token)

	mu   sync.Mutex
	last string
}

// NewPersistingSource crée la source ; initial est le jeton déjà connu.
func NewPersistingSource(src oauth2.TokenSource, initial *oauth2.Token, save func(*oauth2.Token)) *PersistingSource {
	return &PersistingSource{src: src, save: save, last: initial.AccessToken}
}

func (p *PersistingSource) Token() (*oauth2.Token, error) {
	tok, err := p.src.Token()
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if tok.AccessToken != p.last {
		p.last = tok.AccessToken
		p.save(tok)
	}
	return tok, nil
}
