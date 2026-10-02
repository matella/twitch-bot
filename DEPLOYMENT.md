# Déploiement sur matelab (Docker Compose)

## Prérequis

- Docker avec le plugin Compose (`docker compose version`).
- Un compte Twitch pour le bot, et de préférence une **application Twitch** à toi (<https://dev.twitch.tv/console>) : le bot s'y connecte depuis l'administration et son jeton est renouvelé tout seul. Sans application, il faut coller un jeton OAuth fixe (scopes `chat:read` et `chat:edit`, p. ex. via twitchtokengenerator.com), qui expire. Un jeton donne accès au chat au nom du bot : ne le partage pas.
- (Pour la musique) un compte **Spotify Premium** et une application créée sur <https://developer.spotify.com/dashboard>.

## 1. Récupérer et configurer

```bash
git clone https://github.com/matella/twitch-bot.git
cd twitch-bot/deploy
cp .env.example .env
nano .env        # TWITCH_CHANNEL, TWITCH_CLIENT_ID/SECRET, ADMIN_PASSWORD, et SPOTIFY_* si besoin
chmod 600 .env
```

Le port publié sur matelab est **9190** (`HOST_PORT` dans `.env` pour le changer) ; le port interne du conteneur reste 9090.

## 2. Lancer

```bash
docker compose up -d --build
docker compose logs -f
```

Le build télécharge les dépendances Go (versions figées dans `go.sum`) : il faut un accès réseau.

Logs attendus : `interface d'administration`. Avec une application Twitch, le bot attend ensuite que tu connectes son compte (étape 3 bis) ; avec un jeton fixe, tu verras `connecté à Twitch`.

Le bot ne s'arrête jamais sur une erreur Twitch : il réessaie (5 s, puis jusqu'à 2 min) et l'administration affiche la raison dans la pastille « Bot ». Si elle indique `login authentication failed`, le jeton ou le nom du bot est incorrect.

## 3. Accéder à l'administration

Par défaut le port n'est publié que sur `127.0.0.1` de matelab, parce que l'authentification Basic circule en clair en HTTP. Deux façons d'y accéder :

**Tunnel SSH (le plus simple)** — depuis ton PC :

```bash
ssh -L 9090:127.0.0.1:9190 ton_user@matelab
```

puis ouvre <http://127.0.0.1:9090> (identifiant `ADMIN_USER`, mot de passe `ADMIN_PASSWORD`). Si tu as changé `HOST_PORT`, utilise ce port côté matelab (`-L 9090:127.0.0.1:<HOST_PORT>`).

**Reverse proxy HTTPS (Nginx Proxy Manager)** — voir la section suivante. Tu peux aussi mettre `BIND_ADDR=0.0.0.0` pour écouter sur le réseau local, mais seulement si tu acceptes le mot de passe en clair sur ce réseau.

### Nginx Proxy Manager

1. **Joindre le bot depuis NPM.** `127.0.0.1` vu depuis le conteneur de NPM est NPM lui-même, donc le port publié en local ne lui sert à rien. Le plus propre : rattacher le bot au réseau Docker de NPM (nom visible avec `docker network ls`) :

   ```bash
   # dans .env, si le réseau ne s'appelle pas « npm_default » :  NPM_NETWORK=nom_du_reseau
   docker compose -f docker-compose.yml -f docker-compose.npm.yml up -d --build
   ```

   Dans NPM, le bot est alors joignable à l'hôte `twitch-bot`, port `9090`. (Si NPM n'est pas dans Docker : cible `127.0.0.1` et le port `9190`.)

2. **Proxy Host** dans NPM : *Domain Names* `ton-domaine`, *Scheme* `http`, *Forward Hostname* `twitch-bot`, *Forward Port* `9090`. Pas besoin de *Websockets Support*. Onglet *SSL* : certificat Let's Encrypt, *Force SSL* activé. NPM conserve l'en-tête `Host` d'origine, ce que le bot exige pour sa protection CSRF.

3. **Dans `.env`** (puis `docker compose ... up -d`) :

   ```
   TRUST_PROXY=true
   TWITCH_REDIRECT_URL=https://ton-domaine/auth/twitch/callback
   SPOTIFY_REDIRECT_URL=https://ton-domaine/auth/spotify/callback
   ```

   `TRUST_PROXY=true` fait croire `X-Forwarded-Proto` (cookie « Secure ») et `X-Forwarded-For` (blocage des tentatives de connexion par client). N'active-le que parce que le bot n'est joignable que via NPM : ne laisse pas `BIND_ADDR=0.0.0.0` en même temps.

4. Déclare ces deux URL de redirection dans la console Twitch et dans le dashboard Spotify (sections 3 bis et 4). L'authentification Basic reste active : l'accès est protégé par `ADMIN_PASSWORD` en plus du HTTPS ; une *Access List* NPM peut ajouter une couche (restriction par IP).

## 3 bis. Connecter le compte Twitch du bot (application Twitch)

1. Dans la console Twitch, ouvre ton application → *OAuth Redirect URLs* et ajoute exactement `https://ton-domaine/auth/twitch/callback` **et** mets la même valeur dans `TWITCH_REDIRECT_URL` (voir Nginx Proxy Manager ci-dessus). Sans reverse proxy, avec le tunnel SSH : `http://localhost:9090/auth/twitch/callback` (Twitch accepte `localhost` en HTTP, pas `127.0.0.1`), et ouvre l'administration via `http://localhost:9090`.
2. Renseigne `TWITCH_CLIENT_ID` et `TWITCH_CLIENT_SECRET` dans `.env`, puis `docker compose up -d`.
3. Dans l'administration, clique sur **Connecter** à côté de « Twitch », et connecte-toi avec le **compte du bot** (pas le tien ; Twitch redemande le compte à chaque fois). Le jeton est conservé en base et renouvelé automatiquement : à ne faire qu'une fois, sauf si tu révoques l'accès.

## 4. Connecter Spotify

1. Dans le dashboard Spotify, ouvre ton application → *Settings* → *Redirect URIs* et ajoute exactement :
   `http://127.0.0.1:9090/auth/spotify/callback`
   (adapte le port si besoin). Spotify n'accepte plus `localhost` ni le HTTP simple, **sauf** l'adresse de boucle `http://127.0.0.1` : c'est pour cela que le tunnel SSH fonctionne tel quel. Avec un reverse proxy HTTPS, déclare plutôt `https://ton-domaine/auth/spotify/callback` **et** mets la même valeur dans `SPOTIFY_REDIRECT_URL`.
2. Renseigne `SPOTIFY_ID` et `SPOTIFY_SECRET` dans `.env`, puis `docker compose up -d`.
3. Dans l'administration, clique sur **Connecter** à côté de « Spotify » et accepte. Le jeton est conservé en base et renouvelé automatiquement : à ne faire qu'une fois.
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

Cette copie contient les jetons Twitch et Spotify en clair : garde-la en lieu sûr.

### Dépendances

Les versions des bibliothèques Go sont figées dans `go.mod` / `go.sum`. La CI GitHub (`.github/workflows/ci.yml`) vérifie qu'ils sont à jour (`go mod tidy` ne doit rien changer), compile, teste, puis construit l'image Docker. Pour mettre à jour une dépendance : `go get <module>@latest && make tidy`, puis commiter `go.mod` et `go.sum`.

### Mise à jour de la base

Le schéma de la base est versionné (`PRAGMA user_version`) et migré automatiquement au démarrage. Un programme plus ancien refuse d'ouvrir une base plus récente : sauvegarde avant de revenir en arrière.

## Dépannage

| Symptôme | Piste |
| --- | --- |
| `variables d'environnement manquantes` | Compléter `.env`, puis `docker compose up -d`. |
| `requires go >= 1.xx` pendant le build | `git pull` (l'image de build est en Go 1.26 et le Dockerfile active le téléchargement automatique du bon compilateur), puis `docker compose build --no-cache`. |
| `bind: address already in use` | Changer `HOST_PORT` dans `.env`. |
| `login authentication failed` | Jeton fixe invalide ou sans les scopes `chat:read` / `chat:edit`, ou `TWITCH_USERNAME` qui ne correspond pas au compte du jeton. Avec une application Twitch : reconnecte le compte du bot depuis l'administration. |
| « Twitch : non connecté » alors que le compte était connecté | Twitch a révoqué l'accès (mot de passe changé, application déconnectée) : clique sur **Connecter**. |
| Twitch refuse la redirection | L'URL déclarée dans la console Twitch n'est pas identique à `TWITCH_REDIRECT_URL` (par défaut `http://localhost:<port>/auth/twitch/callback`). |
| `!queue` / `!np` répondent « Erreur Spotify » | Jeton Spotify ancien sans le droit `user-read-currently-playing` : déconnecte puis reconnecte Spotify. |
| « trop de tentatives » (429) dans l'administration | 10 mots de passe faux : attends 10 minutes. |
| `INVALID_CLIENT: Invalid redirect URI` chez Spotify | L'URI déclarée dans le dashboard n'est pas identique, au caractère près, à `SPOTIFY_REDIRECT_URL`. |
| « aucun lecteur Spotify actif » dans le chat | Ouvrir Spotify sur un appareil et lancer une lecture. |
| L'administration redemande le mot de passe en boucle | Vérifier `ADMIN_USER` / `ADMIN_PASSWORD` dans `.env` (puis recréer le conteneur). |
