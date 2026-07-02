package implement

import (
	"encoding/json"
	"fmt"
	"path/filepath"
	"quietforge/storage"
	"quietforge/tool"
	"quietforge/util"
)

type LspTool struct{}

func (t *LspTool) ID() string {
	return "lsp"
}

func (t *LspTool) Description() string {
	return "Query the Language Server Protocol for code intelligence."
}

func (t *LspTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":   map[string]interface{}{"type": "string", "enum": []string{"definition", "references", "hover", "symbols"}},
			"filePath": map[string]interface{}{"type": "string", "description": "Path to source file"},
			"line":     map[string]interface{}{"type": "integer", "description": "Line number (0-indexed)"},
			"column":   map[string]interface{}{"type": "integer", "description": "Column number"},
		},
		"required": []string{"action", "filePath"},
	}
}

func (t *LspTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Action   string `json:"action"`
		FilePath string `json:"filePath"`
		Line     int    `json:"line"`
		Column   int    `json:"column"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	filePath, err := util.JailPath(ctx.Workspace, params.FilePath)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	fileURI := "file://" + filepath.ToSlash(filePath)
	pos := map[string]any{"line": params.Line, "character": params.Column}
	textDoc := map[string]any{"uri": fileURI}

	repo := ctx.Extra["repo"].(*storage.Repository)
	srv, _, err := tool.GlobalLspManager.GetServer(ctx.Workspace, filePath, repo)
	if err != nil || srv == nil {
		return &tool.ToolResult{Error: "lsp_error", Output: fmt.Sprintf("Failed to get LSP server: %v", err)}, nil
	}

	var output string
	switch params.Action {
	case "definition":
		res, err := srv.Call("textDocument/definition", map[string]any{
			"textDocument": textDoc,
			"position":     pos,
		})
		if err != nil {
			return &tool.ToolResult{Error: "lsp_error", Output: err.Error()}, nil
		}
		output = string(res)

	case "references":
		res, err := srv.Call("textDocument/references", map[string]any{
			"textDocument": textDoc,
			"position":     pos,
			"context":      map[string]any{"includeDeclaration": true},
		})
		if err != nil {
			return &tool.ToolResult{Error: "lsp_error", Output: err.Error()}, nil
		}
		output = string(res)

	case "hover":
		res, err := srv.Call("textDocument/hover", map[string]any{
			"textDocument": textDoc,
			"position":     pos,
		})
		if err != nil {
			return &tool.ToolResult{Error: "lsp_error", Output: err.Error()}, nil
		}
		output = string(res)

	case "symbols":
		res, err := srv.Call("textDocument/documentSymbol", map[string]any{
			"textDocument": textDoc,
		})
		if err != nil {
			return &tool.ToolResult{Error: "lsp_error", Output: err.Error()}, nil
		}
		output = string(res)

	default:
		return &tool.ToolResult{Error: "invalid_action", Output: "Unsupported action"}, nil
	}

	return &tool.ToolResult{Output: output}, nil
}
