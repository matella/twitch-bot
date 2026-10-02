package bot

import (
	"testing"
	"time"

	"github.com/matella/twitch-bot/internal/model"
)

func TestLevelOf(t *testing.T) {
	tests := []struct {
		login  string
		badges map[string]int
		want   model.Level
	}{
		{"bob", nil, model.LevelEveryone},
		{"bob", map[string]int{"subscriber": 3}, model.LevelSubscriber},
		{"bob", map[string]int{"subscriber": 0}, model.LevelSubscriber},
		{"bob", map[string]int{"founder": 0}, model.LevelSubscriber},
		{"bob", map[string]int{"vip": 1, "subscriber": 1}, model.LevelVIP},
		{"bob", map[string]int{"moderator": 1, "vip": 1}, model.LevelModerator},
		{"bob", map[string]int{"broadcaster": 1}, model.LevelBroadcaster},
		{"machaine", nil, model.LevelBroadcaster},
	}
	for _, tt := range tests {
		if got := levelOf(tt.login, "MaChaine", tt.badges); got != tt.want {
			t.Errorf("levelOf(%q, %v) = %v, attendu %v", tt.login, tt.badges, got, tt.want)
		}
	}
}

func TestWindowLimiter(t *testing.T) {
	w := newWindow(3, 10*time.Second)
	now := time.Unix(1_700_000_000, 0)
	for i := 0; i < 3; i++ {
		if !w.allow(now) {
			t.Fatalf("envoi %d refusé", i)
		}
	}
	if w.allow(now.Add(5 * time.Second)) {
		t.Fatal("le 4e envoi dans la fenêtre devrait être refusé")
	}
	if !w.allow(now.Add(11 * time.Second)) {
		t.Fatal("la fenêtre est écoulée : l'envoi devrait passer")
	}
}

func TestConnectedFollowsActivity(t *testing.T) {
	b := New("chan", StaticCredentials{}, nil)
	if b.Connected() {
		t.Fatal("un bot neuf ne devrait pas être connecté")
	}
	b.touch()
	if !b.Connected() {
		t.Fatal("activité récente : devrait être connecté")
	}
	b.lastActivity.Store(time.Now().Add(-2 * activityTimeout).UnixNano())
	if b.Connected() {
		t.Fatal("silence prolongé : devrait être hors ligne")
	}
}
