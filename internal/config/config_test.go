package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string {
	return func(k string) string { return m[k] }
}

func base() map[string]string {
	return map[string]string{
		"TWITCH_CHANNEL":  "#MaChaine",
		"TWITCH_USERNAME": "MonBot",
		"TWITCH_TOKEN":    "abc123",
		"ADMIN_PASSWORD":  "un-mot-de-passe",
	}
}

func TestLoadDefaultsAndNormalization(t *testing.T) {
	c, err := Load(env(base()))
	if err != nil {
		t.Fatal(err)
	}
	if c.Channel != "machaine" || c.Username != "monbot" {
		t.Errorf("normalisation : channel=%q username=%q", c.Channel, c.Username)
	}
	if c.Token != "oauth:abc123" {
		t.Errorf("token = %q", c.Token)
	}
	if c.Port != "9090" || c.DBPath != "bot.db" || c.AdminUser != "admin" {
		t.Errorf("valeurs par défaut : %+v", c)
	}
	if c.SongCooldown != 30*time.Second {
		t.Errorf("cooldown = %v", c.SongCooldown)
	}
	if c.SpotifyEnabled() {
		t.Error("Spotify ne devrait pas être activé sans identifiants")
	}
	if c.SpotifyRedirectURL != "http://127.0.0.1:9090/auth/spotify/callback" {
		t.Errorf("redirect = %q", c.SpotifyRedirectURL)
	}
}

func TestLoadKeepsExistingOAuthPrefix(t *testing.T) {
	m := base()
	m["TWITCH_TOKEN"] = "oauth:deja"
	c, err := Load(env(m))
	if err != nil || c.Token != "oauth:deja" {
		t.Fatalf("token = %q, err = %v", c.Token, err)
	}
}

func TestLoadErrors(t *testing.T) {
	t.Run("variables manquantes", func(t *testing.T) {
		_, err := Load(env(map[string]string{}))
		if err == nil {
			t.Fatal("erreur attendue")
		}
		for _, k := range []string{"TWITCH_CHANNEL", "TWITCH_USERNAME", "TWITCH_TOKEN", "ADMIN_PASSWORD"} {
			if !strings.Contains(err.Error(), k) {
				t.Errorf("%s absent du message : %v", k, err)
			}
		}
	})

	cases := map[string]func(map[string]string){
		"mot de passe court":      func(m map[string]string) { m["ADMIN_PASSWORD"] = "court" },
		"port non numérique":      func(m map[string]string) { m["PORT"] = "abc" },
		"port hors bornes":        func(m map[string]string) { m["PORT"] = "70000" },
		"spotify incomplet":       func(m map[string]string) { m["SPOTIFY_ID"] = "x" },
		"secret sans identifiant": func(m map[string]string) { m["SPOTIFY_SECRET"] = "x" },
		"cooldown négatif":        func(m map[string]string) { m["SONG_COOLDOWN_SECONDS"] = "-5" },
		"cooldown non numérique":  func(m map[string]string) { m["SONG_COOLDOWN_SECONDS"] = "vite" },
		"cooldown trop grand":     func(m map[string]string) { m["SONG_COOLDOWN_SECONDS"] = "99999" },
	}
	for name, mutate := range cases {
		m := base()
		mutate(m)
		if _, err := Load(env(m)); err == nil {
			t.Errorf("%s : erreur attendue", name)
		}
	}
}

func TestLoadSpotifyAndOverrides(t *testing.T) {
	m := base()
	m["SPOTIFY_ID"] = "id"
	m["SPOTIFY_SECRET"] = "secret"
	m["PORT"] = "9191"
	m["SONG_COOLDOWN_SECONDS"] = "0"
	m["SPOTIFY_REDIRECT_URL"] = "https://bot.exemple.be/auth/spotify/callback"
	c, err := Load(env(m))
	if err != nil {
		t.Fatal(err)
	}
	if !c.SpotifyEnabled() || c.Port != "9191" || c.SongCooldown != 0 {
		t.Errorf("config = %+v", c)
	}
	if c.SpotifyRedirectURL != "https://bot.exemple.be/auth/spotify/callback" {
		t.Errorf("redirect = %q", c.SpotifyRedirectURL)
	}
}
