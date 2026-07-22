package implement

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"quietforge/session"
	"quietforge/tool"
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
	desc := "Execute shell commands asynchronously with optional timeout. Supports background task execution via the `background` parameter.\n\n"
	
	if runtime.GOOS == "windows" {
		desc += `Windows PowerShell Notes:
- The shell is executed using powershell.exe.
- If commands depend on each other, DO NOT use '&&' as Windows PowerShell 5.1 does not support it. Use 'cmd1; if ($?) { cmd2 }' instead.
- Use double quotes for paths with spaces and interpolated strings.
- Prefer full cmdlet names (e.g., Get-ChildItem) over aliases.
- Avoid 'cd <dir> && <cmd>'. Use the 'workdir' parameter instead.
`
	} else {
		desc += `Bash Notes:
- The shell is executed using sh.
- Use '&&' to chain dependent commands (e.g., 'mkdir out && ls out').
- Avoid 'cd <dir> && <cmd>'. Use the 'workdir' parameter instead.
`
	}
	
	desc += `
General Guidance:
- Directory Verification: Before creating files/directories, verify the parent exists.
- Quoting: Always quote file paths that contain spaces.
- Background Tasks: Set 'background': true for long-running processes (e.g., starting a server). You will receive an event when it completes.
- Binary Output: The shell automatically detects binary outputs (e.g., images) and converts them to Data URIs.`

	return desc
}

func (t *ShellTool) Parameters() map[string]interface{} {
	cmdDesc := "The command to execute"
	if runtime.GOOS == "windows" {
		cmdDesc += ". Note: DO NOT use the Unix 'timeout' command inside this string on Windows; use the tool's timeout parameter instead."
	}
	
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"command":     map[string]interface{}{"type": "string", "description": cmdDesc},
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

	if warn := validateCommand(params.Command); warn != "" {
		return &tool.ToolResult{Error: fmt.Sprintf("Command rejected: %s", warn)}, nil
	}

	if ctx != nil && ctx.Agent == "plan" {
		if warn := validatePlanCommand(params.Command); warn != "" {
			return &tool.ToolResult{Error: fmt.Sprintf("Command rejected in plan mode: %s. Switch to build mode to edit files.", warn)}, nil
		}
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

	execCtx, execCancel := context.WithTimeout(context.Background(), time.Duration(params.Timeout)*time.Millisecond)

	var cmd *exec.Cmd
	var ps1Path string
	if runtime.GOOS == "windows" {
		ps1File, err := os.CreateTemp("", "qf_cmd_*.ps1")
		if err != nil {
			execCancel()
			return nil, fmt.Errorf("failed to create temp ps1 file: %v", err)
		}
		ps1Path = ps1File.Name()
		ps1File.WriteString("[console]::InputEncoding = [console]::OutputEncoding = New-Object System.Text.UTF8Encoding\r\n$OutputEncoding = New-Object System.Text.UTF8Encoding\r\n" + params.Command)
		ps1File.Close()
		
		cmd = exec.CommandContext(execCtx, "powershell.exe", "-NoProfile", "-NonInteractive", "-ExecutionPolicy", "Bypass", "-File", ps1Path)
	} else {
		cmd = exec.CommandContext(execCtx, "sh", "-c", params.Command)
	}
	cmd.Dir = workdir
	
	cmd.Cancel = func() error {
		killProcessTree(cmd)
		return nil
	}
	cmd.WaitDelay = 2 * time.Second

	// Force common languages to output UTF-8 when piped
	cmd.Env = append(os.Environ(), 
		"PYTHONIOENCODING=utf-8", 
		"PYTHONUTF8=1",
		"JAVA_TOOL_OPTIONS=-Dfile.encoding=UTF-8",
		"RUBYOPT=-Eutf-8",
	)

	if params.Background {
		var stdoutBuf, stderrBuf bytes.Buffer
		cmd.Stdout = &stdoutBuf
		cmd.Stderr = &stderrBuf
		if err := cmd.Start(); err != nil {
			if ps1Path != "" {
				os.Remove(ps1Path)
			}
			execCancel()
			return &tool.ToolResult{Error: err.Error()}, nil
		}

		bgShellMu.Lock()
		bgShellID++
		bgID := fmt.Sprintf("bg_%d", bgShellID)
		bgShellCmds[bgID] = cmd
		bgShellMu.Unlock()

		go runBackgroundCommand(bgID, cmd, &stdoutBuf, &stderrBuf, params.Command, execCtx, execCancel, ctx, ps1Path, params.Timeout)
		return &tool.ToolResult{Output: fmt.Sprintf("Command `%s` started in background (id=%s). You will be notified when it completes.", params.Command, bgID)}, nil
	}

	defer execCancel()
	if ps1Path != "" {
		defer os.Remove(ps1Path)
	}

	var stdoutBuf, stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf

	if err := cmd.Start(); err != nil {
		return &tool.ToolResult{Error: fmt.Sprintf("failed to start command: %v", err)}, nil
	}

	err := cmd.Wait()
	if execCtx.Err() == context.DeadlineExceeded {
		err = fmt.Errorf("command timed out after %d ms", params.Timeout)
	}
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

func runBackgroundCommand(bgID string, cmd *exec.Cmd, stdoutBuf, stderrBuf *bytes.Buffer, command string, execCtx context.Context, execCancel context.CancelFunc, ctx *tool.ToolContext, ps1Path string, timeout int) {
	defer bgShellRemove(bgID)
	defer execCancel()
	if ps1Path != "" {
		defer os.Remove(ps1Path)
	}

	err := cmd.Wait()

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
		if execCtx.Err() == context.DeadlineExceeded {
			result += fmt.Sprintf("\n\n[Command timed out after %d ms]", timeout)
		} else if err != nil {
			result += fmt.Sprintf("\n\n[Command failed: %v]", err)
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

func killProcessTree(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	pid := cmd.Process.Pid
	if pid <= 0 {
		return
	}
	if runtime.GOOS == "windows" {
		// taskkill /F /T forces termination of the process and all its children
		exec.Command("taskkill", "/F", "/T", "/PID", fmt.Sprintf("%d", pid)).Run()
	} else {
		cmd.Process.Kill()
	}
}

func KillBackgroundShells() {
	bgShellMu.Lock()
	defer bgShellMu.Unlock()
	for id, cmd := range bgShellCmds {
		killProcessTree(cmd)
		delete(bgShellCmds, id)
	}
}

func appendToActiveSession(msgContent string, ctx *tool.ToolContext) {
	s, ok := ctx.Extra["session"].(*session.Session)
	if !ok || s == nil {
		return
	}
	
	s.QueueFollowup(msgContent)
}

const maxCommandLength = 50000

func validateCommand(cmd string) string {
	if len(cmd) > maxCommandLength {
		return fmt.Sprintf("command too long (%d chars, max %d)", len(cmd), maxCommandLength)
	}

	low := strings.ToLower(strings.TrimSpace(cmd))

	dangerous := []struct {
		pattern string
		reason  string
	}{
		{`rm -rf /`, "recursive root deletion"},
		{`rm -rf ~`, "recursive home deletion"},
		{`rm -rf --no-preserve-root`, "forced root deletion"},
		{`:(){ :|:& };:`, "fork bomb"},
		{`dd if=/dev/zero`, "disk wipe (dd)"},
		{`dd if=/dev/random`, "disk overwrite (dd)"},
		{`mkfs.`, "filesystem format"},
		{`format `, "disk format"},
		{`fdisk`, "partition editor"},
		{`mkswap`, "swap format"},
		{`> /dev/sd`, "raw block device write"},
		{`. > /dev/sd`, "raw block device write"},
		{`Remove-Item -Recurse -Force`, "forced recursive deletion (PowerShell)"},
		{`Remove-Item -Force -Recurse`, "forced recursive deletion (PowerShell)"},
		{`del /f /s /q`, "forced recursive deletion (cmd)"},
		{`rd /s /q`, "forced directory deletion (cmd)"},
		{`rmdir /s /q`, "forced directory deletion (cmd)"},
		{`cipher /w:`, "disk wipe (cipher)"},
		{`reg delete`, "registry deletion"},
		{`reg add`, "registry modification"},
		{`New-ItemProperty -Path`, "registry modification (PowerShell)"},
		{`Set-ItemProperty -Path`, "registry modification (PowerShell)"},
		{`Remove-ItemProperty -Path`, "registry deletion (PowerShell)"},
		{`Stop-Computer`, "system shutdown"},
		{`Restart-Computer`, "system restart"},
		{`shutdown /s`, "system shutdown"},
		{`shutdown /r`, "system restart"},
		{`-EncodedCommand`, "encoded PowerShell command"},
		{`Invoke-Expression`, "arbitrary expression execution"},
		{`iex `, "invoke expression"},
		{`Start-BitsTransfer`, "background file download"},
		{`Net.WebClient`, "web client download"},
		{`Net.Sockets.TCPClient`, "network connection"},
		{`[System.IO.File]::`, "direct .NET file access"},
		{`[System.IO.Directory]::`, "direct .NET directory access"},
		{`[System.IO.DriveInfo]::`, "direct .NET drive access"},
		{`[System.Management]::`, "WMI access"},
		{`Get-WmiObject`, "WMI access"},
		{`Set-MpPreference`, "Windows Defender modification"},
		{`Add-MpPreference`, "Windows Defender modification"},
		{`Set-ExecutionPolicy`, "PowerShell execution policy change"},
		{`Set-MpPreference -DisableRealtimeMonitoring`, "disable real-time monitoring"},
	}

	for _, d := range dangerous {
		if strings.Contains(low, d.pattern) {
			return d.reason
		}
	}
	return ""
}

func validatePlanCommand(cmd string) string {
	low := strings.ToLower(strings.TrimSpace(cmd))

	// Block: output redirect to file (not null devices or fd-to-fd)
	if hasFileRedirect(low) {
		return "output redirect to file"
	}

	// Block: PowerShell write cmdlets
	writeCmdlets := []string{
		"set-content ", "add-content ", "out-file ",
		"new-item ", "copy-item ", "move-item ", "remove-item ",
	}
	for _, wc := range writeCmdlets {
		if strings.Contains(low, wc) {
			return "file modification command (" + strings.TrimSpace(wc) + ")"
		}
	}

	// Block: sed -i (in-place edit)
	lowParts := strings.Fields(low)
	for i, part := range lowParts {
		if part == "sed" {
			for j := i + 1; j < len(lowParts) && j <= i+3; j++ {
				if strings.HasPrefix(lowParts[j], "-i") {
					return "in-place file edit (sed -i)"
				}
			}
		}
	}

	// Block: heredoc combined with file redirect
	if (strings.Contains(low, "@\"") || strings.Contains(low, "@'") || strings.Contains(low, "<<")) && hasFileRedirect(low) {
		return "heredoc file write"
	}

	// Block: Start-Process spawning powershell to write files
	if strings.Contains(low, "start-process") && strings.Contains(low, "powershell") {
		return "self-modifying shell command"
	}

	return ""
}

func hasFileRedirect(cmd string) bool {
	low := strings.ToLower(cmd)

	// Remove null-device redirects
	for _, n := range []string{"> $null", ">> $null", "> /dev/null", ">> /dev/null", "> nul", ">> nul", ">nul", ">>nul"} {
		low = strings.ReplaceAll(low, n, " ")
	}

	// Scan for remaining `>`
	for i := 0; i < len(low); i++ {
		if low[i] == '>' {
			// Skip fd-to-fd redirects like 2>&1
			if i+1 < len(low) && low[i+1] == '&' {
				continue
			}
			return true
		}
	}
	return false
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
