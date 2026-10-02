// Package web embarque l'interface d'administration dans le binaire :
// plus de dépendance au répertoire de travail ni de fichiers à copier au déploiement.
package web

import (
	"embed"
	"io/fs"
)

//go:embed static
var staticFS embed.FS

// Static renvoie le contenu du dossier static/ à la racine.
func Static() fs.FS {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err) // impossible : le dossier est embarqué à la compilation
	}
	return sub
}
