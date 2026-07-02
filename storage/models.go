package storage

type SessionRow struct {
	ID               string                 `json:"id"`
	AgentID          string                 `json:"agent_id"`
	Workspace        string                 `json:"workspace"`
	CreatedAt        int64                  `json:"created_at"`
	UpdatedAt        int64                  `json:"updated_at"`
	Metadata         map[string]any         `json:"metadata"`
	PromptTokens     int                    `json:"prompt_tokens"`
	CompletionTokens int                    `json:"completion_tokens"`
}

type MessageRow struct {
	ID        string                 `json:"id"`
	SessionID string                 `json:"session_id"`
	Role      string                 `json:"role"`
	CreatedAt int64                  `json:"created_at"`
	Metadata  map[string]any 		 `json:"metadata"`
}

type MessagePartRow struct {
	ID           int    `json:"id"`
	MessageID    string `json:"message_id"`
	Type         string `json:"type"`
	Content      string `json:"content"`
	ToolCallID   string `json:"tool_call_id"`
	ToolName     string `json:"tool_name"`
	Arguments    string `json:"arguments"`
	Metadata     map[string]any `json:"metadata"`
}

type TodoRow struct {
	ID         string `json:"id"`
	SessionID  string `json:"session_id"`
	Content    string `json:"content"`
	Status     string `json:"status"`
	CreatedAt  int64  `json:"created_at"`
	Completedt int64  `json:"completed_at"`
}

type WorkspaceFileRow struct {
	Path      string `json:"path"`
	Workspace string `json:"workspace"`
	Purpose   string `json:"purpose"`
	FileHash  string `json:"file_hash"`
	UpdatedAt int64  `json:"updated_at"`
}

type WorkspaceSymbolRow struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
	Name      string `json:"name"`
	Type      string `json:"type"`
	LineStart int    `json:"line_start"`
	LineEnd   int    `json:"line_end"`
	UpdatedAt int64  `json:"updated_at"`
}

type WorkspaceEdgeRow struct {
	ID         string `json:"id"`
	Workspace  string `json:"workspace"`
	SourcePath string `json:"source_path"`
	TargetPath string `json:"target_path"`
	EdgeType   string `json:"edge_type"`
}

type WorkspaceDiagnosticRow struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Path      string `json:"path"`
	Symbol    string `json:"symbol"`
	Source    string `json:"source"`
	Status    string `json:"status"` // "active" or "resolved"
	Severity  string `json:"severity"`
	Message   string `json:"message"`
	UpdatedAt int64  `json:"updated_at"`
}

type ArchitectureRow struct {
	ID        string `json:"id"`
	Workspace string `json:"workspace"`
	Scope     string `json:"scope"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	UpdatedAt int64  `json:"updated_at"`
}
