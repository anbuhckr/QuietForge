package implement

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"quietforge/tool"
	"quietforge/util"
)

type WriteArtifactTool struct{}

func (t *WriteArtifactTool) ID() string {
	return "write_artifact"
}

func (t *WriteArtifactTool) Description() string {
	return "Write a structured markdown artifact (like an implementation plan) to the disk. The file will be automatically saved in the workspace's .agent/artifacts directory."
}

func (t *WriteArtifactTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"filename": map[string]interface{}{"type": "string", "description": "The name of the artifact file (e.g., implementation_plan.md)"},
			"content":  map[string]interface{}{"type": "string", "description": "The markdown content to write to the artifact"},
		},
		"required": []string{"filename", "content"},
	}
}

func (t *WriteArtifactTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Filename string `json:"filename"`
		Content  string `json:"content"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	if params.Filename == "" {
		return &tool.ToolResult{Error: "invalid_args", Output: "filename cannot be empty"}, nil
	}

	artifactPath := filepath.Join(".agent", "artifacts", params.Filename)
	pathStr, err := util.JailPath(ctx.Workspace, artifactPath)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	if err := os.MkdirAll(filepath.Dir(pathStr), 0755); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: fmt.Sprintf("Failed to create directories: %v", err)}, nil
	}

	if err := os.WriteFile(pathStr, []byte(params.Content), 0644); err != nil {
		return &tool.ToolResult{Error: "write_error", Output: fmt.Sprintf("Failed to write artifact: %v", err)}, nil
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Artifact saved: %s", params.Filename),
		Output: fmt.Sprintf("Successfully saved artifact to %s", artifactPath),
		Metadata: map[string]interface{}{
			"size": len(params.Content),
		},
	}, nil
}
