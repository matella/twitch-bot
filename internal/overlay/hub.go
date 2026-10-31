// Package overlay diffuse le chat Twitch vers une page d'incrustation (source navigateur d'OBS).
package overlay

import (
	"regexp"
	"sort"
	"strconv"
	"sync"
)

// Message est un message du chat tel qu'affiché par l'incrustation.
type Message struct {
	Login  string   `json:"login"`
	Name   string   `json:"name"`   // pseudo affiché (casse d'origine)
	Color  string   `json:"color"`  // couleur « #rrggbb » du pseudo, ou vide
	Badges []string `json:"badges"` // « set/version », ex. « subscriber/12 »
	Text   string   `json:"text"`
}

// subBuffer : nombre de messages en attente par spectateur de l'incrustation avant de les perdre.
const subBuffer = 64

var colorRe = regexp.MustCompile(`^#[0-9a-fA-F]{6}$`)

// SafeColor renvoie la couleur si elle a la forme « #rrggbb », sinon une chaîne vide.
func SafeColor(c string) string {
	if colorRe.MatchString(c) {
		return c
	}
	return ""
}

// BadgeKeys renvoie les badges d'un message sous la forme « set/version », triés.
func BadgeKeys(badges map[string]int) []string {
	keys := make([]string, 0, len(badges))
	for set, version := range badges {
		keys = append(keys, set+"/"+strconv.Itoa(version))
	}
	sort.Strings(keys)
	return keys
}

// Hub relaie chaque message à tous les abonnés. Publish ne bloque jamais :
// un abonné trop lent perd des messages plutôt que de ralentir le bot.
type Hub struct {
	mu   sync.Mutex
	subs map[chan Message]struct{}
}

// NewHub crée un hub sans abonné.
func NewHub() *Hub {
	return &Hub{subs: map[chan Message]struct{}{}}
}

// Publish envoie m à tous les abonnés.
func (h *Hub) Publish(m Message) {
	h.mu.Lock()
	defer h.mu.Unlock()
	for ch := range h.subs {
		select {
		case ch <- m:
		default:
		}
	}
}

// Subscribe renvoie le canal des messages et la fonction qui met fin à l'abonnement.
func (h *Hub) Subscribe() (<-chan Message, func()) {
	ch := make(chan Message, subBuffer)
	h.mu.Lock()
	h.subs[ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		delete(h.subs, ch)
		h.mu.Unlock()
	}
}
