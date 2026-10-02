// Package model contient les types partagés entre les couches (sans dépendance externe).
package model

import "errors"

var (
	// ErrNotFound est renvoyé quand un élément demandé n'existe pas.
	ErrNotFound = errors.New("introuvable")
	// ErrExists est renvoyé quand on crée un élément qui existe déjà.
	ErrExists = errors.New("existe déjà")
)

// Statuts d'une demande de musique.
const (
	StatusQueued = "queued"
	StatusFailed = "failed"
)

// Command est une commande de chat personnalisée (!nom -> réponse).
type Command struct {
	Name            string `json:"name"`
	Response        string `json:"response"`
	CooldownSeconds int    `json:"cooldown_seconds"`
	UpdatedAt       int64  `json:"updated_at"`
}

// SongRequest est l'historique d'une demande de musique faite depuis le chat.
type SongRequest struct {
	ID          int64  `json:"id"`
	TrackID     string `json:"track_id"`
	TrackName   string `json:"track_name"`
	Artist      string `json:"artist"`
	RequestedBy string `json:"requested_by"`
	Status      string `json:"status"`
	CreatedAt   int64  `json:"created_at"`
}
