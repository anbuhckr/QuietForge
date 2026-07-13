package storage

import (
	"database/sql"
	"encoding/json"
	"fmt"
)

type Repository struct {
	DB *Database
}

func NewRepository(db *Database) *Repository {
	return &Repository{DB: db}
}

func (r *Repository) Close() error {
	if r.DB != nil {
		return r.DB.Close()
	}
	return nil
}

func (r *Repository) UpsertSession(session SessionRow) error {
	meta, err := json.Marshal(session.Metadata)
	if err != nil {
		meta = []byte("{}")
	}
	_, err = r.DB.Conn.Exec(`
		INSERT INTO sessions (id, agent_id, workspace, created_at, updated_at, metadata, prompt_tokens, completion_tokens)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			agent_id = excluded.agent_id,
			workspace = excluded.workspace,
			updated_at = excluded.updated_at,
			metadata = excluded.metadata,
			prompt_tokens = excluded.prompt_tokens,
			completion_tokens = excluded.completion_tokens
	`, session.ID, session.AgentID, session.Workspace, session.CreatedAt, session.UpdatedAt, string(meta), session.PromptTokens, session.CompletionTokens)
	return err
}

func (r *Repository) GetSession(sessionID string) (*SessionRow, error) {
	row := r.DB.Conn.QueryRow(`SELECT id, agent_id, workspace, created_at, updated_at, metadata, prompt_tokens, completion_tokens FROM sessions WHERE id LIKE ?`, sessionID+"%")
	var s SessionRow
	var metaStr string
	err := row.Scan(&s.ID, &s.AgentID, &s.Workspace, &s.CreatedAt, &s.UpdatedAt, &metaStr, &s.PromptTokens, &s.CompletionTokens)
	if err != nil {
		return nil, err
	}
	if metaStr != "" {
		json.Unmarshal([]byte(metaStr), &s.Metadata)
	}
	return &s, nil
}

func (r *Repository) CreateMessage(message MessageRow, parts []MessagePartRow) error {
	meta, err := json.Marshal(message.Metadata)
	if err != nil {
		meta = []byte("{}")
	}
	tx, err := r.DB.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec(
		"INSERT INTO messages (id, session_id, role, created_at, metadata) VALUES (?, ?, ?, ?, ?)",
		message.ID, message.SessionID, message.Role, message.CreatedAt, string(meta),
	)
	if err != nil {
		return err
	}
	for _, part := range parts {
		_, err = tx.Exec(
			"INSERT INTO message_parts (message_id, type, content, tool_call_id, tool_name, arguments) VALUES (?, ?, ?, ?, ?, ?)",
			part.MessageID, part.Type, nullString(part.Content), nullString(part.ToolCallID), nullString(part.ToolName), nullString(part.Arguments),
		)
		if err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (r *Repository) UpdateMessageMetadata(messageID string, metadata map[string]any) error {
	meta, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = r.DB.Conn.Exec("UPDATE messages SET metadata = ? WHERE id = ?", string(meta), messageID)
	return err
}

func (r *Repository) AppendDisplayMessage(sessionID, messageID, role, parts, metadata string, createdAt int64) error {
	_, err := r.DB.Conn.Exec(
		"INSERT INTO display_log (session_id, message_id, role, parts, metadata, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		sessionID, messageID, role, parts, metadata, createdAt,
	)
	return err
}

func (r *Repository) GetDisplayLog(sessionID string) ([]map[string]any, error) {
	rows, err := r.DB.Conn.Query(`SELECT message_id, role, parts, metadata, created_at FROM display_log WHERE session_id = ? ORDER BY id ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var result []map[string]any
	for rows.Next() {
		var messageID, role, partsStr, metaStr string
		var createdAt int64
		if err := rows.Scan(&messageID, &role, &partsStr, &metaStr, &createdAt); err != nil {
			return nil, err
		}

		var parts []any
		if partsStr != "" {
			json.Unmarshal([]byte(partsStr), &parts)
		}
		var meta map[string]any
		if metaStr != "" {
			json.Unmarshal([]byte(metaStr), &meta)
		}

		entry := map[string]any{
			"id":         messageID,
			"role":       role,
			"parts":      parts,
			"created_at": createdAt,
		}
		if meta != nil {
			entry["metadata"] = meta
			if snap, ok := meta["snapshot"]; ok {
				entry["snapshot"] = snap
			}
			if runMeta, ok := meta["run_meta"]; ok {
				entry["run_meta"] = runMeta
			}
		}
		result = append(result, entry)
	}
	return result, rows.Err()
}

func (r *Repository) UpdateDisplayMessageMetadata(messageID string, metadata map[string]any) error {
	meta, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = r.DB.Conn.Exec("UPDATE display_log SET metadata = ? WHERE message_id = ?", string(meta), messageID)
	return err
}

func (r *Repository) DeleteMessagesAfter(sessionID string, createdAt int64) error {
	if _, err := r.DB.Conn.Exec("DELETE FROM messages WHERE session_id = ? AND created_at >= ?", sessionID, createdAt); err != nil {
		return err
	}
	_, err := r.DB.Conn.Exec("DELETE FROM display_log WHERE session_id = ? AND created_at >= ?", sessionID, createdAt)
	return err
}

func (r *Repository) GetMessages(sessionID string) ([]MessageRow, error) {
	rows, err := r.DB.Conn.Query(`SELECT id, session_id, role, created_at, metadata FROM messages WHERE session_id = ? ORDER BY created_at ASC`, sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var msgs []MessageRow
	for rows.Next() {
		var m MessageRow
		var metaStr string
		if err := rows.Scan(&m.ID, &m.SessionID, &m.Role, &m.CreatedAt, &metaStr); err != nil {
			return nil, err
		}
		if metaStr != "" {
			json.Unmarshal([]byte(metaStr), &m.Metadata)
		}
		msgs = append(msgs, m)
	}
	return msgs, rows.Err()
}

func (r *Repository) GetMessageParts(messageID string) ([]MessagePartRow, error) {
	rows, err := r.DB.Conn.Query(`SELECT id, message_id, type, content, tool_call_id, tool_name, arguments FROM message_parts WHERE message_id = ? ORDER BY id ASC`, messageID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var parts []MessagePartRow
	for rows.Next() {
		var p MessagePartRow
		var content, toolCallID, toolName, args sql.NullString
		if err := rows.Scan(&p.ID, &p.MessageID, &p.Type, &content, &toolCallID, &toolName, &args); err != nil {
			return nil, err
		}
		p.Content = content.String
		p.ToolCallID = toolCallID.String
		p.ToolName = toolName.String
		p.Arguments = args.String
		parts = append(parts, p)
	}
	return parts, rows.Err()
}

func (r *Repository) CreateTodo(todo TodoRow) error {
	_, err := r.DB.Conn.Exec(
		"INSERT OR IGNORE INTO todos (id, session_id, content, status, created_at) VALUES (?, ?, ?, ?, ?)",
		todo.ID, todo.SessionID, todo.Content, todo.Status, todo.CreatedAt,
	)
	return err
}

func (r *Repository) UpdateTodo(todoID string, updates map[string]any) error {
	for key, value := range updates {
		var err error
		if key == "status" {
			status, _ := value.(string)
			_, err = r.DB.Conn.Exec(
				"UPDATE todos SET status = ?, completed_at = CASE WHEN ? = 'completed' THEN ? ELSE completed_at END WHERE id = ?",
				status, status, 0, todoID,
			)
		} else {
			_, err = r.DB.Conn.Exec(
				fmt.Sprintf("UPDATE todos SET %s = ? WHERE id = ?", key), value, todoID,
			)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) ListSessions(limit int, workspace string) ([]SessionRow, error) {
	var rows *sql.Rows
	var err error
	if workspace != "" {
		rows, err = r.DB.Conn.Query(
			"SELECT id, agent_id, workspace, created_at, updated_at, metadata, prompt_tokens, completion_tokens FROM sessions WHERE workspace = ? ORDER BY updated_at DESC LIMIT ?",
			workspace, limit,
		)
	} else {
		rows, err = r.DB.Conn.Query(
			"SELECT id, agent_id, workspace, created_at, updated_at, metadata, prompt_tokens, completion_tokens FROM sessions ORDER BY updated_at DESC LIMIT ?",
			limit,
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var sessions []SessionRow
	for rows.Next() {
		var s SessionRow
		var metaStr string
		if err := rows.Scan(&s.ID, &s.AgentID, &s.Workspace, &s.CreatedAt, &s.UpdatedAt, &metaStr, &s.PromptTokens, &s.CompletionTokens); err != nil {
			return nil, err
		}
		if metaStr != "" {
			json.Unmarshal([]byte(metaStr), &s.Metadata)
		}
		sessions = append(sessions, s)
	}
	return sessions, rows.Err()
}

func (r *Repository) ListTodos(sessionID string) ([]TodoRow, error) {
	rows, err := r.DB.Conn.Query("SELECT id, session_id, content, status, created_at, completed_at FROM todos WHERE session_id = ? ORDER BY created_at ASC", sessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var todos []TodoRow
	for rows.Next() {
		var t TodoRow
		var completedAt sql.NullInt64
		if err := rows.Scan(&t.ID, &t.SessionID, &t.Content, &t.Status, &t.CreatedAt, &completedAt); err != nil {
			return nil, err
		}
		if completedAt.Valid {
			t.CompletedAt = completedAt.Int64
		}
		todos = append(todos, t)
	}
	return todos, rows.Err()
}

func (r *Repository) DeleteSession(sessionID string) (bool, error) {
	row := r.DB.Conn.QueryRow("SELECT id FROM sessions WHERE id LIKE ?", sessionID+"%")
	var actualID string
	if err := row.Scan(&actualID); err != nil {
		return false, nil
	}

	tx, err := r.DB.Conn.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM message_parts WHERE message_id IN (SELECT id FROM messages WHERE session_id = ?)", actualID); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM messages WHERE session_id = ?", actualID); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM todos WHERE session_id = ?", actualID); err != nil {
		return false, err
	}
	if _, err := tx.Exec("DELETE FROM sessions WHERE id = ?", actualID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func (r *Repository) ReplaceMessages(sessionID string, messages []MessageRow, partsByMessage map[string][]MessagePartRow) error {
	tx, err := r.DB.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM message_parts WHERE message_id IN (SELECT id FROM messages WHERE session_id = ?)", sessionID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM messages WHERE session_id = ?", sessionID); err != nil {
		return err
	}
	for _, message := range messages {
		meta, err := json.Marshal(message.Metadata)
		if err != nil {
			meta = []byte("{}")
		}
		if _, err := tx.Exec(
			"INSERT INTO messages (id, session_id, role, created_at, metadata) VALUES (?, ?, ?, ?, ?)",
			message.ID, message.SessionID, message.Role, message.CreatedAt, string(meta),
		); err != nil {
			return err
		}
		for _, part := range partsByMessage[message.ID] {
			if _, err := tx.Exec(
				"INSERT INTO message_parts (message_id, type, content, tool_call_id, tool_name, arguments) VALUES (?, ?, ?, ?, ?, ?)",
				part.MessageID, part.Type, nullString(part.Content), nullString(part.ToolCallID), nullString(part.ToolName), nullString(part.Arguments),
			); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

func (r *Repository) UpsertWorkspaceFile(file WorkspaceFileRow) error {
	_, err := r.DB.Conn.Exec(`
		INSERT INTO workspace_files (workspace, path, purpose, file_hash, updated_at)
		VALUES (?, ?, ?, ?, ?)
		ON CONFLICT(workspace, path) DO UPDATE SET
			purpose = COALESCE(excluded.purpose, purpose),
			file_hash = excluded.file_hash,
			updated_at = excluded.updated_at
	`, file.Workspace, file.Path, file.Purpose, file.FileHash, file.UpdatedAt)
	return err
}

func (r *Repository) UpsertWorkspaceSymbol(sym WorkspaceSymbolRow) error {
	_, err := r.DB.Conn.Exec(`
		INSERT INTO workspace_symbols (id, workspace, path, name, type, line_start, line_end, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			name = excluded.name,
			type = excluded.type,
			line_start = excluded.line_start,
			line_end = excluded.line_end,
			updated_at = excluded.updated_at
	`, sym.ID, sym.Workspace, sym.Path, sym.Name, sym.Type, sym.LineStart, sym.LineEnd, sym.UpdatedAt)
	return err
}

func (r *Repository) DeleteWorkspaceSymbolsByPath(workspace, path string) error {
	_, err := r.DB.Conn.Exec("DELETE FROM workspace_symbols WHERE workspace = ? AND path = ?", workspace, path)
	return err
}

func (r *Repository) ListWorkspaceSymbols(workspace string) ([]WorkspaceSymbolRow, error) {
	rows, err := r.DB.Conn.Query("SELECT id, workspace, path, name, type, line_start, line_end, updated_at FROM workspace_symbols WHERE workspace = ? ORDER BY path, line_start", workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var syms []WorkspaceSymbolRow
	for rows.Next() {
		var s WorkspaceSymbolRow
		if err := rows.Scan(&s.ID, &s.Workspace, &s.Path, &s.Name, &s.Type, &s.LineStart, &s.LineEnd, &s.UpdatedAt); err != nil {
			return nil, err
		}
		syms = append(syms, s)
	}
	return syms, rows.Err()
}


func (r *Repository) ListWorkspaceFiles(workspace string) ([]WorkspaceFileRow, error) {
	rows, err := r.DB.Conn.Query("SELECT path, workspace, purpose, file_hash, updated_at FROM workspace_files WHERE workspace = ? ORDER BY path ASC", workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var files []WorkspaceFileRow
	for rows.Next() {
		var f WorkspaceFileRow
		var purpose sql.NullString
		var hash sql.NullString
		if err := rows.Scan(&f.Path, &f.Workspace, &purpose, &hash, &f.UpdatedAt); err != nil {
			return nil, err
		}
		f.Purpose = purpose.String
		f.FileHash = hash.String
		files = append(files, f)
	}
	return files, rows.Err()
}

func (r *Repository) CreateArchitecture(arch ArchitectureRow) error {
	table := "architecture_decisions"
	if arch.Type == "constraint" {
		table = "architecture_constraints"
	}

	// id is primary key.
	query := fmt.Sprintf("INSERT INTO %s (id, workspace, %s, updated_at) VALUES (?, ?, ?, ?)", table, "decision")
	if arch.Type == "constraint" {
		query = fmt.Sprintf("INSERT INTO %s (id, workspace, %s, updated_at) VALUES (?, ?, ?, ?)", table, "constraint_text")
	}

	_, err := r.DB.Conn.Exec(query, arch.ID, arch.Workspace, arch.Text, arch.UpdatedAt)
	return err
}

func (r *Repository) ListArchitecture(workspace string, archType string) ([]ArchitectureRow, error) {
	table := "architecture_decisions"
	column := "decision"
	if archType == "constraint" {
		table = "architecture_constraints"
		column = "constraint_text"
	}

	query := fmt.Sprintf("SELECT id, workspace, %s, updated_at FROM %s WHERE workspace = ? ORDER BY updated_at ASC", column, table)
	rows, err := r.DB.Conn.Query(query, workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var archs []ArchitectureRow
	for rows.Next() {
		var a ArchitectureRow
		a.Type = archType
		if err := rows.Scan(&a.ID, &a.Workspace, &a.Text, &a.UpdatedAt); err != nil {
			return nil, err
		}
		archs = append(archs, a)
	}
	return archs, rows.Err()
}

func (r *Repository) SyncWorkspaceFacts(workspace string, path string, hash string, symbols []WorkspaceSymbolRow, edges []WorkspaceEdgeRow) error {
	tx, err := r.DB.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// 1. Delete old symbols & edges
	_, err = tx.Exec("DELETE FROM workspace_symbols WHERE workspace = ? AND path = ?", workspace, path)
	if err != nil {
		return err
	}
	_, err = tx.Exec("DELETE FROM workspace_edges WHERE workspace = ? AND source_path = ?", workspace, path)
	if err != nil {
		return err
	}

	var updated int64 = 0
	if len(symbols) > 0 {
		updated = symbols[0].UpdatedAt
	}

	// 2. Upsert File Hash (keep purpose intact)
	_, err = tx.Exec(`
		INSERT INTO workspace_files (workspace, path, file_hash, updated_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(workspace, path) DO UPDATE SET
			file_hash = excluded.file_hash,
			updated_at = excluded.updated_at
	`, workspace, path, hash, updated)
	if err != nil {
		return err
	}

	// 3. Insert new symbols
	for _, sym := range symbols {
		_, err = tx.Exec(`
			INSERT INTO workspace_symbols (id, workspace, path, name, type, line_start, line_end, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		`, sym.ID, sym.Workspace, sym.Path, sym.Name, sym.Type, sym.LineStart, sym.LineEnd, sym.UpdatedAt)
		if err != nil {
			return err
		}
	}

	// 4. Insert new edges
	for _, edge := range edges {
		_, err = tx.Exec(`
			INSERT INTO workspace_edges (id, workspace, source_path, target_path, edge_type)
			VALUES (?, ?, ?, ?, ?)
		`, edge.ID, edge.Workspace, edge.SourcePath, edge.TargetPath, edge.EdgeType)
		if err != nil {
			return err
		}
	}

	return tx.Commit()
}

func (r *Repository) DeleteWorkspaceFileAndFacts(workspace string, path string) error {
	tx, err := r.DB.Conn.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	_, err = tx.Exec("DELETE FROM workspace_symbols WHERE workspace = ? AND path = ?", workspace, path)
	if err != nil { return err }
	_, err = tx.Exec("DELETE FROM workspace_edges WHERE workspace = ? AND source_path = ?", workspace, path)
	if err != nil { return err }
	_, err = tx.Exec("DELETE FROM workspace_files WHERE workspace = ? AND path = ?", workspace, path)
	if err != nil { return err }

	return tx.Commit()
}

func (r *Repository) GetSymbolByName(workspace, name string) ([]WorkspaceSymbolRow, error) {
	rows, err := r.DB.Conn.Query("SELECT id, workspace, path, name, type, line_start, line_end, updated_at FROM workspace_symbols WHERE workspace = ? AND name = ? ORDER BY path, line_start", workspace, name)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var syms []WorkspaceSymbolRow
	for rows.Next() {
		var s WorkspaceSymbolRow
		if err := rows.Scan(&s.ID, &s.Workspace, &s.Path, &s.Name, &s.Type, &s.LineStart, &s.LineEnd, &s.UpdatedAt); err != nil {
			return nil, err
		}
		syms = append(syms, s)
	}
	return syms, rows.Err()
}

func (r *Repository) GetIncomingEdges(workspace, targetPath string) ([]WorkspaceEdgeRow, error) {
	rows, err := r.DB.Conn.Query("SELECT id, workspace, source_path, target_path, edge_type FROM workspace_edges WHERE workspace = ? AND target_path = ?", workspace, targetPath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []WorkspaceEdgeRow
	for rows.Next() {
		var e WorkspaceEdgeRow
		if err := rows.Scan(&e.ID, &e.Workspace, &e.SourcePath, &e.TargetPath, &e.EdgeType); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}

func (r *Repository) GetOutgoingEdges(workspace, sourcePath string) ([]WorkspaceEdgeRow, error) {
	rows, err := r.DB.Conn.Query("SELECT id, workspace, source_path, target_path, edge_type FROM workspace_edges WHERE workspace = ? AND source_path = ?", workspace, sourcePath)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var edges []WorkspaceEdgeRow
	for rows.Next() {
		var e WorkspaceEdgeRow
		if err := rows.Scan(&e.ID, &e.Workspace, &e.SourcePath, &e.TargetPath, &e.EdgeType); err != nil {
			return nil, err
		}
		edges = append(edges, e)
	}
	return edges, rows.Err()
}
