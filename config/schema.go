package config

// PermissionRule defines access control.
type PermissionRule struct {
	Permission string `json:"permission"`
	Pattern    string `json:"pattern"`
	Action     string `json:"action"` // "allow", "ask", "deny"
	Always     bool   `json:"always"`
}

// ModelConfig defines the LLM model settings.
type ModelConfig struct {
	ID         string  `json:"id"`
	ProviderID string  `json:"provider_id"`
	Variant    *string `json:"variant,omitempty"`
}

type ProviderConfig struct {
	Model         *string                `json:"model,omitempty"`
	APIKey        *string                `json:"api_key,omitempty"`
	BaseURL       *string                `json:"base_url,omitempty"`
	DisableVision *bool                  `json:"disable_vision,omitempty"`
	ContextWindow *int                   `json:"context_window,omitempty"`
	MaxMessages   *int                   `json:"max_messages,omitempty"`
	Options       map[string]any 		 `json:"options"`
}

// AgentConfig represents agent-specific configuration.
type AgentConfig struct {
	Description *string   		`json:"description,omitempty"`
	Mode        *string   		`json:"mode,omitempty"` // "primary", "subagent", "all"
	Permission  map[string]any  `json:"permission,omitempty"`
	Model       *string   		`json:"model,omitempty"`
	Prompt      *string   		`json:"prompt,omitempty"`
	Temperature *float64  		`json:"temperature,omitempty"`
	TopP        *float64  		`json:"top_p,omitempty"`
	Disable     bool      		`json:"disable"`
	Hidden      *bool     		`json:"hidden,omitempty"`
	Steps       *int      		`json:"steps,omitempty"`
}

// CompactionConfig handles conversation history management.
type CompactionConfig struct {
	Auto                 bool `json:"auto"`
	TailTurns            int  `json:"tail_turns"`
	PreserveRecentTokens int  `json:"preserve_recent_tokens"`
	Reserved             int  `json:"reserved"`
	Prune                bool `json:"prune"`
	ToolTruncationLimit  int  `json:"tool_truncation_limit"`
}

// McpServerConfig and McpConfig define Model Context Protocol servers.
type McpServerConfig struct {
	Command     []string          `json:"command"`
	Type        string            `json:"type"` // "local"
	Cwd         *string           `json:"cwd,omitempty"`
	Environment map[string]string `json:"environment"`
	Disabled    bool              `json:"disabled"`
}

type McpConfig struct {
	Servers map[string]McpServerConfig `json:"servers"`
}

// Config is the top-level configuration object.
type Config struct {
	Model             *string                    `json:"model,omitempty"`
	ContextWindow     *int                       `json:"context_window,omitempty"`
	Provider          map[string]ProviderConfig  `json:"provider"`
	Agent             map[string]AgentConfig     `json:"agent"`
	Permission        map[string]any     		 `json:"permission"`
	DisabledProviders []string                   `json:"disabled_providers"`
	EnabledProviders  []string                   `json:"enabled_providers,omitempty"`
	Shell             *string                    `json:"shell,omitempty"`
	Mcp               *McpConfig                 `json:"mcp,omitempty"`
	Username          *string                    `json:"username,omitempty"`
	Compaction        *CompactionConfig          `json:"compaction,omitempty"`
	Instructions      []string                   `json:"instructions"`
	DefaultAgent      *string                    `json:"default_agent,omitempty"`
	Mode              map[string]any     		 `json:"mode"`
	Port              *int                       `json:"port,omitempty"`
	SSLPort           *int                       `json:"ssl_port,omitempty"`
	SSLCert           *string                    `json:"ssl_cert,omitempty"`
	SSLKey            *string                    `json:"ssl_key,omitempty"`
}