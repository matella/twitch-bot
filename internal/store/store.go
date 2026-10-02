// Package store persiste les commandes, l'historique des demandes et les réglages dans SQLite.
// Le driver utilisé (modernc.org/sqlite) est écrit en Go pur : pas de CGO, binaire statique.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"time"

	_ "modernc.org/sqlite" // enregistre le driver "sqlite"

	"github.com/matella/twitch-bot/internal/model"
)

const schema = `
CREATE TABLE IF NOT EXISTS commands (
	name             TEXT PRIMARY KEY,
	response         TEXT NOT NULL,
	cooldown_seconds INTEGER NOT NULL DEFAULT 5,
	created_at       INTEGER NOT NULL,
	updated_at       INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS song_requests (
	id           INTEGER PRIMARY KEY AUTOINCREMENT,
	track_id     TEXT NOT NULL,
	track_name   TEXT NOT NULL,
	artist       TEXT NOT NULL,
	requested_by TEXT NOT NULL,
	status       TEXT NOT NULL,
	created_at   INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS settings (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
`

// maxHistory est le nombre maximal de demandes conservées.
const maxHistory = 1000

// Store est l'accès à la base.
type Store struct {
	db *sql.DB
}

// Open ouvre (et crée si besoin) la base SQLite située à path.
func Open(path string) (*Store, error) {
	q := url.Values{}
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "synchronous(NORMAL)")
	db, err := sql.Open("sqlite", "file:"+path+"?"+q.Encode())
	if err != nil {
		return nil, err
	}
	// Une seule connexion : les écritures sont sérialisées, le volume ici est minuscule.
	db.SetMaxOpenConns(1)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("ouverture de la base %q : %w", path, err)
	}
	if _, err := db.ExecContext(ctx, schema); err != nil {
		db.Close()
		return nil, fmt.Errorf("création du schéma : %w", err)
	}
	return &Store{db: db}, nil
}

// Close ferme la base.
func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().Unix() }

// ---- commandes ----

func (s *Store) GetCommand(ctx context.Context, name string) (*model.Command, error) {
	var c model.Command
	err := s.db.QueryRowContext(ctx,
		`SELECT name, response, cooldown_seconds, updated_at FROM commands WHERE name = ?`, name,
	).Scan(&c.Name, &c.Response, &c.CooldownSeconds, &c.UpdatedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) ListCommands(ctx context.Context) ([]model.Command, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT name, response, cooldown_seconds, updated_at FROM commands ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Command
	for rows.Next() {
		var c model.Command
		if err := rows.Scan(&c.Name, &c.Response, &c.CooldownSeconds, &c.UpdatedAt); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) AddCommand(ctx context.Context, c model.Command) error {
	t := now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO commands (name, response, cooldown_seconds, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		c.Name, c.Response, c.CooldownSeconds, t, t)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrExists
	}
	return nil
}

func (s *Store) UpdateCommand(ctx context.Context, c model.Command) error {
	res, err := s.db.ExecContext(ctx,
		`UPDATE commands SET response = ?, cooldown_seconds = ?, updated_at = ? WHERE name = ?`,
		c.Response, c.CooldownSeconds, now(), c.Name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrNotFound
	}
	return nil
}

func (s *Store) DeleteCommand(ctx context.Context, name string) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM commands WHERE name = ?`, name)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrNotFound
	}
	return nil
}

// ---- demandes de musique ----

func (s *Store) AddSongRequest(ctx context.Context, r model.SongRequest) error {
	if _, err := s.db.ExecContext(ctx,
		`INSERT INTO song_requests (track_id, track_name, artist, requested_by, status, created_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		r.TrackID, r.TrackName, r.Artist, r.RequestedBy, r.Status, now()); err != nil {
		return err
	}
	// Borne la taille de l'historique (best effort : l'échec n'est pas bloquant).
	_, _ = s.db.ExecContext(ctx,
		`DELETE FROM song_requests WHERE id <= (SELECT MAX(id) FROM song_requests) - ?`, maxHistory)
	return nil
}

func (s *Store) RecentSongRequests(ctx context.Context, limit int) ([]model.SongRequest, error) {
	rows, err := s.db.QueryContext(ctx,
		`SELECT id, track_id, track_name, artist, requested_by, status, created_at
		 FROM song_requests ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.SongRequest
	for rows.Next() {
		var r model.SongRequest
		if err := rows.Scan(&r.ID, &r.TrackID, &r.TrackName, &r.Artist, &r.RequestedBy, &r.Status, &r.CreatedAt); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

func (s *Store) DeleteSongRequest(ctx context.Context, id int64) error {
	res, err := s.db.ExecContext(ctx, `DELETE FROM song_requests WHERE id = ?`, id)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return model.ErrNotFound
	}
	return nil
}

func (s *Store) ClearSongRequests(ctx context.Context) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM song_requests`)
	return err
}

// ---- réglages (jeton Spotify, etc.) ----

// GetSetting renvoie model.ErrNotFound si la clé n'existe pas.
func (s *Store) GetSetting(ctx context.Context, key string) (string, error) {
	var v string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&v)
	if errors.Is(err, sql.ErrNoRows) {
		return "", model.ErrNotFound
	}
	return v, err
}

func (s *Store) SetSetting(ctx context.Context, key, value string) error {
	_, err := s.db.ExecContext(ctx,
		`INSERT INTO settings (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key, value)
	return err
}

func (s *Store) DeleteSetting(ctx context.Context, key string) error {
	_, err := s.db.ExecContext(ctx, `DELETE FROM settings WHERE key = ?`, key)
	return err
}
