// Package twitchauth connecte le bot à son compte Twitch (OAuth « authorization code »).
// Contrairement à un jeton collé à la main, le jeton obtenu ici est renouvelé automatiquement.
package twitchauth

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"sync"

	"golang.org/x/oauth2"

	"github.com/matella/twitch-bot/internal/oauthx"
)

const (
	tokenKey    = "twitch_token"
	loginKey    = "twitch_login"
	validateURL = "https://id.twitch.tv/oauth2/validate"
)

// ErrNotConnected est renvoyé tant qu'aucun compte n'a été connecté (ou si son jeton est révoqué).
var ErrNotConnected = errors.New("compte Twitch non connecté")

var scopes = []string{"chat:read", "chat:edit"}

// Config contient les identifiants de l'application Twitch.
type Config struct {
	ClientID     string
	ClientSecret string
	RedirectURL  string
	// TokenURL et ValidateURL remplacent les adresses de Twitch (tests uniquement).
	TokenURL    string
	ValidateURL string
}

// Client gère le jeton Twitch du compte du bot.
type Client struct {
	oauth    *oauth2.Config
	settings oauthx.Settings
	appCtx   context.Context
	validate string

	mu      sync.RWMutex
	src     oauth2.TokenSource // nil tant qu'aucun compte n'est connecté
	login   string
	revoked bool
}

// New crée le client et recharge le jeton enregistré, s'il existe.
func New(appCtx context.Context, cfg Config, settings oauthx.Settings) (*Client, error) {
	c := &Client{
		appCtx:   appCtx,
		settings: settings,
		validate: validateURL,
		oauth: &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: cfg.ClientSecret,
			RedirectURL:  cfg.RedirectURL,
			Scopes:       scopes,
			Endpoint: oauth2.Endpoint{
				AuthURL:   "https://id.twitch.tv/oauth2/authorize",
				TokenURL:  "https://id.twitch.tv/oauth2/token",
				AuthStyle: oauth2.AuthStyleInParams,
			},
		},
	}
	if cfg.TokenURL != "" {
		c.oauth.Endpoint.TokenURL = cfg.TokenURL
	}
	if cfg.ValidateURL != "" {
		c.validate = cfg.ValidateURL
	}
	tok, found, err := oauthx.Load(appCtx, settings, tokenKey)
	if err != nil {
		return nil, fmt.Errorf("lecture du jeton Twitch : %w", err)
	}
	if found {
		c.login, _ = settings.GetSetting(appCtx, loginKey)
		c.setToken(tok)
		slog.Info("compte Twitch rechargé", "login", c.login)
	}
	return c, nil
}

// AuthURL renvoie l'adresse d'autorisation. force_verify oblige Twitch à redemander le compte :
// il faut s'y connecter avec le compte DU BOT, pas avec celui du streamer.
func (c *Client) AuthURL(state string) string {
	return c.oauth.AuthCodeURL(state, oauth2.SetAuthURLParam("force_verify", "true"))
}

// Exchange échange le code d'autorisation contre un jeton et le conserve.
func (c *Client) Exchange(ctx context.Context, code string) error {
	tok, err := c.oauth.Exchange(ctx, code)
	if err != nil {
		return err
	}
	login, err := c.fetchLogin(ctx, tok.AccessToken)
	if err != nil {
		return fmt.Errorf("validation du jeton : %w", err)
	}
	oauthx.Save(c.appCtx, c.settings, tokenKey, tok)
	if err := c.settings.SetSetting(c.appCtx, loginKey, login); err != nil {
		return err
	}
	c.mu.Lock()
	c.login = login
	c.mu.Unlock()
	c.setToken(tok)
	return nil
}

// fetchLogin interroge /validate : confirme le jeton et donne le pseudo du compte.
func (c *Client) fetchLogin(ctx context.Context, access string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.validate, nil)
	if err != nil {
		return "", err
	}
	req.Header.Set("Authorization", "OAuth "+access)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("statut %d", resp.StatusCode)
	}
	var v struct {
		Login string `json:"login"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&v); err != nil || v.Login == "" {
		return "", errors.New("réponse inattendue")
	}
	return strings.ToLower(v.Login), nil
}

func (c *Client) setToken(tok *oauth2.Token) {
	src := oauth2.ReuseTokenSource(tok, oauthx.NewPersistingSource(
		c.oauth.TokenSource(c.appCtx, tok), tok,
		func(t *oauth2.Token) { oauthx.Save(c.appCtx, c.settings, tokenKey, t) }))
	c.mu.Lock()
	c.src, c.revoked = src, false
	c.mu.Unlock()
}

// Connected indique si un compte est connecté et son jeton encore utilisable.
func (c *Client) Connected() bool {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.src != nil && !c.revoked
}

// Account renvoie le pseudo du compte connecté.
func (c *Client) Account() string {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.login
}

// Disconnect oublie le compte et supprime le jeton enregistré.
func (c *Client) Disconnect(ctx context.Context) error {
	c.mu.Lock()
	c.src, c.login, c.revoked = nil, "", false
	c.mu.Unlock()
	if err := c.settings.DeleteSetting(ctx, tokenKey); err != nil {
		return err
	}
	return c.settings.DeleteSetting(ctx, loginKey)
}

// Credentials renvoie le pseudo et le mot de passe IRC (« oauth:... ») du bot,
// en renouvelant le jeton si nécessaire.
func (c *Client) Credentials(ctx context.Context) (user, pass string, err error) {
	c.mu.RLock()
	src, login := c.src, c.login
	c.mu.RUnlock()
	if src == nil {
		return "", "", ErrNotConnected
	}
	tok, err := src.Token()
	if err != nil {
		if oauthx.IsAuthRevoked(err) {
			c.mu.Lock()
			c.revoked = true
			c.mu.Unlock()
			slog.Warn("jeton Twitch refusé : reconnecte le compte du bot depuis l'administration")
			return "", "", ErrNotConnected
		}
		return "", "", err
	}
	return login, "oauth:" + tok.AccessToken, nil
}
