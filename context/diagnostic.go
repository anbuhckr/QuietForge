package context

import (
	"fmt"
	"quietforge/storage"
)

type DiagnosticProvider struct {
	Repo *storage.Repository
}

func (p *DiagnosticProvider) ID() string {
	return "diagnostic"
}

func (p *DiagnosticProvider) SoftLimit() int {
	return 500
}

func (p *DiagnosticProvider) Gather(req ContextRequest) ([]ContextFragment, error) {
	if req.Workspace == "" {
		return nil, nil
	}

	// We don't parse raw text anymore. We query the DB for active errors.
	rows, err := p.Repo.DB.Conn.Query(`
		SELECT symbol, COUNT(*) as fail_count, MAX(message) as latest_err 
		FROM workspace_diagnostics 
		WHERE workspace = ? AND status = 'active' AND symbol IS NOT NULL
		GROUP BY symbol
	`, req.Workspace)

	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var fragments []ContextFragment
	for rows.Next() {
		var symbol, latestErr string
		var count int
		rows.Scan(&symbol, &count, &latestErr)
		
		fragments = append(fragments, ContextFragment{
			ProviderID: p.ID(),
			ID:         fmt.Sprintf("diag:%s", symbol),
			Priority:   95.0, // High priority
			Confidence: 1.0,
			TokenCost:  30,
			Data: map[string]any{
				"symbol":       symbol,
				"fail_count":   count,
				"latest_error": latestErr,
				"hint":         fmt.Sprintf("This symbol has failed compilation %d times. Please fix it.", count),
			},
		})
	}
	return fragments, nil
}
