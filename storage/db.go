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

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	db.SetConnMaxLifetime(0)

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
	queries := []string{
		`CREATE TABLE IF NOT EXISTS sessions (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL DEFAULT 'build',
			workspace TEXT,
			created_at INTEGER NOT NULL,
			updated_at INTEGER NOT NULL,
			metadata TEXT DEFAULT '{}',
			prompt_tokens INTEGER NOT NULL DEFAULT 0,
			completion_tokens INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS messages (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			role TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			metadata TEXT DEFAULT '{}'
		)`,
		`CREATE TABLE IF NOT EXISTS message_parts (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			message_id TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
			type TEXT NOT NULL,
			content TEXT,
			tool_call_id TEXT,
			tool_name TEXT,
			arguments TEXT,
			tool_result_id TEXT,
			is_error BOOLEAN DEFAULT 0
		)`,
		`CREATE TABLE IF NOT EXISTS todos (
			id TEXT PRIMARY KEY,
			session_id TEXT NOT NULL REFERENCES sessions(id) ON DELETE CASCADE,
			status TEXT NOT NULL DEFAULT 'pending',
			content TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			completed_at INTEGER
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_embeddings (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			kind TEXT NOT NULL,
			object_id TEXT NOT NULL,
			chunk_index INTEGER NOT NULL,
			model TEXT NOT NULL,
			dimension INTEGER NOT NULL,
			hash TEXT NOT NULL,
			embedding BLOB NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_embeddings_ws ON workspace_embeddings(workspace)`,
		`CREATE INDEX IF NOT EXISTS idx_messages_session_id ON messages(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_message_parts_message_id ON message_parts(message_id)`,
		`CREATE INDEX IF NOT EXISTS idx_todos_session_id ON todos(session_id)`,
		`CREATE TABLE IF NOT EXISTS workspace_files_v2 (
			workspace TEXT NOT NULL,
			path TEXT NOT NULL,
			purpose TEXT,
			file_hash TEXT,
			updated_at INTEGER NOT NULL,
			PRIMARY KEY (workspace, path)
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_symbols (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			path TEXT NOT NULL,
			name TEXT NOT NULL,
			type TEXT NOT NULL,
			line_start INTEGER,
			line_end INTEGER,
			updated_at INTEGER NOT NULL,
			FOREIGN KEY (workspace, path) REFERENCES workspace_files_v2(workspace, path) ON DELETE CASCADE
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_edges (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			source_path TEXT NOT NULL,
			target_path TEXT NOT NULL,
			edge_type TEXT NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS workspace_diagnostics (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			path TEXT NOT NULL,
			symbol TEXT,
			source TEXT,
			status TEXT DEFAULT 'active',
			severity TEXT NOT NULL,
			message TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_symbols_path ON workspace_symbols(workspace, path)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_edges_source ON workspace_edges(workspace, source_path)`,
		`CREATE TABLE IF NOT EXISTS display_log (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			session_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			role TEXT NOT NULL,
			parts TEXT NOT NULL DEFAULT '[]',
			metadata TEXT NOT NULL DEFAULT '{}',
			created_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_display_log_session ON display_log(session_id)`,
		`CREATE INDEX IF NOT EXISTS idx_display_log_message ON display_log(message_id)`,
		`CREATE TABLE IF NOT EXISTS architecture_decisions_v2 (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			decision TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE TABLE IF NOT EXISTS architecture_constraints_v2 (
			id TEXT PRIMARY KEY,
			workspace TEXT NOT NULL,
			constraint_text TEXT NOT NULL,
			updated_at INTEGER NOT NULL
		)`,
		`CREATE INDEX IF NOT EXISTS idx_workspace_files_workspace ON workspace_files_v2(workspace)`,
		`CREATE INDEX IF NOT EXISTS idx_arch_dec_workspace ON architecture_decisions_v2(workspace)`,
		`CREATE INDEX IF NOT EXISTS idx_arch_con_workspace ON architecture_constraints_v2(workspace)`,
	}
	for _, q := range queries {
		if _, err := d.Conn.Exec(q); err != nil {
			return err
		}
	}
	// Run column migration only for tables/columns that may be missing from older schemas
	migrateColumnIfMissing := func(table, column, clause string) {
		var count int
		if err := d.Conn.QueryRow("SELECT COUNT(*) FROM pragma_table_info(?) WHERE name=?", table, column).Scan(&count); err != nil || count > 0 {
			return
		}
		d.Conn.Exec("ALTER TABLE " + table + " ADD COLUMN " + clause)
	}

	migrateColumnIfMissing("todos", "completed_at", "completed_at INTEGER")
	migrateColumnIfMissing("sessions", "prompt_tokens", "prompt_tokens INTEGER NOT NULL DEFAULT 0")
	migrateColumnIfMissing("sessions", "completion_tokens", "completion_tokens INTEGER NOT NULL DEFAULT 0")
	migrateColumnIfMissing("workspace_files", "file_hash", "file_hash TEXT")
	migrateColumnIfMissing("workspace_diagnostics", "symbol", "symbol TEXT")
	migrateColumnIfMissing("workspace_diagnostics", "source", "source TEXT")
	migrateColumnIfMissing("workspace_diagnostics", "status", "status TEXT DEFAULT 'active'")

	// Perform zero-downtime internal migration for workspace memory to bind to workspace instead of session_id
	var err error
	var tableExists bool
	err = d.Conn.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='workspace_files')").Scan(&tableExists)
	if err == nil && tableExists {
		// Check if it has 'symbols' column
		var hasSymbols bool
		d.Conn.QueryRow("SELECT EXISTS(SELECT 1 FROM pragma_table_info('workspace_files') WHERE name='symbols')").Scan(&hasSymbols)
		
		if hasSymbols {
			d.Conn.Exec(`
				INSERT OR IGNORE INTO workspace_files_v2 (workspace, path, purpose, updated_at)
				SELECT workspace, path, purpose, updated_at
				FROM workspace_files;
			`)
			d.Conn.Exec("DROP TABLE workspace_files")
		} else {
			// Already stripped
			d.Conn.Exec(`
				INSERT OR IGNORE INTO workspace_files_v2 (workspace, path, purpose, updated_at)
				SELECT workspace, path, purpose, updated_at
				FROM workspace_files;
			`)
			d.Conn.Exec("DROP TABLE workspace_files")
		}
	}
	// Always rename v2 to primary if we just created it or after drop
	var v2Exists bool
	d.Conn.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='workspace_files_v2')").Scan(&v2Exists)
	if v2Exists {
		d.Conn.Exec("ALTER TABLE workspace_files_v2 RENAME TO workspace_files")
	}

	// Migrate architecture_decisions
	err = d.Conn.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='architecture_decisions')").Scan(&tableExists)
	if err == nil && tableExists {
		d.Conn.Exec(`
			INSERT OR IGNORE INTO architecture_decisions_v2 (id, workspace, decision, updated_at)
			SELECT w.id, COALESCE(s.workspace, 'default_workspace'), w.decision, w.updated_at
			FROM architecture_decisions w
			LEFT JOIN sessions s ON w.session_id = s.id;
		`)
		d.Conn.Exec("DROP TABLE architecture_decisions")
	}
	d.Conn.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='architecture_decisions_v2')").Scan(&v2Exists)
	if v2Exists {
		d.Conn.Exec("ALTER TABLE architecture_decisions_v2 RENAME TO architecture_decisions")
	}

	// Migrate architecture_constraints
	err = d.Conn.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='architecture_constraints')").Scan(&tableExists)
	if err == nil && tableExists {
		d.Conn.Exec(`
			INSERT OR IGNORE INTO architecture_constraints_v2 (id, workspace, constraint_text, updated_at)
			SELECT w.id, COALESCE(s.workspace, 'default_workspace'), w.constraint_text, w.updated_at
			FROM architecture_constraints w
			LEFT JOIN sessions s ON w.session_id = s.id;
		`)
		d.Conn.Exec("DROP TABLE architecture_constraints")
	}
	d.Conn.QueryRow("SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='architecture_constraints_v2')").Scan(&v2Exists)
	if v2Exists {
		d.Conn.Exec("ALTER TABLE architecture_constraints_v2 RENAME TO architecture_constraints")
	}

	return nil
}
