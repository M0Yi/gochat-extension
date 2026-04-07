package store

import (
	"database/sql"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/m0yi/gochat-server/internal/types"
	"golang.org/x/crypto/bcrypt"
)

type AdminStore struct {
	db *sql.DB
}

func NewAdminStore(dbPath string) (*AdminStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open admin db: %w", err)
	}
	db.SetMaxOpenConns(1)

	if err := migrateAdmin(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("admin migrate: %w", err)
	}

	return &AdminStore{db: db}, nil
}

func migrateAdmin(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS admin_users (
			id TEXT PRIMARY KEY,
			username TEXT NOT NULL UNIQUE,
			password_hash TEXT NOT NULL,
			role TEXT NOT NULL DEFAULT 'admin',
			created_at DATETIME NOT NULL,
			last_login DATETIME,
			banned INTEGER NOT NULL DEFAULT 0
		);
		CREATE TABLE IF NOT EXISTS admin_settings (
			key TEXT PRIMARY KEY,
			value TEXT NOT NULL,
			updated_at DATETIME NOT NULL
		);
		CREATE TABLE IF NOT EXISTS admin_audit_log (
			id TEXT PRIMARY KEY,
			action TEXT NOT NULL,
			detail TEXT,
			operator TEXT NOT NULL,
			created_at DATETIME NOT NULL
		);
		CREATE INDEX IF NOT EXISTS idx_audit_log_time ON admin_audit_log(created_at);
	`)
	if err != nil {
		return err
	}

	var count int
	err = db.QueryRow("SELECT COUNT(*) FROM admin_users").Scan(&count)
	if err != nil {
		return err
	}
	if count == 0 {
		hash, _ := bcrypt.GenerateFromPassword([]byte("admin"), bcrypt.DefaultCost)
		_, err = db.Exec(
			"INSERT INTO admin_users (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)",
			uuid.New().String(), "admin", string(hash), "superadmin", time.Now(),
		)
	}
	return err
}

func (as *AdminStore) Close() error {
	return as.db.Close()
}

func (as *AdminStore) Authenticate(username, password string) (*types.AdminUser, error) {
	var u types.AdminUser
	var hash string
	var banned int
	var lastLogin sql.NullTime

	err := as.db.QueryRow(
		"SELECT id, username, role, created_at, last_login, banned, password_hash FROM admin_users WHERE username = ?",
		username,
	).Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &lastLogin, &banned, &hash)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("invalid credentials")
	}
	if err != nil {
		return nil, err
	}
	if lastLogin.Valid {
		u.LastLogin = lastLogin.Time
	}

	if banned == 1 {
		return nil, fmt.Errorf("account banned")
	}

	if err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)); err != nil {
		return nil, fmt.Errorf("invalid credentials")
	}

	as.db.Exec("UPDATE admin_users SET last_login = ? WHERE id = ?", time.Now(), u.ID)
	return &u, nil
}

func (as *AdminStore) ListUsers() ([]types.AdminUser, error) {
	rows, err := as.db.Query("SELECT id, username, role, created_at, last_login, banned FROM admin_users ORDER BY created_at ASC")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var users []types.AdminUser
	for rows.Next() {
		var u types.AdminUser
		var banned int
		var lastLogin sql.NullTime
		if err := rows.Scan(&u.ID, &u.Username, &u.Role, &u.CreatedAt, &lastLogin, &banned); err != nil {
			return nil, err
		}
		if lastLogin.Valid {
			u.LastLogin = lastLogin.Time
		}
		u.Banned = banned == 1
		users = append(users, u)
	}
	if users == nil {
		users = []types.AdminUser{}
	}
	return users, nil
}

func (as *AdminStore) CreateUser(username, password, role string) (*types.AdminUser, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &types.AdminUser{
		ID:        uuid.New().String(),
		Username:  username,
		Role:      role,
		CreatedAt: time.Now(),
	}
	_, err = as.db.Exec(
		"INSERT INTO admin_users (id, username, password_hash, role, created_at) VALUES (?, ?, ?, ?, ?)",
		u.ID, u.Username, string(hash), u.Role, u.CreatedAt,
	)
	if err != nil {
		return nil, fmt.Errorf("create user: %w", err)
	}
	return u, nil
}

func (as *AdminStore) DeleteUser(id string) error {
	res, err := as.db.Exec("DELETE FROM admin_users WHERE id = ? AND role != 'superadmin'", id)
	if err != nil {
		return err
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("cannot delete this user")
	}
	return nil
}

func (as *AdminStore) BanUser(id string, banned bool) error {
	b := 0
	if banned {
		b = 1
	}
	_, err := as.db.Exec("UPDATE admin_users SET banned = ? WHERE id = ? AND role != 'superadmin'", b, id)
	return err
}

func (as *AdminStore) ResetPassword(id, newPassword string) error {
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return err
	}
	_, err = as.db.Exec("UPDATE admin_users SET password_hash = ? WHERE id = ?", string(hash), id)
	return err
}

func (as *AdminStore) GetSetting(key string) (string, error) {
	val, _, err := as.GetSettingValue(key)
	return val, err
}

func (as *AdminStore) GetSettingValue(key string) (string, bool, error) {
	var val string
	err := as.db.QueryRow("SELECT value FROM admin_settings WHERE key = ?", key).Scan(&val)
	if err == sql.ErrNoRows {
		return "", false, nil
	}
	if err != nil {
		return "", false, err
	}
	return val, true, nil
}

func (as *AdminStore) SetSetting(key, value string) error {
	_, err := as.db.Exec(
		"INSERT OR REPLACE INTO admin_settings (key, value, updated_at) VALUES (?, ?, ?)",
		key, value, time.Now(),
	)
	return err
}

type AuditLogEntry struct {
	ID        string    `json:"id"`
	Action    string    `json:"action"`
	Detail    string    `json:"detail"`
	Operator  string    `json:"operator"`
	CreatedAt time.Time `json:"createdAt"`
}

func (as *AdminStore) AddAuditLog(action, detail, operator string) error {
	_, err := as.db.Exec(
		"INSERT INTO admin_audit_log (id, action, detail, operator, created_at) VALUES (?, ?, ?, ?, ?)",
		uuid.New().String(), action, detail, operator, time.Now(),
	)
	return err
}

func (as *AdminStore) ListAuditLogs(limit int) ([]AuditLogEntry, error) {
	if limit <= 0 || limit > 200 {
		limit = 50
	}
	rows, err := as.db.Query(
		"SELECT id, action, detail, operator, created_at FROM admin_audit_log ORDER BY created_at DESC LIMIT ?",
		limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []AuditLogEntry
	for rows.Next() {
		var l AuditLogEntry
		if err := rows.Scan(&l.ID, &l.Action, &l.Detail, &l.Operator, &l.CreatedAt); err != nil {
			return nil, err
		}
		logs = append(logs, l)
	}
	if logs == nil {
		logs = []AuditLogEntry{}
	}
	return logs, nil
}

func (as *AdminStore) TotalConversations() int {
	var n int
	as.db.QueryRow("SELECT COUNT(DISTINCT conversation_id) FROM tasks").Scan(&n)
	return n
}

func (as *AdminStore) TotalTasks() int {
	var n int
	as.db.QueryRow("SELECT COUNT(*) FROM tasks").Scan(&n)
	return n
}
