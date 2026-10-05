package chat

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/matella/twitch-bot/internal/model"
)

type fakeStore struct {
	cmds     map[string]model.Command
	requests []model.SongRequest
	settings model.MusicSettings
}

func (s *fakeStore) ListCommands(context.Context) ([]model.Command, error) {
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

func (s *fakeStore) IncrementUse(_ context.Context, name string) (int64, error) {
	c, ok := s.cmds[name]
	if !ok {
		return 0, model.ErrNotFound
	}
	c.UseCount++
	s.cmds[name] = c
	return c.UseCount, nil
}

func (s *fakeStore) AddSongRequest(_ context.Context, r model.SongRequest) error {
	s.requests = append([]model.SongRequest{r}, s.requests...)
	return nil
}

func (s *fakeStore) RecentSongRequests(_ context.Context, limit int) ([]model.SongRequest, error) {
	if len(s.requests) > limit {
		return s.requests[:limit], nil
	}
	return s.requests, nil
}

func (s *fakeStore) GetMusicSettings(context.Context) (model.MusicSettings, error) {
	return s.settings, nil
}

type fakeMusic struct {
	connected bool
	findErr   error
	queueErr  error
	queued    []string
	skipped   int
	track     *Track // résultat de Find ; sinon un titre fabriqué à partir de la requête
	playback  *Playback
	pbErr     error
}

func (m *fakeMusic) Connected() bool { return m.connected }
func (m *fakeMusic) Find(_ context.Context, q string) (*Track, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	if m.track != nil {
		return m.track, nil
	}
	return &Track{ID: "id-" + q, Name: "Titre " + q, Artist: "Artiste", Artists: []string{"Artiste"}, Duration: 3 * time.Minute}, nil
}
func (m *fakeMusic) Queue(_ context.Context, id string) error {
	if m.queueErr != nil {
		return m.queueErr
	}
	m.queued = append(m.queued, id)
	return nil
}
func (m *fakeMusic) Playback(context.Context) (*Playback, error) {
	if m.pbErr != nil {
		return nil, m.pbErr
	}
	if m.playback != nil {
		return m.playback, nil
	}
	return &Playback{}, nil
}
func (m *fakeMusic) Skip(context.Context) error { m.skipped++; return nil }

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

var (
	bob   = User{Name: "bob"}
	alice = User{Name: "alice"}
	sub   = User{Name: "sam", Level: model.LevelSubscriber}
	mod   = User{Name: "mona", Level: model.LevelModerator}
)

func newHandler(music Music) (*Handler, *fakeStore, *clock) {
	st := &fakeStore{
		cmds: map[string]model.Command{
			"discord": {Name: "discord", Response: "Rejoins-nous {user} !", CooldownSeconds: 10, Enabled: true, Aliases: []string{"dc"}},
			"echo":    {Name: "echo", Response: "{args}", Enabled: true},
		},
		settings: model.DefaultMusicSettings(),
	}
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	h := New(st, music, Options{Channel: "chan", SongCooldown: 30 * time.Second, Now: clk.now, Intn: func(n int) int { return 0 }})
	return h, st, clk
}

func TestIgnoresNonCommands(t *testing.T) {
	h, _, _ := newHandler(nil)
	if got := h.Handle(context.Background(), bob, "salut tout le monde"); got != "" {
		t.Fatalf("réponse inattendue : %q", got)
	}
	if got := h.Handle(context.Background(), bob, "!inconnue"); got != "" {
		t.Fatalf("commande inconnue a répondu : %q", got)
	}
}

func TestCustomCommandAndCooldown(t *testing.T) {
	h, st, clk := newHandler(nil)
	ctx := context.Background()
	if got := h.Handle(ctx, bob, "!Discord"); got != "Rejoins-nous bob !" {
		t.Fatalf("réponse = %q", got)
	}
	if got := h.Handle(ctx, alice, "!discord"); got != "" {
		t.Fatalf("le délai global n'a pas bloqué : %q", got)
	}
	if got := h.Handle(ctx, alice, "!dc"); got != "" {
		t.Fatalf("l'alias partage le délai de la commande : %q", got)
	}
	clk.advance(11 * time.Second)
	if got := h.Handle(ctx, alice, "!dc"); got == "" {
		t.Fatal("l'alias devrait répondre après le délai")
	}
	if st.cmds["discord"].UseCount != 2 {
		t.Fatalf("compteur = %d", st.cmds["discord"].UseCount)
	}
}

func TestCustomCommandUserCooldownPermissionAndDisabled(t *testing.T) {
	h, st, clk := newHandler(nil)
	ctx := context.Background()
	st.cmds["so"] = model.Command{Name: "so", Response: "Va voir {touser} !", UserCooldownSeconds: 20, Enabled: true, Permission: model.LevelModerator}
	st.cmds["off"] = model.Command{Name: "off", Response: "x", Enabled: false}
	st.cmds["n"] = model.Command{Name: "n", Response: "{count}e fois", Enabled: true}

	if got := h.Handle(ctx, bob, "!so alice"); got != "" {
		t.Fatalf("un spectateur ne devrait pas pouvoir utiliser !so : %q", got)
	}
	if got := h.Handle(ctx, mod, "!so @alice"); got != "Va voir alice !" {
		t.Fatalf("réponse = %q", got)
	}
	if got := h.Handle(ctx, mod, "!so bob"); got != "" {
		t.Fatalf("le délai par spectateur n'a pas bloqué : %q", got)
	}
	other := User{Name: "marc", Level: model.LevelModerator}
	if got := h.Handle(ctx, other, "!so bob"); got == "" {
		t.Fatal("un autre modérateur ne devrait pas être bloqué")
	}
	clk.advance(21 * time.Second)
	if got := h.Handle(ctx, mod, "!so bob"); got == "" {
		t.Fatal("après le délai, la commande devrait répondre")
	}
	if got := h.Handle(ctx, bob, "!off"); got != "" {
		t.Fatalf("commande désactivée a répondu : %q", got)
	}
	if got := h.Handle(ctx, bob, "!n"); got != "1e fois" {
		t.Fatalf("{count} = %q", got)
	}
}

func TestOutgoingMessagesCannotInjectTwitchCommands(t *testing.T) {
	h, _, _ := newHandler(nil)
	got := h.Handle(context.Background(), User{Name: "mallory"}, "!echo /ban victim")
	if strings.HasPrefix(got, "/") || strings.HasPrefix(got, ".") {
		t.Fatalf("message sortant interprétable comme commande Twitch : %q", got)
	}
	if got := h.Handle(context.Background(), User{Name: "mallory"}, "!echo !autrebot truc"); strings.HasPrefix(got, "!") {
		t.Fatalf("le bot relaie une commande destinée à un autre bot : %q", got)
	}
}

func TestSongRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("spotify absent", func(t *testing.T) {
		h, _, _ := newHandler(nil)
		if got := h.Handle(ctx, bob, "!song x"); !strings.Contains(got, "pas disponibles") {
			t.Fatalf("réponse = %q", got)
		}
	})

	t.Run("non connecté", func(t *testing.T) {
		h, _, _ := newHandler(&fakeMusic{connected: false})
		if got := h.Handle(ctx, bob, "!song x"); !strings.Contains(got, "pas disponibles") {
			t.Fatalf("réponse = %q", got)
		}
	})

	t.Run("usage", func(t *testing.T) {
		h, _, _ := newHandler(&fakeMusic{connected: true})
		if got := h.Handle(ctx, bob, "!song"); !strings.HasPrefix(got, "Utilisation") {
			t.Fatalf("réponse = %q", got)
		}
	})

	t.Run("trop long", func(t *testing.T) {
		h, _, _ := newHandler(&fakeMusic{connected: true})
		if got := h.Handle(ctx, bob, "!song "+strings.Repeat("a", 101)); !strings.Contains(got, "trop longue") {
			t.Fatalf("réponse = %q", got)
		}
	})

	t.Run("succès, historique et délai par spectateur", func(t *testing.T) {
		music := &fakeMusic{connected: true}
		h, st, clk := newHandler(music)
		got := h.Handle(ctx, bob, "!sr daft punk")
		if !strings.Contains(got, "ajouté") || len(music.queued) != 1 {
			t.Fatalf("réponse = %q, queued = %v", got, music.queued)
		}
		if len(st.requests) != 1 || st.requests[0].Status != model.StatusQueued || st.requests[0].RequestedBy != "bob" {
			t.Fatalf("historique = %+v", st.requests)
		}
		if got := h.Handle(ctx, bob, "!song autre"); !strings.Contains(got, "patiente") {
			t.Fatalf("le délai n'a pas bloqué : %q", got)
		}
		if got := h.Handle(ctx, alice, "!song autre"); !strings.Contains(got, "ajouté") {
			t.Fatalf("un autre spectateur ne devrait pas être bloqué : %q", got)
		}
		clk.advance(31 * time.Second)
		if got := h.Handle(ctx, bob, "!song encore"); !strings.Contains(got, "ajouté") {
			t.Fatalf("bob devrait pouvoir redemander : %q", got)
		}
	})

	t.Run("aucun résultat ne consomme pas le délai", func(t *testing.T) {
		music := &fakeMusic{connected: true, findErr: ErrNoResult}
		h, _, _ := newHandler(music)
		if got := h.Handle(ctx, bob, "!song zzz"); !strings.Contains(got, "Aucun résultat") {
			t.Fatalf("réponse = %q", got)
		}
		music.findErr = nil
		if got := h.Handle(ctx, bob, "!song vrai titre"); !strings.Contains(got, "ajouté") {
			t.Fatalf("la recherche ratée a bloqué bob : %q", got)
		}
	})

	t.Run("une erreur Spotify consomme le délai", func(t *testing.T) {
		music := &fakeMusic{connected: true, findErr: errors.New("boom")}
		h, _, _ := newHandler(music)
		if got := h.Handle(ctx, bob, "!song x"); !strings.Contains(got, "Erreur Spotify") {
			t.Fatalf("réponse = %q", got)
		}
		if got := h.Handle(ctx, bob, "!song x"); !strings.Contains(got, "patiente") {
			t.Fatalf("les échecs ne doivent pas permettre de marteler l'API : %q", got)
		}
	})

	t.Run("aucun lecteur actif", func(t *testing.T) {
		h, st, _ := newHandler(&fakeMusic{connected: true, queueErr: ErrNoDevice})
		if got := h.Handle(ctx, bob, "!song x"); !strings.Contains(got, "aucun lecteur") {
			t.Fatalf("réponse = %q", got)
		}
		if len(st.requests) != 1 || st.requests[0].Status != model.StatusFailed {
			t.Fatalf("historique = %+v", st.requests)
		}
	})
}

func TestSongRequestRules(t *testing.T) {
	ctx := context.Background()

	t.Run("réservé aux abonnés", func(t *testing.T) {
		music := &fakeMusic{connected: true}
		h, st, _ := newHandler(music)
		st.settings.RequestLevel = model.LevelSubscriber
		if got := h.Handle(ctx, bob, "!song x"); !strings.Contains(got, "réservées aux abonnés") {
			t.Fatalf("réponse = %q", got)
		}
		if got := h.Handle(ctx, sub, "!song x"); !strings.Contains(got, "ajouté") {
			t.Fatalf("un abonné devrait pouvoir demander : %q", got)
		}
	})

	t.Run("explicite, durée et liste de blocage", func(t *testing.T) {
		music := &fakeMusic{connected: true}
		h, st, _ := newHandler(music)
		st.settings.BlockExplicit = true
		st.settings.MaxDurationSeconds = 300
		st.settings.Blocklist = []string{"rickroll"}

		music.track = &Track{ID: "a", Name: "Chanson", Artist: "X", Artists: []string{"X"}, Explicit: true}
		if got := h.Handle(ctx, bob, "!song a"); !strings.Contains(got, "explicites") {
			t.Fatalf("explicite : %q", got)
		}
		music.track = &Track{ID: "b", Name: "Épopée", Artist: "X", Artists: []string{"X"}, Duration: 12 * time.Minute}
		if got := h.Handle(ctx, bob, "!song b"); !strings.Contains(got, "trop long") {
			t.Fatalf("durée : %q", got)
		}
		music.track = &Track{ID: "c", Name: "Never Gonna Give You Up", Artist: "Rick", Artists: []string{"Rickroll Band"}}
		if got := h.Handle(ctx, bob, "!song c"); !strings.Contains(got, "pas autorisé") {
			t.Fatalf("liste de blocage : %q", got)
		}
		if len(music.queued) != 0 {
			t.Fatalf("des titres refusés ont été ajoutés : %v", music.queued)
		}
		// Un refus ne consomme pas le délai.
		music.track = &Track{ID: "d", Name: "Ok", Artist: "Y", Artists: []string{"Y"}, Duration: time.Minute}
		if got := h.Handle(ctx, bob, "!song d"); !strings.Contains(got, "ajouté") {
			t.Fatalf("après des refus : %q", got)
		}
	})

	t.Run("doublons", func(t *testing.T) {
		music := &fakeMusic{connected: true, playback: &Playback{
			Current: &Track{ID: "en-cours"},
			Queue:   []Track{{ID: "deja-la"}},
		}}
		h, _, _ := newHandler(music)
		music.track = &Track{ID: "en-cours", Name: "A", Artist: "X"}
		if got := h.Handle(ctx, bob, "!song a"); !strings.Contains(got, "en train de jouer") {
			t.Fatalf("titre en cours : %q", got)
		}
		music.track = &Track{ID: "deja-la", Name: "B", Artist: "X"}
		if got := h.Handle(ctx, alice, "!song b"); !strings.Contains(got, "déjà dans la file") {
			t.Fatalf("titre déjà en file : %q", got)
		}
	})

	t.Run("plafonds par spectateur et global", func(t *testing.T) {
		music := &fakeMusic{connected: true}
		h, st, _ := newHandler(music)
		st.settings.MaxPerUser = 1
		st.settings.MaxPending = 2
		// bob a déjà un titre en attente (présent dans la file Spotify).
		st.requests = []model.SongRequest{{TrackID: "t1", Artist: "X", TrackName: "Un", RequestedBy: "bob", Status: model.StatusQueued}}
		music.playback = &Playback{Queue: []Track{{ID: "t1"}}}

		if got := h.Handle(ctx, bob, "!song nouveau"); !strings.Contains(got, "déjà 1 demande") {
			t.Fatalf("plafond par spectateur : %q", got)
		}
		if got := h.Handle(ctx, alice, "!song autre"); !strings.Contains(got, "ajouté") {
			t.Fatalf("alice devrait passer : %q", got)
		}
		// La file du bot compte maintenant 2 demandes (bob et alice) : plafond global atteint.
		music.playback = &Playback{Queue: []Track{{ID: "t1"}, {ID: "id-autre"}}}
		if got := h.Handle(ctx, User{Name: "carl"}, "!song encore"); !strings.Contains(got, "file de demandes est pleine") {
			t.Fatalf("plafond global : %q", got)
		}
	})

	t.Run("état de lecture illisible : règles ignorées", func(t *testing.T) {
		music := &fakeMusic{connected: true, pbErr: errors.New("403")}
		h, _, _ := newHandler(music)
		if got := h.Handle(ctx, bob, "!song x"); !strings.Contains(got, "ajouté") {
			t.Fatalf("réponse = %q", got)
		}
	})
}

func TestQueueNowPlayingSkipAndHelp(t *testing.T) {
	ctx := context.Background()
	music := &fakeMusic{connected: true}
	h, st, clk := newHandler(music)

	if got := h.Handle(ctx, bob, "!queue"); got != "Aucune demande en attente." {
		t.Fatalf("queue vide = %q", got)
	}
	clk.advance(10 * time.Second)

	h.Handle(ctx, bob, "!song un")
	music.playback = &Playback{Queue: []Track{{ID: "id-un"}}}
	if got := h.Handle(ctx, bob, "!queue"); !strings.Contains(got, "Artiste - Titre un (bob)") {
		t.Fatalf("queue = %q", got)
	}
	if got := h.Handle(ctx, alice, "!queue"); got != "" {
		t.Fatalf("!queue doit être en délai global : %q", got)
	}

	// Repli sur l'historique quand l'état de lecture est illisible.
	clk.advance(10 * time.Second)
	music.pbErr = errors.New("indispo")
	if got := h.Handle(ctx, bob, "!queue"); !strings.Contains(got, "Dernières demandes") {
		t.Fatalf("repli = %q", got)
	}
	music.pbErr = nil

	clk.advance(10 * time.Second)
	if got := h.Handle(ctx, bob, "!np"); got != "Aucun titre en cours de lecture." {
		t.Fatalf("np vide = %q", got)
	}
	clk.advance(10 * time.Second)
	music.playback = &Playback{Current: &Track{ID: "x", Name: "Titre", Artist: "Art"}}
	if got := h.Handle(ctx, bob, "!currentsong"); got != "En cours : Art - Titre" {
		t.Fatalf("np = %q", got)
	}

	if got := h.Handle(ctx, bob, "!skip"); got != "" || music.skipped != 0 {
		t.Fatalf("un spectateur ne peut pas passer un titre : %q", got)
	}
	if got := h.Handle(ctx, mod, "!skip"); got != "Titre passé." || music.skipped != 1 {
		t.Fatalf("skip modérateur = %q (skipped=%d)", got, music.skipped)
	}
	st.settings.SkipLevel = model.LevelEveryone
	clk.advance(10 * time.Second)
	if got := h.Handle(ctx, bob, "!skip"); got != "Titre passé." {
		t.Fatalf("skip ouvert à tous = %q", got)
	}

	clk.advance(10 * time.Second)
	help := h.Handle(ctx, bob, "!help")
	if !strings.Contains(help, "!song") || !strings.Contains(help, "!discord") || strings.Contains(help, "!dc") {
		t.Fatalf("help = %q", help)
	}
}

func TestInfoCommandsHaveGlobalCooldown(t *testing.T) {
	ctx := context.Background()
	h, _, clk := newHandler(&fakeMusic{connected: true})
	if got := h.Handle(ctx, bob, "!help"); got == "" {
		t.Fatal("la première réponse devrait passer")
	}
	if got := h.Handle(ctx, alice, "!help"); got != "" {
		t.Fatalf("!help ne doit pas pouvoir être spammé : %q", got)
	}
	clk.advance(infoCooldown + time.Second)
	if got := h.Handle(ctx, alice, "!help"); got == "" {
		t.Fatal("après le délai, !help devrait répondre")
	}
}

func TestManageCommandsFromChat(t *testing.T) {
	ctx := context.Background()
	h, st, clk := newHandler(nil)

	if got := h.Handle(ctx, bob, "!addcmd salut Bonjour"); got != "" {
		t.Fatalf("un spectateur ne peut pas gérer les commandes : %q", got)
	}
	if got := h.Handle(ctx, mod, "!addcmd !Salut Bonjour {user}"); got != "!salut ajoutée." {
		t.Fatalf("addcmd = %q", got)
	}
	if st.cmds["salut"].Response != "Bonjour {user}" || !st.cmds["salut"].Enabled {
		t.Fatalf("commande créée = %+v", st.cmds["salut"])
	}
	if got := h.Handle(ctx, mod, "!addcmd salut autre"); !strings.Contains(got, "existe déjà") {
		t.Fatalf("doublon = %q", got)
	}
	if got := h.Handle(ctx, mod, "!addcmd dc encore"); !strings.Contains(got, "existe déjà") {
		t.Fatalf("collision avec un alias = %q", got)
	}
	if got := h.Handle(ctx, mod, "!addcmd song x"); !strings.Contains(got, "Impossible") {
		t.Fatalf("nom réservé = %q", got)
	}
	if got := h.Handle(ctx, mod, "!addcmd"); !strings.HasPrefix(got, "Utilisation") {
		t.Fatalf("usage = %q", got)
	}
	// La commande est utilisable tout de suite (cache invalidé).
	if got := h.Handle(ctx, bob, "!salut"); got != "Bonjour bob" {
		t.Fatalf("commande ajoutée = %q", got)
	}
	if got := h.Handle(ctx, mod, "!editcmd salut Coucou"); got != "!salut modifiée." {
		t.Fatalf("editcmd = %q", got)
	}
	clk.advance(10 * time.Second)
	if got := h.Handle(ctx, bob, "!salut"); got != "Coucou" {
		t.Fatalf("après modification = %q", got)
	}
	if got := h.Handle(ctx, mod, "!editcmd nexistepas x"); !strings.Contains(got, "n'existe pas") {
		t.Fatalf("editcmd inconnue = %q", got)
	}
	if got := h.Handle(ctx, mod, "!delcmd salut"); got != "!salut supprimée." {
		t.Fatalf("delcmd = %q", got)
	}
	if got := h.Handle(ctx, mod, "!delcmd salut"); !strings.Contains(got, "n'existe pas") {
		t.Fatalf("delcmd répétée = %q", got)
	}
}

func TestParseTrackID(t *testing.T) {
	const id = "4uLU6hMCjMI75M1A2tKUQC"
	for _, in := range []string{
		"https://open.spotify.com/track/" + id + "?si=abc",
		"https://open.spotify.com/intl-fr/track/" + id,
		"spotify:track:" + id,
	} {
		if got, ok := ParseTrackID(in); !ok || got != id {
			t.Errorf("ParseTrackID(%q) = (%q, %v)", in, got, ok)
		}
	}
	for _, in := range []string{"daft punk", "https://open.spotify.com/album/" + id, "spotify:track:court"} {
		if _, ok := ParseTrackID(in); ok {
			t.Errorf("ParseTrackID(%q) devrait être faux", in)
		}
	}
}

func TestRenamedRequestCommand(t *testing.T) {
	ctx := context.Background()
	h, st, _ := newHandler(&fakeMusic{connected: true})
	st.settings.RequestCommand = "requestspotify"
	st.settings.RequestAliases = []string{"rs"}

	// Le nouveau nom et son alias déclenchent la demande de musique.
	for _, cmd := range []string{"!requestspotify", "!RS"} {
		got := h.Handle(ctx, bob, cmd)
		if !strings.HasPrefix(got, "Utilisation") {
			t.Errorf("%s : réponse = %q", cmd, got)
		}
		if !strings.Contains(got, "!requestspotify") {
			t.Errorf("%s : usage sans le nom configuré : %q", cmd, got)
		}
	}
	// L'ancien nom ne déclenche plus rien et n'est pas une commande personnalisée.
	if got := h.Handle(ctx, bob, "!song x"); got != "" {
		t.Errorf("!song répond encore : %q", got)
	}
	// L'aide annonce le nom configuré.
	if got := h.Handle(ctx, bob, "!help"); !strings.Contains(got, "!requestspotify") {
		t.Errorf("aide = %q", got)
	}
}
