package model

import "testing"

func TestOverlayPositionMiddleLeftIsAccepted(t *testing.T) {
	o := DefaultOverlaySettings()
	o.Position = "middle-left"
	if err := o.Normalize(); err != nil {
		t.Fatalf("middle-left refusé : %v", err)
	}
}

func TestOverlaySettingsNormalize(t *testing.T) {
	ok := DefaultOverlaySettings()
	if err := ok.Normalize(); err != nil {
		t.Fatalf("valeurs par défaut refusées : %v", err)
	}
	for _, tt := range []func(*OverlaySettings){
		func(o *OverlaySettings) { o.MaxMessages = 0 },
		func(o *OverlaySettings) { o.MaxMessages = 101 },
		func(o *OverlaySettings) { o.FontSize = 11 },
		func(o *OverlaySettings) { o.FontSize = 73 },
		func(o *OverlaySettings) { o.FontFamily = "comic" },
		func(o *OverlaySettings) { o.TextColor = "red" },
		func(o *OverlaySettings) { o.TextColor = "#fff" },
		func(o *OverlaySettings) { o.Position = "center" },
		func(o *OverlaySettings) { o.MarginTop = -1 },
		func(o *OverlaySettings) { o.MarginRight = -5 },
		func(o *OverlaySettings) { o.MarginBottom = -1 },
		func(o *OverlaySettings) { o.MarginLeft = -1 },
		func(o *OverlaySettings) { o.Width = 99 },
		func(o *OverlaySettings) { o.Width = 1921 },
	} {
		o := DefaultOverlaySettings()
		tt(&o)
		if err := o.Normalize(); err == nil {
			t.Errorf("Normalize(%+v) accepté, attendu une erreur", o)
		}
	}
}
