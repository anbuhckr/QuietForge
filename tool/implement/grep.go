package implement

import (
	"quietforge/tool"
	"quietforge/util"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"time"
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
			"pattern": map[string]interface{}{"type": "string", "description": "The regex pattern to search for"},
			"path":    map[string]interface{}{"type": "string", "description": "Directory to search in (default: current dir)"},
			"include": map[string]interface{}{"type": "string", "description": "File pattern like '*.py'"},
		},
		"required": []string{"pattern"},
	}
}

func (t *GrepTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Pattern string `json:"pattern"`
		Path    string `json:"path"`
		Include string `json:"include"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	searchPathStr, err := util.JailPath(ctx.Workspace, params.Path)
	if err != nil {
		return &tool.ToolResult{Error: "access_denied", Output: err.Error()}, nil
	}

	include := params.Include

	var cmd *exec.Cmd
	rgPath, err := exec.LookPath("rg")
	if err == nil {
		cmdArgs := []string{"-n", "--no-heading", params.Pattern, searchPathStr}
		if include != "" {
			cmdArgs = append(cmdArgs, "-g", include)
		}
		cmd = exec.Command(rgPath, cmdArgs...)
	} else {
		gitPath, err := exec.LookPath("git")
		if err == nil {
			cmdArgs := []string{"grep", "--untracked", "-n", "-E", "-I", "-e", params.Pattern, "--"}
			if include != "" {
				if searchPathStr == "." {
					cmdArgs = append(cmdArgs, include)
				} else {
					cmdArgs = append(cmdArgs, fmt.Sprintf("%s/**/%s", searchPathStr, include))
				}
			} else {
				cmdArgs = append(cmdArgs, searchPathStr)
			}
			cmd = exec.Command(gitPath, cmdArgs...)
		} else {
			return &tool.ToolResult{Error: "not_found", Output: "Neither ripgrep (rg) nor git were found. Please install one to enable fast code searching."}, nil
		}
	}

	if ctx.Workspace != "" {
		cmd.Dir = ctx.Workspace
	}

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	execCtx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		done <- cmd.Run()
	}()
	select {
	case err = <-done:
	case <-execCtx.Done():
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		return &tool.ToolResult{Error: "timeout", Output: "Command timed out after 30 seconds"}, nil
	}
	if err != nil {
		if _, ok := err.(*exec.ExitError); ok {
			return &tool.ToolResult{Output: "(no matches)"}, nil
		}
		return &tool.ToolResult{Error: "exec_error", Output: err.Error()}, nil
	}

	output := strings.TrimSpace(stdout.String())
	if output == "" {
		return &tool.ToolResult{
			Title:  fmt.Sprintf("Grep: %s", params.Pattern),
			Output: "(no matches)",
		}, nil
	}

	lines := strings.Split(output, "\n")
	
	type GrepMatch struct {
		File    string `json:"file"`
		Line    int    `json:"line"`
		Content string `json:"content"`
	}
	
	var matches []GrepMatch
	for _, l := range lines {
		// handle C:\path:line:content for windows
		parts := strings.Split(l, ":")
		if len(parts) >= 3 {
			filePart := parts[0]
			lineIdx := 1
			if len(parts[0]) == 1 && len(parts) >= 4 { // likely Windows drive letter
				filePart = parts[0] + ":" + parts[1]
				lineIdx = 2
			}
			
			lineNum, err := strconv.Atoi(parts[lineIdx])
			if err == nil {
				content := strings.Join(parts[lineIdx+1:], ":")
				matches = append(matches, GrepMatch{
					File:    filePart,
					Line:    lineNum,
					Content: content,
				})
			}
		}
	}
	
	if matches == nil {
		matches = []GrepMatch{}
	}
	
	b, _ := json.Marshal(matches)

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Grep: %s", params.Pattern),
		Output: string(b),
	}, nil
}
