package implement

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"quietforge/storage"
	"quietforge/tool"
)

type WorkspaceTool struct {
	Repo *storage.Repository
}

func (t *WorkspaceTool) ID() string {
	return "workspace"
}

func (t *WorkspaceTool) Description() string {
	return "Read and write the project workspace graph and architectural decisions to persistent SQLite memory."
}

func (t *WorkspaceTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"action":      map[string]interface{}{"type": "string", "enum": []string{"list_files", "upsert_file", "list_architecture", "add_architecture", "find_symbol", "find_references", "get_dependencies"}, "description": "The action to perform"},
			"path":        map[string]interface{}{"type": "string", "description": "Target file path for upsert_file, find_references, or get_dependencies"},
			"purpose":     map[string]interface{}{"type": "string", "description": "High-level semantic purpose of this file"},
			"symbol_name": map[string]interface{}{"type": "string", "description": "Exact name of the symbol for find_symbol"},
			"arch_type":   map[string]interface{}{"type": "string", "enum": []string{"decision", "constraint"}, "description": "Architecture type (for add/list architecture)"},
			"text":        map[string]interface{}{"type": "string", "description": "Text of the decision or constraint (for add_architecture)"},
			"scope":       map[string]interface{}{"type": "string", "enum": []string{"global", "file", "symbol"}, "description": "Scope of the architecture note"},
		},
		"required": []string{"action"},
	}
}

func (t *WorkspaceTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var repo *storage.Repository
	if t.Repo != nil {
		repo = t.Repo
	} else if r, ok := ctx.Extra["repo"].(*storage.Repository); ok {
		repo = r
	}
	if repo == nil {
		return &tool.ToolResult{Error: "not_initialized", Output: "Workspace tool not initialized"}, nil
	}

	var params struct {
		Action     string `json:"action"`
		Path       string `json:"path"`
		Purpose    string `json:"purpose"`
		SymbolName string `json:"symbol_name"`
		ArchType   string `json:"arch_type"`
		Text       string `json:"text"`
		Scope      string `json:"scope"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	switch params.Action {
	case "list_files":
		files, err := repo.ListWorkspaceFiles(ctx.Workspace)
		if err != nil {
			return &tool.ToolResult{Error: "db_error", Output: err.Error()}, nil
		}
		if len(files) == 0 {
			return &tool.ToolResult{Output: "No workspace files tracked yet."}, nil
		}
		var sb strings.Builder
		// Load symbols to enrich the output
		syms, _ := repo.ListWorkspaceSymbols(ctx.Workspace)
		symMap := make(map[string][]storage.WorkspaceSymbolRow)
		for _, s := range syms {
			symMap[s.Path] = append(symMap[s.Path], s)
		}

		for _, f := range files {
			sb.WriteString(fmt.Sprintf("- %s: %s\n", f.Path, f.Purpose))
			if fileSyms := symMap[f.Path]; len(fileSyms) > 0 {
				sb.WriteString("  Symbols: ")
				for i, s := range fileSyms {
					if i > 0 {
						sb.WriteString(", ")
					}
					sb.WriteString(s.Name)
				}
				sb.WriteString("\n")
			}
		}
		return &tool.ToolResult{Title: "Workspace Files", Output: sb.String()}, nil

	case "upsert_file":
		if params.Path == "" {
			return &tool.ToolResult{Error: "missing_arg", Output: "path is required"}, nil
		}
		file := storage.WorkspaceFileRow{
			Path:      params.Path,
			Workspace: ctx.Workspace,
			Purpose:   params.Purpose,
			UpdatedAt: time.Now().Unix(),
		}
		if err := repo.UpsertWorkspaceFile(file); err != nil {
			return &tool.ToolResult{Error: "db_error", Output: err.Error()}, nil
		}
		return &tool.ToolResult{Title: "File updated", Output: fmt.Sprintf("Upserted %s in workspace memory", params.Path)}, nil

	case "list_architecture":
		if params.ArchType == "" {
			params.ArchType = "decision"
		}
		archs, err := repo.ListArchitecture(ctx.Workspace, params.ArchType)
		if err != nil {
			return &tool.ToolResult{Error: "db_error", Output: err.Error()}, nil
		}
		if len(archs) == 0 {
			return &tool.ToolResult{Output: fmt.Sprintf("No architecture %ss tracked yet.", params.ArchType)}, nil
		}
		var sb strings.Builder
		for _, a := range archs {
			sb.WriteString(fmt.Sprintf("- [%s] %s\n", a.ID[:8], a.Text))
		}
		return &tool.ToolResult{Title: fmt.Sprintf("Architecture %ss", params.ArchType), Output: sb.String()}, nil

	case "add_architecture":
		if params.Text == "" {
			return &tool.ToolResult{Error: "missing_arg", Output: "text is required"}, nil
		}
		if params.ArchType == "" {
			params.ArchType = "decision"
		}
		if params.Scope == "" {
			params.Scope = "global"
		}
		b := make([]byte, 4)
		rand.Read(b)
		id := fmt.Sprintf("arch-%d-%x", time.Now().UnixNano(), b)
		arch := storage.ArchitectureRow{
			ID:        id,
			Workspace: ctx.Workspace,
			Scope:     params.Scope,
			Type:      params.ArchType,
			Text:      params.Text,
			UpdatedAt: time.Now().Unix(),
		}
		if err := repo.CreateArchitecture(arch); err != nil {
			return &tool.ToolResult{Error: "db_error", Output: err.Error()}, nil
		}
		return &tool.ToolResult{Title: "Architecture added", Output: fmt.Sprintf("Added %s: %s", params.ArchType, params.Text)}, nil
	case "find_symbol":
		if params.SymbolName == "" {
			return &tool.ToolResult{Error: "missing_arg", Output: "symbol_name is required"}, nil
		}
		syms, err := repo.GetSymbolByName(ctx.Workspace, params.SymbolName)
		if err != nil {
			return &tool.ToolResult{Error: "db_error", Output: err.Error()}, nil
		}
		if len(syms) == 0 {
			return &tool.ToolResult{Output: fmt.Sprintf("Symbol '%s' not found in workspace memory.", params.SymbolName)}, nil
		}
		var sb strings.Builder
		for _, s := range syms {
			sb.WriteString(fmt.Sprintf("- %s in %s (lines %d-%d)\n", s.Type, s.Path, s.LineStart, s.LineEnd))
		}
		return &tool.ToolResult{Title: "Symbol Lookup", Output: sb.String()}, nil

	case "find_references":
		if params.Path == "" {
			return &tool.ToolResult{Error: "missing_arg", Output: "path is required"}, nil
		}
		edges, err := repo.GetIncomingEdges(ctx.Workspace, params.Path)
		if err != nil {
			return &tool.ToolResult{Error: "db_error", Output: err.Error()}, nil
		}
		if len(edges) == 0 {
			return &tool.ToolResult{Output: fmt.Sprintf("No tracked incoming references to %s.", params.Path)}, nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Files referencing %s:\n", params.Path))
		for _, e := range edges {
			sb.WriteString(fmt.Sprintf("- %s (via %s)\n", e.SourcePath, e.EdgeType))
		}
		return &tool.ToolResult{Title: "References", Output: sb.String()}, nil

	case "get_dependencies":
		if params.Path == "" {
			return &tool.ToolResult{Error: "missing_arg", Output: "path is required"}, nil
		}
		edges, err := repo.GetOutgoingEdges(ctx.Workspace, params.Path)
		if err != nil {
			return &tool.ToolResult{Error: "db_error", Output: err.Error()}, nil
		}
		if len(edges) == 0 {
			return &tool.ToolResult{Output: fmt.Sprintf("%s has no tracked outgoing dependencies.", params.Path)}, nil
		}
		var sb strings.Builder
		sb.WriteString(fmt.Sprintf("Dependencies of %s:\n", params.Path))
		for _, e := range edges {
			sb.WriteString(fmt.Sprintf("- %s (via %s)\n", e.TargetPath, e.EdgeType))
		}
		return &tool.ToolResult{Title: "Dependencies", Output: sb.String()}, nil
	}

	return &tool.ToolResult{Error: "invalid_arg", Output: fmt.Sprintf("Unknown action: %s", params.Action)}, nil
}
