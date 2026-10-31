package model

import "testing"

func TestOverlaySettingsNormalize(t *testing.T) {
	ok := DefaultOverlaySettings()
	if err := ok.Normalize(); err != nil {
		t.Fatalf("valeurs par défaut refusées : %v", err)
	}
	for _, tt := range []OverlaySettings{
		{MaxMessages: 0, FontSize: 22},
		{MaxMessages: 101, FontSize: 22},
		{MaxMessages: 40, FontSize: 11},
		{MaxMessages: 40, FontSize: 73},
	} {
		if err := tt.Normalize(); err == nil {
			t.Errorf("Normalize(%+v) accepté, attendu une erreur", tt)
		}
	}
}
