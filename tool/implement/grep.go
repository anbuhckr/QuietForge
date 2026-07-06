package implement

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"quietforge/tool"
	"quietforge/util"
	"strconv"
	"strings"
	"time"
)

const (
	grepTimeout         = 30 * time.Second
	maxSearchMatches    = 200
	maxReturnedMatches  = 50
)

type GrepTool struct{}

func (t *GrepTool) ID() string {
	return "grep"
}

func (t *GrepTool) Description() string {
	return "Search file contents using regular expressions. Returns matching file paths and line numbers."
}

func (t *GrepTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"pattern":     map[string]interface{}{"type": "string", "description": "The regex pattern to search for"},
			"path":        map[string]interface{}{"type": "string", "description": "Directory to search in (default: current dir)"},
			"include":     map[string]interface{}{"type": "string", "description": "File pattern like '*.py'"},
			"hidden":      map[string]interface{}{"type": "boolean", "description": "Include hidden files and directories (rg only)"},
			"ignore_case": map[string]interface{}{"type": "boolean", "description": "Case-insensitive search (-i flag)"},
			"literal":     map[string]interface{}{"type": "boolean", "description": "Treat pattern as literal string, not regex (-F flag)"},
			"context":     map[string]interface{}{"type": "integer", "description": "Show N lines of context around each match (-C flag)"},
		},
		"required": []string{"pattern"},
	}
}

type grepParams struct {
	Pattern    string `json:"pattern"`
	Path       string `json:"path"`
	Include    string `json:"include"`
	Hidden     bool   `json:"hidden"`
	IgnoreCase bool   `json:"ignore_case"`
	Literal    bool   `json:"literal"`
	Context    int    `json:"context"`
}

type GrepMatch struct {
	File    string `json:"file"`
	Line    int    `json:"line"`
	Content string `json:"content"`
}

func (t *GrepTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params grepParams
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	searchPathStr, err := util.JailPath(ctx.Workspace, params.Path)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	rgPath, rgErr := exec.LookPath("rg")
	if rgErr == nil {
		return t.runRg(rgPath, params, searchPathStr, ctx.Workspace)
	}

	gitPath, gitErr := exec.LookPath("git")
	if gitErr == nil {
		return t.runGit(gitPath, params, searchPathStr, ctx.Workspace)
	}

	return &tool.ToolResult{
		Error:  "not_found",
		Output: "Neither ripgrep (rg) nor git were found. Please install one to enable fast code searching.",
	}, nil
}

// runRg uses ripgrep with --json for deterministic output parsing.
func (t *GrepTool) runRg(rgPath string, params grepParams, searchPath, workspace string) (*tool.ToolResult, error) {
	cmdArgs := []string{"-n", "--json"}

	// Max results cap prevents huge outputs
	if params.Context == 0 {
		cmdArgs = append(cmdArgs, "-m", strconv.Itoa(maxSearchMatches))
	}

	if params.Include != "" {
		cmdArgs = append(cmdArgs, "-g", params.Include)
	}
	if params.Hidden {
		cmdArgs = append(cmdArgs, "--hidden", "-g", "!.git")
	}
	if params.IgnoreCase {
		cmdArgs = append(cmdArgs, "-i")
	}
	if params.Literal {
		cmdArgs = append(cmdArgs, "-F")
	}
	if params.Context > 0 {
		cmdArgs = append(cmdArgs, "-C", strconv.Itoa(params.Context))
	}

	cmdArgs = append(cmdArgs, params.Pattern, searchPath)

	return t.executeGrep(rgPath, cmdArgs, params, "rg", workspace)
}

// runGit falls back to git grep when ripgrep is unavailable.
// Note: hidden file support is not available in git grep mode.
func (t *GrepTool) runGit(gitPath string, params grepParams, searchPath, workspace string) (*tool.ToolResult, error) {
	cmdArgs := []string{"grep", "--untracked", "-n", "-E", "-I", fmt.Sprintf("--max-count=%d", maxSearchMatches)}

	if params.IgnoreCase {
		cmdArgs = append(cmdArgs, "-i")
	}
	if params.Literal {
		cmdArgs = append(cmdArgs, "-F")
	}
	if params.Context > 0 {
		cmdArgs = append(cmdArgs, "-C", strconv.Itoa(params.Context))
	}

	cmdArgs = append(cmdArgs, "-e", params.Pattern, "--")

	if params.Include != "" {
		if searchPath == "." {
			cmdArgs = append(cmdArgs, params.Include)
		} else {
			cmdArgs = append(cmdArgs, fmt.Sprintf("%s/**/%s", searchPath, params.Include))
		}
	} else {
		cmdArgs = append(cmdArgs, searchPath)
	}

	return t.executeGrep(gitPath, cmdArgs, params, "git", workspace)
}

// executeGrep runs the command with timeout and parses the output.
func (t *GrepTool) executeGrep(exePath string, exeArgs []string, params grepParams, backend, workspace string) (*tool.ToolResult, error) {
	ctx, cancel := context.WithTimeout(context.Background(), grepTimeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, exePath, exeArgs...)
	if workspace != "" {
		cmd.Dir = workspace
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	err := cmd.Run()

	if ctx.Err() == context.DeadlineExceeded {
		return &tool.ToolResult{Error: "timeout", Output: "Command timed out after 30 seconds"}, nil
	}

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			switch exitErr.ExitCode() {
			case 1:
				return t.noMatches(params), nil
			default:
				return &tool.ToolResult{
					Error:  "exec_error",
					Output: fmt.Sprintf("Grep failed (exit %d): %s", exitErr.ExitCode(), stderr.String()),
				}, nil
			}
		}
		return &tool.ToolResult{Error: "exec_error", Output: err.Error()}, nil
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return t.noMatches(params), nil
	}

	var matches []GrepMatch

	if backend == "rg" {
		// Parse ripgrep --json output (deterministic, handles both match and context events)
		matches = parseRgJSON(output)
	} else {
		// Parse git grep text output
		matches = parseTextGrep(output)
	}

	if matches == nil {
		matches = []GrepMatch{}
	}

	totalBeforeTruncation := len(matches)
	if totalBeforeTruncation > maxReturnedMatches {
		matches = matches[:maxReturnedMatches]
		matches = append(matches, GrepMatch{
			File:    "[Truncated]",
			Line:    0,
			Content: fmt.Sprintf("[Grep output truncated. Found %d matches (capped at %d by search), showing first %d. Use a more specific pattern to refine results.]", totalBeforeTruncation, maxSearchMatches, maxReturnedMatches),
		})
	}

	result := map[string]interface{}{
		"matches":       matches,
		"matched_count": totalBeforeTruncation,
	}
	b, err := json.Marshal(result)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal grep results: %w", err)
	}

	dirName := params.Path
	if dirName == "" {
		dirName = "."
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Grep: %s in %s", params.Pattern, dirName),
		Output: string(b),
	}, nil
}

// rgEvent represents a single line of ripgrep --json output.
type rgEvent struct {
	Type string `json:"type"`
	Data rgData `json:"data"`
}

type rgData struct {
	Path       rgText `json:"path"`
	Lines      rgText `json:"lines"`
	LineNumber int    `json:"line_number"`
}

type rgText struct {
	Text string `json:"text"`
}

// parseRgJSON parses ripgrep --json output into GrepMatch structures.
// Handles both "match" and "context" event types.
func parseRgJSON(output string) []GrepMatch {
	lines := strings.Split(output, "\n")
	matches := make([]GrepMatch, 0, maxSearchMatches)

	for _, line := range lines {
		if line == "" {
			continue
		}
		var event rgEvent
		if err := json.Unmarshal([]byte(line), &event); err != nil {
			continue
		}
		switch event.Type {
		case "match":
			content := strings.TrimRight(event.Data.Lines.Text, "\r\n")
			matches = append(matches, GrepMatch{
				File:    event.Data.Path.Text,
				Line:    event.Data.LineNumber,
				Content: content,
			})
		case "context":
			content := strings.TrimRight(event.Data.Lines.Text, "\r\n")
			matches = append(matches, GrepMatch{
				File:    event.Data.Path.Text,
				Line:    event.Data.LineNumber,
				Content: "│ " + content, // visual indicator that this is a context line
			})
		}
	}
	return matches
}

// parseTextGrep parses git grep text output.
// Handles paths containing colons by finding the last two ":" separators.
func parseTextGrep(output string) []GrepMatch {
	rawLines := strings.Split(output, "\n")
	matches := make([]GrepMatch, 0, maxReturnedMatches)

	for _, l := range rawLines {
		if l == "" || strings.HasPrefix(l, "--") {
			continue
		}

		// rg -C output uses "-" as context separator: "file.py-3-content"
		sep := ":"
		if strings.Count(l, "-") >= 2 && strings.Count(l, ":") < 2 {
			sep = "-"
		}

		// Find the last separator (line number position)
		// then find the separator before that (file path end)
		// This handles paths like C:\foo\bar.go:15:content
		lastSep := strings.LastIndex(l, sep)
		if lastSep < 0 {
			continue
		}
		prevSep := strings.LastIndex(l[:lastSep], sep)
		if prevSep < 0 {
			continue
		}

		filePart := l[:prevSep]
		linePart := l[prevSep+1 : lastSep]
		content := l[lastSep+1:]

		lineNum, err := strconv.Atoi(linePart)
		if err != nil {
			continue
		}

		content = strings.TrimRight(content, "\r\n")
		if sep == "-" {
			content = "│ " + content // context line indicator
		}

		matches = append(matches, GrepMatch{
			File:    filePart,
			Line:    lineNum,
			Content: content,
		})
	}
	return matches
}

func (t *GrepTool) noMatches(params grepParams) *tool.ToolResult {
	dirName := params.Path
	if dirName == "" {
		dirName = "."
	}
	return &tool.ToolResult{
		Title:  fmt.Sprintf("Grep: %s in %s", params.Pattern, dirName),
		Output: `{"matches":[],"matched_count":0}`,
	}
}
