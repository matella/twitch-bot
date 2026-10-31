package overlay

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"testing/fstest"
	"time"

	"github.com/matella/twitch-bot/internal/model"
)

const testToken = "jeton-secret-de-test"

func TestSafeColor(t *testing.T) {
	for _, tt := range []struct{ in, want string }{
		{"#FF0000", "#FF0000"},
		{"#00ff7f", "#00ff7f"},
		{"", ""},
		{"red", ""},
		{"#fff", ""},
		{"#ff0000;background:url(x)", ""},
	} {
		if got := SafeColor(tt.in); got != tt.want {
			t.Errorf("SafeColor(%q) = %q, attendu %q", tt.in, got, tt.want)
		}
	}
}

func TestBadgeKeys(t *testing.T) {
	got := BadgeKeys(map[string]int{"subscriber": 12, "broadcaster": 1, "moderator": 1})
	want := []string{"broadcaster/1", "moderator/1", "subscriber/12"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("BadgeKeys = %v, attendu %v", got, want)
	}
	if got := BadgeKeys(nil); len(got) != 0 {
		t.Errorf("BadgeKeys(nil) = %v, attendu vide", got)
	}
}

func TestHubPublishSubscribe(t *testing.T) {
	h := NewHub()
	ch, unsubscribe := h.Subscribe()
	h.Publish(Message{Name: "a", Text: "salut"})
	select {
	case m := <-ch:
		if m.Text != "salut" {
			t.Errorf("texte = %q, attendu %q", m.Text, "salut")
		}
	case <-time.After(time.Second):
		t.Fatal("message non reçu")
	}
	unsubscribe()
	h.Publish(Message{Text: "perdu"})
	select {
	case m := <-ch:
		t.Errorf("message reçu après désabonnement : %+v", m)
	default:
	}
}

func TestHubDoesNotBlockOnSlowSubscriber(t *testing.T) {
	h := NewHub()
	_, unsubscribe := h.Subscribe()
	defer unsubscribe()
	done := make(chan struct{})
	go func() {
		for i := 0; i < 2*subBuffer; i++ {
			h.Publish(Message{Text: "x"})
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Publish bloque sur un abonné lent")
	}
}

func TestCatalogRefresh(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /token", func(w http.ResponseWriter, r *http.Request) {
		if r.FormValue("grant_type") != "client_credentials" || r.FormValue("client_id") != "cid" {
			http.Error(w, "mauvaise requête", http.StatusBadRequest)
			return
		}
		_, _ = io.WriteString(w, `{"access_token":"app-token"}`)
	})
	mux.HandleFunc("GET /helix/users", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer app-token" || r.Header.Get("Client-Id") != "cid" {
			http.Error(w, "non autorisé", http.StatusUnauthorized)
			return
		}
		if r.URL.Query().Get("login") != "salon" {
			http.Error(w, "inconnu", http.StatusNotFound)
			return
		}
		_, _ = io.WriteString(w, `{"data":[{"id":"42","login":"salon"}]}`)
	})
	mux.HandleFunc("GET /helix/chat/badges/global", func(w http.ResponseWriter, _ *http.Request) {
		_, _ = io.WriteString(w, `{"data":[`+
			`{"set_id":"subscriber","versions":[{"id":"0","image_url_1x":"https://cdn/g0-1","image_url_2x":"https://cdn/g0-2"}]},`+
			`{"set_id":"moderator","versions":[{"id":"1","image_url_1x":"https://cdn/mod"}]}]}`)
	})
	mux.HandleFunc("GET /helix/chat/badges", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("broadcaster_id") != "42" {
			http.Error(w, "salon inconnu", http.StatusBadRequest)
			return
		}
		// Le badge du salon remplace le badge global de même nom.
		_, _ = io.WriteString(w, `{"data":[{"set_id":"subscriber","versions":[{"id":"0","image_url_2x":"https://cdn/c0"}]}]}`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	c := NewCatalog("cid", "secret", "salon")
	c.helix = srv.URL + "/helix"
	c.tokenURL = srv.URL + "/token"

	n, err := c.Refresh(context.Background())
	if err != nil {
		t.Fatalf("Refresh : %v", err)
	}
	if n != 2 {
		t.Errorf("nombre de badges = %d, attendu 2", n)
	}
	want := map[string]string{"subscriber/0": "https://cdn/c0", "moderator/1": "https://cdn/mod"}
	if got := c.All(); !reflect.DeepEqual(got, want) {
		t.Errorf("All() = %v, attendu %v", got, want)
	}
}

func TestNilCatalogIsEmpty(t *testing.T) {
	var c *Catalog
	if got := c.All(); len(got) != 0 {
		t.Errorf("All() sur catalogue nil = %v, attendu vide", got)
	}
}

// fakeSettings renvoie des réglages fixes pour les tests.
type fakeSettings struct{}

func (fakeSettings) GetOverlaySettings(context.Context) (model.OverlaySettings, error) {
	return model.OverlaySettings{MaxMessages: 7, FontSize: 30, ShowBadges: false}, nil
}

func TestOpenModeWithoutToken(t *testing.T) {
	srv := httptest.NewServer(NewHandler("", testAssets(), NewHub(), nil, fakeSettings{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/overlay/settings")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("sans jeton configuré : statut %d, attendu 200", resp.StatusCode)
	}
	var got model.OverlaySettings
	if err := json.NewDecoder(resp.Body).Decode(&got); err != nil {
		t.Fatal(err)
	}
	if got.MaxMessages != 7 || got.FontSize != 30 || got.ShowBadges {
		t.Errorf("réglages = %+v", got)
	}
}

func TestSettingsRequireToken(t *testing.T) {
	srv := httptest.NewServer(NewHandler(testToken, testAssets(), NewHub(), nil, fakeSettings{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/overlay/settings")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("réglages sans jeton : statut %d, attendu 401", resp.StatusCode)
	}
}

func testAssets() fstest.MapFS {
	return fstest.MapFS{
		"overlay.html": {Data: []byte("<!doctype html><title>chat</title>")},
		"overlay.css":  {Data: []byte("body{}")},
		"overlay.js":   {Data: []byte("")},
	}
}

func TestHandlerRequiresToken(t *testing.T) {
	srv := httptest.NewServer(NewHandler(testToken, testAssets(), NewHub(), nil, fakeSettings{}))
	defer srv.Close()

	for _, path := range []string{"/overlay/chat", "/overlay/badges", "/overlay/events", "/overlay/chat?token=mauvais"} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusUnauthorized {
			t.Errorf("%s : statut %d, attendu 401", path, resp.StatusCode)
		}
	}

	resp, err := http.Get(srv.URL + "/overlay/badges?token=" + testToken)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || strings.TrimSpace(string(body)) != "{}" {
		t.Errorf("badges avec jeton : statut %d, corps %q", resp.StatusCode, body)
	}

	resp, err = http.Get(srv.URL + "/overlay/chat?token=" + testToken)
	if err != nil {
		t.Fatal(err)
	}
	page, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(page), "<title>chat</title>") {
		t.Errorf("page avec jeton : statut %d", resp.StatusCode)
	}
	if csp := resp.Header.Get("Content-Security-Policy"); !strings.Contains(csp, "img-src https://static-cdn.jtvnw.net") {
		t.Errorf("CSP sans le CDN des badges : %q", csp)
	}
}

func TestAssetsAllowlist(t *testing.T) {
	srv := httptest.NewServer(NewHandler(testToken, testAssets(), NewHub(), nil, fakeSettings{}))
	defer srv.Close()

	resp, err := http.Get(srv.URL + "/overlay/assets/overlay.css")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("overlay.css : statut %d, attendu 200", resp.StatusCode)
	}

	// La page d'administration et la page de l'incrustation ne sont pas des fichiers « assets ».
	resp, err = http.Get(srv.URL + "/overlay/assets/overlay.html")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("overlay.html via assets : statut %d, attendu 404", resp.StatusCode)
	}
}

func TestEventsStreamsMessages(t *testing.T) {
	hub := NewHub()
	srv := httptest.NewServer(NewHandler(testToken, testAssets(), hub, nil, fakeSettings{}))
	defer srv.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, srv.URL+"/overlay/events?token="+testToken, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, attendu text/event-stream", ct)
	}

	// Les en-têtes ne sont envoyés qu'une fois l'abonnement en place : la publication ne se perd pas.
	hub.Publish(Message{Login: "viewer", Name: "Viewer", Text: "<b>bonjour</b>"})
	sc := bufio.NewScanner(resp.Body)
	for sc.Scan() {
		line := sc.Text()
		if !strings.HasPrefix(line, "data: ") {
			continue
		}
		var m Message
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, "data: ")), &m); err != nil {
			t.Fatalf("JSON invalide : %v", err)
		}
		if m.Text != "<b>bonjour</b>" || m.Name != "Viewer" {
			t.Errorf("message reçu = %+v", m)
		}
		return
	}
	t.Fatalf("aucun message reçu : %v", sc.Err())
}
