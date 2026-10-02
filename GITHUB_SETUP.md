# GitHub Setup Guide

## 1. Create Repository on GitHub

1. Go to [github.com/new](https://github.com/new)
2. Repository name: `twitch-bot`
3. Description: "High-performance Twitch bot with Spotify integration built in Go"
4. **Do NOT initialize with README, .gitignore, or license** (we already have these)
5. Click "Create repository"

## 2. Push to GitHub

After creating the repository, GitHub will show you commands. Run these from `/home/claude/twitch-bot`:

```bash
git branch -M main
git remote add origin https://github.com/YOUR_USERNAME/twitch-bot.git
git push -u origin main
```

Replace `YOUR_USERNAME` with your GitHub username.

### If using SSH (recommended):

```bash
git remote add origin git@github.com:YOUR_USERNAME/twitch-bot.git
git push -u origin main
```

## 3. Verify

Check your GitHub repository URL:
```bash
git remote -v
```

You should see:
```
origin  https://github.com/YOUR_USERNAME/twitch-bot.git (fetch)
origin  https://github.com/YOUR_USERNAME/twitch-bot.git (push)
```

## 4. Future Commits

After the initial push, future commits are simpler:

```bash
git add .
git commit -m "Your commit message"
git push
```

## Notes

- The project is already initialized with `git init`
- Initial commit and port configuration changes are ready to push
- All files are staged and committed locally
- Repository is now ready for GitHub collaboration
