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
}

func (s *fakeStore) GetCommand(_ context.Context, name string) (*model.Command, error) {
	c, ok := s.cmds[name]
	if !ok {
		return nil, model.ErrNotFound
	}
	return &c, nil
}

func (s *fakeStore) ListCommands(context.Context) ([]model.Command, error) {
	var out []model.Command
	for _, c := range s.cmds {
		out = append(out, c)
	}
	return out, nil
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

type fakeMusic struct {
	connected bool
	findErr   error
	queueErr  error
	queued    []string
}

func (m *fakeMusic) Connected() bool { return m.connected }
func (m *fakeMusic) Find(_ context.Context, q string) (*Track, error) {
	if m.findErr != nil {
		return nil, m.findErr
	}
	return &Track{ID: "id-" + q, Name: "Titre " + q, Artist: "Artiste"}, nil
}
func (m *fakeMusic) Queue(_ context.Context, id string) error {
	if m.queueErr != nil {
		return m.queueErr
	}
	m.queued = append(m.queued, id)
	return nil
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time          { return c.t }
func (c *clock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newHandler(music Music) (*Handler, *fakeStore, *clock) {
	st := &fakeStore{cmds: map[string]model.Command{
		"discord": {Name: "discord", Response: "Rejoins-nous {user} !", CooldownSeconds: 10},
		"echo":    {Name: "echo", Response: "{args}", CooldownSeconds: 0},
	}}
	clk := &clock{t: time.Unix(1_700_000_000, 0)}
	h := New(st, music, Options{Channel: "chan", SongCooldown: 30 * time.Second, Now: clk.now})
	return h, st, clk
}

func TestIgnoresNonCommands(t *testing.T) {
	h, _, _ := newHandler(nil)
	if got := h.Handle(context.Background(), "bob", "salut tout le monde"); got != "" {
		t.Fatalf("réponse inattendue : %q", got)
	}
	if got := h.Handle(context.Background(), "bob", "!inconnue"); got != "" {
		t.Fatalf("commande inconnue a répondu : %q", got)
	}
}

func TestCustomCommandAndCooldown(t *testing.T) {
	h, _, clk := newHandler(nil)
	ctx := context.Background()
	if got := h.Handle(ctx, "bob", "!Discord"); got != "Rejoins-nous bob !" {
		t.Fatalf("réponse = %q", got)
	}
	if got := h.Handle(ctx, "alice", "!discord"); got != "" {
		t.Fatalf("le délai global n'a pas bloqué : %q", got)
	}
	clk.advance(11 * time.Second)
	if got := h.Handle(ctx, "alice", "!discord"); got == "" {
		t.Fatal("la commande devrait de nouveau répondre après le délai")
	}
}

func TestOutgoingMessagesCannotInjectTwitchCommands(t *testing.T) {
	h, _, _ := newHandler(nil)
	got := h.Handle(context.Background(), "mallory", "!echo /ban victim")
	if strings.HasPrefix(got, "/") || strings.HasPrefix(got, ".") {
		t.Fatalf("message sortant interprétable comme commande Twitch : %q", got)
	}
}

func TestSongRequest(t *testing.T) {
	ctx := context.Background()

	t.Run("spotify absent", func(t *testing.T) {
		h, _, _ := newHandler(nil)
		if got := h.Handle(ctx, "bob", "!song x"); !strings.Contains(got, "pas disponibles") {
			t.Fatalf("réponse = %q", got)
		}
	})

	t.Run("non connecté", func(t *testing.T) {
		h, _, _ := newHandler(&fakeMusic{connected: false})
		if got := h.Handle(ctx, "bob", "!song x"); !strings.Contains(got, "pas disponibles") {
			t.Fatalf("réponse = %q", got)
		}
	})

	t.Run("usage", func(t *testing.T) {
		h, _, _ := newHandler(&fakeMusic{connected: true})
		if got := h.Handle(ctx, "bob", "!song"); !strings.HasPrefix(got, "Utilisation") {
			t.Fatalf("réponse = %q", got)
		}
	})

	t.Run("trop long", func(t *testing.T) {
		h, _, _ := newHandler(&fakeMusic{connected: true})
		if got := h.Handle(ctx, "bob", "!song "+strings.Repeat("a", 101)); !strings.Contains(got, "trop longue") {
			t.Fatalf("réponse = %q", got)
		}
	})

	t.Run("succès, historique et délai par spectateur", func(t *testing.T) {
		music := &fakeMusic{connected: true}
		h, st, clk := newHandler(music)
		got := h.Handle(ctx, "bob", "!sr daft punk")
		if !strings.Contains(got, "ajouté") || len(music.queued) != 1 {
			t.Fatalf("réponse = %q, queued = %v", got, music.queued)
		}
		if len(st.requests) != 1 || st.requests[0].Status != model.StatusQueued || st.requests[0].RequestedBy != "bob" {
			t.Fatalf("historique = %+v", st.requests)
		}
		if got := h.Handle(ctx, "bob", "!song autre"); !strings.Contains(got, "patiente") {
			t.Fatalf("le délai n'a pas bloqué : %q", got)
		}
		if got := h.Handle(ctx, "alice", "!song autre"); !strings.Contains(got, "ajouté") {
			t.Fatalf("un autre spectateur ne devrait pas être bloqué : %q", got)
		}
		clk.advance(31 * time.Second)
		if got := h.Handle(ctx, "bob", "!song encore"); !strings.Contains(got, "ajouté") {
			t.Fatalf("bob devrait pouvoir redemander : %q", got)
		}
	})

	t.Run("aucun résultat ne consomme pas le délai", func(t *testing.T) {
		music := &fakeMusic{connected: true, findErr: ErrNoResult}
		h, _, _ := newHandler(music)
		if got := h.Handle(ctx, "bob", "!song zzz"); !strings.Contains(got, "Aucun résultat") {
			t.Fatalf("réponse = %q", got)
		}
		music.findErr = nil
		if got := h.Handle(ctx, "bob", "!song vrai titre"); !strings.Contains(got, "ajouté") {
			t.Fatalf("la recherche ratée a bloqué bob : %q", got)
		}
	})

	t.Run("aucun lecteur actif", func(t *testing.T) {
		h, st, _ := newHandler(&fakeMusic{connected: true, queueErr: ErrNoDevice})
		if got := h.Handle(ctx, "bob", "!song x"); !strings.Contains(got, "aucun lecteur") {
			t.Fatalf("réponse = %q", got)
		}
		if len(st.requests) != 1 || st.requests[0].Status != model.StatusFailed {
			t.Fatalf("historique = %+v", st.requests)
		}
	})

	t.Run("erreur spotify générique", func(t *testing.T) {
		h, _, _ := newHandler(&fakeMusic{connected: true, findErr: errors.New("boom")})
		if got := h.Handle(ctx, "bob", "!song x"); !strings.Contains(got, "Erreur Spotify") {
			t.Fatalf("réponse = %q", got)
		}
	})
}

func TestQueueAndHelp(t *testing.T) {
	ctx := context.Background()
	h, _, _ := newHandler(&fakeMusic{connected: true})
	if got := h.Handle(ctx, "bob", "!queue"); got != "Aucune demande récente." {
		t.Fatalf("queue vide = %q", got)
	}
	h.Handle(ctx, "bob", "!song un")
	if got := h.Handle(ctx, "bob", "!queue"); !strings.Contains(got, "Artiste - Titre un (bob)") {
		t.Fatalf("queue = %q", got)
	}
	help := h.Handle(ctx, "bob", "!help")
	if !strings.Contains(help, "!song") || !strings.Contains(help, "!discord") {
		t.Fatalf("help = %q", help)
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
