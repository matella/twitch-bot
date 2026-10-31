package overlay

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

const (
	helixURL     = "https://api.twitch.tv/helix"
	tokenURL     = "https://id.twitch.tv/oauth2/token"
	refreshEvery = 6 * time.Hour
	retryEvery   = time.Minute
)

// Catalog associe chaque badge (« set/version ») à l'URL de son image, à partir de l'API Helix de Twitch.
// Les badges du salon remplacent les badges globaux de même nom.
type Catalog struct {
	clientID, secret, channel string
	helix, tokenURL           string // remplaçables dans les tests
	client                    *http.Client

	mu     sync.RWMutex
	images map[string]string
}

// NewCatalog crée le catalogue. Rien n'est téléchargé avant Run ou Refresh.
func NewCatalog(clientID, secret, channel string) *Catalog {
	return &Catalog{
		clientID: clientID,
		secret:   secret,
		channel:  channel,
		helix:    helixURL,
		tokenURL: tokenURL,
		client:   &http.Client{Timeout: 10 * time.Second},
	}
}

// All renvoie une copie de la table « set/version » -> URL. Sûr même si c est nil.
func (c *Catalog) All() map[string]string {
	out := map[string]string{}
	if c == nil {
		return out
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	for k, v := range c.images {
		out[k] = v
	}
	return out
}

// Run charge le catalogue puis le rafraîchit régulièrement, jusqu'à l'annulation de ctx.
// En cas d'échec, il réessaie plus vite.
func (c *Catalog) Run(ctx context.Context) {
	for {
		next := refreshEvery
		if n, err := c.Refresh(ctx); err != nil {
			slog.Warn("badges Twitch indisponibles, nouvel essai bientôt", "err", err)
			next = retryEvery
		} else {
			slog.Info("badges Twitch chargés", "count", n)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(next):
		}
	}
}

// Refresh télécharge le catalogue et le remplace ; renvoie le nombre de badges connus.
func (c *Catalog) Refresh(ctx context.Context) (int, error) {
	token, err := c.appToken(ctx)
	if err != nil {
		return 0, fmt.Errorf("jeton d'application : %w", err)
	}
	id, err := c.broadcasterID(ctx, token)
	if err != nil {
		return 0, err
	}
	images := map[string]string{}
	for _, path := range []string{"/chat/badges/global", "/chat/badges?broadcaster_id=" + url.QueryEscape(id)} {
		var resp badgesResponse
		if err := c.get(ctx, token, path, &resp); err != nil {
			return 0, err
		}
		resp.addTo(images)
	}
	c.mu.Lock()
	c.images = images
	c.mu.Unlock()
	return len(images), nil
}

// appToken obtient un jeton d'application (client credentials), sans compte utilisateur.
func (c *Catalog) appToken(ctx context.Context) (string, error) {
	if c.clientID == "" || c.secret == "" {
		return "", errors.New("TWITCH_CLIENT_ID et TWITCH_CLIENT_SECRET requis")
	}
	form := url.Values{
		"client_id":     {c.clientID},
		"client_secret": {c.secret},
		"grant_type":    {"client_credentials"},
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.tokenURL, strings.NewReader(form.Encode()))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	var out struct {
		AccessToken string `json:"access_token"`
	}
	if err := c.do(req, &out); err != nil {
		return "", err
	}
	if out.AccessToken == "" {
		return "", errors.New("réponse sans jeton")
	}
	return out.AccessToken, nil
}

// broadcasterID renvoie l'identifiant numérique du salon, requis par les badges du salon.
func (c *Catalog) broadcasterID(ctx context.Context, token string) (string, error) {
	var resp struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.get(ctx, token, "/users?login="+url.QueryEscape(c.channel), &resp); err != nil {
		return "", err
	}
	if len(resp.Data) == 0 {
		return "", fmt.Errorf("salon %q introuvable", c.channel)
	}
	return resp.Data[0].ID, nil
}

func (c *Catalog) get(ctx context.Context, token, path string, out any) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.helix+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Client-Id", c.clientID)
	return c.do(req, out)
}

// do envoie la requête et décode la réponse. Les erreurs ne citent jamais la requête entière :
// le secret et le jeton ne doivent pas finir dans les journaux.
func (c *Catalog) do(req *http.Request, out any) error {
	resp, err := c.client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("%s %s : statut %d", req.Method, req.URL.Path, resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// badgesResponse est la réponse de /chat/badges (global ou du salon).
type badgesResponse struct {
	Data []struct {
		SetID    string `json:"set_id"`
		Versions []struct {
			ID    string `json:"id"`
			URL1x string `json:"image_url_1x"`
			URL2x string `json:"image_url_2x"`
		} `json:"versions"`
	} `json:"data"`
}

// addTo ajoute les images de la réponse à images ; les entrées existantes sont remplacées.
func (r badgesResponse) addTo(images map[string]string) {
	for _, set := range r.Data {
		for _, v := range set.Versions {
			u := v.URL2x
			if u == "" {
				u = v.URL1x
			}
			if u != "" {
				images[set.SetID+"/"+v.ID] = u
			}
		}
	}
}
