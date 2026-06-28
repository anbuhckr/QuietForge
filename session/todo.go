package session

import (
	"fmt"
	"strings"
	"time"

	"quietforge/storage"
)

type TodoManager struct {
	Repo      *storage.Repository
	SessionID string
	Items     []storage.TodoRow
}

func NewTodoManager(repo *storage.Repository, sessionID string) *TodoManager {
	return &TodoManager{
		Repo:      repo,
		SessionID: sessionID,
		Items:     make([]storage.TodoRow, 0),
	}
}

func (m *TodoManager) Load() error {
	if m.Repo == nil {
		return nil
	}
	var err error
	m.Items, err = m.Repo.ListTodos(m.SessionID)
	return err
}

func (m *TodoManager) Add(content, status string) (storage.TodoRow, error) {
	if status == "" {
		status = "pending"
	}
	todo := storage.TodoRow{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		SessionID: m.SessionID,
		Content:   content,
		Status:    status,
		CreatedAt: time.Now().Unix(),
	}
	if m.Repo != nil {
		if err := m.Repo.CreateTodo(todo); err != nil {
			return todo, err
		}
	}
	m.Items = append(m.Items, todo)
	return todo, nil
}

func (m *TodoManager) Update(todoID, status string) bool {
	if m.Repo != nil {
		m.Repo.UpdateTodo(todoID, map[string]any{"status": status})
	}
	for i, item := range m.Items {
		if item.ID == todoID {
			m.Items[i].Status = status
			return true
		}
	}
	return false
}

func (m *TodoManager) List() ([]storage.TodoRow, error) {
	if len(m.Items) == 0 && m.Repo != nil {
		var err error
		m.Items, err = m.Repo.ListTodos(m.SessionID)
		if err != nil {
			return nil, err
		}
	}
	result := make([]storage.TodoRow, len(m.Items))
	copy(result, m.Items)
	return result, nil
}

func (m *TodoManager) GetStatus() string {
	if len(m.Items) == 0 {
		return ""
	}
	var lines []string
	for _, t := range m.Items {
		m := statusMarker(t.Status)
		shortID := t.ID
		if len(shortID) > 8 {
			shortID = shortID[:8]
		}
		lines = append(lines, fmt.Sprintf("%s %s %s", m, shortID, t.Content))
	}
	return strings.Join(lines, "\n")
}

func statusMarker(status string) string {
	switch status {
	case "pending":
		return "[ ]"
	case "in_progress":
		return "[~]"
	case "completed":
		return "[x]"
	case "cancelled":
		return "[-]"
	default:
		return "[?]"
	}
}
