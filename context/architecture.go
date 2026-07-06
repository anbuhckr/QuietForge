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

	decisions, err := p.Repo.ListArchitecture(req.Workspace, "decision")
	if err != nil {
		return nil, err
	}
	constraints, err := p.Repo.ListArchitecture(req.Workspace, "constraint")
	if err != nil {
		return nil, err
	}

	var fragments []ContextFragment
	for _, a := range decisions {
		fragments = append(fragments, ContextFragment{
			ProviderID: p.ID(),
			ID:         fmt.Sprintf("arch:%s", a.ID),
			Priority:   100.0,
			Confidence: 1.0,
			TokenCost:  len(a.Text) / 4,
			Data: map[string]string{
				"type": "decision",
				"text": a.Text,
			},
		})
	}
	for _, a := range constraints {
		fragments = append(fragments, ContextFragment{
			ProviderID: p.ID(),
			ID:         fmt.Sprintf("arch:%s", a.ID),
			Priority:   100.0,
			Confidence: 1.0,
			TokenCost:  len(a.Text) / 4,
			Data: map[string]string{
				"type": "constraint",
				"text": a.Text,
			},
		})
	}
	return fragments, nil
}
