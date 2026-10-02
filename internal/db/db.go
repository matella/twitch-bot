package db

import (
	"database/sql"
	"sync"

	_ "github.com/mattn/go-sqlite3"
	"github.com/jmoiron/sqlx"
)

// DB wraps the database connection with thread-safe operations
type DB struct {
	conn *sqlx.DB
	mu   sync.RWMutex
}

// New creates a new database connection and initializes schema
func New(dbPath string) (*DB, error) {
	conn, err := sqlx.Open("sqlite3", dbPath)
	if err != nil {
		return nil, err
	}

	// Test connection
	if err := conn.Ping(); err != nil {
		return nil, err
	}

	db := &DB{conn: conn}

	// Initialize schema
	if err := db.initSchema(); err != nil {
		return nil, err
	}

	return db, nil
}

// Close closes the database connection
func (db *DB) Close() error {
	return db.conn.Close()
}

// initSchema creates tables if they don't exist
func (db *DB) initSchema() error {
	schema := `
	CREATE TABLE IF NOT EXISTS commands (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		name TEXT UNIQUE NOT NULL,
		response TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS song_queue (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		spotify_uri TEXT NOT NULL,
		track_name TEXT NOT NULL,
		artist_name TEXT NOT NULL,
		requested_by TEXT NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS settings (
		key TEXT PRIMARY KEY,
		value TEXT NOT NULL,
		updated_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE TABLE IF NOT EXISTS bot_stats (
		id INTEGER PRIMARY KEY AUTOINCREMENT,
		event_type TEXT NOT NULL,
		event_data TEXT,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	);

	CREATE INDEX IF NOT EXISTS idx_queue_created ON song_queue(created_at);
	CREATE INDEX IF NOT EXISTS idx_stats_created ON bot_stats(created_at);
	`

	_, err := db.conn.Exec(schema)
	return err
}

// Command operations

// Command represents a chat command
type Command struct {
	ID        int    `db:"id"`
	Name      string `db:"name"`
	Response  string `db:"response"`
	CreatedAt string `db:"created_at"`
	UpdatedAt string `db:"updated_at"`
}

// GetCommand retrieves a command by name
func (db *DB) GetCommand(name string) (*Command, error) {
	var cmd Command
	err := db.conn.Get(&cmd, "SELECT * FROM commands WHERE name = ?", name)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	return &cmd, err
}

// GetAllCommands retrieves all commands
func (db *DB) GetAllCommands() ([]Command, error) {
	var commands []Command
	err := db.conn.Select(&commands, "SELECT * FROM commands ORDER BY name")
	return commands, err
}

// AddCommand adds a new command
func (db *DB) AddCommand(name, response string) error {
	_, err := db.conn.Exec(
		"INSERT INTO commands (name, response) VALUES (?, ?)",
		name, response,
	)
	return err
}

// UpdateCommand updates an existing command
func (db *DB) UpdateCommand(name, response string) error {
	_, err := db.conn.Exec(
		"UPDATE commands SET response = ?, updated_at = CURRENT_TIMESTAMP WHERE name = ?",
		response, name,
	)
	return err
}

// DeleteCommand deletes a command
func (db *DB) DeleteCommand(name string) error {
	_, err := db.conn.Exec("DELETE FROM commands WHERE name = ?", name)
	return err
}

// Song queue operations

// QueuedSong represents a song in the queue
type QueuedSong struct {
	ID          int    `db:"id"`
	SpotifyURI  string `db:"spotify_uri"`
	TrackName   string `db:"track_name"`
	ArtistName  string `db:"artist_name"`
	RequestedBy string `db:"requested_by"`
	CreatedAt   string `db:"created_at"`
}

// AddSongToQueue adds a song to the request queue
func (db *DB) AddSongToQueue(spotifyURI, trackName, artistName, requestedBy string) error {
	_, err := db.conn.Exec(
		"INSERT INTO song_queue (spotify_uri, track_name, artist_name, requested_by) VALUES (?, ?, ?, ?)",
		spotifyURI, trackName, artistName, requestedBy,
	)
	return err
}

// GetQueue retrieves songs from the queue (limit 50 for performance)
func (db *DB) GetQueue(limit int) ([]QueuedSong, error) {
	if limit == 0 {
		limit = 50
	}
	var songs []QueuedSong
	err := db.conn.Select(&songs, "SELECT * FROM song_queue ORDER BY created_at LIMIT ?", limit)
	return songs, err
}

// RemoveFromQueue removes a song from the queue by ID
func (db *DB) RemoveFromQueue(id int) error {
	_, err := db.conn.Exec("DELETE FROM song_queue WHERE id = ?", id)
	return err
}

// ClearQueue clears all songs from the queue
func (db *DB) ClearQueue() error {
	_, err := db.conn.Exec("DELETE FROM song_queue")
	return err
}

// Settings operations

// GetSetting retrieves a setting value
func (db *DB) GetSetting(key string) (string, error) {
	var value string
	err := db.conn.Get(&value, "SELECT value FROM settings WHERE key = ?", key)
	if err == sql.ErrNoRows {
		return "", nil
	}
	return value, err
}

// SetSetting sets a setting value
func (db *DB) SetSetting(key, value string) error {
	_, err := db.conn.Exec(
		"INSERT INTO settings (key, value) VALUES (?, ?) ON CONFLICT(key) DO UPDATE SET value = ?, updated_at = CURRENT_TIMESTAMP",
		key, value, value,
	)
	return err
}

// Stats operations

// LogEvent logs a bot event
func (db *DB) LogEvent(eventType, eventData string) error {
	_, err := db.conn.Exec(
		"INSERT INTO bot_stats (event_type, event_data) VALUES (?, ?)",
		eventType, eventData,
	)
	return err
}
