package implement

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"quietforge/tool"
	"quietforge/util"
	"sort"
	"strings"
)

type GlobTool struct{}

func (t *GlobTool) ID() string {
	return "glob"
}

func (t *GlobTool) Description() string {
	return "Find files by glob pattern matching on filename."
}

func (t *GlobTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{"type": "string", "description": "Glob pattern like '**/*.py'"},
			"path":    map[string]interface{}{"type": "string", "description": "Directory to search in"},
		},
		"required": []string{"pattern"},
	}
}

func (t *GlobTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	searchPathStr, err := util.JailPath(ctx.Workspace, params.Path)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	base, err := filepath.Abs(searchPathStr)
	if err != nil {
		base = searchPathStr
	}

	info, err := os.Stat(base)
	if err != nil || !info.IsDir() {
		return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("Directory not found: %s", searchPathStr)}, nil
	}

	pattern := params.Pattern

	var matches []string

	err = filepath.WalkDir(base, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == base {
			return nil
		}

		rel, err := filepath.Rel(base, path)
		if err != nil {
			return nil
		}

		relForward := filepath.ToSlash(rel)
		name := d.Name()
		patternSlash := filepath.ToSlash(pattern)
		matched := false

		if !strings.Contains(patternSlash, "/") {
			matched, _ = filepath.Match(patternSlash, name)
		} else {
			if strings.HasPrefix(patternSlash, "**/") {
				sub := patternSlash[3:]
				if !strings.Contains(sub, "/") {
					matched, _ = filepath.Match(sub, name)
				} else {
					if strings.HasPrefix(sub, "*") {
						matched = strings.HasSuffix(relForward, sub[1:])
					} else {
						matched = strings.HasSuffix(relForward, sub) || relForward == sub
					}
				}
			} else {
				matched, _ = filepath.Match(patternSlash, relForward)
			}
		}

		if matched {
			matches = append(matches, relForward)
		}

		return nil
	})

	if err != nil {
		return &tool.ToolResult{Error: "walk_error", Output: err.Error()}, nil
	}

	sort.Strings(matches)
	if len(matches) > 200 {
		matches = matches[:200]
	}

	if len(matches) == 0 {
		return &tool.ToolResult{Output: "(no matches)"}, nil
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Found %d file(s)", len(matches)),
		Output: strings.Join(matches, "\n"),
	}, nil
}
