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
	_, err = r.DB.Conn.Exec(
		"INSERT INTO messages (id, session_id, role, created_at, metadata) VALUES (?, ?, ?, ?, ?)",
		message.ID, message.SessionID, message.Role, message.CreatedAt, string(meta),
	)
	if err != nil {
		return err
	}
	for _, part := range parts {
		_, err = r.DB.Conn.Exec(
			"INSERT INTO message_parts (message_id, type, content, tool_call_id, tool_name, arguments) VALUES (?, ?, ?, ?, ?, ?)",
			part.MessageID, part.Type, nullString(part.Content), nullString(part.ToolCallID), nullString(part.ToolName), nullString(part.Arguments),
		)
		if err != nil {
			return err
		}
	}
	return nil
}

func (r *Repository) UpdateMessageMetadata(messageID string, metadata map[string]any) error {
	meta, err := json.Marshal(metadata)
	if err != nil {
		return err
	}
	_, err = r.DB.Conn.Exec("UPDATE messages SET metadata = ? WHERE id = ?", string(meta), messageID)
	return err
}

func (r *Repository) DeleteMessagesAfter(sessionID string, createdAt int64) error {
	_, err := r.DB.Conn.Exec("DELETE FROM messages WHERE session_id = ? AND created_at >= ?", sessionID, createdAt)
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
		"INSERT INTO todos (id, session_id, content, status, created_at) VALUES (?, ?, ?, ?, ?)",
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
			t.Completedt = completedAt.Int64
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

	r.DB.Conn.Exec("DELETE FROM message_parts WHERE message_id IN (SELECT id FROM messages WHERE session_id = ?)", actualID)
	r.DB.Conn.Exec("DELETE FROM messages WHERE session_id = ?", actualID)
	r.DB.Conn.Exec("DELETE FROM todos WHERE session_id = ?", actualID)
	_, err := r.DB.Conn.Exec("DELETE FROM sessions WHERE id = ?", actualID)
	if err != nil {
		return false, err
	}
	return true, nil
}

func nullString(s string) sql.NullString {
	if s == "" {
		return sql.NullString{}
	}
	return sql.NullString{String: s, Valid: true}
}

