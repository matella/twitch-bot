// Package store persiste les commandes, l'historique des demandes et les réglages dans SQLite.
// Le driver utilisé (modernc.org/sqlite) est écrit en Go pur : pas de CGO, binaire statique.
package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	_ "modernc.org/sqlite" // enregistre le driver "sqlite"

	"github.com/matella/twitch-bot/internal/model"
)

// migrations fait évoluer la base : l'élément i fait passer le schéma de la version i à i+1
// (suivie par PRAGMA user_version). Ne jamais modifier une migration déjà publiée : en ajouter une.
var migrations = []string{
	// 1 : schéma initial
	`CREATE TABLE IF NOT EXISTS commands (
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
	);`,
	// 2 : rôles, délai par spectateur, activation, alias, compteur d'utilisations
	`ALTER TABLE commands ADD COLUMN permission TEXT NOT NULL DEFAULT 'everyone';
	ALTER TABLE commands ADD COLUMN user_cooldown_seconds INTEGER NOT NULL DEFAULT 0;
	ALTER TABLE commands ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;
	ALTER TABLE commands ADD COLUMN aliases TEXT NOT NULL DEFAULT '';
	ALTER TABLE commands ADD COLUMN use_count INTEGER NOT NULL DEFAULT 0;`,
}

func migrate(ctx context.Context, db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
		return err
	}
	if version > len(migrations) {
		return fmt.Errorf("la base est en version %d, ce programme ne connaît que la %d (programme trop ancien ?)", version, len(migrations))
	}
	for v := version; v < len(migrations); v++ {
		tx, err := db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, migrations[v]); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %d : %w", v+1, err)
		}
		// PRAGMA n'accepte pas de paramètre ; v+1 est un entier maîtrisé.
		if _, err := tx.ExecContext(ctx, fmt.Sprintf(`PRAGMA user_version = %d`, v+1)); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

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
	if err := migrate(ctx, db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migration du schéma : %w", err)
	}
	return &Store{db: db}, nil
}

// Close ferme la base.
func (s *Store) Close() error { return s.db.Close() }

func now() int64 { return time.Now().Unix() }

// ---- commandes ----

const commandCols = `name, response, cooldown_seconds, user_cooldown_seconds, permission, enabled, aliases, use_count, updated_at`

type scanner interface{ Scan(dest ...any) error }

func scanCommand(sc scanner) (model.Command, error) {
	var (
		c       model.Command
		perm    string
		aliases string
	)
	if err := sc.Scan(&c.Name, &c.Response, &c.CooldownSeconds, &c.UserCooldownSeconds,
		&perm, &c.Enabled, &aliases, &c.UseCount, &c.UpdatedAt); err != nil {
		return model.Command{}, err
	}
	c.Permission, _ = model.ParseLevel(perm) // valeur inconnue : « tout le monde »
	c.Aliases = strings.Fields(aliases)
	if c.Aliases == nil {
		c.Aliases = []string{}
	}
	return c, nil
}

func (s *Store) GetCommand(ctx context.Context, name string) (*model.Command, error) {
	c, err := scanCommand(s.db.QueryRowContext(ctx, `SELECT `+commandCols+` FROM commands WHERE name = ?`, name))
	if errors.Is(err, sql.ErrNoRows) {
		return nil, model.ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	return &c, nil
}

func (s *Store) ListCommands(ctx context.Context) ([]model.Command, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT `+commandCols+` FROM commands ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []model.Command
	for rows.Next() {
		c, err := scanCommand(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

func (s *Store) AddCommand(ctx context.Context, c model.Command) error {
	t := now()
	res, err := s.db.ExecContext(ctx,
		`INSERT INTO commands (name, response, cooldown_seconds, user_cooldown_seconds, permission, enabled, aliases, created_at, updated_at)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?) ON CONFLICT(name) DO NOTHING`,
		c.Name, c.Response, c.CooldownSeconds, c.UserCooldownSeconds, c.Permission.String(), c.Enabled,
		strings.Join(c.Aliases, " "), t, t)
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
		`UPDATE commands SET response = ?, cooldown_seconds = ?, user_cooldown_seconds = ?, permission = ?,
		        enabled = ?, aliases = ?, updated_at = ? WHERE name = ?`,
		c.Response, c.CooldownSeconds, c.UserCooldownSeconds, c.Permission.String(), c.Enabled,
		strings.Join(c.Aliases, " "), now(), c.Name)
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

// IncrementUse compte une utilisation de la commande et renvoie le nouveau total.
func (s *Store) IncrementUse(ctx context.Context, name string) (int64, error) {
	var n int64
	err := s.db.QueryRowContext(ctx,
		`UPDATE commands SET use_count = use_count + 1 WHERE name = ? RETURNING use_count`, name).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0, model.ErrNotFound
	}
	return n, err
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

// ---- réglages de la musique ----

const musicSettingsKey = "music_settings"

// GetMusicSettings renvoie les réglages enregistrés, ou les valeurs par défaut.
func (s *Store) GetMusicSettings(ctx context.Context) (model.MusicSettings, error) {
	raw, err := s.GetSetting(ctx, musicSettingsKey)
	if errors.Is(err, model.ErrNotFound) {
		return model.DefaultMusicSettings(), nil
	}
	if err != nil {
		return model.MusicSettings{}, err
	}
	ms := model.DefaultMusicSettings()
	if err := json.Unmarshal([]byte(raw), &ms); err != nil {
		return model.MusicSettings{}, fmt.Errorf("réglages de la musique illisibles : %w", err)
	}
	if ms.Blocklist == nil {
		ms.Blocklist = []string{}
	}
	return ms, nil
}

func (s *Store) SetMusicSettings(ctx context.Context, ms model.MusicSettings) error {
	b, err := json.Marshal(ms)
	if err != nil {
		return err
	}
	return s.SetSetting(ctx, musicSettingsKey, string(b))
}
