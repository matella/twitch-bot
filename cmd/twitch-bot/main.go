// Commande twitch-bot : bot Twitch avec demandes de musique Spotify et interface d'administration.
//
// Toute la configuration passe par des variables d'environnement (voir internal/config).
// "twitch-bot healthcheck" interroge /api/health et sert au HEALTHCHECK Docker.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/matella/twitch-bot/internal/bot"
	"github.com/matella/twitch-bot/internal/chat"
	"github.com/matella/twitch-bot/internal/config"
	"github.com/matella/twitch-bot/internal/server"
	"github.com/matella/twitch-bot/internal/spotify"
	"github.com/matella/twitch-bot/internal/store"
	"github.com/matella/twitch-bot/web"
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "healthcheck" {
		os.Exit(healthcheck())
	}
	if err := run(); err != nil {
		slog.Error("arrêt sur erreur", "err", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	db, err := store.Open(cfg.DBPath)
	if err != nil {
		return err
	}
	defer db.Close()

	// Les interfaces restent nil (et non « nil typé ») si Spotify n'est pas configuré.
	var music chat.Music
	var spotifyAdmin server.Spotify
	if cfg.SpotifyEnabled() {
		sp, err := spotify.New(ctx, spotify.Config{
			ClientID:     cfg.SpotifyID,
			ClientSecret: cfg.SpotifySecret,
			RedirectURL:  cfg.SpotifyRedirectURL,
		}, db)
		if err != nil {
			return err
		}
		music, spotifyAdmin = sp, sp
		slog.Info("Spotify configuré", "redirect_url", cfg.SpotifyRedirectURL, "connected", sp.Connected())
	} else {
		slog.Warn("Spotify non configuré : les demandes de musique sont désactivées")
	}

	handler := chat.New(db, music, chat.Options{Channel: cfg.Channel, SongCooldown: cfg.SongCooldown})
	twitchBot := bot.New(cfg.Channel, cfg.Username, cfg.Token, handler)

	srv := &http.Server{
		Addr: ":" + cfg.Port,
		Handler: server.New(server.Options{
			Channel:       cfg.Channel,
			AdminUser:     cfg.AdminUser,
			AdminPassword: cfg.AdminPassword,
			Assets:        web.Static(),
		}, db, spotifyAdmin, twitchBot),
		ReadHeaderTimeout: 5 * time.Second,
		ReadTimeout:       15 * time.Second,
		WriteTimeout:      30 * time.Second,
		IdleTimeout:       60 * time.Second,
	}

	errc := make(chan error, 2)
	go func() {
		slog.Info("interface d'administration", "addr", srv.Addr)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errc <- fmt.Errorf("serveur web : %w", err)
		}
	}()
	botDone := make(chan struct{})
	go func() {
		defer close(botDone)
		if err := twitchBot.Run(ctx); err != nil {
			errc <- fmt.Errorf("bot Twitch : %w", err)
		}
	}()

	var runErr error
	select {
	case <-ctx.Done():
		slog.Info("arrêt demandé")
	case runErr = <-errc:
		stop() // arrête aussi l'autre composant
	}

	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.Shutdown(shutdownCtx); err != nil {
		slog.Warn("arrêt du serveur web", "err", err)
	}
	select {
	case <-botDone:
	case <-shutdownCtx.Done():
		slog.Warn("le bot ne s'est pas arrêté à temps")
	}
	return runErr
}

// healthcheck renvoie 0 si le serveur local répond correctement.
func healthcheck() int {
	port := os.Getenv("PORT")
	if port == "" {
		port = "9090"
	}
	client := &http.Client{Timeout: 3 * time.Second}
	resp, err := client.Get("http://127.0.0.1:" + port + "/api/health")
	if err != nil {
		fmt.Fprintln(os.Stderr, "healthcheck :", err)
		return 1
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		fmt.Fprintln(os.Stderr, "healthcheck : statut", resp.StatusCode)
		return 1
	}
	return 0
}
