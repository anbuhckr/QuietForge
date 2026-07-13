package implement

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"time"

	"quietforge/storage"
	"quietforge/tool"
)

type TodoWriteTool struct {
	Repo *storage.Repository
}

func (t *TodoWriteTool) ID() string {
	return "todowrite"
}

func (t *TodoWriteTool) Description() string {
	return "Create and manage a structured task list for the current session."
}

func (t *TodoWriteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":  map[string]interface{}{"type": "string", "enum": []string{"add", "update", "list"}, "description": "Action to perform"},
			"content": map[string]interface{}{"type": "string", "description": "Task description (required for add)"},
			"todo_id": map[string]interface{}{"type": "string", "description": "Todo ID to update"},
			"status":  map[string]interface{}{"type": "string", "enum": []string{"pending", "in_progress", "completed", "cancelled"}},
		},
		"required": []string{"action"},
	}
}

func (t *TodoWriteTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var repo *storage.Repository
	if t.Repo != nil {
		repo = t.Repo
	} else if r, ok := ctx.Extra["repo"].(*storage.Repository); ok {
		repo = r
	}
	if repo == nil {
		return &tool.ToolResult{Error: "not_initialized", Output: "Todo tool not initialized"}, nil
	}

	var params struct {
		Action  string `json:"action"`
		Content string `json:"content"`
		TodoID  string `json:"todo_id"`
		Status  string `json:"status"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	switch params.Action {
	case "add":
		if params.Content == "" {
			return &tool.ToolResult{Error: "missing_arg", Output: "Content is required"}, nil
		}
		b := make([]byte, 4)
		rand.Read(b)
		id := fmt.Sprintf("todo-%d-%x", time.Now().UnixNano(), b)
		todo := storage.TodoRow{
			ID:        id,
			SessionID: ctx.SessionID,
			Content:   params.Content,
			Status:    "pending",
			CreatedAt: time.Now().Unix(),
		}
		if err := repo.CreateTodo(todo); err != nil {
			return &tool.ToolResult{Error: "create_error", Output: fmt.Sprintf("Failed to create todo: %v", err)}, nil
		}
		return &tool.ToolResult{Title: "Todo added", Output: fmt.Sprintf("Added: %s", params.Content)}, nil

	case "update":
		if params.TodoID == "" || params.Status == "" {
			return &tool.ToolResult{Error: "missing_arg", Output: "todo_id and status required"}, nil
		}
		if err := repo.UpdateTodo(params.TodoID, map[string]any{"status": params.Status}); err != nil {
			return &tool.ToolResult{Error: "update_error", Output: fmt.Sprintf("Failed to update todo: %v", err)}, nil
		}
		return &tool.ToolResult{Output: fmt.Sprintf("Todo %s -> %s", params.TodoID, params.Status)}, nil

	case "list":
		todos, err := repo.ListTodos(ctx.SessionID)
		if err != nil || len(todos) == 0 {
			return &tool.ToolResult{Output: "(no todos)"}, nil
		}
		var lines string
		for _, td := range todos {
			m := "[?]"
			switch td.Status {
			case "pending":
				m = "[ ]"
			case "in_progress":
				m = "[~]"
			case "completed":
				m = "[x]"
			case "cancelled":
				m = "[-]"
			}
			lines += fmt.Sprintf("%s %s %s\n", m, td.ID, td.Content)
		}
		return &tool.ToolResult{Title: fmt.Sprintf("Todos (%d)", len(todos)), Output: lines}, nil
	}

	return &tool.ToolResult{Error: "invalid_arg", Output: fmt.Sprintf("Unknown action: %s", params.Action)}, nil
}
