package implement

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"quietforge/storage"
	"quietforge/tool"
	"quietforge/util"
)

type WriteTool struct{}

func (t *WriteTool) ID() string {
	return "write"
}

func (t *WriteTool) Description() string {
	return "Write a new file or overwrite an existing file. Creates parent directories automatically."
}

func (t *WriteTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filePath": map[string]interface{}{"type": "string", "description": "Absolute path to the file to write"},
			"content":  map[string]interface{}{"type": "string", "description": "The content to write to the file"},
		},
		"required": []string{"filePath", "content"},
	}
}

func (t *WriteTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		FilePath string `json:"filePath"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	pathStr, err := util.JailPath(ctx.Workspace, params.FilePath)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	if err := os.MkdirAll(filepath.Dir(pathStr), 0755); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: fmt.Sprintf("Failed to create directories: %v", err)}, nil
	}

	if err := os.WriteFile(pathStr, []byte(params.Content), 0644); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: fmt.Sprintf("Failed to write file: %v", err)}, nil
	}
	
	if repo, ok := ctx.Extra["repo"].(*storage.Repository); ok && repo != nil {
		tool.GlobalLspManager.NotifyFileChanged(ctx.Workspace, pathStr, params.Content, repo)
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Written %d bytes to %s", len(params.Content), pathStr),
		Output: fmt.Sprintf("File written: %s", pathStr),
		Metadata: map[string]interface{}{
			"size": len(params.Content),
		},
	}, nil
}
