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
	"time"

	"github.com/matella/twitch-bot/internal/chat"
	"github.com/matella/twitch-bot/internal/model"
)

type fakeStore struct {
	cmds     map[string]model.Command
	requests []model.SongRequest
	failNext bool
	music    model.MusicSettings
}

func (s *fakeStore) GetMusicSettings(context.Context) (model.MusicSettings, error) {
	if s.music.Blocklist == nil {
		return model.DefaultMusicSettings(), nil
	}
	return s.music, nil
}
func (s *fakeStore) SetMusicSettings(_ context.Context, m model.MusicSettings) error {
	s.music = m
	return nil
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
	playback     *chat.Playback
	skipErr      error
	skipped      int
}

func (f *fakeSpotify) Playback(context.Context) (*chat.Playback, error) {
	if f.playback == nil {
		return &chat.Playback{}, nil
	}
	return f.playback, nil
}
func (f *fakeSpotify) Skip(context.Context) error { f.skipped++; return f.skipErr }

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

type fakeBot struct {
	up         bool
	err        string
	reconnects *int
}

func (b fakeBot) Connected() bool   { return b.up }
func (b fakeBot) LastError() string { return b.err }
func (b fakeBot) Reconnect() {
	if b.reconnects != nil {
		*b.reconnects++
	}
}

type fakeTwitch struct {
	connected bool
	code      string
}

func (f *fakeTwitch) AuthURL(state string) string {
	return "https://id.twitch.tv/oauth2/authorize?state=" + state
}
func (f *fakeTwitch) Exchange(_ context.Context, code string) error {
	f.code, f.connected = code, true
	return nil
}
func (f *fakeTwitch) Connected() bool                  { return f.connected }
func (f *fakeTwitch) Account() string                  { return "monbot" }
func (f *fakeTwitch) Disconnect(context.Context) error { f.connected = false; return nil }

const (
	user = "admin"
	pass = "mot-de-passe-test"
)

func setup(t *testing.T, sp Spotify) (http.Handler, *fakeStore) {
	t.Helper()
	h, st, _ := setupFull(t, sp, nil, Options{})
	return h, st
}

// setupFull permet de fournir Twitch et des options ; il renvoie le compteur de reconnexions du bot.
func setupFull(t *testing.T, sp Spotify, tw Twitch, o Options) (http.Handler, *fakeStore, *int) {
	t.Helper()
	st := &fakeStore{cmds: map[string]model.Command{}}
	o.Channel, o.AdminUser, o.AdminPassword = "chan", user, pass
	o.Assets = fstest.MapFS{"index.html": {Data: []byte("<h1>admin</h1>")}}
	reconnects := new(int)
	h := New(o, st, sp, tw, fakeBot{up: true, reconnects: reconnects})
	return h, st, reconnects
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

	for _, path := range []string{"/", "/api/status", "/api/commands", "/api/requests", "/auth/spotify/login", "/auth/twitch/login", "/api/music-settings", "/api/spotify/queue"} {
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
		{"GET", "/auth/twitch/login"}, {"GET", "/auth/twitch/callback"}, {"POST", "/api/twitch/disconnect"},
		{"GET", "/api/spotify/queue"}, {"POST", "/api/spotify/skip"},
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

func TestStatusTwitchAndBotError(t *testing.T) {
	h, _, _ := setupFull(t, nil, &fakeTwitch{connected: true}, Options{})
	var s statusResponse
	json.Unmarshal(do(h, "GET", "/api/status", "").Body.Bytes(), &s)
	if !s.Twitch.Configured || !s.Twitch.Connected || s.Twitch.Account != "monbot" || s.BotError != "" {
		t.Errorf("statut = %+v", s)
	}

	st := &fakeStore{cmds: map[string]model.Command{}}
	h = New(Options{AdminUser: user, AdminPassword: pass, Assets: fstest.MapFS{}}, st, nil, nil, fakeBot{up: false, err: "jeton refusé"})
	s = statusResponse{}
	json.Unmarshal(do(h, "GET", "/api/status", "").Body.Bytes(), &s)
	if s.BotConnected || s.BotError != "jeton refusé" || s.Twitch.Configured {
		t.Errorf("statut bot hors ligne = %+v", s)
	}
}

func TestTwitchOAuthFlowReconnectsBot(t *testing.T) {
	tw := &fakeTwitch{}
	h, _, reconnects := setupFull(t, nil, tw, Options{})

	login := do(h, "GET", "/auth/twitch/login", "")
	cookies := login.Result().Cookies()
	if login.Code != http.StatusFound || len(cookies) != 1 || cookies[0].Name != "oauth_state_twitch" {
		t.Fatalf("login = %d %+v", login.Code, cookies)
	}
	withCookie := func(r *http.Request) { r.AddCookie(cookies[0]) }
	w := do(h, "GET", "/auth/twitch/callback?state="+cookies[0].Value+"&code=abc", "", withCookie)
	if w.Header().Get("Location") != "/?twitch=connected" || tw.code != "abc" || *reconnects != 1 {
		t.Fatalf("callback = %d %q (code %q, reconnexions %d)", w.Code, w.Header().Get("Location"), tw.code, *reconnects)
	}
	// Le cookie du fournisseur Twitch ne vaut pas pour Spotify.
	if w := do(h, "GET", "/auth/spotify/callback?state="+cookies[0].Value+"&code=abc", "", withCookie); w.Code != http.StatusNotFound {
		t.Errorf("callback spotify sans spotify = %d", w.Code)
	}
	if w := do(h, "POST", "/api/twitch/disconnect", ""); w.Code != 200 || tw.connected || *reconnects != 2 {
		t.Errorf("déconnexion = %d, connected=%v, reconnexions=%d", w.Code, tw.connected, *reconnects)
	}
}

func TestCommandExtendedFields(t *testing.T) {
	changed := 0
	h, st, _ := setupFull(t, nil, nil, Options{OnCommandsChanged: func() { changed++ }})

	body := `{"name":"discord","response":"lien","permission":"moderator","user_cooldown_seconds":30,"enabled":false,"aliases":["!DC","serveur","dc"]}`
	w := do(h, "POST", "/api/commands", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("création = %d %s", w.Code, w.Body.String())
	}
	got := st.cmds["discord"]
	if got.Permission != model.LevelModerator || got.UserCooldownSeconds != 30 || got.Enabled ||
		len(got.Aliases) != 2 || got.Aliases[0] != "dc" || got.Aliases[1] != "serveur" {
		t.Fatalf("commande = %+v", got)
	}
	if !strings.Contains(w.Body.String(), `"permission":"moderator"`) {
		t.Errorf("le rôle doit être sérialisé en texte : %s", w.Body.String())
	}

	// Collisions de nom ou d'alias entre commandes.
	for name, b := range map[string]string{
		"nom = alias existant":   `{"name":"dc","response":"x"}`,
		"alias = autre commande": `{"name":"autre","response":"x","aliases":["discord"]}`,
		"alias = alias existant": `{"name":"autre","response":"x","aliases":["serveur"]}`,
	} {
		if w := do(h, "POST", "/api/commands", b); w.Code != http.StatusConflict {
			t.Errorf("%s : %d %s", name, w.Code, w.Body.String())
		}
	}
	for name, b := range map[string]string{
		"rôle inconnu":  `{"name":"ok","response":"x","permission":"roi"}`,
		"alias réservé": `{"name":"ok","response":"x","aliases":["song"]}`,
		"délai user":    `{"name":"ok","response":"x","user_cooldown_seconds":-1}`,
	} {
		if w := do(h, "POST", "/api/commands", b); w.Code != http.StatusBadRequest {
			t.Errorf("%s : %d %s", name, w.Code, w.Body.String())
		}
	}

	// Mise à jour : une commande peut garder ses propres alias.
	if w := do(h, "PUT", "/api/commands/discord", `{"response":"nouveau","aliases":["dc"]}`); w.Code != 200 {
		t.Fatalf("mise à jour = %d %s", w.Code, w.Body.String())
	}
	if changed != 2 {
		t.Errorf("OnCommandsChanged appelée %d fois, attendu 2", changed)
	}
}

func TestMusicSettingsAPI(t *testing.T) {
	h, st := setup(t, nil)
	var ms model.MusicSettings
	w := do(h, "GET", "/api/music-settings", "")
	if err := json.Unmarshal(w.Body.Bytes(), &ms); err != nil || ms.MaxPerUser != 3 || ms.SkipLevel != model.LevelModerator {
		t.Fatalf("valeurs par défaut = %s (%v)", w.Body.String(), err)
	}

	put := `{"request_level":"subscriber","skip_level":"vip","max_pending":5,"max_per_user":1,"max_duration_seconds":240,"block_explicit":true,"blocklist":[" Rick ","rick",""]}`
	if w := do(h, "PUT", "/api/music-settings", put); w.Code != 200 {
		t.Fatalf("PUT = %d %s", w.Code, w.Body.String())
	}
	if st.music.RequestLevel != model.LevelSubscriber || !st.music.BlockExplicit ||
		len(st.music.Blocklist) != 1 || st.music.Blocklist[0] != "rick" {
		t.Fatalf("réglages enregistrés = %+v", st.music)
	}
	for name, b := range map[string]string{
		"rôle inconnu":  `{"request_level":"dieu"}`,
		"négatif":       `{"max_pending":-1}`,
		"champ inconnu": `{"x":1}`,
	} {
		if w := do(h, "PUT", "/api/music-settings", b); w.Code != http.StatusBadRequest {
			t.Errorf("%s : %d", name, w.Code)
		}
	}
}

func TestSpotifyQueueAndSkip(t *testing.T) {
	sp := &fakeSpotify{playback: &chat.Playback{
		Current: &chat.Track{ID: "a", Name: "En cours", Artist: "X", Duration: 3 * time.Minute},
		Queue:   []chat.Track{{ID: "b", Name: "Suivant", Artist: "Y", Explicit: true}},
	}}
	h, _ := setup(t, sp)
	w := do(h, "GET", "/api/spotify/queue", "")
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"duration_seconds":180`) || !strings.Contains(w.Body.String(), `"Suivant"`) {
		t.Fatalf("file = %d %s", w.Code, w.Body.String())
	}
	if w := do(h, "POST", "/api/spotify/skip", ""); w.Code != 200 || sp.skipped != 1 {
		t.Errorf("skip = %d (%d)", w.Code, sp.skipped)
	}
	sp.skipErr = chat.ErrNoDevice
	if w := do(h, "POST", "/api/spotify/skip", ""); w.Code != http.StatusConflict {
		t.Errorf("skip sans lecteur = %d", w.Code)
	}
	sp.skipErr = errors.New("boom")
	if w := do(h, "POST", "/api/spotify/skip", ""); w.Code != http.StatusBadGateway || strings.Contains(w.Body.String(), "boom") {
		t.Errorf("skip en erreur = %d %s", w.Code, w.Body.String())
	}
}

func TestAuthBruteForceLockout(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	h, _, _ := setupFull(t, nil, nil, Options{Now: func() time.Time { return now }})
	bad := func(r *http.Request) { r.SetBasicAuth(user, "faux") }
	for i := 0; i < maxFails; i++ {
		if w := do(h, "GET", "/api/status", "", bad); w.Code != http.StatusUnauthorized {
			t.Fatalf("tentative %d = %d", i, w.Code)
		}
	}
	// Même avec le bon mot de passe, l'appelant est bloqué.
	w := do(h, "GET", "/api/status", "")
	if w.Code != http.StatusTooManyRequests || w.Header().Get("Retry-After") == "" {
		t.Fatalf("après %d échecs : %d", maxFails, w.Code)
	}
	now = now.Add(blockFor + time.Second)
	if w := do(h, "GET", "/api/status", ""); w.Code != 200 {
		t.Fatalf("après le blocage : %d", w.Code)
	}
	// Un succès remet le compteur à zéro.
	for i := 0; i < maxFails-1; i++ {
		do(h, "GET", "/api/status", "", bad)
	}
	do(h, "GET", "/api/status", "")
	for i := 0; i < maxFails-1; i++ {
		do(h, "GET", "/api/status", "", bad)
	}
	if w := do(h, "GET", "/api/status", ""); w.Code != 200 {
		t.Fatalf("le compteur n'a pas été remis à zéro : %d", w.Code)
	}
}

func TestForwardedProtoOnlyTrustedBehindProxy(t *testing.T) {
	for trust, wantSecure := range map[bool]bool{false: false, true: true} {
		h, _, _ := setupFull(t, &fakeSpotify{}, nil, Options{TrustProxy: trust})
		w := do(h, "GET", "/auth/spotify/login", "", func(r *http.Request) { r.Header.Set("X-Forwarded-Proto", "https") })
		cookies := w.Result().Cookies()
		if len(cookies) != 1 || cookies[0].Secure != wantSecure {
			t.Errorf("TrustProxy=%v : cookie = %+v, attendu Secure=%v", trust, cookies, wantSecure)
		}
	}
}
