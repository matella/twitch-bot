// Package config charge la configuration depuis les variables d'environnement.
// Les secrets ne passent volontairement jamais par la ligne de commande
// (visible dans la liste des processus).
package config

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Config est la configuration complète de l'application.
type Config struct {
	Channel  string // chaîne Twitch à rejoindre (sans #, minuscules)
	Username string // compte du bot (minuscules)
	Token    string // jeton OAuth Twitch, toujours préfixé "oauth:"

	SpotifyID          string
	SpotifySecret      string
	SpotifyRedirectURL string

	Port          string
	DBPath        string
	AdminUser     string
	AdminPassword string

	SongCooldown time.Duration
}

// SpotifyEnabled indique si les identifiants Spotify sont fournis.
func (c Config) SpotifyEnabled() bool { return c.SpotifyID != "" && c.SpotifySecret != "" }

// MinPasswordLen est la longueur minimale du mot de passe admin.
const MinPasswordLen = 8

// Load lit la configuration via getenv (os.Getenv en production).
func Load(getenv func(string) string) (Config, error) {
	get := func(k string) string { return strings.TrimSpace(getenv(k)) }
	def := func(k, d string) string {
		if v := get(k); v != "" {
			return v
		}
		return d
	}

	c := Config{
		Channel:       strings.ToLower(strings.TrimPrefix(get("TWITCH_CHANNEL"), "#")),
		Username:      strings.ToLower(get("TWITCH_USERNAME")),
		Token:         get("TWITCH_TOKEN"),
		SpotifyID:     get("SPOTIFY_ID"),
		SpotifySecret: get("SPOTIFY_SECRET"),
		Port:          def("PORT", "9090"),
		DBPath:        def("DB_PATH", "bot.db"),
		AdminUser:     def("ADMIN_USER", "admin"),
		AdminPassword: getenv("ADMIN_PASSWORD"), // pas de TrimSpace : un mot de passe peut contenir des espaces
	}
	if c.Token != "" && !strings.HasPrefix(c.Token, "oauth:") {
		c.Token = "oauth:" + c.Token
	}

	var missing []string
	for _, kv := range []struct{ key, val string }{
		{"TWITCH_CHANNEL", c.Channel},
		{"TWITCH_USERNAME", c.Username},
		{"TWITCH_TOKEN", c.Token},
		{"ADMIN_PASSWORD", c.AdminPassword},
	} {
		if kv.val == "" {
			missing = append(missing, kv.key)
		}
	}
	if len(missing) > 0 {
		return Config{}, fmt.Errorf("variables d'environnement manquantes : %s", strings.Join(missing, ", "))
	}

	if len(c.AdminPassword) < MinPasswordLen {
		return Config{}, fmt.Errorf("ADMIN_PASSWORD trop court (%d caractères minimum)", MinPasswordLen)
	}
	if n, err := strconv.Atoi(c.Port); err != nil || n < 1 || n > 65535 {
		return Config{}, fmt.Errorf("PORT invalide : %q", c.Port)
	}
	if (c.SpotifyID == "") != (c.SpotifySecret == "") {
		return Config{}, errors.New("SPOTIFY_ID et SPOTIFY_SECRET doivent être définis ensemble")
	}

	c.SpotifyRedirectURL = def("SPOTIFY_REDIRECT_URL", "http://127.0.0.1:"+c.Port+"/auth/spotify/callback")

	secs := 30
	if v := get("SONG_COOLDOWN_SECONDS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 0 || n > 3600 {
			return Config{}, fmt.Errorf("SONG_COOLDOWN_SECONDS invalide : %q (0 à 3600)", v)
		}
		secs = n
	}
	c.SongCooldown = time.Duration(secs) * time.Second
	return c, nil
}
