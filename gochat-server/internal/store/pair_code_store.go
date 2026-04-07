package store

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"math/big"
	"strings"
	"time"
)

type PairCodeStore struct {
	db *sql.DB
}

type PairCodeSession struct {
	Code         string
	SessionToken string
	ChannelID    string
	ClaimedBy    string
	CreatedAt    time.Time
	ExpiresAt    time.Time
	ClaimedAt    *time.Time
}

func NewPairCodeStore(dbPath string) (*PairCodeStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := migratePairCodes(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate pair codes: %w", err)
	}

	return &PairCodeStore{db: db}, nil
}

func migratePairCodes(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS pair_codes (
			code TEXT PRIMARY KEY,
			session_token TEXT NOT NULL UNIQUE,
			channel_id TEXT NOT NULL,
			claimed_by TEXT NOT NULL DEFAULT '',
			created_at DATETIME NOT NULL,
			expires_at DATETIME NOT NULL,
			claimed_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_pair_codes_session_token ON pair_codes(session_token);
		CREATE INDEX IF NOT EXISTS idx_pair_codes_channel_id ON pair_codes(channel_id);
	`)
	return err
}

func (ps *PairCodeStore) Close() error {
	return ps.db.Close()
}

func normalizePairCode(code string) string {
	return strings.ReplaceAll(strings.TrimSpace(code), " ", "")
}

func generatePairCode() (string, error) {
	n, err := rand.Int(rand.Reader, big.NewInt(1000000))
	if err != nil {
		return "", fmt.Errorf("generate pair code: %w", err)
	}
	return fmt.Sprintf("%06d", n.Int64()), nil
}

func generateSessionToken() (string, error) {
	buf := make([]byte, 24)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("generate session token: %w", err)
	}
	return hex.EncodeToString(buf), nil
}

func (ps *PairCodeStore) CleanupExpired(now time.Time) error {
	_, err := ps.db.Exec(`DELETE FROM pair_codes WHERE expires_at <= ?`, now)
	if err != nil {
		return fmt.Errorf("cleanup expired pair codes: %w", err)
	}
	return nil
}

func (ps *PairCodeStore) ExpiredUnclaimedChannelIDs(now time.Time) ([]string, error) {
	rows, err := ps.db.Query(`SELECT channel_id FROM pair_codes WHERE expires_at <= ? AND claimed_at IS NULL`, now)
	if err != nil {
		return nil, fmt.Errorf("query expired pair codes: %w", err)
	}
	defer rows.Close()

	seen := make(map[string]struct{})
	channelIDs := make([]string, 0)
	for rows.Next() {
		var channelID string
		if err := rows.Scan(&channelID); err != nil {
			return nil, fmt.Errorf("scan expired pair code: %w", err)
		}
		channelID = strings.TrimSpace(channelID)
		if channelID == "" {
			continue
		}
		if _, ok := seen[channelID]; ok {
			continue
		}
		seen[channelID] = struct{}{}
		channelIDs = append(channelIDs, channelID)
	}
	return channelIDs, nil
}

func (ps *PairCodeStore) Create(channelID string, ttl time.Duration) (*PairCodeSession, error) {
	now := time.Now()
	if err := ps.CleanupExpired(now); err != nil {
		return nil, err
	}

	expiresAt := now.Add(ttl)
	for attempt := 0; attempt < 20; attempt++ {
		code, err := generatePairCode()
		if err != nil {
			return nil, err
		}
		sessionToken, err := generateSessionToken()
		if err != nil {
			return nil, err
		}

		_, err = ps.db.Exec(
			`INSERT INTO pair_codes (code, session_token, channel_id, claimed_by, created_at, expires_at) VALUES (?, ?, ?, '', ?, ?)`,
			code, sessionToken, channelID, now, expiresAt,
		)
		if err == nil {
			return &PairCodeSession{
				Code:         code,
				SessionToken: sessionToken,
				ChannelID:    channelID,
				CreatedAt:    now,
				ExpiresAt:    expiresAt,
			}, nil
		}
	}

	return nil, fmt.Errorf("failed to allocate unique pair code")
}

func (ps *PairCodeStore) GetBySessionToken(sessionToken string) (*PairCodeSession, error) {
	return ps.getOne(`SELECT code, session_token, channel_id, claimed_by, created_at, expires_at, claimed_at FROM pair_codes WHERE session_token = ?`, strings.TrimSpace(sessionToken))
}

func (ps *PairCodeStore) GetByCode(code string) (*PairCodeSession, error) {
	return ps.getOne(`SELECT code, session_token, channel_id, claimed_by, created_at, expires_at, claimed_at FROM pair_codes WHERE code = ?`, normalizePairCode(code))
}

func (ps *PairCodeStore) getOne(query string, arg string) (*PairCodeSession, error) {
	var session PairCodeSession
	var claimedAt sql.NullTime

	err := ps.db.QueryRow(query, arg).Scan(
		&session.Code,
		&session.SessionToken,
		&session.ChannelID,
		&session.ClaimedBy,
		&session.CreatedAt,
		&session.ExpiresAt,
		&claimedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("pair code not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query pair code: %w", err)
	}
	if claimedAt.Valid {
		session.ClaimedAt = &claimedAt.Time
	}
	return &session, nil
}

func (ps *PairCodeStore) Claim(code, claimedBy string) (*PairCodeSession, error) {
	now := time.Now()
	if err := ps.CleanupExpired(now); err != nil {
		return nil, err
	}

	tx, err := ps.db.Begin()
	if err != nil {
		return nil, fmt.Errorf("begin claim transaction: %w", err)
	}
	defer tx.Rollback()

	var session PairCodeSession
	var claimedAt sql.NullTime
	err = tx.QueryRow(
		`SELECT code, session_token, channel_id, claimed_by, created_at, expires_at, claimed_at FROM pair_codes WHERE code = ?`,
		normalizePairCode(code),
	).Scan(
		&session.Code,
		&session.SessionToken,
		&session.ChannelID,
		&session.ClaimedBy,
		&session.CreatedAt,
		&session.ExpiresAt,
		&claimedAt,
	)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("pair code not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query pair code: %w", err)
	}
	if claimedAt.Valid {
		return nil, fmt.Errorf("pair code already used")
	}
	if !session.ExpiresAt.After(now) {
		return nil, fmt.Errorf("pair code expired")
	}

	res, err := tx.Exec(
		`UPDATE pair_codes SET claimed_by = ?, claimed_at = ? WHERE code = ? AND claimed_at IS NULL`,
		strings.TrimSpace(claimedBy), now, session.Code,
	)
	if err != nil {
		return nil, fmt.Errorf("claim pair code: %w", err)
	}
	rows, _ := res.RowsAffected()
	if rows == 0 {
		return nil, fmt.Errorf("pair code already used")
	}

	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit pair code claim: %w", err)
	}

	session.ClaimedBy = strings.TrimSpace(claimedBy)
	session.ClaimedAt = &now
	return &session, nil
}
