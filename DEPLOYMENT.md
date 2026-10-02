# Déploiement sur matelab (Docker Compose)

## Prérequis

- Docker avec le plugin Compose (`docker compose version`).
- Un compte Twitch pour le bot, avec un jeton OAuth (scopes `chat:read` et `chat:edit`). Un générateur tiers comme twitchtokengenerator.com fait l'affaire ; une application Twitch à toi est plus propre. Ce jeton donne accès au chat au nom du bot : ne le partage pas.
- (Pour la musique) un compte **Spotify Premium** et une application créée sur <https://developer.spotify.com/dashboard>.

## 1. Récupérer et configurer

```bash
git clone https://github.com/matella/twitch-bot.git
cd twitch-bot/deploy
cp .env.example .env
nano .env        # TWITCH_*, ADMIN_PASSWORD, et SPOTIFY_* si besoin
chmod 600 .env
```

Si le port 9090 est déjà pris sur matelab, décommente `HOST_PORT` dans `.env` et choisis-en un autre.

## 2. Lancer

```bash
docker compose up -d --build
docker compose logs -f
```

Le build télécharge les dépendances Go : il faut un accès réseau. Si `go.sum` est absent du dépôt, `go mod tidy` les résout pendant le build (voir « Dépendances » plus bas).

Logs attendus : `interface d'administration`, puis `connecté à Twitch`. Si le bot s'arrête avec `login authentication failed`, le jeton ou le nom du bot est incorrect.

## 3. Accéder à l'administration

Par défaut le port n'est publié que sur `127.0.0.1` de matelab, parce que l'authentification Basic circule en clair en HTTP. Deux façons d'y accéder :

**Tunnel SSH (le plus simple)** — depuis ton PC :

```bash
ssh -L 9090:127.0.0.1:9090 ton_user@matelab
```

puis ouvre <http://127.0.0.1:9090> (identifiant `ADMIN_USER`, mot de passe `ADMIN_PASSWORD`). Si tu as changé `HOST_PORT`, utilise ce port côté matelab (`-L 9090:127.0.0.1:<HOST_PORT>`).

**Reverse proxy HTTPS** — si matelab en a déjà un, fais-le pointer vers `127.0.0.1:<HOST_PORT>` (il doit conserver l'en-tête `Host` d'origine). Tu peux aussi mettre `BIND_ADDR=0.0.0.0` pour écouter sur le réseau local, mais seulement si tu acceptes le mot de passe en clair sur ce réseau.

## 4. Connecter Spotify

1. Dans le dashboard Spotify, ouvre ton application → *Settings* → *Redirect URIs* et ajoute exactement :
   `http://127.0.0.1:9090/auth/spotify/callback`
   (adapte le port si besoin). Spotify n'accepte plus `localhost` ni le HTTP simple, **sauf** l'adresse de boucle `http://127.0.0.1` : c'est pour cela que le tunnel SSH fonctionne tel quel. Avec un reverse proxy HTTPS, déclare plutôt `https://ton-domaine/auth/spotify/callback` **et** mets la même valeur dans `SPOTIFY_REDIRECT_URL`.
2. Renseigne `SPOTIFY_ID` et `SPOTIFY_SECRET` dans `.env`, puis `docker compose up -d`.
3. Dans l'administration, clique sur **Connecter Spotify** et accepte. Le jeton est conservé en base et renouvelé automatiquement : à ne faire qu'une fois.
4. Ouvre Spotify sur un appareil (un lecteur actif est nécessaire pour ajouter à la file), puis teste `!song daft punk one more time` dans le chat.

## Exploitation

| Action | Commande (dans `deploy/`) |
| --- | --- |
| Logs | `docker compose logs -f` |
| État / santé | `docker compose ps` |
| Redémarrer | `docker compose restart` |
| Mettre à jour | `git pull && docker compose up -d --build` |
| Arrêter | `docker compose down` (les données restent dans le volume) |

### Sauvegarde

La base (commandes, historique, jeton Spotify) est dans le volume `/data`. Arrête le conteneur pour copier un fichier cohérent :

```bash
docker compose stop
docker compose cp twitch-bot:/data/bot.db ./bot-$(date +%F).db
docker compose start
```

Cette copie contient le jeton Spotify en clair : garde-la en lieu sûr.

### Dépendances

Les versions des bibliothèques Go sont figées dans `go.mod` / `go.sum`. La CI GitHub (`.github/workflows/ci.yml`) les résout, compile, teste, puis les commite elle-même (`chore: fige go.mod et go.sum`) si elles ont changé. Pense à faire un `git pull` avant de rebuilder pour les récupérer.

## Dépannage

| Symptôme | Piste |
| --- | --- |
| `variables d'environnement manquantes` | Compléter `.env`, puis `docker compose up -d`. |
| `requires go >= 1.xx` pendant le build | `git pull` (l'image de build est en Go 1.26 et le Dockerfile active le téléchargement automatique du bon compilateur), puis `docker compose build --no-cache`. |
| `bind: address already in use` | Changer `HOST_PORT` dans `.env`. |
| `login authentication failed` | Jeton Twitch invalide ou sans les scopes `chat:read` / `chat:edit`, ou `TWITCH_USERNAME` qui ne correspond pas au compte du jeton. |
| `INVALID_CLIENT: Invalid redirect URI` chez Spotify | L'URI déclarée dans le dashboard n'est pas identique, au caractère près, à `SPOTIFY_REDIRECT_URL`. |
| « aucun lecteur Spotify actif » dans le chat | Ouvrir Spotify sur un appareil et lancer une lecture. |
| L'administration redemande le mot de passe en boucle | Vérifier `ADMIN_USER` / `ADMIN_PASSWORD` dans `.env` (puis recréer le conteneur). |
