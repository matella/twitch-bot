# twitch-bot

Bot Twitch maison (dans l'esprit de Nightbot / StreamElements) pour tourner sur ton homelab :

- **commandes personnalisées** (`!discord`, `!lurk`…) avec variables `{user}`, `{channel}`, `{args}` et délai par commande ;
- **demandes de musique Spotify** : `!song <titre ou lien Spotify>` ajoute le titre à la **vraie file de lecture** de ton compte ;
- **site d'administration** (protégé par mot de passe) pour gérer les commandes, voir l'historique des demandes et connecter Spotify.

Un seul binaire Go, un seul processus, SQLite pour le stockage, interface web embarquée dans le binaire.

## Commandes du chat

| Commande | Effet |
| --- | --- |
| `!song <titre>` ou `!sr <titre>` | Cherche le titre (ou lit le lien/URI Spotify) et l'ajoute à la file Spotify. Un délai s'applique par spectateur (30 s par défaut). |
| `!queue` | Affiche les dernières demandes enregistrées par le bot. |
| `!help` | Liste les commandes disponibles, personnalisées comprises. |
| `!<nom>` | Répond avec le texte configuré dans l'administration. |

Les messages du bot sont en français ; ils sont regroupés dans `internal/chat/handler.go`.

## Configuration

Tout passe par des variables d'environnement (jamais par la ligne de commande, pour ne pas exposer les secrets). Modèle complet : [`deploy/.env.example`](deploy/.env.example).

| Variable | Obligatoire | Description |
| --- | --- | --- |
| `TWITCH_CHANNEL` | oui | Chaîne à rejoindre (sans `#`). |
| `TWITCH_USERNAME` | oui | Compte Twitch du bot. |
| `TWITCH_TOKEN` | oui | Jeton OAuth du compte du bot (`chat:read`, `chat:edit`). |
| `ADMIN_PASSWORD` | oui | Mot de passe de l'administration (8 caractères minimum). |
| `ADMIN_USER` | non | Identifiant admin (défaut : `admin`). |
| `SPOTIFY_ID`, `SPOTIFY_SECRET` | non | Application Spotify. Sans elles, les demandes de musique sont désactivées. |
| `SPOTIFY_REDIRECT_URL` | non | Défaut : `http://127.0.0.1:<PORT>/auth/spotify/callback`. |
| `SONG_COOLDOWN_SECONDS` | non | Délai entre deux demandes d'un même spectateur (défaut : 30). |
| `PORT` | non | Port d'écoute du binaire (défaut : 9090). |
| `DB_PATH` | non | Fichier SQLite (défaut : `bot.db`). |

## Démarrage

Déploiement sur le homelab avec Docker Compose : voir [DEPLOYMENT.md](DEPLOYMENT.md).

En local, avec Go installé :

```bash
cp deploy/.env.example deploy/.env   # puis remplir
make tidy                            # résout les dépendances (écrit go.mod / go.sum)
make run
```

## Sécurité

- Toute l'administration exige une authentification HTTP Basic, sauf `/api/health` (qui ne renvoie que `ok`).
- HTTP Basic envoie le mot de passe en clair : n'expose le port que sur `127.0.0.1` (tunnel SSH) ou derrière un reverse proxy HTTPS.
- Les requêtes modifiantes venant d'un autre site sont refusées (en-têtes `Sec-Fetch-Site` / `Origin`, `Content-Type: application/json` obligatoire).
- Les messages envoyés au chat sont nettoyés : un `/` ou `.` initial est retiré, pour qu'un spectateur ne puisse pas faire exécuter `/ban` ou `/timeout` au bot s'il est modérateur.
- Le jeton Spotify (refresh token) est stocké **en clair** dans la base SQLite : protège le volume `/data` et ses sauvegardes.

## Limites connues

- Le délai d'une commande personnalisée est **global** (pas par spectateur) ; celui de `!song` est par spectateur.
- `!queue` montre l'historique des demandes passées par le bot, pas la file Spotify réelle.
- Ajouter à la file Spotify exige un compte **Spotify Premium** et un lecteur Spotify actif (appli ouverte sur un appareil).
- L'indicateur « connecté » de l'administration reflète la dernière connexion réussie à Twitch ; la bibliothèque se reconnecte seule en cas de coupure.
- Pas encore de restriction par rôle (abonnés, modérateurs) ni de file d'attente avec vote/skip.

## Développement

```bash
go test ./...   # nécessite l'accès aux modules Go (proxy.golang.org)
go vet ./...
```

Organisation du code :

```
cmd/twitch-bot/     point d'entrée (+ sous-commande « healthcheck »)
internal/config/    lecture et validation des variables d'environnement
internal/commands/  parsing, validation, variables, nettoyage des messages (logique pure)
internal/chat/      logique du bot, indépendante de Twitch et de Spotify (logique pure)
internal/server/    API d'administration + authentification (bibliothèque standard uniquement)
internal/store/     SQLite (driver Go pur, sans CGO)
internal/spotify/   OAuth Spotify, recherche, file de lecture
internal/bot/       client Twitch IRC
web/static/         interface d'administration (embarquée dans le binaire)
```

Les paquets « logique pure » et `server` se testent sans dépendance externe.
