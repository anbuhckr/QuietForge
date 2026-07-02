package context

type ContextRequest struct {
	Workspace string
	SessionID string
	Prompt    string
	Intent    string
	ToolName  string // e.g. "shell" (for diagnostic requests)
	Output    string // Raw tool output
}

type ContextFragment struct {
	ProviderID string  `json:"provider_id"`
	ID         string  `json:"id"`
	Priority   float64 `json:"-"`
	TokenCost  int     `json:"-"`
	Confidence float64 `json:"-"`
	Data       any     `json:"data"`
}

type ContextProvider interface {
	ID() string
	SoftLimit() int
	Gather(req ContextRequest) ([]ContextFragment, error)
}
