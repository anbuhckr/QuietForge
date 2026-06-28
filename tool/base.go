package tool

import (
	"context"
)

type ToolContext struct {
	SessionID   string
	MessageID   string
	Agent       string
	CallID      string
	Workspace   string
	Context     context.Context
	Extra       map[string]interface{}
}

type ToolResult struct {
	Title       string                   `json:"title,omitempty"`
	Output      string                   `json:"output,omitempty"`
	Metadata    map[string]interface{}   `json:"metadata,omitempty"`
	Attachments []map[string]interface{} `json:"attachments,omitempty"`
	Error       string                   `json:"error,omitempty"`
}

type Tool interface {
	ID() string
	Description() string
	Parameters() map[string]interface{}
	Execute(args []byte, ctx *ToolContext) (*ToolResult, error)
}
