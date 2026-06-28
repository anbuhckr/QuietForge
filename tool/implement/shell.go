package implement

import (
	"quietforge/session"
	"quietforge/tool"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"
)

type ShellTool struct{}

func (t *ShellTool) ID() string {
	return "shell"
}

func (t *ShellTool) Description() string {
	return "Execute shell commands asynchronously with optional timeout."
}

func (t *ShellTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command":     map[string]interface{}{"type": "string", "description": "The command to execute"},
			"description": map[string]interface{}{"type": "string", "description": "Short description"},
			"timeout":     map[string]interface{}{"type": "integer", "description": "Timeout in milliseconds"},
			"workdir":     map[string]interface{}{"type": "string", "description": "Working directory"},
			"background":  map[string]interface{}{"type": "boolean", "description": "Run in the background"},
		},
		"required": []string{"command"},
	}
}

var (
	bgShellMu   sync.Mutex
	bgShellCmds = map[string]*exec.Cmd{}
	bgShellID   int
)

func bgShellRemove(id string) {
	bgShellMu.Lock()
	defer bgShellMu.Unlock()
	delete(bgShellCmds, id)
}

func (t *ShellTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		Command    string `json:"command"`
		Timeout    int    `json:"timeout"`
		Workdir    string `json:"workdir"`
		Background bool   `json:"background"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return nil, fmt.Errorf("invalid arguments: %v", err)
	}

	if params.Timeout == 0 {
		params.Timeout = 120000
	}

	workdir := params.Workdir
	if workdir == "" {
		workdir = ctx.Workspace
	}
	if !filepath.IsAbs(workdir) && ctx.Workspace != "" {
		workdir = filepath.Join(ctx.Workspace, workdir)
	}

	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.Command("powershell", "-Command", params.Command)
	} else {
		cmd = exec.Command("sh", "-c", params.Command)
	}
	cmd.Dir = workdir

	if params.Background {
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
		if err := cmd.Start(); err != nil {
			return &tool.ToolResult{Error: err.Error()}, nil
		}

		bgShellMu.Lock()
		bgShellID++
		bgID := fmt.Sprintf("bg_%d", bgShellID)
		bgShellCmds[bgID] = cmd
		bgShellMu.Unlock()

		go runBackgroundCommand(bgID, cmd, &stdoutBuf, &stderrBuf, params.Command, params.Timeout, ctx)
		return &tool.ToolResult{Output: fmt.Sprintf("Command `%s` started in background (id=%s). You will be notified when it completes.", params.Command, bgID)}, nil
	}

	execCtx, cancel := context.WithTimeout(context.Background(), time.Duration(params.Timeout)*time.Millisecond)
	defer cancel()
	cmd = exec.CommandContext(execCtx, cmd.Path, cmd.Args...)
	cmd.Dir = workdir
	
	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	err := cmd.Run()
	stdoutBytes := stdoutBuf.Bytes()
	errorStr := stderrBuf.String()

	isBin := isShellOutputBinary(stdoutBytes)
	if isBin {
		contentType := http.DetectContentType(stdoutBytes)
		if strings.HasPrefix(contentType, "image/") {
			base64Data := base64.StdEncoding.EncodeToString(stdoutBytes)
			dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, base64Data)

			return &tool.ToolResult{
				Output: fmt.Sprintf("Command `%s` produced an image (%d bytes).", params.Command, len(stdoutBytes)),
				Attachments: []map[string]interface{}{
					{"url": dataURL},
				},
			}, nil
		}
		return &tool.ToolResult{
			Error:  "binary_output",
			Output: fmt.Sprintf("Command `%s` produced binary output (%d bytes). Binary output cannot be displayed. Use a more specific command to inspect the file.", params.Command, len(stdoutBytes)),
		}, nil
	}

	resultStr := string(stdoutBytes)
	if errorStr != "" {
		resultStr += "\n" + errorStr
	}

	lines := strings.Split(resultStr, "\n")
	if len(lines) > 500 {
		omitted := len(lines) - 400
		resultStr = strings.Join(lines[:200], "\n") + fmt.Sprintf("\n\n... [%d lines omitted. WARNING: Shell output heavily truncated!] ...\n\n", omitted) + strings.Join(lines[len(lines)-200:], "\n")
	}

	if err != nil {
		return &tool.ToolResult{
			Title:  fmt.Sprintf("Exit code error: %v", err),
			Output: strings.TrimSpace(resultStr),
		}, nil
	}

	if strings.TrimSpace(resultStr) == "" {
		resultStr = "(no output)"
	}

	return &tool.ToolResult{Output: strings.TrimSpace(resultStr)}, nil
}

func runBackgroundCommand(bgID string, cmd *exec.Cmd, stdoutBuf, stderrBuf *bytes.Buffer, command string, timeout int, ctx *tool.ToolContext) {
	defer bgShellRemove(bgID)

	done := make(chan struct{})
	go func() {
		cmd.Wait()
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Duration(timeout) * time.Millisecond):
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		<-done
	}

	stdoutBytes := stdoutBuf.Bytes()
	errorStr := stderrBuf.String()

	result := ""
	if isShellOutputBinary(stdoutBytes) {
		result = fmt.Sprintf("Background command `%s` produced binary output (%d bytes). Binary output cannot be displayed.", command, len(stdoutBytes))
	} else {
		result = string(stdoutBytes)
		if errorStr != "" {
			exitCode := 0
			if cmd.ProcessState != nil {
				exitCode = cmd.ProcessState.ExitCode()
			}
			result += fmt.Sprintf("\n(exit code: %d)", exitCode)
			result += "\n" + errorStr
		}

		lines := strings.Split(result, "\n")
		if len(lines) > 500 {
			omitted := len(lines) - 400
			result = strings.Join(lines[:200], "\n") + fmt.Sprintf("\n\n...[omitted %d lines]...\n\n", omitted) + strings.Join(lines[len(lines)-200:], "\n")
		}
	}

	msgContent := fmt.Sprintf("[Background Task Completed] `%s`\n\n%s", command, result)
	appendToActiveSession(msgContent, ctx)
	fmt.Printf("\n[Background Task Finished] `%s`. Result appended to context.", command)
}

func KillBackgroundShells() {
	bgShellMu.Lock()
	defer bgShellMu.Unlock()
	for id, cmd := range bgShellCmds {
		if cmd.Process != nil {
			cmd.Process.Kill()
		}
		delete(bgShellCmds, id)
	}
}

func appendToActiveSession(msgContent string, ctx *tool.ToolContext) {
	s, ok := ctx.Extra["session"].(*session.Session)
	if !ok || s == nil {
		return
	}
	sessionID := s.SessionID

	msg := session.Message{
		ID:        fmt.Sprintf("%d", time.Now().UnixNano()),
		SessionID: sessionID,
		Role:      "system",
		Parts: []session.MessagePart{
			{Type: "text", Content: msgContent},
		},
		CreatedAt: time.Now().UnixMilli(),
	}
	s.AddMessage(msg)
}

func isShellOutputBinary(data []byte) bool {
	contentType := http.DetectContentType(data)
	if strings.HasPrefix(contentType, "image/") || strings.HasPrefix(contentType, "video/") || strings.HasPrefix(contentType, "audio/") {
		return true
	}
	nullCount := 0
	maxCheck := len(data)
	if maxCheck > 8192 {
		maxCheck = 8192
	}
	for _, b := range data[:maxCheck] {
		if b == 0 {
			nullCount++
		}
	}
	return nullCount > 0
}
