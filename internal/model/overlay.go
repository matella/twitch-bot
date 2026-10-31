package model

import (
	"errors"
	"regexp"
)

// Positions possibles de l'incrustation dans la source OBS.
var OverlayPositions = []string{"top-left", "top-right", "bottom-left", "bottom-right"}

// Familles de police possibles (les piles CSS sont choisies dans la page).
var OverlayFonts = []string{"sans", "serif", "mono"}

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// OverlaySettings règle l'affichage de l'incrustation du chat (modifiable depuis l'administration).
type OverlaySettings struct {
	MaxMessages  int    `json:"max_messages"` // messages gardés à l'écran
	FontSize     int    `json:"font_size"`    // taille de police en pixels
	FontFamily   string `json:"font_family"`  // sans | serif | mono
	TextColor    string `json:"text_color"`   // couleur du texte « #rrggbb »
	ShowBadges   bool   `json:"show_badges"`  // afficher les badges à côté du pseudo
	Position     string `json:"position"`     // coin de la source : top-left, top-right, bottom-left, bottom-right
	MarginTop    int    `json:"margin_top"`   // marges par rapport aux bords de la source, en pixels
	MarginRight  int    `json:"margin_right"`
	MarginBottom int    `json:"margin_bottom"`
	MarginLeft   int    `json:"margin_left"`
	Width        int    `json:"width"` // largeur du chat, en pixels
}

// DefaultOverlaySettings sert tant que rien n'a été enregistré.
func DefaultOverlaySettings() OverlaySettings {
	return OverlaySettings{
		MaxMessages:  40,
		FontSize:     22,
		FontFamily:   "sans",
		TextColor:    "#ffffff",
		ShowBadges:   true,
		Position:     "bottom-left",
		MarginTop:    12,
		MarginRight:  12,
		MarginBottom: 12,
		MarginLeft:   12,
		Width:        600,
	}
}

// Normalize vérifie les bornes et les valeurs autorisées des réglages.
func (o *OverlaySettings) Normalize() error {
	if o.MaxMessages < 1 || o.MaxMessages > 100 {
		return errors.New("nombre de messages invalide (1 à 100)")
	}
	if o.FontSize < 12 || o.FontSize > 72 {
		return errors.New("taille de police invalide (12 à 72)")
	}
	if !contains(OverlayFonts, o.FontFamily) {
		return errors.New("police invalide")
	}
	if !colorRe.MatchString(o.TextColor) {
		return errors.New("couleur du texte invalide (format #rrggbb)")
	}
	if !contains(OverlayPositions, o.Position) {
		return errors.New("position invalide")
	}
	for _, m := range []int{o.MarginTop, o.MarginRight, o.MarginBottom, o.MarginLeft} {
		if m < 0 {
			return errors.New("marge invalide (négative)")
		}
	}
	if o.Width < 100 || o.Width > 1920 {
		return errors.New("largeur invalide (100 à 1920)")
	}
	return nil
}

func contains(list []string, v string) bool {
	for _, s := range list {
		if s == v {
			return true
		}
	}
	return false
}
