// Package model contient les types partagés entre les couches (sans dépendance externe).
package model

import (
	"errors"
	"fmt"
	"strings"
)

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

// Level est le rôle d'un spectateur. Les niveaux sont hiérarchiques :
// un modérateur peut tout ce que peut un VIP, qui peut tout ce que peut un abonné.
type Level int

const (
	LevelEveryone Level = iota
	LevelSubscriber
	LevelVIP
	LevelModerator
	LevelBroadcaster
)

var levelNames = [...]string{"everyone", "subscriber", "vip", "moderator", "broadcaster"}

// String renvoie le nom technique du niveau (celui de l'API et de la base).
func (l Level) String() string {
	if l < 0 || int(l) >= len(levelNames) {
		return levelNames[0]
	}
	return levelNames[l]
}

// Label renvoie le nom lisible, utilisé dans les messages du chat.
func (l Level) Label() string {
	switch l {
	case LevelSubscriber:
		return "abonnés"
	case LevelVIP:
		return "VIP"
	case LevelModerator:
		return "modérateurs"
	case LevelBroadcaster:
		return "streamer"
	}
	return "tout le monde"
}

// ParseLevel convertit un nom technique ; la chaîne vide donne LevelEveryone.
func ParseLevel(s string) (Level, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return LevelEveryone, nil
	}
	for i, n := range levelNames {
		if n == s {
			return Level(i), nil
		}
	}
	return 0, fmt.Errorf("rôle inconnu %q (everyone, subscriber, vip, moderator, broadcaster)", s)
}

func (l Level) MarshalText() ([]byte, error) { return []byte(l.String()), nil }

func (l *Level) UnmarshalText(b []byte) error {
	v, err := ParseLevel(string(b))
	if err != nil {
		return err
	}
	*l = v
	return nil
}

// Command est une commande de chat personnalisée (!nom -> réponse).
type Command struct {
	Name                string   `json:"name"`
	Response            string   `json:"response"`
	CooldownSeconds     int      `json:"cooldown_seconds"`      // délai global
	UserCooldownSeconds int      `json:"user_cooldown_seconds"` // délai par spectateur
	Permission          Level    `json:"permission"`
	Enabled             bool     `json:"enabled"`
	Aliases             []string `json:"aliases"`
	UseCount            int64    `json:"use_count"`
	UpdatedAt           int64    `json:"updated_at"`
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

// MusicSettings regroupe les règles des demandes de musique, modifiables depuis l'administration.
// Pour les limites numériques, 0 signifie « pas de limite ».
type MusicSettings struct {
	RequestLevel       Level    `json:"request_level"`        // qui peut utiliser !song
	SkipLevel          Level    `json:"skip_level"`           // qui peut utiliser !skip
	MaxPending         int      `json:"max_pending"`          // demandes du bot en attente dans la file
	MaxPerUser         int      `json:"max_per_user"`         // demandes en attente par spectateur
	MaxDurationSeconds int      `json:"max_duration_seconds"` // durée maximale d'un titre
	BlockExplicit      bool     `json:"block_explicit"`
	Blocklist          []string `json:"blocklist"` // fragments d'artistes ou de titres refusés
}

// Bornes de MusicSettings.
const (
	MaxMusicLimit    = 10000
	MaxBlocklistSize = 200
	MaxBlockTermLen  = 100
)

// DefaultMusicSettings renvoie les réglages appliqués tant que rien n'a été enregistré.
func DefaultMusicSettings() MusicSettings {
	return MusicSettings{
		RequestLevel:       LevelEveryone,
		SkipLevel:          LevelModerator,
		MaxPending:         20,
		MaxPerUser:         3,
		MaxDurationSeconds: 600,
		Blocklist:          []string{},
	}
}

// Normalize nettoie la liste de blocage (minuscules, sans doublons ni vides) et la valide.
func (s *MusicSettings) Normalize() error {
	for _, n := range []struct {
		name string
		v    int
	}{
		{"max_pending", s.MaxPending}, {"max_per_user", s.MaxPerUser}, {"max_duration_seconds", s.MaxDurationSeconds},
	} {
		if n.v < 0 || n.v > MaxMusicLimit {
			return fmt.Errorf("%s invalide (0 à %d)", n.name, MaxMusicLimit)
		}
	}
	seen := map[string]bool{}
	out := make([]string, 0, len(s.Blocklist))
	for _, t := range s.Blocklist {
		t = strings.ToLower(strings.TrimSpace(t))
		if t == "" || seen[t] {
			continue
		}
		if len([]rune(t)) > MaxBlockTermLen {
			return fmt.Errorf("terme de la liste de blocage trop long (%d caractères max)", MaxBlockTermLen)
		}
		seen[t] = true
		out = append(out, t)
	}
	if len(out) > MaxBlocklistSize {
		return fmt.Errorf("liste de blocage trop longue (%d termes max)", MaxBlocklistSize)
	}
	s.Blocklist = out
	return nil
}
