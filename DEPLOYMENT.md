# Deployment Guide for matelab

This guide covers deploying the Twitch Bot to your matelab infrastructure.

## Prerequisites

- Ubuntu/Debian-based Linux system
- Docker & Docker Compose (recommended), OR Go 1.21+
- Root or sudo access
- Git installed

## Quick Deploy (Recommended - Docker)

### 1. SSH into matelab

```bash
ssh user@matelab
```

### 2. Clone the repository

```bash
git clone https://github.com/matella/twitch-bot.git
cd twitch-bot
```

### 3. Create configuration file

```bash
cp deploy/.env.example deploy/.env
nano deploy/.env
```

Edit the file with your credentials:

```env
TWITCH_CHANNEL=your_twitch_channel
TWITCH_USERNAME=your_bot_username
TWITCH_TOKEN=your_oauth_token

SPOTIFY_ID=your_spotify_client_id
SPOTIFY_SECRET=your_spotify_client_secret
```

### 4. Deploy with Docker Compose

```bash
cd deploy
docker-compose up -d
```

### 5. Verify it's running

```bash
docker-compose logs -f twitch-bot
```

Access the admin dashboard at: **http://matelab:9090**

---

## Manual Deploy (Systemd)

### 1. Prepare configuration

```bash
# Create environment file
sudo mkdir -p /opt/twitch-bot
sudo nano /opt/twitch-bot/.env
```

Add your credentials:
```env
TWITCH_CHANNEL=your_channel
TWITCH_USERNAME=your_bot
TWITCH_TOKEN=your_token
SPOTIFY_ID=your_id
SPOTIFY_SECRET=your_secret
```

### 2. Run automated deployment script

```bash
sudo bash deploy/deploy.sh
```

The script will:
- Create the `twitch-bot` system user
- Clone the latest code from GitHub
- Build the binary (using Go or Docker)
- Install the systemd service
- Start the service

### 3. Verify deployment

```bash
# Check service status
systemctl status twitch-bot

# View logs
journalctl -u twitch-bot -f

# Health check
curl http://localhost:9090/api/health
```

---

## Configuration Files

### Environment Variables (`.env`)

Located in the deploy directory or passed to Docker:

```env
# Twitch
TWITCH_CHANNEL=your_channel_name
TWITCH_USERNAME=your_bot_username
TWITCH_TOKEN=oauth:xxxxxxxxxxxx

# Spotify (optional)
SPOTIFY_ID=your_client_id
SPOTIFY_SECRET=your_client_secret
```

Get your credentials:
- **Twitch Token**: https://twitchtokengenerator.com/ (scopes: chat:read, chat:edit)
- **Spotify ID/Secret**: https://developer.spotify.com/dashboard → Create App

### Systemd Service File

Located at: `/etc/systemd/system/twitch-bot.service`

Edit with:
```bash
sudo systemctl edit twitch-bot
```

---

## Management Commands

### Using Docker Compose

```bash
# Start
docker-compose -f deploy/docker-compose.yml up -d

# Stop
docker-compose -f deploy/docker-compose.yml down

# View logs
docker-compose -f deploy/docker-compose.yml logs -f

# Restart
docker-compose -f deploy/docker-compose.yml restart

# Update to latest
docker-compose -f deploy/docker-compose.yml down
git pull
docker-compose -f deploy/docker-compose.yml up -d --build
```

### Using Systemd

```bash
# Start
sudo systemctl start twitch-bot

# Stop
sudo systemctl stop twitch-bot

# Restart
sudo systemctl restart twitch-bot

# View logs (latest 50 lines)
journalctl -u twitch-bot -n 50

# Follow logs (tail -f)
journalctl -u twitch-bot -f

# Service status
systemctl status twitch-bot

# Enable on boot
sudo systemctl enable twitch-bot
```

---

## Updating

### Docker Compose

```bash
cd deploy
docker-compose down
git pull origin main
docker-compose up -d --build
```

### Systemd

```bash
cd /opt/twitch-bot
sudo systemctl stop twitch-bot
git pull origin main
go build -o twitch-bot ./cmd
sudo systemctl start twitch-bot
```

Or use the deployment script:
```bash
sudo bash deploy/deploy.sh
```

---

## Troubleshooting

### Service won't start

Check logs:
```bash
journalctl -u twitch-bot -n 100
```

### Port already in use

Change the port:
```bash
# Systemd
sudo systemctl edit twitch-bot
# Add or modify: ExecStart=/opt/twitch-bot/twitch-bot -port=9091 ...

# Docker Compose
# Edit deploy/docker-compose.yml and change: ports: - "9091:9090"
```

### Database locked error

Ensure only one instance is running:
```bash
# Systemd
sudo systemctl restart twitch-bot

# Docker
docker-compose restart twitch-bot
```

### Can't connect to Twitch

1. Verify token is valid
2. Check if token has chat scopes
3. Verify channel name is correct
4. Check logs for details

### Spotify integration not working

1. Verify client ID and secret are correct
2. Check if Spotify app is enabled in Developer Dashboard
3. Ensure rate limits aren't exceeded (search one track per second max)

---

## Monitoring

### Health Check

```bash
curl http://localhost:9090/api/health
```

Response:
```json
{
  "success": true,
  "data": {
    "status": "ok",
    "bot_running": true
  }
}
```

### Admin Dashboard

Access at: `http://matelab:9090`

Features:
- Real-time bot status
- Manage commands
- View/manage song queue
- Bot statistics

---

## Performance & Resources

Typical resource usage:
- **Memory**: 40-80 MB
- **CPU**: <5% on idle
- **Disk**: ~10 MB (executable + database)

Resource limits (systemd):
- Memory: 256 MB (can adjust in service file)
- CPU: 50% quota (can adjust in service file)

---

## Security Considerations

1. **Environment Variables**
   - Never commit `.env` files
   - Restrict file permissions: `chmod 600 .env`
   - Use strong, unique tokens

2. **Service User**
   - Bot runs as `twitch-bot` (unprivileged user)
   - Limited filesystem access

3. **Port Access**
   - Admin dashboard on port 9090
   - Only expose behind reverse proxy in production
   - Use firewall rules to restrict access

4. **Database**
   - Located in `/var/lib/twitch-bot/bot.db`
   - Only readable by twitch-bot user
   - Backed up regularly (recommended)

---

## Backup & Restore

### Backup Database

```bash
# Systemd
sudo cp /var/lib/twitch-bot/bot.db /backup/twitch-bot-$(date +%Y%m%d).db

# Docker
docker cp twitch-bot:/data/bot.db ./backup/twitch-bot-$(date +%Y%m%d).db
```

### Restore Database

```bash
# Systemd
sudo cp /backup/twitch-bot-YYYYMMDD.db /var/lib/twitch-bot/bot.db
sudo chown twitch-bot:twitch-bot /var/lib/twitch-bot/bot.db
sudo systemctl restart twitch-bot

# Docker
docker cp ./backup/twitch-bot-YYYYMMDD.db twitch-bot:/data/bot.db
docker restart twitch-bot
```

---

## Getting Help

- **GitHub Issues**: https://github.com/matella/twitch-bot/issues
- **Logs**: `journalctl -u twitch-bot -f` (systemd) or `docker-compose logs -f` (Docker)
- **Health Check**: `curl http://localhost:9090/api/health`

---

## Next Steps

1. Access the admin dashboard at `http://matelab:9090`
2. Test with custom commands: `!help`
3. Add your first song request: `!song artist song`
4. Create custom commands in the dashboard
5. Configure Twitch channel points integration (future)
