package model

import "errors"

// OverlaySettings règle l'affichage de l'incrustation du chat (modifiable depuis l'administration).
type OverlaySettings struct {
	MaxMessages int  `json:"max_messages"` // messages gardés à l'écran
	FontSize    int  `json:"font_size"`    // taille de police en pixels
	ShowBadges  bool `json:"show_badges"`  // afficher les badges à côté du pseudo
}

// DefaultOverlaySettings sert tant que rien n'a été enregistré.
func DefaultOverlaySettings() OverlaySettings {
	return OverlaySettings{MaxMessages: 40, FontSize: 22, ShowBadges: true}
}

// Normalize vérifie les bornes des réglages.
func (o *OverlaySettings) Normalize() error {
	if o.MaxMessages < 1 || o.MaxMessages > 100 {
		return errors.New("nombre de messages invalide (1 à 100)")
	}
	if o.FontSize < 12 || o.FontSize > 72 {
		return errors.New("taille de police invalide (12 à 72)")
	}
	return nil
}
