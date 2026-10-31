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

	c, err := load(nil)
	if err != nil || c.OverlayEnabled() {
		t.Fatalf("sans OVERLAY_TOKEN : activée=%v, err=%v ; attendu désactivée sans erreur", c.OverlayEnabled(), err)
	}

	c, err = load(map[string]string{"OVERLAY_TOKEN": "0123456789abcdef"})
	if err != nil || !c.OverlayEnabled() {
		t.Fatalf("jeton valide : activée=%v, err=%v", c.OverlayEnabled(), err)
	}

	if _, err := load(map[string]string{"OVERLAY_TOKEN": "court"}); err == nil || !strings.Contains(err.Error(), "OVERLAY_TOKEN") {
		t.Errorf("jeton trop court : err = %v, attendu une erreur OVERLAY_TOKEN", err)
	}
}
