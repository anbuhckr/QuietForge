package tool

import (
	"bufio"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"quietforge/util"
)

type mcpSession struct {
	stdin  *bufio.Writer
	sc     *bufio.Scanner
	stderr io.ReadCloser
	mu     sync.Mutex
	seq    int64
	cancel context.CancelFunc
}

func newMcpSession(ctx context.Context, command string, args []string, env map[string]string, workspace string) (*mcpSession, error) {
	ctx, cancel := context.WithCancel(ctx)
	cmd := exec.CommandContext(ctx, command, args...)
	cmd.Env = os.Environ()
	for k, v := range env {
		cmd.Env = append(cmd.Env, k+"="+v)
	}
	if workspace != "" {
		cmd.Env = append(cmd.Env, "WORKSPACE_DIR="+workspace)
		cmd.Dir = workspace
	}

	stdin, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		return nil, err
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		cancel()
		return nil, err
	}

	if err := cmd.Start(); err != nil {
		cancel()
		return nil, err
	}

	sc := bufio.NewScanner(stdout)
	sc.Buffer(make([]byte, 0, 1024*1024), 50*1024*1024)

	s := &mcpSession{
		stdin:  bufio.NewWriter(stdin),
		sc:     sc,
		stderr: stderr,
		cancel: cancel,
	}

	go func() {
		stderrSc := bufio.NewScanner(stderr)
		stderrSc.Buffer(make([]byte, 0, 64*1024), 10*1024*1024)
		for stderrSc.Scan() {
			log.Printf("MCP stderr [%s]: %s", command, stderrSc.Text())
		}
		if err := stderrSc.Err(); err != nil {
			log.Printf("MCP stderr read error [%s]: %v", command, err)
		}
	}()

	return s, nil
}

func (s *mcpSession) readLine() (string, error) {
	if s.sc.Scan() {
		return s.sc.Text(), nil
	}
	return "", s.sc.Err()
}

const mcpCallTimeout = 5 * time.Minute

func (s *mcpSession) call(method string, params any) (json.RawMessage, error) {
	s.mu.Lock()
	s.seq++
	id := s.seq
	s.mu.Unlock()

	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}

	s.mu.Lock()
	_, err = fmt.Fprintf(s.stdin, "%s\n", data)
	if err == nil {
		err = s.stdin.Flush()
	}
	s.mu.Unlock()
	if err != nil {
		return nil, err
	}

	type readResult struct {
		line string
		err  error
	}
	ch := make(chan readResult, 1)
	go func() {
		line, err := s.readLine()
		ch <- readResult{line, err}
	}()

	var line string
	select {
	case r := <-ch:
		line, err = r.line, r.err
	case <-time.After(mcpCallTimeout):
		s.cancel()
		return nil, fmt.Errorf("MCP call timed out after %v", mcpCallTimeout)
	}
	if err != nil {
		return nil, fmt.Errorf("no response: %w", err)
	}

	var resp map[string]any
	if err := json.Unmarshal([]byte(line), &resp); err != nil {
		return nil, fmt.Errorf("bad JSON: %w", err)
	}
	if errStr, ok := resp["error"].(map[string]any); ok {
		msg, _ := errStr["message"].(string)
		return nil, fmt.Errorf("MCP error: %s", msg)
	}

	result, _ := resp["result"].(json.RawMessage)
	if result == nil {
		result, _ = json.Marshal(resp["result"])
	}
	return result, nil
}

func (s *mcpSession) notify(method string, params any) error {
	req := map[string]any{
		"jsonrpc": "2.0",
		"method":  method,
	}
	if params != nil {
		req["params"] = params
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	s.mu.Lock()
	_, err = fmt.Fprintf(s.stdin, "%s\n", data)
	if err == nil {
		err = s.stdin.Flush()
	}
	s.mu.Unlock()
	return err
}

func (s *mcpSession) close() {
	s.cancel()
}

type mcpDynamicTool struct {
	serverName  string
	toolName    string
	desc        string
	inputSchema map[string]any
	manager     *McpManager
}

func (t *mcpDynamicTool) ID() string {
	return fmt.Sprintf("%s__%s", t.serverName, t.toolName)
}

func (t *mcpDynamicTool) Description() string {
	return fmt.Sprintf("[MCP Server: %s] %s", t.serverName, t.desc)
}

func (t *mcpDynamicTool) Parameters() map[string]any {
	return t.inputSchema
}

func handleBinaryMcpOutput(b []byte, t *mcpDynamicTool) *ToolResult {
	contentType := http.DetectContentType(b)
	if strings.HasPrefix(contentType, "image/") {
		base64Data := base64.StdEncoding.EncodeToString(b)
		dataURL := fmt.Sprintf("data:%s;base64,%s", contentType, base64Data)
		return &ToolResult{
			Output: fmt.Sprintf("MCP tool %s returned an image (%d bytes).", t.toolName, len(b)),
			Attachments: []map[string]interface{}{
				{"url": dataURL},
			},
		}
	}
	return &ToolResult{Error: "binary_content", Output: fmt.Sprintf("MCP tool %s returned binary content. Binary content cannot be displayed.", t.toolName)}
}

func (t *mcpDynamicTool) Execute(args []byte, ctx *ToolContext) (*ToolResult, error) {
	var params map[string]any
	if err := json.Unmarshal(args, &params); err != nil {
		return &ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}
	
	res, err := t.manager.callTool(ctx.Context, t.serverName, t.toolName, params, ctx.Workspace)
	if err != nil {
		return &ToolResult{Error: "mcp_error", Output: err.Error()}, nil
	}
	
	var outStr string
	switch v := res.(type) {
	case []byte:
		if looksBinary(v) {
			if r := handleBinaryMcpOutput(v, t); r != nil {
				return r, nil
			}
		}
		outStr = string(v)
	case string:
		b := []byte(v)
		if looksBinary(b) {
			if r := handleBinaryMcpOutput(b, t); r != nil {
				return r, nil
			}
		}
		outStr = v
	default:
		outStr = fmt.Sprintf("%v", res)
		b := []byte(outStr)
		if looksBinary(b) {
			if r := handleBinaryMcpOutput(b, t); r != nil {
				return r, nil
			}
		}
	}
	return &ToolResult{Output: outStr}, nil
}

type McpManager struct {
	registry  *Registry
	servers   map[string]McpServerDef
	sessions  map[string]*mcpSession
	sessionWs map[string]string
	mu        sync.Mutex
	wg        sync.WaitGroup
	Workspace string
	cancel    context.CancelFunc
}

func NewMcpManager(registry *Registry) *McpManager {
	return &McpManager{
		registry:  registry,
		servers:   make(map[string]McpServerDef),
		sessions:  make(map[string]*mcpSession),
		sessionWs: make(map[string]string),
	}
}



type McpServerDef struct {
	Name        string
	Command     string
	Args        []string
	Environment map[string]string
	Disabled    bool
}

func (m *McpManager) ConnectServers(ctx context.Context, servers []McpServerDef) {
	ctx, cancel := context.WithCancel(ctx)
	m.mu.Lock()
	m.cancel = cancel
	m.mu.Unlock()

	for _, srv := range servers {
		if srv.Disabled {
			continue
		}
		m.mu.Lock()
		m.servers[srv.Name] = srv
		m.mu.Unlock()
		m.wg.Add(1)
		go m.runServer(ctx, srv)
	}
}

func (m *McpManager) RestartServers(ctx context.Context, servers []McpServerDef) {
	m.Close()
	m.mu.Lock()
	m.servers = make(map[string]McpServerDef)
	m.sessions = make(map[string]*mcpSession)
	for _, t := range m.registry.GetAll() {
		if _, ok := t.(*mcpDynamicTool); ok {
			m.registry.RemoveTool(t.ID())
		}
	}
	m.mu.Unlock()
	m.ConnectServers(ctx, servers)
}

func (m *McpManager) startMcpSession(ctx context.Context, srv McpServerDef, ws string) (*mcpSession, error) {
	jailedWs, err := util.JailPath(util.GlobalWorkspacesRoot, ws)
	if err != nil {
		return nil, fmt.Errorf("MCP workspace jailing failed: %v", err)
	}
	log.Printf("MCP: starting server %s in workspace %s", srv.Name, jailedWs)
	session, err := newMcpSession(ctx, srv.Command, srv.Args, srv.Environment, jailedWs)
	if err != nil {
		return nil, err
	}

	initCtx, initCancel := context.WithTimeout(ctx, 15*time.Second)
	defer initCancel()

	initDone := make(chan error, 1)
	go func() {
		_, err := session.call("initialize", map[string]any{
			"protocolVersion": "2024-11-05",
			"capabilities":    map[string]any{},
			"clientInfo": map[string]any{
				"name":    "quietforge",
				"version": "1.0",
			},
		})
		initDone <- err
	}()

	select {
	case err = <-initDone:
	case <-initCtx.Done():
		err = fmt.Errorf("initialize timed out after 15s")
	}

	if err != nil {
		session.close()
		return nil, err
	}

	session.notify("notifications/initialized", nil)
	return session, nil
}

func (m *McpManager) callTool(ctx context.Context, serverName, toolName string, args map[string]any, ws string) (any, error) {
	m.mu.Lock()
	srvDef, ok := m.servers[serverName]
	session := m.sessions[serverName]
	currWs := m.sessionWs[serverName]
	
	if !ok {
		m.mu.Unlock()
		return nil, fmt.Errorf("server %s not found", serverName)
	}

	if session == nil || currWs != ws {
		if session != nil {
			log.Printf("MCP: Workspace changed for %s, restarting server...", serverName)
			session.close()
		}
		
		newSession, err := m.startMcpSession(context.Background(), srvDef, ws)
		if err != nil {
			m.mu.Unlock()
			return nil, err
		}
		
		m.sessions[serverName] = newSession
		m.sessionWs[serverName] = ws
		session = newSession
	}
	m.mu.Unlock()

	result, err := session.call("tools/call", map[string]any{
		"name":      toolName,
		"arguments": args,
	})
	if err != nil {
		m.mu.Lock()
		if m.sessions[serverName] == session {
			session.close()
			delete(m.sessions, serverName)
		}
		m.mu.Unlock()
		return nil, err
	}
	return result, nil
}

func (m *McpManager) runServer(ctx context.Context, srv McpServerDef) {
	defer m.wg.Done()

	session, err := m.startMcpSession(ctx, srv, m.Workspace)
	if err != nil {
		log.Printf("Failed to start MCP server %s: %v", srv.Name, err)
		return
	}

	m.mu.Lock()
	m.sessions[srv.Name] = session
	m.sessionWs[srv.Name] = m.Workspace
	m.mu.Unlock()

	listCtx, listCancel := context.WithTimeout(ctx, 15*time.Second)
	defer listCancel()

	listDone := make(chan error, 1)
	var toolsResult json.RawMessage
	go func() {
		var listErr error
		toolsResult, listErr = session.call("tools/list", nil)
		listDone <- listErr
	}()

	select {
	case err = <-listDone:
	case <-listCtx.Done():
		err = fmt.Errorf("tools/list timed out after 15s")
	}

	if err != nil {
		log.Printf("MCP tools/list failed for %s: %v", srv.Name, err)
		session.close()
		return
	}

	var toolsResp struct {
		Tools []struct {
			Name        string         `json:"name"`
			Description string         `json:"description"`
			InputSchema map[string]any `json:"inputSchema"`
		} `json:"tools"`
	}
	if err := json.Unmarshal(toolsResult, &toolsResp); err != nil {
		log.Printf("MCP tools/list parse failed for %s: %v", srv.Name, err)
		session.close()
		return
	}

	for _, t := range toolsResp.Tools {
		dynTool := &mcpDynamicTool{
			serverName:  srv.Name,
			toolName:    t.Name,
			desc:        t.Description,
			inputSchema: t.InputSchema,
			manager:     m,
		}
		m.registry.Register(dynTool)
	}

	log.Printf("MCP Server Connected: %s (%d tools loaded)", srv.Name, len(toolsResp.Tools))
	
	// Wait for global cancellation, but if the session was replaced, it will be closed independently.
	<-ctx.Done()
	
	m.mu.Lock()
	if currentSession := m.sessions[srv.Name]; currentSession == session {
		delete(m.sessions, srv.Name)
	}
	m.mu.Unlock()
	
	session.close()
}

func (m *McpManager) Close() {
	m.mu.Lock()
	if m.cancel != nil {
		m.cancel()
	}
	for _, s := range m.sessions {
		s.close()
	}
	m.mu.Unlock()
	m.wg.Wait()
}

func looksBinary(data []byte) bool {
	if len(data) == 0 {
		return false
	}
	if data[0] == 0 {
		return true
	}
	if len(data) > 512 {
		ct := http.DetectContentType(data[:512])
		if strings.HasPrefix(ct, "image/") || strings.HasPrefix(ct, "video/") || strings.HasPrefix(ct, "audio/") ||
			ct == "application/zip" || ct == "application/gzip" || ct == "application/pdf" ||
			ct == "application/x-gzip" || ct == "application/x-tar" {
			return true
		}
	}
	nulls := bytes.Count(data[:min(len(data), 8192)], []byte{0})
	return nulls > 0
}
