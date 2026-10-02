package main

import (
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"

	"github.com/matella/twitch-bot/internal/bot"
	"github.com/matella/twitch-bot/internal/db"
	"github.com/matella/twitch-bot/internal/server"
	"github.com/matella/twitch-bot/internal/spotify"
)

func main() {
	// Parse command-line flags
	dbPath := flag.String("db", "./bot.db", "Path to SQLite database")
	webPort := flag.String("port", "9090", "Web server port")
	twitchChannel := flag.String("channel", "", "Twitch channel to connect to")
	twitchUsername := flag.String("username", "", "Twitch bot username")
	twitchToken := flag.String("token", "", "Twitch OAuth token")
	spotifyID := flag.String("spotify-id", "", "Spotify client ID")
	spotifySecret := flag.String("spotify-secret", "", "Spotify client secret")
	flag.Parse()

	// Validate required flags
	if *twitchChannel == "" || *twitchUsername == "" || *twitchToken == "" {
		log.Fatal("Missing required flags: -channel, -username, -token")
	}

	// Initialize database
	database, err := db.New(*dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// Initialize Spotify client (optional)
	var spotifyClient *spotify.Client
	if *spotifyID != "" && *spotifySecret != "" {
		spotifyClient = spotify.New(*spotifyID, *spotifySecret, database)
	}

	// Initialize bot
	botClient := bot.New(
		*twitchChannel,
		*twitchUsername,
		*twitchToken,
		database,
		spotifyClient,
	)

	// Initialize web server
	webServer := server.New(*webPort, database, botClient)

	// Start bot in goroutine
	go func() {
		if err := botClient.Connect(); err != nil {
			log.Fatalf("Bot connection failed: %v", err)
		}
	}()

	// Start web server in goroutine
	go func() {
		addr := fmt.Sprintf(":%s", *webPort)
		log.Printf("Starting web server on %s", addr)
		if err := http.ListenAndServe(addr, webServer.Handler()); err != nil && err != http.ErrServerClosed {
			log.Fatalf("Web server error: %v", err)
		}
	}()

	// Wait for interrupt signal
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)
	<-sigChan

	log.Println("Shutting down...")
	botClient.Disconnect()
	database.Close()
}
