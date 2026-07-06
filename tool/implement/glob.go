package implement

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"quietforge/tool"
	"quietforge/util"
	"sort"
	"strings"
	"time"
)

const (
	globTimeout     = 15 * time.Second
	maxGlobResults  = 1000
)

var errMaxResults = errors.New("max results reached")

type GlobTool struct{}

func (t *GlobTool) ID() string {
	return "glob"
}

func (t *GlobTool) Description() string {
	return "Find files by glob pattern matching. Supports standard glob syntax including ** for recursive matching."
}

func (t *GlobTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern": map[string]interface{}{"type": "string", "description": "Glob pattern like '**/*.py' or 'src/**/test_*.go'"},
			"path":    map[string]interface{}{"type": "string", "description": "Directory to search in (default: current dir)"},
			"exclude": map[string]interface{}{"type": "string", "description": "Glob pattern to exclude (e.g., 'vendor/**')"},
			"max":     map[string]interface{}{"type": "integer", "description": "Maximum results to return (default: 1000)"},
		},
		"required": []string{"pattern"},
	}
}

type globParams struct {
	Pattern string `json:"pattern"`
	Path    string `json:"path"`
	Exclude string `json:"exclude"`
	Max     int    `json:"max"`
}

type GlobMatch struct {
	Path  string `json:"path"`
	IsDir bool   `json:"is_dir"`
	Size  int64  `json:"size"`
}

func (t *GlobTool) Execute(args []byte, toolCtx *tool.ToolContext) (*tool.ToolResult, error) {
	var params globParams
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	searchPathStr, err := util.JailPath(toolCtx.Workspace, params.Path)
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

	maxResults := params.Max
	if maxResults <= 0 || maxResults > maxGlobResults {
		maxResults = maxGlobResults
	}

	// Validate pattern
	patternForward := filepath.ToSlash(params.Pattern)
	parts := toParts(patternForward)
	for _, part := range parts {
		if part == "**" {
			continue
		}
		if _, err := filepath.Match(part, "x"); err != nil {
			return &tool.ToolResult{Error: "invalid_pattern", Output: fmt.Sprintf("Invalid glob pattern: %v", err)}, nil
		}
	}

	// Validate exclude pattern
	if params.Exclude != "" {
		excludeForward := filepath.ToSlash(params.Exclude)
		excludeParts := toParts(excludeForward)
		for _, part := range excludeParts {
			if part == "**" {
				continue
			}
			if _, err := filepath.Match(part, "x"); err != nil {
				return &tool.ToolResult{Error: "invalid_exclude", Output: fmt.Sprintf("Invalid exclude pattern: %v", err)}, nil
			}
		}
	}

	// Timeout context for cancellation
	ctx, cancel := context.WithTimeout(toolCtx.Context, globTimeout)
	defer cancel()

	matches, walkWarning := t.globWalk(ctx, base, patternForward, params.Exclude, maxResults)

	sort.Slice(matches, func(i, j int) bool {
		if matches[i].IsDir != matches[j].IsDir {
			return matches[i].IsDir
		}
		return matches[i].Path < matches[j].Path
	})

	if matches == nil {
		matches = []GlobMatch{}
	}

	result := map[string]interface{}{
		"matches":       matches,
		"matched_count": len(matches),
		"truncated":     len(matches) >= maxResults,
	}
	if walkWarning != "" {
		result["warning"] = walkWarning
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal glob results: %w", err)
	}

	title := fmt.Sprintf("Glob: %s", params.Pattern)
	if params.Exclude != "" {
		title += fmt.Sprintf(" (exclude: %s)", params.Exclude)
	}

	return &tool.ToolResult{
		Title:  title,
		Output: string(b),
	}, nil
}

// globWalk walks the filesystem with proper directory pruning and pattern matching.
func (t *GlobTool) globWalk(ctx context.Context, base, pattern, exclude string, maxResults int) ([]GlobMatch, string) {
	patternParts := toParts(pattern)
	var excludeParts []string
	if exclude != "" {
		excludeParts = toParts(filepath.ToSlash(exclude))
	}

	var matches []GlobMatch

	walkErr := filepath.WalkDir(base, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if path == base {
			return nil
		}

		// Check cancellation
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		rel, relErr := filepath.Rel(base, path)
		if relErr != nil {
			return nil
		}
		relForward := filepath.ToSlash(rel)
		pathParts := toParts(relForward)

			// Skip symlinks — avoid reporting links that escape the workspace
		if d.Type()&fs.ModeSymlink != 0 {
			return nil
		}

		// Skip hidden files/directories immediately — avoid walking .git, .cache, etc.
		if strings.HasPrefix(d.Name(), ".") {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		// Exclude check BEFORE matching — prune exclude directories early
		if len(excludeParts) > 0 && matchParts(excludeParts, pathParts) {
			if d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}

		// Attempt to match the pattern
		matched, matchErr := matchPattern(patternParts, pathParts)
		if matchErr != nil {
			return nil
		}

		if matched {
			var size int64
			if !d.IsDir() {
				if fi, fiErr := d.Info(); fiErr == nil {
					size = fi.Size()
				}
			}

			matches = append(matches, GlobMatch{
				Path:  relForward,
				IsDir: d.IsDir(),
				Size:  size,
			})

			if len(matches) >= maxResults {
				return errMaxResults
			}
			// File matched — don't descend further into this particular entry
			return nil
		}

		// Not matched. Continue walking ONLY if this is a directory.
		// Don't use SkipDir here — the pattern might match deeper descendants
		// (e.g. src/cmd doesn't match "src/**/main.go" but cmd/app/main.go does).
		if !d.IsDir() {
			return nil
		}
		return nil
	})

	if walkErr != nil && !errors.Is(walkErr, errMaxResults) && !errors.Is(walkErr, context.Canceled) && !errors.Is(walkErr, context.DeadlineExceeded) {
		// Unexpected walk error — return what we have with a warning so the LLM knows results may be partial
		return matches, fmt.Sprintf("Walk terminated early: %v. Results may be incomplete.", walkErr)
	}
	return matches, ""
}

// toParts splits a forward-slash path into individual components.
func toParts(path string) []string {
	if path == "" || path == "." {
		return nil
	}
	return strings.Split(path, "/")
}

// matchPattern checks whether pathParts matches patternParts using glob semantics.
// Returns (matched, error). Returns error for malformed patterns.
func matchPattern(patternParts, pathParts []string) (bool, error) {
	pi, ppi := 0, 0
	for pi < len(patternParts) {
		if patternParts[pi] == "**" {
			pi++
			if pi >= len(patternParts) {
				return true, nil // trailing ** matches everything
			}
			// Try to match the remaining pattern at each position
			for ppi <= len(pathParts)-len(patternParts)+pi {
				matched, err := matchPattern(patternParts[pi:], pathParts[ppi:])
				if err != nil {
					return false, err
				}
				if matched {
					return true, nil
				}
				ppi++
			}
			return false, nil
		}

		if ppi >= len(pathParts) {
			return false, nil
		}

		matched, err := filepath.Match(patternParts[pi], pathParts[ppi])
		if err != nil {
			return false, err
		}
		if !matched {
			return false, nil
		}
		pi++
		ppi++
	}
	return ppi >= len(pathParts), nil
}

// matchParts is a convenience wrapper that discards the error.
// Used for exclude matching where we don't care about reporting malformed patterns.
func matchParts(patternParts, pathParts []string) bool {
	m, _ := matchPattern(patternParts, pathParts)
	return m
}
