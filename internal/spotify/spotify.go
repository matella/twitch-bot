// Package spotify connecte le bot au compte Spotify du streamer (OAuth « authorization code »)
// afin de pouvoir chercher des titres et agir sur la file de lecture réelle.
//
// Le flux « client credentials » ne suffit pas : il ne donne accès à aucun compte
// utilisateur, donc impossible d'agir sur la file de lecture.
package spotify

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"sync"
	"time"

	sp "github.com/zmb3/spotify/v2"
	spauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2"

	"github.com/matella/twitch-bot/internal/chat"
	"github.com/matella/twitch-bot/internal/oauthx"
)

const (
	tokenKey   = "spotify_token"
	accountKey = "spotify_account"
)

// Droits demandés : agir sur la file de lecture et lire l'état du lecteur.
// Un jeton obtenu avant l'ajout d'un droit doit être renouvelé en reconnectant le compte.
var scopes = []string{"user-modify-playback-state", "user-read-playback-state", "user-read-currently-playing"}

// Config contient les identifiants de l'application Spotify.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
}

// Client implémente chat.Music et l'interface Spotify du serveur d'administration.
type Client struct {
	oauth    *oauth2.Config
	settings oauthx.Settings
	appCtx   context.Context // vit aussi longtemps que l'application : sert au renouvellement du jeton

	mu      sync.RWMutex
	api     *sp.Client // nil tant qu'aucun compte n'est connecté
	account string
	revoked bool // le fournisseur a refusé le renouvellement : reconnexion nécessaire
}

var _ chat.Music = (*Client)(nil)

// New crée le client et recharge le jeton enregistré, s'il existe.
// appCtx doit rester valide pendant toute la vie de l'application.
func New(appCtx context.Context, cfg Config, settings oauthx.Settings) (*Client, error) {
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
	tok, found, err := oauthx.Load(appCtx, settings, tokenKey)
	if err != nil {
		return nil, fmt.Errorf("lecture du jeton Spotify : %w", err)
	}
	if found {
		c.account, _ = settings.GetSetting(appCtx, accountKey)
		c.setToken(tok)
		slog.Info("compte Spotify rechargé", "account", c.account)
	}
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
	oauthx.Save(c.appCtx, c.settings, tokenKey, tok)
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
	src := oauthx.NewPersistingSource(c.oauth.TokenSource(c.appCtx, tok), tok,
		func(t *oauth2.Token) { oauthx.Save(c.appCtx, c.settings, tokenKey, t) })
	api := sp.New(oauth2.NewClient(c.appCtx, src))
	c.mu.Lock()
	c.api, c.revoked = api, false
	c.mu.Unlock()
}

func (c *Client) client() *sp.Client {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.api
}

// check examine une erreur d'appel : un refus définitif du renouvellement du jeton
// fait repasser le compte en « non connecté » au lieu de répondre « erreur » indéfiniment.
func (c *Client) check(err error) error {
	if err != nil && oauthx.IsAuthRevoked(err) {
		c.mu.Lock()
		c.revoked = true
		c.mu.Unlock()
		slog.Warn("jeton Spotify refusé : reconnecte le compte depuis l'administration")
		return chat.ErrNotConnected
	}
	return err
}

// Connected indique si un compte Spotify est connecté et utilisable.
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.api != nil && !c.revoked
}

// Account renvoie le nom du compte connecté (vide si inconnu).
func (c *Client) Account() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.account
}

// Disconnect oublie le compte et supprime le jeton enregistré.
func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	c.api, c.account, c.revoked = nil, "", false
	c.mu.Unlock()
	if err := c.settings.DeleteSetting(ctx, tokenKey); err != nil {
		return err
	}
	return c.settings.DeleteSetting(ctx, accountKey)
}

func (c *Client) usable() (*sp.Client, error) {
	if !c.Connected() {
		return nil, chat.ErrNotConnected
	}
	return c.client(), nil
}

func toTrack(t sp.FullTrack) chat.Track {
	artists := make([]string, len(t.Artists))
	for i, a := range t.Artists {
		artists[i] = a.Name
	}
	main := ""
	if len(artists) > 0 {
		main = artists[0]
	}
	return chat.Track{
		ID: string(t.ID), Name: t.Name, Artist: main, Artists: artists,
		Duration: time.Duration(t.Duration) * time.Millisecond, Explicit: t.Explicit,
	}
}

// Find cherche un titre : lien/URI Spotify ou recherche texte (premier résultat).
func (c *Client) Find(ctx context.Context, query string) (*chat.Track, error) {
	api, err := c.usable()
	if err != nil {
		return nil, err
	}
	// "from_token" : marché du compte connecté (exclut les titres injouables chez lui).
	market := sp.Country("from_token")

	if id, ok := chat.ParseTrackID(query); ok {
		t, err := api.GetTrack(ctx, sp.ID(id), market)
		if err != nil {
			return nil, c.check(fmt.Errorf("lecture du titre %s : %w", id, err))
		}
		tr := toTrack(*t)
		return &tr, nil
	}

	res, err := api.Search(ctx, query, sp.SearchTypeTrack, sp.Limit(1), market)
	if err != nil {
		return nil, c.check(fmt.Errorf("recherche : %w", err))
	}
	if res == nil || res.Tracks == nil || len(res.Tracks.Tracks) == 0 {
		return nil, chat.ErrNoResult
	}
	tr := toTrack(res.Tracks.Tracks[0])
	return &tr, nil
}

func isNoDevice(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "no active device")
}

// Queue ajoute le titre à la file de lecture du compte connecté.
func (c *Client) Queue(ctx context.Context, trackID string) error {
	api, err := c.usable()
	if err != nil {
		return err
	}
	if err := api.QueueSong(ctx, sp.ID(trackID)); err != nil {
		// Spotify répond « No active device found » quand aucun lecteur n'est ouvert.
		if isNoDevice(err) {
			return chat.ErrNoDevice
		}
		return c.check(err)
	}
	return nil
}

// Playback renvoie le titre en cours et la file à venir.
func (c *Client) Playback(ctx context.Context) (*chat.Playback, error) {
	api, err := c.usable()
	if err != nil {
		return nil, err
	}
	q, err := api.GetQueue(ctx)
	if err != nil {
		return nil, c.check(fmt.Errorf("lecture de la file : %w", err))
	}
	pb := &chat.Playback{Queue: make([]chat.Track, 0, len(q.Items))}
	if q.CurrentlyPlaying.ID != "" {
		cur := toTrack(q.CurrentlyPlaying)
		pb.Current = &cur
	}
	for _, t := range q.Items {
		pb.Queue = append(pb.Queue, toTrack(t))
	}
	return pb, nil
}

// Skip passe au titre suivant.
func (c *Client) Skip(ctx context.Context) error {
	api, err := c.usable()
	if err != nil {
		return err
	}
	if err := api.Next(ctx); err != nil {
		if isNoDevice(err) {
			return chat.ErrNoDevice
		}
		return c.check(err)
	}
	return nil
}
