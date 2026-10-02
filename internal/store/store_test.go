package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/matella/twitch-bot/internal/model"
)

func open(t *testing.T) *Store {
	t.Helper()
	s, err := Open(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestCommands(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.GetCommand(ctx, "absente"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("GetCommand absente = %v", err)
	}

	c := model.Command{Name: "lurk", Response: "merci {user}", CooldownSeconds: 7}
	if err := s.AddCommand(ctx, c); err != nil {
		t.Fatal(err)
	}
	if err := s.AddCommand(ctx, c); !errors.Is(err, model.ErrExists) {
		t.Fatalf("doublon = %v", err)
	}

	got, err := s.GetCommand(ctx, "lurk")
	if err != nil || got.Response != "merci {user}" || got.CooldownSeconds != 7 || got.UpdatedAt == 0 {
		t.Fatalf("GetCommand = %+v, %v", got, err)
	}

	if err := s.UpdateCommand(ctx, model.Command{Name: "lurk", Response: "nouveau", CooldownSeconds: 0}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpdateCommand(ctx, model.Command{Name: "inconnue", Response: "x"}); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("update inconnue = %v", err)
	}

	if err := s.AddCommand(ctx, model.Command{Name: "alpha", Response: "a", CooldownSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	list, err := s.ListCommands(ctx)
	if err != nil || len(list) != 2 || list[0].Name != "alpha" || list[1].Response != "nouveau" {
		t.Fatalf("ListCommands = %+v, %v", list, err)
	}

	if err := s.DeleteCommand(ctx, "lurk"); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteCommand(ctx, "lurk"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("delete répété = %v", err)
	}
}

func TestCommandExtendedFieldsAndCounter(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	c := model.Command{Name: "so", Response: "va voir {touser}", CooldownSeconds: 3, UserCooldownSeconds: 20,
		Permission: model.LevelModerator, Enabled: true, Aliases: []string{"shoutout", "so2"}}
	if err := s.AddCommand(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetCommand(ctx, "so")
	if err != nil || got.Permission != model.LevelModerator || got.UserCooldownSeconds != 20 || !got.Enabled ||
		len(got.Aliases) != 2 || got.Aliases[1] != "so2" || got.UseCount != 0 {
		t.Fatalf("GetCommand = %+v, %v", got, err)
	}

	for want := int64(1); want <= 3; want++ {
		if n, err := s.IncrementUse(ctx, "so"); err != nil || n != want {
			t.Fatalf("IncrementUse = %d, %v (attendu %d)", n, err, want)
		}
	}
	if _, err := s.IncrementUse(ctx, "absente"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("IncrementUse absente = %v", err)
	}

	// Une mise à jour ne remet pas le compteur à zéro.
	c.Enabled, c.Aliases = false, nil
	if err := s.UpdateCommand(ctx, c); err != nil {
		t.Fatal(err)
	}
	got, _ = s.GetCommand(ctx, "so")
	if got.UseCount != 3 || got.Enabled || len(got.Aliases) != 0 || got.Aliases == nil {
		t.Fatalf("après mise à jour = %+v", got)
	}
}

func TestMusicSettingsRoundTrip(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	def, err := s.GetMusicSettings(ctx)
	if err != nil || def.MaxPerUser != 3 || def.SkipLevel != model.LevelModerator {
		t.Fatalf("valeurs par défaut = %+v, %v", def, err)
	}
	want := model.MusicSettings{RequestLevel: model.LevelVIP, MaxPending: 7, BlockExplicit: true, Blocklist: []string{"x"}}
	if err := s.SetMusicSettings(ctx, want); err != nil {
		t.Fatal(err)
	}
	got, err := s.GetMusicSettings(ctx)
	if err != nil || got.RequestLevel != model.LevelVIP || got.MaxPending != 7 || !got.BlockExplicit || got.Blocklist[0] != "x" {
		t.Fatalf("relecture = %+v, %v", got, err)
	}
}

func TestMigrationFromVersionZeroDatabase(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "old.db")

	// Base créée par l'ancienne version : schéma initial, user_version = 0, une commande.
	old, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(migrations[0]); err != nil {
		t.Fatal(err)
	}
	if _, err := old.Exec(`INSERT INTO commands (name, response, cooldown_seconds, created_at, updated_at) VALUES ('vieille', 'ok', 9, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	old.Close()

	s, err := Open(path)
	if err != nil {
		t.Fatalf("la migration a échoué : %v", err)
	}
	defer s.Close()
	got, err := s.GetCommand(ctx, "vieille")
	if err != nil || got.CooldownSeconds != 9 || !got.Enabled || got.Permission != model.LevelEveryone {
		t.Fatalf("commande migrée = %+v, %v", got, err)
	}
	var v int
	s.db.QueryRow(`PRAGMA user_version`).Scan(&v)
	if v != len(migrations) {
		t.Fatalf("user_version = %d, attendu %d", v, len(migrations))
	}
}

func TestOpenRefusesNewerDatabase(t *testing.T) {
	path := filepath.Join(t.TempDir(), "futur.db")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(fmt.Sprintf(`PRAGMA user_version = %d`, len(migrations)+1)); err != nil {
		t.Fatal(err)
	}
	db.Close()
	if _, err := Open(path); err == nil {
		t.Fatal("une base plus récente que le programme doit être refusée")
	}
}

func TestSongRequests(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	for _, name := range []string{"un", "deux", "trois"} {
		if err := s.AddSongRequest(ctx, model.SongRequest{
			TrackID: "id-" + name, TrackName: name, Artist: "art", RequestedBy: "bob", Status: model.StatusQueued,
		}); err != nil {
			t.Fatal(err)
		}
	}
	reqs, err := s.RecentSongRequests(ctx, 2)
	if err != nil || len(reqs) != 2 || reqs[0].TrackName != "trois" || reqs[1].TrackName != "deux" {
		t.Fatalf("RecentSongRequests = %+v, %v", reqs, err)
	}
	if reqs[0].CreatedAt == 0 || reqs[0].Status != model.StatusQueued {
		t.Fatalf("champs manquants : %+v", reqs[0])
	}

	if err := s.DeleteSongRequest(ctx, reqs[0].ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteSongRequest(ctx, reqs[0].ID); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("delete répété = %v", err)
	}
	if err := s.ClearSongRequests(ctx); err != nil {
		t.Fatal(err)
	}
	if reqs, _ := s.RecentSongRequests(ctx, 10); len(reqs) != 0 {
		t.Fatalf("après vidage : %+v", reqs)
	}
}

func TestSongRequestHistoryIsBounded(t *testing.T) {
	ctx := context.Background()
	s := open(t)
	for i := 0; i < maxHistory+25; i++ {
		if err := s.AddSongRequest(ctx, model.SongRequest{TrackID: "x", TrackName: "t", Artist: "a", RequestedBy: "u", Status: model.StatusQueued}); err != nil {
			t.Fatal(err)
		}
	}
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM song_requests`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != maxHistory {
		t.Fatalf("historique = %d lignes, attendu %d", n, maxHistory)
	}
}

func TestSettings(t *testing.T) {
	ctx := context.Background()
	s := open(t)

	if _, err := s.GetSetting(ctx, "k"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("GetSetting absent = %v", err)
	}
	if err := s.SetSetting(ctx, "k", "v1"); err != nil {
		t.Fatal(err)
	}
	if err := s.SetSetting(ctx, "k", "v2"); err != nil {
		t.Fatal(err)
	}
	if v, err := s.GetSetting(ctx, "k"); err != nil || v != "v2" {
		t.Fatalf("GetSetting = %q, %v", v, err)
	}
	if err := s.DeleteSetting(ctx, "k"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.GetSetting(ctx, "k"); !errors.Is(err, model.ErrNotFound) {
		t.Fatalf("après suppression = %v", err)
	}
}

func TestPersistsAcrossReopen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "persist.db")
	s, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.AddCommand(ctx, model.Command{Name: "x", Response: "y", CooldownSeconds: 1}); err != nil {
		t.Fatal(err)
	}
	s.Close()

	s2, err := Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer s2.Close()
	if _, err := s2.GetCommand(ctx, "x"); err != nil {
		t.Fatalf("la commande n'a pas survécu à la réouverture : %v", err)
	}
}
