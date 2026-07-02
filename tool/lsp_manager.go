package tool

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"quietforge/storage"
)

type LspManager struct {
	mu      sync.Mutex
	servers map[string]*LspServer
}

var GlobalLspManager = &LspManager{
	servers: make(map[string]*LspServer),
}

func (m *LspManager) getLanguageForFile(workspace, filePath string) (string, string) {
	ext := filepath.Ext(filePath)
	
	// Fast path detection via root files
	if _, err := os.Stat(filepath.Join(workspace, "go.mod")); err == nil {
		if ext == ".go" {
			return "go", "gopls"
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "package.json")); err == nil {
		if ext == ".js" || ext == ".ts" || ext == ".tsx" || ext == ".jsx" {
			return "typescript", "typescript-language-server --stdio"
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "pyproject.toml")); err == nil || (func() bool { _, err := os.Stat(filepath.Join(workspace, "requirements.txt")); return err == nil })() {
		if ext == ".py" {
			return "python", "pylsp"
		}
	}
	if _, err := os.Stat(filepath.Join(workspace, "Cargo.toml")); err == nil {
		if ext == ".rs" {
			return "rust", "rust-analyzer"
		}
	}

	// Fallback to extensions
	switch ext {
	case ".go":
		return "go", "gopls"
	case ".py":
		return "python", "pylsp"
	case ".js", ".ts", ".jsx", ".tsx":
		return "typescript", "typescript-language-server --stdio"
	case ".rs":
		return "rust", "rust-analyzer"
	default:
		return "", ""
	}
}

func (m *LspManager) GetServer(workspace, filePath string, repo *storage.Repository) (*LspServer, string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	lang, cmdStr := m.getLanguageForFile(workspace, filePath)
	if cmdStr == "" {
		return nil, "", fmt.Errorf("unsupported language or no language server configured for %s", filepath.Ext(filePath))
	}

	key := fmt.Sprintf("%s::%s", workspace, lang)
	if srv, exists := m.servers[key]; exists {
		srv.lastUsed = time.Now()
		return srv, lang, nil
	}

	// Split cmdStr to args
	parts := strings.Fields(cmdStr)
	srv, err := newLspServer(workspace, repo, parts[0], parts[1:]...)
	if err != nil {
		return nil, "", err
	}
	
	initParams := map[string]any{
		"processId":             os.Getpid(),
		"rootUri":              "file://" + filepath.ToSlash(workspace),
		"capabilities":         map[string]any{},
		"initializationOptions": map[string]any{},
		"trace":                "off",
		"workspaceFolders": []map[string]any{
			{"uri": "file://" + filepath.ToSlash(workspace), "name": filepath.Base(workspace)},
		},
	}
	
	if _, err := srv.Call("initialize", initParams); err != nil {
		srv.Close()
		return nil, "", fmt.Errorf("LSP initialize failed: %v", err)
	}
	srv.Notify("initialized", nil)
	
	m.servers[key] = srv
	return srv, lang, nil
}

func (m *LspManager) NotifyFileChanged(workspace, filePath, text string, repo *storage.Repository) {
	srv, lang, err := m.GetServer(workspace, filePath, repo)
	if err != nil || srv == nil {
		return
	}

	srv.mu.Lock()
	version := srv.versions[filePath] + 1
	srv.versions[filePath] = version
	srv.mu.Unlock()

	uri := "file://" + filepath.ToSlash(filePath)
	
	if version == 1 {
		srv.Notify("textDocument/didOpen", map[string]any{
			"textDocument": map[string]any{
				"uri":        uri,
				"languageId": lang,
				"version":    version,
				"text":       text,
			},
		})
	} else {
		srv.Notify("textDocument/didChange", map[string]any{
			"textDocument": map[string]any{
				"uri":     uri,
				"version": version,
			},
			"contentChanges": []map[string]any{
				{"text": text},
			},
		})
	}
}

type LspServer struct {
	cmd       *exec.Cmd
	stdin     *bufio.Writer
	stdout    *bufio.Scanner
	mu        sync.Mutex
	seq       int64
	pending   map[int64]chan json.RawMessage
	workspace string
	repo      *storage.Repository
	lastUsed  time.Time
	versions  map[string]int
}

func newLspServer(workspace string, repo *storage.Repository, cmdName string, args ...string) (*LspServer, error) {
	c := exec.Command(cmdName, args...)
	c.Dir = workspace
	
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

	srv := &LspServer{
		cmd:       c,
		stdin:     bufio.NewWriter(stdin),
		stdout:    bufio.NewScanner(stdout),
		pending:   make(map[int64]chan json.RawMessage),
		workspace: workspace,
		repo:      repo,
		lastUsed:  time.Now(),
		versions:  make(map[string]int),
	}
	
	srv.stdout.Split(splitLspMessages)
	go srv.readLoop()
	return srv, nil
}

func (s *LspServer) Call(method string, params any) (json.RawMessage, error) {
	s.lastUsed = time.Now()
	s.mu.Lock()
	s.seq++
	id := s.seq
	ch := make(chan json.RawMessage, 1)
	s.pending[id] = ch
	s.mu.Unlock()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	}
	if err := s.send(req); err != nil {
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, err
	}

	select {
	case res := <-ch:
		return res, nil
	case <-time.After(30 * time.Second):
		s.mu.Lock()
		delete(s.pending, id)
		s.mu.Unlock()
		return nil, fmt.Errorf("timeout waiting for LSP response")
	}
}

func (s *LspServer) Notify(method string, params any) error {
	s.lastUsed = time.Now()
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
		"params":  params,
	}
	return s.send(req)
}

func (s *LspServer) send(msg map[string]any) error {
	b, err := json.Marshal(msg)
	if err != nil {
		return err
	}
	hdr := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(b))
	
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, err := s.stdin.WriteString(hdr); err != nil {
		return err
	}
	if _, err := s.stdin.Write(b); err != nil {
		return err
	}
	return s.stdin.Flush()
}

func (s *LspServer) Close() {
	if s.cmd != nil && s.cmd.Process != nil {
		s.cmd.Process.Kill()
	}
}

func (s *LspServer) readLoop() {
	for s.stdout.Scan() {
		b := s.stdout.Bytes()
		var msg struct {
			ID     *int64          `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
			Result json.RawMessage `json:"result"`
			Error  *struct {
				Code    int    `json:"code"`
				Message string `json:"message"`
			} `json:"error"`
		}
		if err := json.Unmarshal(b, &msg); err != nil {
			continue
		}

		if msg.ID != nil {
			s.mu.Lock()
			ch := s.pending[*msg.ID]
			delete(s.pending, *msg.ID)
			s.mu.Unlock()
			if ch != nil {
				if msg.Error != nil {
					// We return a synthesized error JSON
					ch <- []byte(fmt.Sprintf(`{"error": %q}`, msg.Error.Message))
				} else {
					ch <- msg.Result
				}
			}
		} else if msg.Method == "textDocument/publishDiagnostics" {
			var params struct {
				URI         string `json:"uri"`
				Diagnostics []struct {
					Message string `json:"message"`
					Severity int `json:"severity"`
				} `json:"diagnostics"`
			}
			if err := json.Unmarshal(msg.Params, &params); err == nil {
				s.handleDiagnostics(params.URI, params.Diagnostics)
			}
		}
	}
}

func (s *LspServer) handleDiagnostics(uri string, diags []struct{Message string `json:"message"`; Severity int `json:"severity"`}) {
	if s.repo == nil || s.workspace == "" {
		return
	}
	
	path := strings.TrimPrefix(uri, "file://")
	path = filepath.FromSlash(path)
	
	s.repo.DB.Conn.Exec("UPDATE workspace_diagnostics SET status = 'resolved' WHERE workspace = ? AND path = ? AND source = 'lsp'", s.workspace, path)
	
	for _, diag := range diags {
		if diag.Severity == 1 { // Error
			id := fmt.Sprintf("lsp-%d", time.Now().UnixNano())
			s.repo.DB.Conn.Exec(
				"INSERT INTO workspace_diagnostics (id, workspace, path, symbol, source, status, severity, message, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
				id, s.workspace, path, "lsp-diagnostic", "lsp", "active", "error", diag.Message, time.Now().Unix(),
			)
		}
	}
}

func splitLspMessages(data []byte, atEOF bool) (advance int, token []byte, err error) {
	if atEOF && len(data) == 0 {
		return 0, nil, nil
	}
	
	idx := bytes.Index(data, []byte("\r\n\r\n"))
	if idx < 0 {
		return 0, nil, nil
	}
	
	header := data[:idx]
	var clen int
	lines := bytes.Split(header, []byte("\r\n"))
	for _, line := range lines {
		if bytes.HasPrefix(bytes.ToLower(line), []byte("content-length:")) {
			fmt.Sscanf(string(bytes.TrimSpace(line[15:])), "%d", &clen)
		}
	}
	
	if clen == 0 {
		return idx + 4, []byte{}, nil
	}
	
	if len(data) < idx+4+clen {
		return 0, nil, nil
	}
	
	return idx + 4 + clen, data[idx+4 : idx+4+clen], nil
}
