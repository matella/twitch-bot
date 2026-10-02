// Package commands regroupe la logique pure des commandes de chat :
// parsing, validation, rendu des variables et nettoyage des messages sortants.
package commands

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	MaxResponseRunes       = 400
	MaxCooldownSeconds     = 3600
	DefaultCooldownSeconds = 5
	// MaxMessageRunes est la limite d'un message Twitch.
	MaxMessageRunes = 500
)

var nameRE = regexp.MustCompile(`^[\p{L}\p{N}_]{1,32}$`)

// Commandes intégrées : une commande personnalisée ne peut pas les masquer.
var reserved = map[string]struct{}{"song": {}, "sr": {}, "queue": {}, "help": {}}

// IsReserved indique si le nom est une commande intégrée.
func IsReserved(name string) bool {
	_, ok := reserved[name]
	return ok
}

// NormalizeName retire le "!" éventuel, les espaces, et met en minuscules.
func NormalizeName(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "!")
	return strings.ToLower(strings.TrimSpace(s))
}

// ValidateName valide un nom déjà normalisé.
func ValidateName(name string) error {
	if !nameRE.MatchString(name) {
		return errors.New("nom invalide : 1 à 32 lettres, chiffres ou _")
	}
	if IsReserved(name) {
		return fmt.Errorf("!%s est une commande intégrée", name)
	}
	return nil
}

// ValidateResponse valide le texte de réponse.
func ValidateResponse(r string) error {
	n := utf8.RuneCountInString(strings.TrimSpace(r))
	if n == 0 {
		return errors.New("la réponse est vide")
	}
	if n > MaxResponseRunes {
		return fmt.Errorf("réponse trop longue (%d caractères max)", MaxResponseRunes)
	}
	return nil
}

// ValidateCooldown valide un délai en secondes.
func ValidateCooldown(c int) error {
	if c < 0 || c > MaxCooldownSeconds {
		return fmt.Errorf("délai invalide (0 à %d secondes)", MaxCooldownSeconds)
	}
	return nil
}

// Parse décompose une ligne de chat "!nom arguments".
// ok est faux si la ligne n'est pas une commande.
func Parse(line string) (name, args string, ok bool) {
	line = strings.TrimSpace(line)
	if !strings.HasPrefix(line, "!") {
		return "", "", false
	}
	rest := line[1:]
	if i := strings.IndexFunc(rest, unicode.IsSpace); i < 0 {
		name = rest
	} else {
		name, args = rest[:i], strings.TrimSpace(rest[i:])
	}
	name = strings.ToLower(name)
	if name == "" {
		return "", "", false
	}
	return name, args, true
}

// Render remplace {user}, {channel} et {args} dans le modèle.
// Le remplacement se fait en une seule passe : le contenu de {args}
// (fourni par un spectateur) n'est jamais réinterprété.
func Render(tmpl, user, channel, args string) string {
	return strings.NewReplacer("{user}", user, "{channel}", channel, "{args}", args).Replace(tmpl)
}

// SanitizeOutgoing prépare un message à envoyer sur Twitch :
//   - supprime les caractères de contrôle (sauts de ligne, etc.) ;
//   - retire les "/" et "." initiaux, que Twitch interprète comme des commandes
//     (/ban, /timeout...) si le bot est modérateur ;
//   - tronque à la limite de 500 caractères.
func SanitizeOutgoing(s string) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return ' '
		}
		return r
	}, s)
	s = strings.TrimSpace(s)
	s = strings.TrimLeft(s, "/. ")
	if utf8.RuneCountInString(s) > MaxMessageRunes {
		r := []rune(s)
		s = string(r[:MaxMessageRunes-1]) + "…"
	}
	return s
}
