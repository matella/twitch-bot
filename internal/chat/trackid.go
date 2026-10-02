package chat

import "regexp"

var trackRE = regexp.MustCompile(`(?:open\.spotify\.com/(?:intl-[a-z]{2}/)?track/|spotify:track:)([A-Za-z0-9]{22})`)

// ParseTrackID extrait l'identifiant d'un titre depuis un lien open.spotify.com
// ou une URI spotify:track:... Renvoie ok=false si la requête est une recherche texte.
func ParseTrackID(query string) (id string, ok bool) {
	m := trackRE.FindStringSubmatch(query)
	if m == nil {
		return "", false
	}
	return m[1], true
}
