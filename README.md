# twitch-bot

Bot Twitch maison (dans l'esprit de Nightbot / StreamElements) pour tourner sur ton homelab :

- **commandes personnalisées** (`!discord`, `!lurk`…) : variables, alias, rôle requis (abonnés, VIP, modérateurs…), délais global et par spectateur, compteur d'utilisations, activation/désactivation ;
- **demandes de musique Spotify** : `!song <titre ou lien Spotify>` ajoute le titre à la **vraie file de lecture** de ton compte, avec des règles (plafonds, doublons, durée, contenu explicite, liste de blocage) ;
- **site d'administration** (protégé par mot de passe) pour tout régler, voir la file Spotify en direct, et connecter les comptes Twitch du bot et Spotify.

Un seul binaire Go, un seul processus, SQLite pour le stockage, interface web embarquée dans le binaire.

## Commandes du chat

| Commande | Qui | Effet |
| --- | --- | --- |
| `!song <titre>` / `!sr <titre>` | réglable (tout le monde par défaut) | Cherche le titre (ou lit le lien/URI Spotify) et l'ajoute à la file. Refusée si le titre joue déjà ou est déjà dans la file, ou si un plafond / une règle de l'administration l'interdit. Délai par spectateur (30 s par défaut). |
| `!queue` | tous | Les prochaines demandes **encore dans la file Spotify** (repli sur l'historique si Spotify ne répond pas). |
| `!np` / `!currentsong` | tous | Titre en cours de lecture. |
| `!skip` | réglable (modérateurs par défaut) | Passe au titre suivant. |
| `!help` | tous | Liste les commandes accessibles à l'auteur. |
| `!addcmd <nom> <réponse>` | modérateurs | Crée une commande. |
| `!editcmd <nom> <réponse>` | modérateurs | Change la réponse d'une commande. |
| `!delcmd <nom>` | modérateurs | Supprime une commande. |
| `!<nom>` | selon la commande | Répond avec le texte configuré. |

`!queue`, `!np` et `!help` ont un délai global de 8 s. Les rôles sont hiérarchiques : streamer > modérateur > VIP > abonné > tout le monde.

### Variables des commandes

`{user}` auteur · `{channel}` chaîne · `{args}` texte après la commande · `{touser}` premier mot de `{args}` sans `@` (sinon l'auteur) · `{count}` nombre d'utilisations · `{random}` nombre de 1 à 100 · `{pick:a|b|c}` une option au hasard.

Les messages du bot sont en français ; ils sont regroupés dans `internal/chat/handler.go`.

## Configuration

Tout passe par des variables d'environnement (jamais par la ligne de commande, pour ne pas exposer les secrets). Modèle complet : [`deploy/.env.example`](deploy/.env.example).

| Variable | Obligatoire | Description |
| --- | --- | --- |
| `TWITCH_CHANNEL` | oui | Chaîne à rejoindre (sans `#`). |
| `TWITCH_CLIENT_ID`, `TWITCH_CLIENT_SECRET` | recommandé | Application Twitch : le compte du bot se connecte depuis l'administration et son **jeton est renouvelé automatiquement**. |
| `TWITCH_REDIRECT_URL` | non | Défaut : `http://localhost:<PORT>/auth/twitch/callback`. |
| `TWITCH_USERNAME`, `TWITCH_TOKEN` | sans application Twitch | Compte et jeton OAuth fixes du bot (`chat:read`, `chat:edit`). Un tel jeton expire. |
| `ADMIN_PASSWORD` | oui | Mot de passe de l'administration (8 caractères minimum). |
| `ADMIN_USER` | non | Identifiant admin (défaut : `admin`). |
| `SPOTIFY_ID`, `SPOTIFY_SECRET` | non | Application Spotify. Sans elles, les demandes de musique sont désactivées. |
| `SPOTIFY_REDIRECT_URL` | non | Défaut : `http://127.0.0.1:<PORT>/auth/spotify/callback`. |
| `SONG_COOLDOWN_SECONDS` | non | Délai entre deux demandes d'un même spectateur (défaut : 30). |
| `TRUST_PROXY` | non | `true` derrière un reverse proxy HTTPS : croire `X-Forwarded-Proto` et `X-Forwarded-For`. |
| `PORT` | non | Port d'écoute du binaire (défaut : 9090). |
| `DB_PATH` | non | Fichier SQLite (défaut : `bot.db`). |

## Démarrage

Déploiement sur le homelab avec Docker Compose : voir [DEPLOYMENT.md](DEPLOYMENT.md).

En local, avec Go installé :

```bash
cp deploy/.env.example deploy/.env   # puis remplir
make run
```

## Sécurité

- Toute l'administration exige une authentification HTTP Basic, sauf `/api/health` (qui ne renvoie que `ok`). Après 10 échecs en 10 minutes, l'appelant est bloqué 10 minutes (clé : adresse IP, ou dernier `X-Forwarded-For` avec `TRUST_PROXY`).
- HTTP Basic envoie le mot de passe en clair : n'expose le port que sur `127.0.0.1` (tunnel SSH) ou derrière un reverse proxy HTTPS.
- Les requêtes modifiantes venant d'un autre site sont refusées (en-têtes `Sec-Fetch-Site` / `Origin`, `Content-Type: application/json` obligatoire).
- Les messages envoyés au chat sont nettoyés : un `/` ou `.` initial est retiré, pour qu'un spectateur ne puisse pas faire exécuter `/ban` ou `/timeout` au bot s'il est modérateur. Dans `{args}`, les `!`, `/` et `.` initiaux de chaque mot sont aussi retirés (le bot ne déclenche pas les autres bots). Une commande avec `{args}` ouverte à tout le monde laisse tout de même les spectateurs faire écrire du texte au bot : l'administration l'avertit.
- Le bot n'envoie jamais plus de 15 messages par 30 secondes (Twitch bride les comptes non modérateurs à 20).
- Les jetons Twitch et Spotify (refresh tokens) sont stockés **en clair** dans la base SQLite : protège le volume `/data` et ses sauvegardes.

## Limites connues

- Spotify ne permet pas de **retirer** un titre de la file : pas de `!wrongsong`. `!skip` passe le titre en cours.
- Le plafond de demandes ne compte que les titres mis en file par ce bot et encore visibles dans la file Spotify (Spotify renvoie une file tronquée d'une vingtaine de titres).
- Si l'état de lecture Spotify est illisible, les règles « doublons » et « plafonds » sont ignorées (les autres s'appliquent) plutôt que de bloquer toutes les demandes.
- Les jetons Spotify obtenus avant la version qui ajoute `!queue` / `!np` n'ont pas le droit `user-read-currently-playing` : reconnecte Spotify depuis l'administration.
- Ajouter à la file Spotify exige un compte **Spotify Premium** et un lecteur Spotify actif (appli ouverte sur un appareil).
- L'état « connecté » du bot se base sur les signes de vie de Twitch (PING toutes les 15 s) : il repasse hors ligne après 90 s de silence.
- Les modifications de commandes faites depuis l'administration ou le chat sont prises en compte immédiatement ; un changement direct dans la base peut mettre 3 s à apparaître.

## Développement

```bash
go test ./...   # nécessite l'accès aux modules Go (proxy.golang.org)
go vet ./...
```

Organisation du code :

```
cmd/twitch-bot/     point d'entrée (+ sous-commande « healthcheck »)
internal/config/    lecture et validation des variables d'environnement
internal/model/     types partagés (commandes, rôles, réglages de la musique)
internal/commands/  parsing, validation, variables, nettoyage des messages (logique pure)
internal/chat/      logique du bot, indépendante de Twitch et de Spotify (logique pure)
internal/server/    API d'administration + authentification (bibliothèque standard uniquement)
internal/store/     SQLite (driver Go pur, sans CGO)
internal/oauthx/    jeton OAuth en base (partagé par Spotify et Twitch)
internal/spotify/   OAuth Spotify, recherche, file de lecture, skip
internal/twitchauth/ OAuth Twitch du compte du bot, renouvellement du jeton
internal/bot/       client Twitch IRC (reconnexion automatique, rôles, limiteur d'envoi)
web/static/         interface d'administration (embarquée dans le binaire)
```

Les paquets « logique pure » et `server` se testent sans dépendance externe.
