package server

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/matella/twitch-bot/internal/model"
)

type fakeStore struct {
	cmds     map[string]model.Command
	requests []model.SongRequest
	failNext bool
}

func (s *fakeStore) ListCommands(context.Context) ([]model.Command, error) {
	if s.failNext {
		return nil, errors.New("base indisponible")
	}
	var out []model.Command
	for _, c := range s.cmds {
		out = append(out, c)
	}
	return out, nil
}
func (s *fakeStore) AddCommand(_ context.Context, c model.Command) error {
	if _, ok := s.cmds[c.Name]; ok {
		return model.ErrExists
	}
	s.cmds[c.Name] = c
	return nil
}
func (s *fakeStore) UpdateCommand(_ context.Context, c model.Command) error {
	if _, ok := s.cmds[c.Name]; !ok {
		return model.ErrNotFound
	}
	s.cmds[c.Name] = c
	return nil
}
func (s *fakeStore) DeleteCommand(_ context.Context, name string) error {
	if _, ok := s.cmds[name]; !ok {
		return model.ErrNotFound
	}
	delete(s.cmds, name)
	return nil
}
func (s *fakeStore) RecentSongRequests(_ context.Context, limit int) ([]model.SongRequest, error) {
	return s.requests, nil
}
func (s *fakeStore) DeleteSongRequest(_ context.Context, id int64) error {
	for i, r := range s.requests {
		if r.ID == id {
			s.requests = append(s.requests[:i], s.requests[i+1:]...)
			return nil
		}
	}
	return model.ErrNotFound
}
func (s *fakeStore) ClearSongRequests(context.Context) error { s.requests = nil; return nil }

type fakeSpotify struct {
	connected    bool
	exchangeErr  error
	exchangeCode string
}

func (f *fakeSpotify) AuthURL(state string) string {
	return "https://accounts.spotify.com/authorize?state=" + state
}
func (f *fakeSpotify) Exchange(_ context.Context, code string) error {
	f.exchangeCode = code
	if f.exchangeErr == nil {
		f.connected = true
	}
	return f.exchangeErr
}
func (f *fakeSpotify) Connected() bool                  { return f.connected }
func (f *fakeSpotify) Account() string                  { return "Streamer" }
func (f *fakeSpotify) Disconnect(context.Context) error { f.connected = false; return nil }

type fakeBot struct{ up bool }

func (b fakeBot) Connected() bool { return b.up }

const (
	user = "admin"
	pass = "mot-de-passe-test"
)

func setup(t *testing.T, sp Spotify) (http.Handler, *fakeStore) {
	t.Helper()
	st := &fakeStore{cmds: map[string]model.Command{}}
	assets := fstest.MapFS{"index.html": {Data: []byte("<h1>admin</h1>")}}
	h := New(Options{Channel: "chan", AdminUser: user, AdminPassword: pass, Assets: assets}, st, sp, fakeBot{up: true})
	return h, st
}

func do(h http.Handler, method, path, body string, mutate ...func(*http.Request)) *httptest.ResponseRecorder {
	var r *http.Request
	if body != "" {
		r = httptest.NewRequest(method, path, strings.NewReader(body))
		r.Header.Set("Content-Type", "application/json")
	} else {
		r = httptest.NewRequest(method, path, nil)
	}
	r.SetBasicAuth(user, pass)
	for _, m := range mutate {
		m(r)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestAuth(t *testing.T) {
	h, _ := setup(t, nil)

	// health : public
	w := httptest.NewRecorder()
	h.ServeHTTP(w, httptest.NewRequest("GET", "/api/health", nil))
	if w.Code != 200 {
		t.Fatalf("health = %d", w.Code)
	}

	for _, path := range []string{"/", "/api/status", "/api/commands", "/api/requests", "/auth/spotify/login"} {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest("GET", path, nil))
		if w.Code != http.StatusUnauthorized || w.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("GET %s sans identifiants = %d", path, w.Code)
		}
	}

	for name, creds := range map[string][2]string{
		"mauvais mot de passe": {user, "faux"},
		"mauvais utilisateur":  {"root", pass},
		"vide":                 {"", ""},
	} {
		r := httptest.NewRequest("GET", "/api/commands", nil)
		r.SetBasicAuth(creds[0], creds[1])
		w := httptest.NewRecorder()
		h.ServeHTTP(w, r)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s : %d", name, w.Code)
		}
	}

	if w := do(h, "GET", "/", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "admin") {
		t.Errorf("page statique = %d %q", w.Code, w.Body.String())
	}
	if w := do(h, "GET", "/api/health", ""); w.Code != 200 {
		t.Errorf("health authentifié = %d", w.Code)
	}
}

func TestCSRFGuards(t *testing.T) {
	h, st := setup(t, nil)
	body := `{"name":"lurk","response":"merci"}`

	cross := func(r *http.Request) { r.Header.Set("Sec-Fetch-Site", "cross-site") }
	if w := do(h, "POST", "/api/commands", body, cross); w.Code != http.StatusForbidden {
		t.Errorf("Sec-Fetch-Site cross-site = %d", w.Code)
	}
	badOrigin := func(r *http.Request) { r.Header.Set("Origin", "https://evil.example") }
	if w := do(h, "POST", "/api/commands", body, badOrigin); w.Code != http.StatusForbidden {
		t.Errorf("Origin étranger = %d", w.Code)
	}
	if len(st.cmds) != 0 {
		t.Fatal("une requête bloquée a tout de même créé une commande")
	}
	sameOrigin := func(r *http.Request) {
		r.Header.Set("Origin", "http://"+r.Host)
		r.Header.Set("Sec-Fetch-Site", "same-origin")
	}
	if w := do(h, "POST", "/api/commands", body, sameOrigin); w.Code != http.StatusCreated {
		t.Errorf("requête same-origin = %d %s", w.Code, w.Body.String())
	}
	textPlain := func(r *http.Request) { r.Header.Set("Content-Type", "text/plain") }
	if w := do(h, "POST", "/api/commands", `{"name":"x1","response":"y"}`, textPlain); w.Code != http.StatusUnsupportedMediaType {
		t.Errorf("Content-Type text/plain = %d", w.Code)
	}
}

func TestCommandsCRUD(t *testing.T) {
	h, _ := setup(t, nil)

	if w := do(h, "GET", "/api/commands", ""); w.Code != 200 || strings.TrimSpace(w.Body.String()) != "[]" {
		t.Fatalf("liste vide = %d %q", w.Code, w.Body.String())
	}

	w := do(h, "POST", "/api/commands", `{"name":"  !Lurk ","response":"  Merci du lurk {user} !  "}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("création = %d %s", w.Code, w.Body.String())
	}
	var created model.Command
	if err := json.Unmarshal(w.Body.Bytes(), &created); err != nil {
		t.Fatal(err)
	}
	if created.Name != "lurk" || created.Response != "Merci du lurk {user} !" || created.CooldownSeconds != 5 {
		t.Fatalf("commande créée = %+v", created)
	}

	if w := do(h, "POST", "/api/commands", `{"name":"lurk","response":"autre"}`); w.Code != http.StatusConflict {
		t.Errorf("doublon = %d", w.Code)
	}

	invalid := map[string]string{
		"nom réservé":      `{"name":"song","response":"x"}`,
		"nom invalide":     `{"name":"a b","response":"x"}`,
		"réponse vide":     `{"name":"ok","response":"   "}`,
		"délai négatif":    `{"name":"ok","response":"x","cooldown_seconds":-1}`,
		"champ inconnu":    `{"name":"ok","response":"x","admin":true}`,
		"JSON cassé":       `{"name":`,
		"réponse énorme":   `{"name":"ok","response":"` + strings.Repeat("a", 401) + `"}`,
		"délai trop grand": `{"name":"ok","response":"x","cooldown_seconds":99999}`,
	}
	for name, body := range invalid {
		if w := do(h, "POST", "/api/commands", body); w.Code != http.StatusBadRequest {
			t.Errorf("%s : %d %s", name, w.Code, w.Body.String())
		}
	}

	if w := do(h, "PUT", "/api/commands/LURK", `{"response":"nouveau","cooldown_seconds":0}`); w.Code != 200 {
		t.Fatalf("mise à jour = %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "PUT", "/api/commands/inconnue", `{"response":"x"}`); w.Code != http.StatusNotFound {
		t.Errorf("mise à jour inconnue = %d", w.Code)
	}

	w = do(h, "GET", "/api/commands", "")
	var list []model.Command
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 ||
		list[0].Response != "nouveau" || list[0].CooldownSeconds != 0 {
		t.Fatalf("liste = %s (%v)", w.Body.String(), err)
	}
	if !strings.Contains(w.Body.String(), `"cooldown_seconds"`) || !strings.Contains(w.Body.String(), `"name"`) {
		t.Errorf("les clés JSON doivent être en snake_case minuscule : %s", w.Body.String())
	}

	if w := do(h, "DELETE", "/api/commands/lurk", ""); w.Code != 200 {
		t.Errorf("suppression = %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/commands/lurk", ""); w.Code != http.StatusNotFound {
		t.Errorf("suppression répétée = %d", w.Code)
	}
}

func TestInternalErrorsDoNotLeakDetails(t *testing.T) {
	h, st := setup(t, nil)
	st.failNext = true
	w := do(h, "GET", "/api/commands", "")
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "base indisponible") {
		t.Fatalf("erreur interne = %d %q", w.Code, w.Body.String())
	}
}

func TestRequests(t *testing.T) {
	h, st := setup(t, nil)
	st.requests = []model.SongRequest{{ID: 2, TrackName: "B"}, {ID: 1, TrackName: "A"}}

	w := do(h, "GET", "/api/requests?limit=10", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"track_name":"B"`) {
		t.Fatalf("liste = %d %s", w.Code, w.Body.String())
	}
	for _, bad := range []string{"0", "201", "abc"} {
		if w := do(h, "GET", "/api/requests?limit="+bad, ""); w.Code != http.StatusBadRequest {
			t.Errorf("limit=%s : %d", bad, w.Code)
		}
	}
	if w := do(h, "DELETE", "/api/requests/2", ""); w.Code != 200 {
		t.Errorf("suppression = %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/requests/2", ""); w.Code != http.StatusNotFound {
		t.Errorf("suppression répétée = %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/requests/abc", ""); w.Code != http.StatusBadRequest {
		t.Errorf("id invalide = %d", w.Code)
	}
	if w := do(h, "DELETE", "/api/requests", ""); w.Code != 200 || len(st.requests) != 0 {
		t.Errorf("vidage = %d, restant %d", w.Code, len(st.requests))
	}
	if w := do(h, "GET", "/api/requests", ""); strings.TrimSpace(w.Body.String()) != "[]" {
		t.Errorf("liste vide doit être [] : %q", w.Body.String())
	}
}

func TestStatus(t *testing.T) {
	h, _ := setup(t, nil)
	w := do(h, "GET", "/api/status", "")
	var s statusResponse
	if err := json.Unmarshal(w.Body.Bytes(), &s); err != nil {
		t.Fatal(err)
	}
	if s.Channel != "chan" || !s.BotConnected || s.Spotify.Configured {
		t.Errorf("statut sans Spotify = %+v", s)
	}

	sp := &fakeSpotify{connected: true}
	h, _ = setup(t, sp)
	json.Unmarshal(do(h, "GET", "/api/status", "").Body.Bytes(), &s)
	if !s.Spotify.Configured || !s.Spotify.Connected || s.Spotify.Account != "Streamer" {
		t.Errorf("statut avec Spotify = %+v", s)
	}
}

func TestSpotifyDisabled(t *testing.T) {
	h, _ := setup(t, nil)
	for _, c := range []struct{ method, path string }{
		{"GET", "/auth/spotify/login"}, {"GET", "/auth/spotify/callback"}, {"POST", "/api/spotify/disconnect"},
	} {
		if w := do(h, c.method, c.path, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s %s sans Spotify = %d", c.method, c.path, w.Code)
		}
	}
}

func TestSpotifyOAuthFlow(t *testing.T) {
	sp := &fakeSpotify{}
	h, _ := setup(t, sp)

	login := do(h, "GET", "/auth/spotify/login", "")
	if login.Code != http.StatusFound {
		t.Fatalf("login = %d", login.Code)
	}
	loc := login.Header().Get("Location")
	cookies := login.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].Value == "" {
		t.Fatalf("cookie de state = %+v", cookies)
	}
	state := cookies[0].Value
	if !strings.HasSuffix(loc, "state="+state) {
		t.Fatalf("le state de l'URL (%q) ne correspond pas au cookie (%q)", loc, state)
	}

	withCookie := func(r *http.Request) { r.AddCookie(cookies[0]) }

	// state falsifié
	if w := do(h, "GET", "/auth/spotify/callback?state=autre&code=abc", "", withCookie); w.Code != http.StatusBadRequest {
		t.Errorf("state falsifié = %d", w.Code)
	}
	// pas de cookie
	if w := do(h, "GET", "/auth/spotify/callback?state="+state+"&code=abc", ""); w.Code != http.StatusBadRequest {
		t.Errorf("sans cookie = %d", w.Code)
	}
	if sp.exchangeCode != "" {
		t.Fatal("le code a été échangé malgré un state invalide")
	}
	// refus de l'utilisateur
	if w := do(h, "GET", "/auth/spotify/callback?state="+state+"&error=access_denied", "", withCookie); w.Code != http.StatusFound ||
		w.Header().Get("Location") != "/?spotify=denied" {
		t.Errorf("refus = %d %q", w.Code, w.Header().Get("Location"))
	}
	// succès
	w := do(h, "GET", "/auth/spotify/callback?state="+state+"&code=abc", "", withCookie)
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/?spotify=connected" || sp.exchangeCode != "abc" || !sp.connected {
		t.Fatalf("callback = %d %q (code %q)", w.Code, w.Header().Get("Location"), sp.exchangeCode)
	}
	// échec de l'échange
	sp.exchangeErr = errors.New("invalid_grant")
	w = do(h, "GET", "/auth/spotify/callback?state="+state+"&code=abc", "", withCookie)
	if w.Header().Get("Location") != "/?spotify=error" {
		t.Errorf("échec d'échange = %d %q", w.Code, w.Header().Get("Location"))
	}

	if w := do(h, "POST", "/api/spotify/disconnect", ""); w.Code != 200 || sp.connected {
		t.Errorf("déconnexion = %d, connected=%v", w.Code, sp.connected)
	}
}
