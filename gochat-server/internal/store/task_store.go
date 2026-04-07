package store

import (
	"database/sql"
	"fmt"
	"time"

	_ "modernc.org/sqlite"

	"github.com/google/uuid"
	"github.com/m0yi/gochat-server/internal/types"
)

type TaskStore struct {
	db *sql.DB
}

func NewTaskStore(dbPath string) (*TaskStore, error) {
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := migrate(db); err != nil {
		db.Close()
		return nil, fmt.Errorf("migrate: %w", err)
	}

	return &TaskStore{db: db}, nil
}

func migrate(db *sql.DB) error {
	_, err := db.Exec(`
		CREATE TABLE IF NOT EXISTS tasks (
			id TEXT PRIMARY KEY,
			conversation_id TEXT NOT NULL,
			title TEXT NOT NULL,
			description TEXT NOT NULL DEFAULT '',
			done INTEGER NOT NULL DEFAULT 0,
			created_at DATETIME NOT NULL,
			done_at DATETIME
		);
		CREATE INDEX IF NOT EXISTS idx_tasks_conv ON tasks(conversation_id);
	`)
	if err != nil {
		return err
	}

	row := db.QueryRow(`SELECT COUNT(*) FROM pragma_table_info('tasks') WHERE name = 'description'`)
	var count int
	if err := row.Scan(&count); err != nil {
		return err
	}
	if count == 0 {
		_, err = db.Exec(`ALTER TABLE tasks ADD COLUMN description TEXT NOT NULL DEFAULT ''`)
		return err
	}
	return nil
}

func (ts *TaskStore) Close() error {
	return ts.db.Close()
}

func (ts *TaskStore) CreateTask(conversationID, title string) (*types.Task, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := ts.db.Exec(
		`INSERT INTO tasks (id, conversation_id, title, done, created_at) VALUES (?, ?, ?, 0, ?)`,
		id, conversationID, title, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}

	return &types.Task{
		ID:             id,
		ConversationID: conversationID,
		Title:          title,
		Done:           false,
		CreatedAt:      now,
	}, nil
}

func (ts *TaskStore) CreateTaskWithDescription(conversationID, title, description string) (*types.Task, error) {
	id := uuid.New().String()
	now := time.Now()

	_, err := ts.db.Exec(
		`INSERT INTO tasks (id, conversation_id, title, description, done, created_at) VALUES (?, ?, ?, ?, 0, ?)`,
		id, conversationID, title, description, now,
	)
	if err != nil {
		return nil, fmt.Errorf("insert task: %w", err)
	}

	return &types.Task{
		ID:             id,
		ConversationID: conversationID,
		Title:          title,
		Description:    description,
		Done:           false,
		CreatedAt:      now,
	}, nil
}

func (ts *TaskStore) ListTasks(conversationID string) ([]types.Task, error) {
	rows, err := ts.db.Query(
		`SELECT id, conversation_id, title, description, done, created_at, done_at FROM tasks WHERE conversation_id = ? ORDER BY created_at ASC`,
		conversationID,
	)
	if err != nil {
		return nil, fmt.Errorf("query tasks: %w", err)
	}
	defer rows.Close()

	var tasks []types.Task
	for rows.Next() {
		var t types.Task
		var done int
		var doneAt sql.NullTime

		if err := rows.Scan(&t.ID, &t.ConversationID, &t.Title, &t.Description, &done, &t.CreatedAt, &doneAt); err != nil {
			return nil, fmt.Errorf("scan task: %w", err)
		}
		t.Done = done == 1
		if doneAt.Valid {
			t.DoneAt = &doneAt.Time
		}
		tasks = append(tasks, t)
	}
	if tasks == nil {
		tasks = []types.Task{}
	}
	return tasks, nil
}

func (ts *TaskStore) ToggleTask(id string) (*types.Task, error) {
	var done int
	var t types.Task
	var doneAt sql.NullTime

	err := ts.db.QueryRow(
		`SELECT id, conversation_id, title, description, done, created_at, done_at FROM tasks WHERE id = ?`, id,
	).Scan(&t.ID, &t.ConversationID, &t.Title, &t.Description, &done, &t.CreatedAt, &doneAt)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("task not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query task: %w", err)
	}

	newDone := 1 - done
	var newDoneAt *time.Time
	if newDone == 1 {
		now := time.Now()
		newDoneAt = &now
	}

	_, err = ts.db.Exec(
		`UPDATE tasks SET done = ?, done_at = ? WHERE id = ?`,
		newDone, newDoneAt, id,
	)
	if err != nil {
		return nil, fmt.Errorf("update task: %w", err)
	}

	t.Done = newDone == 1
	t.DoneAt = newDoneAt
	return &t, nil
}

func (ts *TaskStore) DeleteTask(id string) error {
	res, err := ts.db.Exec(`DELETE FROM tasks WHERE id = ?`, id)
	if err != nil {
		return fmt.Errorf("delete task: %w", err)
	}
	n, _ := res.RowsAffected()
	if n == 0 {
		return fmt.Errorf("task not found")
	}
	return nil
}

func (ts *TaskStore) ClearCompleted(conversationID string) (int, error) {
	res, err := ts.db.Exec(`DELETE FROM tasks WHERE conversation_id = ? AND done = 1`, conversationID)
	if err != nil {
		return 0, fmt.Errorf("clear completed: %w", err)
	}
	n, _ := res.RowsAffected()
	return int(n), nil
}

func (ts *TaskStore) GetSummary(conversationID string) (*types.TaskSummary, error) {
	var total, pending, completed int
	err := ts.db.QueryRow(
		`SELECT COUNT(*), SUM(CASE WHEN done = 0 THEN 1 ELSE 0 END), SUM(CASE WHEN done = 1 THEN 1 ELSE 0 END) FROM tasks WHERE conversation_id = ?`,
		conversationID,
	).Scan(&total, &pending, &completed)
	if err != nil {
		return nil, fmt.Errorf("query summary: %w", err)
	}
	return &types.TaskSummary{
		Total:     total,
		Pending:   pending,
		Completed: completed,
	}, nil
}
