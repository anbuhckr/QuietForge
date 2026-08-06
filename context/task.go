package context

import (
	"fmt"
	
)

type TaskProvider struct {
}

func (p *TaskProvider) ID() string {
	return "task"
}

func (p *TaskProvider) SoftLimit() int {
	return 300
}

func (p *TaskProvider) Gather(req ContextRequest) ([]ContextFragment, error) {
	if req.SessionID == "" || req.Workspace == "" {
		return nil, nil
	}
	repo, err := req.RepoResolver(req.Workspace)
	if err != nil || repo == nil {
		return nil, err
	}

	rows, err := repo.DB.Conn.Query("SELECT id, content FROM todos WHERE session_id = ? AND status = 'pending'", req.SessionID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fragments []ContextFragment
	for rows.Next() {
		var id, content string
		rows.Scan(&id, &content)
		
		fragments = append(fragments, ContextFragment{
			ProviderID: p.ID(),
			ID:         fmt.Sprintf("task:%s", id),
			Priority:   90.0, // High priority to remember tasks
			Confidence: 1.0,
			TokenCost:  len(content) / 4, // Rough token estimation
			Data: map[string]string{
				"task_id": id,
				"content": content,
				"status":  "pending",
			},
		})
	}
	return fragments, nil
}
