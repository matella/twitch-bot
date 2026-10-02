package twitchauth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/matella/twitch-bot/internal/model"
)

type memSettings map[string]string

func (m memSettings) GetSetting(_ context.Context, k string) (string, error) {
	v, ok := m[k]
	if !ok {
		return "", model.ErrNotFound
	}
	return v, nil
}
func (m memSettings) SetSetting(_ context.Context, k, v string) error { m[k] = v; return nil }
func (m memSettings) DeleteSetting(_ context.Context, k string) error { delete(m, k); return nil }

// fakeTwitch simule id.twitch.tv : échange de code, renouvellement et validation.
type fakeTwitch struct {
	refreshStatus int // 0 = succès
	refreshes     int
	expiresIn     int
}

func (f *fakeTwitch) handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		w.Header().Set("Content-Type", "application/json")
		switch r.Form.Get("grant_type") {
		case "authorization_code":
			if r.Form.Get("code") != "bon-code" || r.Form.Get("client_secret") != "secret" {
				w.WriteHeader(http.StatusBadRequest)
				w.Write([]byte(`{"status":400,"message":"invalid"}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "acces-1", "refresh_token": "refresh-1", "expires_in": f.expiresIn, "token_type": "bearer"})
		case "refresh_token":
			f.refreshes++
			if f.refreshStatus != 0 {
				w.WriteHeader(f.refreshStatus)
				w.Write([]byte(`{"status":400,"message":"Invalid refresh token"}`))
				return
			}
			json.NewEncoder(w).Encode(map[string]any{"access_token": "acces-2", "refresh_token": "refresh-2", "expires_in": 14400, "token_type": "bearer"})
		}
	})
	mux.HandleFunc("/validate", func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.Header.Get("Authorization"), "OAuth acces-") {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Write([]byte(`{"login":"MonBot","client_id":"id"}`))
	})
	return mux
}

func newClient(t *testing.T, f *fakeTwitch, st memSettings) *Client {
	t.Helper()
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	c, err := New(context.Background(), Config{
		ClientID: "id", ClientSecret: "secret", RedirectURL: "http://localhost/cb",
		TokenURL: srv.URL + "/token", ValidateURL: srv.URL + "/validate",
	}, st)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestAuthURLAsksForChatScopesAndAccountChoice(t *testing.T) {
	c := newClient(t, &fakeTwitch{}, memSettings{})
	u := c.AuthURL("etat")
	for _, want := range []string{"state=etat", "force_verify=true", "chat%3Aread", "chat%3Aedit", "client_id=id"} {
		if !strings.Contains(u, want) {
			t.Errorf("%q absent de %s", want, u)
		}
	}
}

func TestExchangeStoresTokenAndLogin(t *testing.T) {
	st := memSettings{}
	c := newClient(t, &fakeTwitch{expiresIn: 14400}, st)
	ctx := context.Background()

	if _, _, err := c.Credentials(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("avant connexion : %v", err)
	}
	if err := c.Exchange(ctx, "mauvais"); err == nil {
		t.Fatal("un mauvais code doit échouer")
	}
	if err := c.Exchange(ctx, "bon-code"); err != nil {
		t.Fatal(err)
	}
	user, pass, err := c.Credentials(ctx)
	if err != nil || user != "monbot" || pass != "oauth:acces-1" {
		t.Fatalf("Credentials = %q, %q, %v", user, pass, err)
	}
	if !c.Connected() || c.Account() != "monbot" || st[tokenKey] == "" || st[loginKey] != "monbot" {
		t.Fatalf("état = connected %v, account %q, réglages %v", c.Connected(), c.Account(), st)
	}

	// Un redémarrage recharge le compte depuis la base.
	c2 := newClient(t, &fakeTwitch{}, st)
	if !c2.Connected() || c2.Account() != "monbot" {
		t.Fatal("le compte n'a pas été rechargé")
	}

	if err := c.Disconnect(ctx); err != nil {
		t.Fatal(err)
	}
	if c.Connected() || len(st) != 0 {
		t.Fatalf("après déconnexion : connected=%v, réglages=%v", c.Connected(), st)
	}
}

func TestExpiredTokenIsRefreshedAndPersisted(t *testing.T) {
	st := memSettings{}
	f := &fakeTwitch{expiresIn: 1} // expire aussitôt (marge de renouvellement d'oauth2 : 10 s)
	c := newClient(t, f, st)
	ctx := context.Background()
	if err := c.Exchange(ctx, "bon-code"); err != nil {
		t.Fatal(err)
	}
	_, pass, err := c.Credentials(ctx)
	if err != nil || pass != "oauth:acces-2" || f.refreshes != 1 {
		t.Fatalf("Credentials = %q, %v (renouvellements : %d)", pass, err, f.refreshes)
	}
	if !strings.Contains(st[tokenKey], "acces-2") || !strings.Contains(st[tokenKey], "refresh-2") {
		t.Fatalf("le jeton renouvelé n'a pas été enregistré : %s", st[tokenKey])
	}
}

func TestRevokedRefreshTokenRequiresReconnection(t *testing.T) {
	f := &fakeTwitch{expiresIn: 1}
	c := newClient(t, f, memSettings{})
	ctx := context.Background()
	if err := c.Exchange(ctx, "bon-code"); err != nil {
		t.Fatal(err)
	}
	f.refreshStatus = http.StatusBadRequest
	if _, _, err := c.Credentials(ctx); !errors.Is(err, ErrNotConnected) {
		t.Fatalf("jeton révoqué : %v", err)
	}
	if c.Connected() {
		t.Fatal("un jeton révoqué ne doit plus apparaître comme connecté")
	}
}
