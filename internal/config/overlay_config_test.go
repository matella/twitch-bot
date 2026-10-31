package config

import (
	"strings"
	"testing"
)

func TestOverlayToken(t *testing.T) {
	base := map[string]string{
		"TWITCH_CHANNEL":  "macha",
		"TWITCH_USERNAME": "bot",
		"TWITCH_TOKEN":    "abc",
		"ADMIN_PASSWORD":  "motdepasse-long",
	}
	load := func(extra map[string]string) (Config, error) {
		env := map[string]string{}
		for k, v := range base {
			env[k] = v
		}
		for k, v := range extra {
			env[k] = v
		}
		return Load(func(k string) string { return env[k] })
	}

	// Sans jeton, l'incrustation reste utilisable (page ouverte).
	if c, err := load(nil); err != nil || c.OverlayToken != "" {
		t.Fatalf("sans OVERLAY_TOKEN : jeton=%q, err=%v ; attendu un jeton vide sans erreur", c.OverlayToken, err)
	}

	if c, err := load(map[string]string{"OVERLAY_TOKEN": "0123456789abcdef"}); err != nil || c.OverlayToken == "" {
		t.Fatalf("jeton valide : jeton=%q, err=%v", c.OverlayToken, err)
	}

	if _, err := load(map[string]string{"OVERLAY_TOKEN": "court"}); err == nil || !strings.Contains(err.Error(), "OVERLAY_TOKEN") {
		t.Errorf("jeton trop court : err = %v, attendu une erreur OVERLAY_TOKEN", err)
	}
}
