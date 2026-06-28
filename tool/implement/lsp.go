package implement

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"quietforge/tool"
	"quietforge/util"
	"sync"
)

type lspServer struct {
	cmd    *exec.Cmd
	stdin  *bufio.Writer
	stdout *bufio.Scanner
	mu     sync.Mutex
	seq    int64
}

func newLspServer(cmd string, args ...string) (*lspServer, error) {
	c := exec.Command(cmd, args...)
	stdin, err := c.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := c.StdoutPipe()
	if err != nil {
		return nil, err
	}
	if err := c.Start(); err != nil {
		return nil, err
	}
	return &lspServer{
		cmd:    c,
		stdin:  bufio.NewWriter(stdin),
		stdout: bufio.NewScanner(stdout),
	}, nil
}

func (s *lspServer) call(method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	s.seq++
	id := s.seq
	s.mu.Unlock()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	_, err = fmt.Fprintf(s.stdin, "Content-Length: %d\r\n\r\n%s", len(data), data)
	s.stdin.Flush()
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	for s.stdout.Scan() {
		line := s.stdout.Text()
		if line == "" {
			break
		}
	}

	if !s.stdout.Scan() {
		return nil, fmt.Errorf("no response body")
	}
	var resp map[string]any
	if err := json.Unmarshal(s.stdout.Bytes(), &resp); err != nil {
		return nil, err
	}

	if errStr, ok := resp["error"].(map[string]any); ok {
		msg, _ := errStr["message"].(string)
		return nil, fmt.Errorf("LSP error: %s", msg)
	}

	result, _ := resp["result"].(json.RawMessage)
	if result == nil {
		result, _ = json.Marshal(resp["result"])
	}
	return result, nil
}

func (s *lspServer) notify(method string, params any) error {
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	_, err = fmt.Fprintf(s.stdin, "Content-Length: %d\r\n\r\n%s", len(data), data)
	s.stdin.Flush()
	s.mu.Unlock()
	return err
}

func (s *lspServer) close() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}

type LspTool struct {
	mu        sync.Mutex
	server    *lspServer
	serverCmd string
}

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
			"action":   map[string]interface{}{"type": "string", "enum": []string{"definition", "references", "hover", "symbols", "diagnostics"}},
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
	line := params.Line
	column := params.Column

	t.mu.Lock()
	if t.server == nil {
		if t.serverCmd == "" {
			t.serverCmd = "pylsp"
		}
		server, err := newLspServer(t.serverCmd)
		if err != nil {
			t.mu.Unlock()
			return &tool.ToolResult{Error: "lsp_error", Output: fmt.Sprintf("Failed to start LSP server: %v", err)}, nil
		}
		t.server = server

		initParams := map[string]any{
			"processId":             os.Getpid(),
			"rootUri":              "file://" + filepath.ToSlash(ctx.Workspace),
			"capabilities":         map[string]any{},
			"initializationOptions": map[string]any{},
			"trace":                "off",
			"workspaceFolders": []map[string]any{
				{"uri": "file://" + filepath.ToSlash(ctx.Workspace), "name": "workspace"},
			},
		}
		if _, err := t.server.call("initialize", initParams); err != nil {
			t.server.close()
			t.server = nil
			t.mu.Unlock()
			return &tool.ToolResult{Error: "lsp_error", Output: fmt.Sprintf("LSP initialize failed: %v", err)}, nil
		}
		t.server.notify("initialized", nil)
	}
	server := t.server
	t.mu.Unlock()

	pos := map[string]any{"line": line, "character": column}
	textDoc := map[string]any{"uri": fileURI}

	var output string
	switch params.Action {
	case "definition":
		res, err := server.call("textDocument/definition", map[string]any{
			"textDocument": textDoc,
			"position":     pos,
		})
		if err != nil {
			return &tool.ToolResult{Error: "lsp_error", Output: err.Error()}, nil
		}
		output = string(res)

	case "references":
		res, err := server.call("textDocument/references", map[string]any{
			"textDocument": textDoc,
			"position":     pos,
			"context":      map[string]any{"includeDeclaration": true},
		})
		if err != nil {
			return &tool.ToolResult{Error: "lsp_error", Output: err.Error()}, nil
		}
		output = string(res)

	case "hover":
		res, err := server.call("textDocument/hover", map[string]any{
			"textDocument": textDoc,
			"position":     pos,
		})
		if err != nil {
			return &tool.ToolResult{Error: "lsp_error", Output: err.Error()}, nil
		}
		output = string(res)

	case "symbols":
		res, err := server.call("textDocument/documentSymbol", map[string]any{
			"textDocument": textDoc,
		})
		if err != nil {
			return &tool.ToolResult{Error: "lsp_error", Output: err.Error()}, nil
		}
		output = string(res)

	case "diagnostics":
		server.notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        fileURI,
				"languageId": "",
				"version":    1,
				"text":       "",
			},
		})
		output = "Diagnostics requested (check LSP diagnostics notifications)."

	default:
		output = fmt.Sprintf("Action '%s' is partly implemented.", params.Action)
	}

	return &tool.ToolResult{Output: output}, nil
}
