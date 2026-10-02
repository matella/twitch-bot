package spotify

import (
	"context"
	"log"

	"github.com/zmb3/spotify/v2"
	spotifyauth "github.com/zmb3/spotify/v2/auth"
	"golang.org/x/oauth2/clientcredentials"
	"github.com/matella/twitch-bot/internal/db"
)

// Client wraps the Spotify API client
type Client struct {
	client *spotify.Client
	db     *db.DB
}

// Track represents a Spotify track
type Track struct {
	URI    string
	Name   string
	Artist string
}

// New creates a new Spotify client
func New(clientID, clientSecret string, database *db.DB) *Client {
	ctx := context.Background()
	config := &clientcredentials.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		TokenURL:     spotifyauth.TokenURL,
	}

	token, err := config.Token(ctx)
	if err != nil {
		log.Printf("Error getting Spotify token: %v", err)
		return nil
	}

	httpClient := spotifyauth.New().Client(ctx, token)
	spotifyClient := spotify.New(httpClient)

	return &Client{
		client: spotifyClient,
		db:     database,
	}
}

// SearchTrack searches for a track on Spotify
func (c *Client) SearchTrack(query string) (*Track, error) {
	ctx := context.Background()

	results, err := c.client.Search(ctx, query, spotify.SearchTypeTrack, spotify.Limit(1))
	if err != nil {
		return nil, err
	}

	if results.Tracks == nil || len(results.Tracks.Tracks) == 0 {
		return nil, nil
	}

	t := results.Tracks.Tracks[0]
	artistName := ""
	if len(t.Artists) > 0 {
		artistName = t.Artists[0].Name
	}

	return &Track{
		URI:    string(t.URI),
		Name:   t.Name,
		Artist: artistName,
	}, nil
}

// GetTrack retrieves a specific track by ID
func (c *Client) GetTrack(trackID string) (*Track, error) {
	ctx := context.Background()

	id := spotify.ID(trackID)
	track, err := c.client.GetTrack(ctx, id)
	if err != nil {
		return nil, err
	}

	artistName := ""
	if len(track.Artists) > 0 {
		artistName = track.Artists[0].Name
	}

	return &Track{
		URI:    string(track.URI),
		Name:   track.Name,
		Artist: artistName,
	}, nil
}
