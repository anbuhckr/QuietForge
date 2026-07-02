package context

import (
	"fmt"
	"quietforge/storage"
)

type ArchitectureProvider struct {
	Repo *storage.Repository
}

func (p *ArchitectureProvider) ID() string {
	return "architecture"
}

func (p *ArchitectureProvider) SoftLimit() int {
	return 300
}

func (p *ArchitectureProvider) Gather(req ContextRequest) ([]ContextFragment, error) {
	if req.Workspace == "" {
		return nil, nil
	}

	rows, err := p.Repo.DB.Conn.Query("SELECT id, type, text FROM workspace_architecture WHERE workspace = ? AND scope = 'global'", req.Workspace)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fragments []ContextFragment
	for rows.Next() {
		var id, typ, text string
		rows.Scan(&id, &typ, &text)
		
		fragments = append(fragments, ContextFragment{
			ProviderID: p.ID(),
			ID:         fmt.Sprintf("arch:%s", id),
			Priority:   100.0, // Global architecture is critical
			Confidence: 1.0,
			TokenCost:  len(text) / 4, // Rough token estimation
			Data: map[string]string{
				"type": typ,
				"text": text,
			},
		})
	}
	return fragments, nil
}
