// Package spotify connecte le bot au compte Spotify du streamer (OAuth « authorization code »)
// afin de pouvoir chercher des titres et les ajouter à la file de lecture réelle.
//
// Le flux « client credentials » ne suffit pas : il ne donne accès à aucun compte
// utilisateur, donc impossible d'agir sur la file de lecture.
package spotify

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	sp "github.com/zmb3/spotify/v2"
	spauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"

	"github.com/matella/twitch-bot/internal/chat"
	"github.com/matella/twitch-bot/internal/model"
)

const (
	tokenKey   = "spotify_token"
	accountKey = "spotify_account"
)

// Droits demandés : agir sur la file de lecture et lire l'état du lecteur.
var scopes = []string{"user-modify-playback-state", "user-read-playback-state"}

// Settings est le stockage clé/valeur utilisé pour conserver le jeton.
type Settings interface {
	GetSetting(ctx context.Context, key string) (string, error)
	SetSetting(ctx context.Context, key, value string) error
	DeleteSetting(ctx context.Context, key string) error
}

// Config contient les identifiants de l'application Spotify.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// Client implémente chat.Music et l'interface Spotify du serveur d'administration.
type Client struct {
	oauth    *oauth2.Config
	settings Settings
	appCtx   context.Context // vit aussi longtemps que l'application : sert au renouvellement du jeton

	mu      sync.RWMutex
	api     *sp.Client // nil tant qu'aucun compte n'est connecté
	account string
}

var _ chat.Music = (*Client)(nil)

// New crée le client et recharge le jeton enregistré, s'il existe.
// appCtx doit rester valide pendant toute la vie de l'application.
func New(appCtx context.Context, cfg Config, settings Settings) (*Client, error) {
	c := &Client{
		appCtx:   appCtx,
		settings: settings,
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL:   spauth.AuthURL,
				TokenURL:  spauth.TokenURL,
				AuthStyle: oauth2.AuthStyleInHeader,
			},
		},
	}

	raw, err := settings.GetSetting(appCtx, tokenKey)
	switch {
	case errors.Is(err, model.ErrNotFound):
		return c, nil
	case err != nil:
		return nil, fmt.Errorf("lecture du jeton Spotify : %w", err)
	}
	var tok oauth2.Token
	if err := json.Unmarshal([]byte(raw), &tok); err != nil {
		slog.Warn("jeton Spotify illisible, reconnexion nécessaire", "err", err)
		return c, nil
	}
	c.account, _ = settings.GetSetting(appCtx, accountKey)
	c.setToken(&tok)
	slog.Info("compte Spotify rechargé", "account", c.account)
	return c, nil
}

// AuthURL renvoie l'adresse d'autorisation Spotify pour le state donné.
func (c *Client) AuthURL(state string) string { return c.oauth.AuthCodeURL(state) }

// Exchange échange le code d'autorisation contre un jeton et le conserve.
func (c *Client) Exchange(ctx context.Context, code string) error {
	tok, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return err
	}
	c.save(tok)
	c.setToken(tok)

	// Le nom du compte n'est qu'informatif : un échec ici n'empêche pas la connexion.
	if user, err := c.client().CurrentUser(ctx); err != nil {
		slog.Warn("lecture du profil Spotify", "err", err)
	} else {
		name := user.DisplayName
		if name == "" {
			name = user.ID
		}
		c.mu.Lock()
		c.account = name
		c.mu.Unlock()
		if err := c.settings.SetSetting(c.appCtx, accountKey, name); err != nil {
			slog.Warn("enregistrement du nom du compte", "err", err)
		}
	}
	return nil
}

// setToken construit le client API : le jeton est renouvelé automatiquement
// et chaque nouveau jeton est réécrit en base (Spotify peut en changer le refresh token).
func (c *Client) setToken(tok *oauth2.Token) {
	src := &persistingSource{src: c.oauth.TokenSource(c.appCtx, tok), last: tok.AccessToken, save: c.save}
	api := sp.New(oauth2.NewClient(c.appCtx, src))
	c.mu.Lock()
	c.api = api
	c.mu.Unlock()
}

func (c *Client) save(tok *oauth2.Token) {
	b, err := json.Marshal(tok)
	if err != nil {
		slog.Error("sérialisation du jeton Spotify", "err", err)
		return
	}
	if err := c.settings.SetSetting(c.appCtx, tokenKey, string(b)); err != nil {
		slog.Error("enregistrement du jeton Spotify", "err", err)
	}
}

func (c *Client) client() *sp.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.api
}

// Connected indique si un compte Spotify est connecté.
func (c *Client) Connected() bool { return c.client() != nil }

// Account renvoie le nom du compte connecté (vide si inconnu).
func (c *Client) Account() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.account
}

// Disconnect oublie le compte et supprime le jeton enregistré.
func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	c.api, c.account = nil, ""
	c.mu.Unlock()
	if err := c.settings.DeleteSetting(ctx, tokenKey); err != nil {
		return err
	}
	return c.settings.DeleteSetting(ctx, accountKey)
}

// Find cherche un titre : lien/URI Spotify ou recherche texte (premier résultat).
func (c *Client) Find(ctx context.Context, query string) (*chat.Track, error) {
	api := c.client()
	if api == nil {
		return nil, chat.ErrNotConnected
	}

	if id, ok := chat.ParseTrackID(query); ok {
		t, err := api.GetTrack(ctx, sp.ID(id))
		if err != nil {
			return nil, fmt.Errorf("lecture du titre %s : %w", id, err)
		}
		return &chat.Track{ID: string(t.ID), Name: t.Name, Artist: firstArtist(t.Artists)}, nil
	}

	res, err := api.Search(ctx, query, sp.SearchTypeTrack, sp.Limit(1))
	if err != nil {
		return nil, fmt.Errorf("recherche : %w", err)
	}
	if res == nil || res.Tracks == nil || len(res.Tracks.Tracks) == 0 {
		return nil, chat.ErrNoResult
	}
	t := res.Tracks.Tracks[0]
	return &chat.Track{ID: string(t.ID), Name: t.Name, Artist: firstArtist(t.Artists)}, nil
}

func firstArtist(artists []sp.SimpleArtist) string {
	if len(artists) == 0 {
		return ""
	}
	return artists[0].Name
}

// Queue ajoute le titre à la file de lecture du compte connecté.
func (c *Client) Queue(ctx context.Context, trackID string) error {
	api := c.client()
	if api == nil {
		return chat.ErrNotConnected
	}
	if err := api.QueueSong(ctx, sp.ID(trackID)); err != nil {
		// Spotify répond « No active device found » quand aucun lecteur n'est ouvert.
		if strings.Contains(strings.ToLower(err.Error()), "no active device") {
			return chat.ErrNoDevice
		}
		return err
	}
	return nil
}

// persistingSource réécrit en base chaque nouveau jeton d'accès obtenu par renouvellement.
type persistingSource struct {
	src  oauth2.TokenSource
	save func(*oauth2.Token)

	mu   sync.Mutex
	last string
}

func (p *persistingSource) Token() (*oauth2.Token, error) {
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
