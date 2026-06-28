package storage

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"

	_ "modernc.org/sqlite"
)

type Database struct {
	Conn *sql.DB
	Path string
}

func NewDatabase(path string) (*Database, error) {
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, fmt.Errorf("failed to create database directory: %w", err)
	}

	// Use modernc.org/sqlite
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Set pragmas
	_, err = db.Exec("PRAGMA journal_mode=WAL; PRAGMA foreign_keys=ON;")
	if err != nil {
		return nil, fmt.Errorf("failed to set pragmas: %w", err)
	}

	d := &Database{
		Conn: db,
		Path: path,
	}

	if err := d.Migrate(); err != nil {
		return nil, fmt.Errorf("failed to migrate database: %w", err)
	}

	return d, nil
}

func (d *Database) Close() error {
	if d.Conn != nil {
		return d.Conn.Close()
	}
	return nil
}

func (d *Database) Migrate() error {
	query := `
		CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL DEFAULT 'build',
			workspace TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT DEFAULT '{}',
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			metadata TEXT DEFAULT '{}'
		);

		CREATE TABLE IF NOT EXISTS message_parts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			content TEXT,
			tool_call_id TEXT,
			tool_name TEXT,
			arguments TEXT,
			tool_result_id TEXT,
			is_error BOOLEAN DEFAULT 0
		);

		CREATE TABLE IF NOT EXISTS todos (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			status TEXT NOT NULL DEFAULT 'pending',
			content TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			completed_at INTEGER
		);

		CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id);
		CREATE INDEX IF NOT EXISTS idx_message_parts_message_id ON message_parts(message_id);
		CREATE INDEX IF NOT EXISTS idx_todos_session_id ON todos(session_id);
	`
	_, err := d.Conn.Exec(query)
	if err != nil {
		return err
	}
	// Migrate existing tables that may lack columns added later
	d.Conn.Exec("ALTER TABLE todos ADD COLUMN completed_at INTEGER")
	d.Conn.Exec("ALTER TABLE sessions ADD COLUMN prompt_tokens INTEGER NOT NULL DEFAULT 0")
	d.Conn.Exec("ALTER TABLE sessions ADD COLUMN completion_tokens INTEGER NOT NULL DEFAULT 0")
	return nil
}
