package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/m0yi/gochat-server/internal/types"
)

type ChannelStore struct {
	db *sql.DB
}

func NewChannelStore(dbPath string) (*ChannelStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := migrateChannels(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate channels: %w", err)
	}

	return &ChannelStore{db: db}, nil
}

func migrateChannels(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS channels (
			id TEXT PRIMARY KEY,
			name TEXT NOT NULL,
			secret TEXT NOT NULL,
			webhook_url TEXT NOT NULL DEFAULT '',
			enabled INTEGER NOT NULL DEFAULT 1,
			created_at DATETIME NOT NULL
		);
	`)
	return err
}

func (cs *ChannelStore) Close() error {
	return cs.db.Close()
}

func generateSecret() string {
	b := make([]byte, 32)
	rand.Read(b)
	return hex.EncodeToString(b)
}

func (cs *ChannelStore) CreateChannel(name, webhookURL string) (*types.Channel, error) {
	id := uuid.New().String()
	secret := generateSecret()
	now := time.Now()

	_, err := cs.db.Exec(
		`INSERT INTO channels (id, name, secret, webhook_url, enabled, created_at) VALUES (?, ?, ?, ?, 1, ?)`,
		id, name, secret, webhookURL, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert channel: %w", err)
	}

	return &types.Channel{
		ID:         id,
		Name:       name,
		Secret:     secret,
		WebhookURL: webhookURL,
		Enabled:    true,
		CreatedAt:  now,
	}, nil
}

func (cs *ChannelStore) GetChannel(id string) (*types.Channel, error) {
	var ch types.Channel
	var enabled int
	err := cs.db.QueryRow(
		`SELECT id, name, secret, webhook_url, enabled, created_at FROM channels WHERE id = ?`, id,
	).Scan(&ch.ID, &ch.Name, &ch.Secret, &ch.WebhookURL, &enabled, &ch.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("channel not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query channel: %w", err)
	}
	ch.Enabled = enabled == 1
	return &ch, nil
}

func (cs *ChannelStore) GetChannelBySecret(secret string) (*types.Channel, error) {
	var ch types.Channel
	var enabled int
	err := cs.db.QueryRow(
		`SELECT id, name, secret, webhook_url, enabled, created_at FROM channels WHERE secret = ?`, secret,
	).Scan(&ch.ID, &ch.Name, &ch.Secret, &ch.WebhookURL, &enabled, &ch.CreatedAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("channel not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query channel: %w", err)
	}
	ch.Enabled = enabled == 1
	return &ch, nil
}

func (cs *ChannelStore) ListChannels() ([]types.Channel, error) {
	rows, err := cs.db.Query(
		`SELECT id, name, secret, webhook_url, enabled, created_at FROM channels ORDER BY created_at ASC`,
	)
	if err != nil {
		return nil, fmt.Errorf("query channels: %w", err)
	}
	defer rows.Close()

	var channels []types.Channel
	for rows.Next() {
		var ch types.Channel
		var enabled int
		if err := rows.Scan(&ch.ID, &ch.Name, &ch.Secret, &ch.WebhookURL, &enabled, &ch.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan channel: %w", err)
		}
		ch.Enabled = enabled == 1
		channels = append(channels, ch)
	}
	if channels == nil {
		channels = []types.Channel{}
	}
	return channels, nil
}

func (cs *ChannelStore) UpdateChannel(id, name, webhookURL string) (*types.Channel, error) {
	_, err := cs.db.Exec(
		`UPDATE channels SET name = ?, webhook_url = ? WHERE id = ?`,
		name, webhookURL, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update channel: %w", err)
	}
	return cs.GetChannel(id)
}

func (cs *ChannelStore) RegenerateSecret(id string) (*types.Channel, error) {
	secret := generateSecret()
	_, err := cs.db.Exec(`UPDATE channels SET secret = ? WHERE id = ?`, secret, id)
	if err != nil {
		return nil, fmt.Errorf("regenerate secret: %w", err)
	}
	return cs.GetChannel(id)
}

func (cs *ChannelStore) SetEnabled(id string, enabled bool) error {
	e := 0
	if enabled {
		e = 1
	}
	res, err := cs.db.Exec(`UPDATE channels SET enabled = ? WHERE id = ?`, e, id)
	if err != nil {
		return fmt.Errorf("set enabled: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("channel not found")
	}
	return nil
}

func (cs *ChannelStore) DeleteChannel(id string) error {
	res, err := cs.db.Exec(`DELETE FROM channels WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete channel: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("channel not found")
	}
	return nil
}

func (cs *ChannelStore) TotalChannels() int {
	var count int
	cs.db.QueryRow(`SELECT COUNT(*) FROM channels`).Scan(&count)
	return count
}
