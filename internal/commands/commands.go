// Package commands regroupe la logique pure des commandes de chat :
// parsing, validation, rendu des variables et nettoyage des messages sortants.
package commands

import (
	"errors"
	"fmt"
	"math/rand/v2"
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/matella/twitch-bot/internal/model"
)

const (
	MaxResponseRunes       = 400
	MaxCooldownSeconds     = 3600
	DefaultCooldownSeconds = 5
	MaxAliases             = 10
	// MaxMessageRunes est la limite d'un message Twitch.
	MaxMessageRunes = 500
)

var nameRE = regexp.MustCompile(`^[\p{L}\p{N}_]{1,32}$`)

// Commandes intégrées : une commande personnalisée ne peut pas les masquer.
var reserved = map[string]struct{}{
	"song": {}, "sr": {}, "queue": {}, "help": {}, "skip": {}, "np": {}, "currentsong": {},
	"addcmd": {}, "editcmd": {}, "delcmd": {},
}

// builtinsExceptRequest liste les intégrées que la commande de demande de musique ne
// peut pas s'approprier. « song » et « sr » en sont volontairement absents : ce sont ses
// valeurs par défaut, et les reprendre doit rester permis.
var builtinsExceptRequest = map[string]struct{}{
	"queue": {}, "help": {}, "skip": {}, "np": {}, "currentsong": {},
	"addcmd": {}, "editcmd": {}, "delcmd": {},
}

// validateRequestName valide un nom destiné à la commande de demande de musique.
func validateRequestName(name string) error {
	if !nameRE.MatchString(name) {
		return errors.New("nom invalide : 1 à 32 lettres, chiffres ou _")
	}
	if _, bad := builtinsExceptRequest[name]; bad {
		return fmt.Errorf("!%s est une autre commande intégrée", name)
	}
	return nil
}

// NormalizeRequestCommand normalise et valide le nom et les alias de la commande de
// demande de musique. Un nom vide retombe sur « song ». Les alias sont uniques et
// distincts du nom.
func NormalizeRequestCommand(name string, aliases []string) (string, []string, error) {
	name = NormalizeName(name)
	if name == "" {
		name = "song"
	}
	if err := validateRequestName(name); err != nil {
		return "", nil, err
	}
	seen := map[string]bool{name: true}
	out := make([]string, 0, len(aliases))
	for _, a := range aliases {
		a = NormalizeName(a)
		if a == "" {
			continue
		}
		if err := validateRequestName(a); err != nil {
			return "", nil, fmt.Errorf("alias %q : %w", a, err)
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	if len(out) > MaxAliases {
		return "", nil, fmt.Errorf("trop d'alias (%d maximum)", MaxAliases)
	}
	return name, out, nil
}

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

// NormalizeAliases normalise et valide les alias d'une commande : valides, uniques, distincts du nom.
func NormalizeAliases(name string, aliases []string) ([]string, error) {
	out := make([]string, 0, len(aliases))
	seen := map[string]bool{name: true}
	for _, a := range aliases {
		a = NormalizeName(a)
		if a == "" {
			continue
		}
		if err := ValidateName(a); err != nil {
			return nil, fmt.Errorf("alias %q : %w", a, err)
		}
		if seen[a] {
			continue
		}
		seen[a] = true
		out = append(out, a)
	}
	if len(out) > MaxAliases {
		return nil, fmt.Errorf("trop d'alias (%d maximum)", MaxAliases)
	}
	return out, nil
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

// New construit une commande valide avec les réglages par défaut (tout le monde, activée).
func New(name, response string) (model.Command, error) {
	name = NormalizeName(name)
	if err := ValidateName(name); err != nil {
		return model.Command{}, err
	}
	if err := ValidateResponse(response); err != nil {
		return model.Command{}, err
	}
	return model.Command{
		Name:            name,
		Response:        strings.TrimSpace(response),
		CooldownSeconds: DefaultCooldownSeconds,
		Permission:      model.LevelEveryone,
		Enabled:         true,
		Aliases:         []string{},
	}, nil
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

// Vars contient les valeurs substituées dans la réponse d'une commande.
type Vars struct {
	User    string
	Channel string
	Args    string
	Count   int64
	Intn    func(n int) int // tirage dans [0, n) ; injectable pour les tests
}

var varRE = regexp.MustCompile(`\{(user|channel|args|touser|count|random|pick:[^{}]*)\}`)

// Render remplace les variables du modèle :
//
//	{user} auteur · {channel} chaîne · {args} texte après la commande
//	{touser} premier mot de {args} sans « @ » (sinon l'auteur)
//	{count} nombre d'utilisations · {random} nombre de 1 à 100
//	{pick:a|b|c} une des options au hasard
//
// Le remplacement se fait en une seule passe : le contenu de {args}
// (fourni par un spectateur) n'est jamais réinterprété.
func Render(tmpl string, v Vars) string {
	if v.Intn == nil {
		v.Intn = rand.IntN
	}
	args := safeArgs(v.Args)
	return varRE.ReplaceAllStringFunc(tmpl, func(m string) string {
		key := m[1 : len(m)-1]
		switch {
		case key == "user":
			return v.User
		case key == "channel":
			return v.Channel
		case key == "args":
			return args
		case key == "touser":
			if f := strings.Fields(args); len(f) > 0 {
				if t := strings.TrimLeft(f[0], "@"); t != "" {
					return t
				}
			}
			return v.User
		case key == "count":
			return strconv.FormatInt(v.Count, 10)
		case key == "random":
			return strconv.Itoa(v.Intn(100) + 1)
		default: // pick:
			opts := strings.Split(strings.TrimPrefix(key, "pick:"), "|")
			return strings.TrimSpace(opts[v.Intn(len(opts))])
		}
	})
}

// safeArgs neutralise ce qu'un spectateur pourrait faire relayer par le bot : un « ! » initial
// déclencherait les autres bots, un « / » ou « . » une commande Twitch.
func safeArgs(args string) string {
	f := strings.Fields(args)
	for i, w := range f {
		f[i] = strings.TrimLeft(w, "!/.")
	}
	return strings.Join(f, " ")
}

// UsesArgs indique si la réponse relaie du texte saisi par les spectateurs.
func UsesArgs(response string) bool {
	return strings.Contains(response, "{args}")
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
