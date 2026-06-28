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
