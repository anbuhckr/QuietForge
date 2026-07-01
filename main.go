package main

import (
	"bufio"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"embed"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"quietforge/agent"
	"quietforge/config"
	"quietforge/permission"
	"quietforge/provider"
	"quietforge/session"
	"quietforge/storage"
	"quietforge/tool"
	impl "quietforge/tool/implement"
	"quietforge/util"

	"github.com/sashabaranov/go-openai"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/middleware/cors"
	"github.com/gofiber/fiber/v2/middleware/filesystem"
)

//go:embed public/*
var publicFiles embed.FS

var (
	activeSession      *session.Session
	activeConversation string
	engineRunning      bool
	stopRequested      bool
	engineCancel       context.CancelFunc
	engineMu           sync.Mutex
	sessionMu          sync.RWMutex
	liveEvents         []map[string]any
	eventsMu           sync.Mutex
	// token usage stored per-session in activeSession.PromptTokens / CompletionTokens


	bgProcesses   = make(map[string]context.CancelFunc)
	bgProcessesMu sync.Mutex

	toolRegistry    *tool.Registry
	mcpManager      *tool.McpManager

	appCfg          config.Config
	workspaceDir    string
	needsFullRefresh bool
	db           *storage.Database
	repo         *storage.Repository
)

type SSHProcess struct {
	Cmd    *exec.Cmd
	Done   chan struct{}
	Cancel context.CancelFunc
}

type projectEntry struct {
	ID     string           `json:"id"`
	Name   string           `json:"name"`
	Active bool             `json:"active"`
	Path   string           `json:"path"`
	Label  string           `json:"label"`
	Convs  []map[string]any `json:"conversations,omitempty"`
}

func loadCfg() config.Config {
	return config.LoadConfig(".")
}

func buildProviderInstance(id, apiKey, baseURL, model string, disableVision bool) *provider.ProviderInstance {
	if apiKey == "" {
		return nil
	}
	ocfg := openai.DefaultConfig(apiKey)
	if baseURL != "" {
		ocfg.BaseURL = baseURL
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.ResponseHeaderTimeout = 30 * time.Second
	ocfg.HTTPClient = &http.Client{Transport: transport}
	return &provider.ProviderInstance{
		ID:            id,
		Client:        openai.NewClientWithConfig(ocfg),
		Model:         model,
		DisableVision: disableVision,
	}
}

func clientFromCfg(cfg config.Config) *provider.Client {
	globalModel := "gpt-4o"
	if cfg.Model != nil {
		globalModel = *cfg.Model
	}
	
	var instances []provider.ProviderInstance
	
	collect := func(pid string, pc config.ProviderConfig, mdl string) {
		key := ""
		if pc.APIKey != nil { key = *pc.APIKey }
		base := ""
		if pc.BaseURL != nil { base = *pc.BaseURL }
		if pc.Model != nil { mdl = *pc.Model }
		dv := false
		if pc.DisableVision != nil { dv = *pc.DisableVision }
		if inst := buildProviderInstance(pid, key, base, mdl, dv); inst != nil {
			instances = append(instances, *inst)
		}
	}
	
	added := make(map[string]bool)
	for _, pid := range cfg.EnabledProviders {
		if pc, ok := cfg.Provider[pid]; ok {
			collect(pid, pc, globalModel)
			added[pid] = true
		}
	}
	
	for pid, pc := range cfg.Provider {
		if !added[pid] {
			collect(pid, pc, globalModel)
		}
	}

	if len(instances) == 0 {
		if inst := buildProviderInstance("primary", os.Getenv("OPENAI_API_KEY"), "", globalModel, false); inst != nil {
			instances = append(instances, *inst)
		}
	}
	
	return provider.NewMultiClient(instances, globalModel)
}

func promoteFallbackProvider(c *provider.Client, addEvt func(string, map[string]any)) {
	successID := c.GetSuccessfulProviderID()
	if successID == "" {
		return
	}
	currentPrimary := ""
	if len(appCfg.EnabledProviders) > 0 {
		currentPrimary = appCfg.EnabledProviders[0]
	}
	if currentPrimary != "" && successID != currentPrimary {
		rawCfg := loadRawConfig()
		if rawCfg != nil {
			if eps, ok := rawCfg["enabled_providers"].([]interface{}); ok {
				idx := -1
				for idxi, v := range eps {
					if str, ok := v.(string); ok && str == successID {
						idx = idxi
						break
					}
				}
				if idx > 0 {
					eps = append(eps[:idx], eps[idx+1:]...)
					eps = append([]interface{}{successID}, eps...)
					
					if pMap, ok := rawCfg["provider"].(map[string]any); ok {
						newProvMap := make(map[string]any)
						var newEps []interface{}
						for i, oldIDAny := range eps {
							oldID, _ := oldIDAny.(string)
							var newID string
							if i == 0 {
								newID = "primary"
							} else {
								newID = fmt.Sprintf("fallback_%d", i)
							}
							newEps = append(newEps, newID)
							if pCfg, ok := pMap[oldID]; ok {
								newProvMap[newID] = pCfg
							}
						}
						rawCfg["provider"] = newProvMap
						rawCfg["enabled_providers"] = newEps
					} else {
						rawCfg["enabled_providers"] = eps
					}
					
					saveRawConfig(rawCfg)
					appCfg = loadCfg()
					if addEvt != nil {
						addEvt("primary_changed", map[string]any{"new_primary_id": "primary"})
					}
				}
			}
		}
	}
}

func isEngineRunning() bool {
	engineMu.Lock()
	defer engineMu.Unlock()
	return engineRunning
}

var pendingToolApprovals sync.Map

func askPermissionCallback(toolName, toolInput, agentID string) (bool, error) {
	callID := fmt.Sprintf("%d", time.Now().UnixNano())
	ch := make(chan bool, 1)
	pendingToolApprovals.Store(callID, ch)
	defer pendingToolApprovals.Delete(callID)

	var cmdData any = toolInput
	if toolName == "shell" {
		var args map[string]any
		if err := json.Unmarshal([]byte(toolInput), &args); err == nil {
			if cmd, ok := args["command"].(string); ok {
				cmdData = cmd
			}
		}
	}

	addLiveEvent("prompt", map[string]any{
		"call_id": callID,
		"tool":    toolName,
		"command": cmdData,
	})

	select {
	case result := <-ch:
		return result, nil
	case <-time.After(10 * time.Minute):
		return false, fmt.Errorf("timeout waiting for user approval")
	}
}

func buildToolSchemas(agentID string) []map[string]any {
	allowed := agent.GetAgentTools(agentID)
	var schemas []map[string]any
	for _, t := range toolRegistry.GetAll() {
		if agent.IsToolAllowed(t.ID(), allowed) {
			schemas = append(schemas, map[string]any{
				"type": "function",
				"function": map[string]any{
					"name":        t.ID(),
					"description": t.Description(),
					"parameters":  t.Parameters(),
				},
			})
		}
	}
	return schemas
}

func buildOpenAIToolDefs(agentID string) []openai.Tool {
	allowed := agent.GetAgentTools(agentID)
	var tools []openai.Tool
	for _, t := range toolRegistry.GetAll() {
		if agent.IsToolAllowed(t.ID(), allowed) {
			tools = append(tools, openai.Tool{
				Type: "function",
				Function: &openai.FunctionDefinition{
					Name:        t.ID(),
					Description: t.Description(),
					Parameters:  t.Parameters(),
				},
			})
		}
	}
	return tools
}

func getSessionLog() []map[string]any {
	sessionMu.RLock()
	s := activeSession
	sessionMu.RUnlock()
	if s == nil {
		return nil
	}
	raw := s.GetHistory()
	var clean []map[string]any
	for _, m := range raw {
		if m.Role == "system" {
			continue
		}
		entry := map[string]any{
			"id":         m.ID,
			"session_id": m.SessionID,
			"role":       m.Role,
			"parts":      m.Parts,
			"created_at": m.CreatedAt,
		}
		if m.Metadata != nil {
			entry["metadata"] = m.Metadata
			if snap, ok := m.Metadata["snapshot"]; ok {
				entry["snapshot"] = snap
			}
			if runMeta, ok := m.Metadata["run_meta"]; ok {
				entry["run_meta"] = runMeta
			}
		}
		clean = append(clean, entry)
	}
	return clean
}


type projectRegistry struct {
	LastActive string         `json:"last_active"`
	Projects   []projectEntry `json:"projects"`
}

func loadProjectRegistryPath() string {
	return "quietforge_projects.json"
}

func loadProjectRegistry() projectRegistry {
	var pr projectRegistry
	data, err := os.ReadFile(loadProjectRegistryPath())
	if err != nil {
		pr.Projects = []projectEntry{}
		return pr
	}
	json.Unmarshal(data, &pr)
	if pr.Projects == nil {
		pr.Projects = []projectEntry{}
	}
	return pr
}

func saveProjectRegistry(pr projectRegistry) {
	if pr.Projects == nil {
		pr.Projects = []projectEntry{}
	}
	data, _ := json.MarshalIndent(pr, "", "  ")
	os.WriteFile(loadProjectRegistryPath(), data, 0644)
}

func registerProject(path string) projectEntry {
	absPath, _ := filepath.Abs(path)
	pr := loadProjectRegistry()
	pid := "proj_" + strings.ReplaceAll(strings.ReplaceAll(strings.ReplaceAll(absPath, "\\", "_"), "/", "_"), ":", "")
	for _, p := range pr.Projects {
		if p.ID == pid || isAbsPathEqual(p.Path, absPath) {
			p.Path = absPath
			p.Name = filepath.Base(absPath)
			p.Label = filepath.Base(absPath)
			p.Active = false
			saveProjectRegistry(pr)
			return p
		}
	}
	entry := projectEntry{
		ID:    pid,
		Name:  filepath.Base(absPath),
		Path:  absPath,
		Label: filepath.Base(absPath),
	}
	pr.Projects = append([]projectEntry{entry}, pr.Projects...)
	saveProjectRegistry(pr)
	return entry
}

func unregisterProject(path string) string {
	absPath, _ := filepath.Abs(path)
	pr := loadProjectRegistry()
	var filtered []projectEntry
	for _, p := range pr.Projects {
		if !isAbsPathEqual(p.Path, absPath) {
			filtered = append(filtered, p)
		}
	}
	pr.Projects = filtered
	saveProjectRegistry(pr)
	
	// Only delete .agent if the path is inside the global workspaces root to respect the jail
	if isAbsPathEqual(filepath.Dir(absPath), util.GlobalWorkspacesRoot) {
		agentDir := filepath.Join(absPath, ".agent")
		if info, err := os.Stat(agentDir); err == nil && info.IsDir() {
			if err := os.RemoveAll(agentDir); err != nil {
				return fmt.Sprintf("Warning: unregistered but failed to delete .agent: %v", err)
			}
		}
	}
	return ""
}

func listProjectConversations(workspace string) ([]map[string]any, error) {
	if workspace == "" || !dirExists(workspace) {
		return nil, nil
	}
	dbPath := filepath.Join(workspace, ".agent", "sessions.db")
	if !fileExists(dbPath) {
		return nil, nil
	}
	d, err := storage.NewDatabase(dbPath)
	if err != nil {
		return nil, nil
	}
	defer d.Close()
	listRepo := storage.NewRepository(d)
	rows, err := listRepo.ListSessions(50, workspace)
	if err != nil {
		return nil, err
	}
	var items []map[string]any
	for _, row := range rows {
		title := "New conversation"
		msgCount := 0
		msgs, err := listRepo.GetMessages(row.ID)
		if err == nil {
			for _, m := range msgs {
				msgCount++
				if m.Role == "user" && title == "New conversation" {
					parts, err := listRepo.GetMessageParts(m.ID)
					if err == nil {
						for _, p := range parts {
							if p.Type == "text" && p.Content != "" {
								text := p.Content
								if idx := strings.Index(text, "User Request:"); idx >= 0 {
									text = strings.TrimSpace(text[idx+len("User Request:"):])
								}
								lines := strings.SplitN(text, "\n", 2)
								if len(lines[0]) > 58 {
									title = lines[0][:58]
								} else {
									title = lines[0]
								}
								break
							}
						}
					}
				}
			}
		}
		updatedAt := row.UpdatedAt
		if updatedAt == 0 {
			updatedAt = row.CreatedAt
		}
		items = append(items, map[string]any{
			"id":         row.ID,
			"title":      title,
			"updated_at": time.Unix(updatedAt, 0).Format("2006-01-02T15:04:05"),
			"messages":   msgCount,
		})
	}
	for i, j := 0, len(items)-1; i < j; i, j = i+1, j-1 {
		items[i], items[j] = items[j], items[i]
	}
	return items, nil
}

func addLiveEvent(typ string, data map[string]any) {
	if typ == "complete" {
		engineMu.Lock()
		engineRunning = false
		engineMu.Unlock()
	}

	eventsMu.Lock()
	defer eventsMu.Unlock()
	entry := map[string]any{
		"type":            typ,
		"conversation_id": activeConversation,
	}
	for k, v := range data {
		entry[k] = v
	}
	if _, ok := entry["time"]; !ok {
		entry["time"] = time.Now().Format("15:04:05")
	}
	msg := ""
	if m, ok := entry["text"].(string); ok && m != "" {
		msg = m
	} else if m, ok := entry["event"].(string); ok && m != "" {
		msg = m
	} else if m, ok := entry["message"].(string); ok && m != "" {
		msg = m
	}
	entry["event"] = msg
	entry["message"] = msg
	if _, ok := entry["type"]; !ok {
		entry["type"] = "activity"
	}
	liveEvents = append(liveEvents, entry)
	debugLog("addLiveEvent: type=%s msg=%.60s liveEvents=%d", typ, msg, len(liveEvents))
	go broadcastEvent(entry)
}

var subscribers []chan map[string]any
var subsMu sync.Mutex

func broadcastEvent(entry map[string]any) {
	subsMu.Lock()
	defer subsMu.Unlock()
	debugLog("broadcastEvent: type=%s %d subscribers", entry["type"], len(subscribers))
	for i, ch := range subscribers {
		select {
		case ch <- entry:
		default:
			debugLog("broadcastEvent: subscriber %d channel full, dropped", i)
		}
	}
}

func subscribe() chan map[string]any {
	ch := make(chan map[string]any, 64)
	subsMu.Lock()
	subscribers = append(subscribers, ch)
	n := len(subscribers)
	subsMu.Unlock()
	debugLog("subscribe: %d subscribers", n)
	return ch
}

func unsubscribe(ch chan map[string]any) {
	subsMu.Lock()
	defer subsMu.Unlock()
	for i, s := range subscribers {
		if s == ch {
			subscribers = append(subscribers[:i], subscribers[i+1:]...)
			close(ch)
			debugLog("unsubscribe: removed index %d, %d remaining", i, len(subscribers))
			return
		}
	}
}

func initWorkspace(path string) {
	os.MkdirAll(path, 0755)
	agent.LoadCustomAgents(path)
	os.MkdirAll(filepath.Join(path, ".agent"), 0755)

	gitDir := filepath.Join(path, ".git")
	if _, err := os.Stat(gitDir); os.IsNotExist(err) {
		cmd := exec.Command("git", "init")
		cmd.Dir = path
		cmd.Run()
	}

	gitignorePath := filepath.Join(path, ".gitignore")
	if _, err := os.Stat(gitignorePath); os.IsNotExist(err) {
		os.WriteFile(gitignorePath, []byte(".agent/\n__pycache__/\nnode_modules/\n.venv/\nvenv/\n.env\n.DS_Store\n.playwright-mcp/\n.playwright/\n"), 0644)
	} else {
		data, _ := os.ReadFile(gitignorePath)
		if !strings.Contains(string(data), ".agent") {
			f, _ := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
			if f != nil {
				f.WriteString("\n.agent/\n")
				f.Close()
			}
		}
		if !strings.Contains(string(data), ".playwright-mcp") || !strings.Contains(string(data), ".playwright/") {
			f, _ := os.OpenFile(gitignorePath, os.O_APPEND|os.O_WRONLY, 0644)
			if f != nil {
				f.WriteString("\n.playwright-mcp/\n.playwright/\n")
				f.Close()
			}
		}
	}
	
	// Ensure there is at least one commit so that git stash/diff works for artifact tracking
	checkCmd := exec.Command("git", "rev-parse", "HEAD")
	checkCmd.Dir = path
	if err := checkCmd.Run(); err != nil {
		existing := 0
		entries, _ := os.ReadDir(path)
		for _, e := range entries {
			if e.Name() != ".git" && e.Name() != ".agent" && e.Name() != ".gitignore" {
				existing++
			}
		}
		if existing == 0 {
			readme := filepath.Join(path, "README.md")
			os.WriteFile(readme, []byte("# "+filepath.Base(path)+"\n\nInitialized by QuietForge.\n"), 0644)
		}

		exec.Command("git", "-C", path, "add", ".").Run()
		exec.Command("git", "-C", path, "commit", "-m", "Initial commit by QuietForge").Run()
	}
}

func loadLatestSession(ws string) {
	if ws == "" {
		return
	}
	dbPath := filepath.Join(ws, ".agent", "sessions.db")
	if !fileExists(dbPath) {
		return
	}
	d, err := storage.NewDatabase(dbPath)
	if err != nil {
		return
	}
	defer d.Close()
	r := storage.NewRepository(d)
	rows, err := r.ListSessions(1, ws)
	if err != nil || len(rows) == 0 {
		return
	}
	latest := rows[0]
	s := session.NewSession(latest.ID, r, latest.AgentID, configToDict(appCfg), ws)
	if err := s.Load(); err != nil {
		return
	}
	if len(s.GetHistory()) > 0 {
		activeConversation = latest.ID
		activeSession = s
	}
}

func buildTree(dirPath, workspaceRoot string) []map[string]any {
	ignored := map[string]bool{
		".git": true, "node_modules": true, ".venv": true, "venv": true,
		"__pycache__": true, ".agent": true, ".pytest_cache": true,
		".opencode": true, "target": true, "build": true, "dist": true,
		".next": true, ".idea": true,
	}
	var tree []map[string]any
	entries, err := os.ReadDir(dirPath)
	if err != nil {
		return tree
	}
	sort.Slice(entries, func(i, j int) bool {
		ei, ej := entries[i], entries[j]
		if ei.IsDir() != ej.IsDir() {
			return ei.IsDir()
		}
		return strings.ToLower(ei.Name()) < strings.ToLower(ej.Name())
	})
	for _, entry := range entries {
		if ignored[entry.Name()] || strings.HasPrefix(entry.Name(), ".") {
			if entry.Name() != ".env" && entry.Name() != ".agents" && entry.Name() != ".github" {
				continue
			}
		}
		if entry.Name() == ".DS_Store" || entry.Name() == "Thumbs.db" || entry.Name() == ".gitignore" {
			continue
		}
		relPath, _ := filepath.Rel(workspaceRoot, filepath.Join(dirPath, entry.Name()))
		relPath = strings.ReplaceAll(relPath, "\\", "/")
		if entry.IsDir() {
			children := buildTree(filepath.Join(dirPath, entry.Name()), workspaceRoot)
			tree = append(tree, map[string]any{
				"name":     entry.Name(),
				"type":     "dir",
				"path":     relPath,
				"children": children,
			})
		} else {
			tree = append(tree, map[string]any{
				"name": entry.Name(),
				"type": "file",
				"path": relPath,
			})
		}
	}
	return tree
}



var (
	flagPassword string
	flagPort     int
	flagSSLPort  int
	flagSSLCert  string
	flagSSLKey   string
)

func main() {
	var versionFlag bool
	flag.BoolVar(&debugMode, "debug", false, "Enable verbose debug logging")
	flag.BoolVar(&versionFlag, "version", false, "Print version information")
	flag.StringVar(&flagPassword, "password", "", "Set UI password (overrides config)")
	flag.IntVar(&flagPort, "port", 0, "Set HTTP port (overrides config)")
	flag.IntVar(&flagSSLPort, "ssl_port", 0, "Set HTTPS port (overrides config)")
	flag.StringVar(&flagSSLCert, "ssl_cert", "", "Set SSL certificate path (overrides config)")
	flag.StringVar(&flagSSLKey, "ssl_key", "", "Set SSL key path (overrides config)")
	flag.Parse()

	if versionFlag {
		fmt.Println("QuietForge v1.0.5")
		os.Exit(0)
	}
	provider.Debug = debugMode
	killZombieProcesses()
	ensureProjectInit()

	debugLog("main: starting with debug=%v", debugMode)
	appCfg = loadCfg()
	dbPath := "quietforge.db"
	if cfgDB, ok := os.LookupEnv("QUIETFORGE_DB_PATH"); ok && cfgDB != "" {
		dbPath = cfgDB
	}
	if appCfg.Mode != nil {
		if p, ok := appCfg.Mode["db_path"].(string); ok && p != "" {
			dbPath = p
		}
	}
	debugLog("main: db_path=%s", dbPath)

	var err error
	if err := util.InitWorkspacesRoot(); err != nil {
		log.Fatalf("Failed to initialize workspaces root: %v", err)
	}

	db, err = storage.NewDatabase(dbPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer db.Close()

	repo = storage.NewRepository(db)
	toolRegistry = tool.NewRegistry()
	registerTools()

	mcpManager = tool.NewMcpManager(toolRegistry)
	mcpManager.Workspace = workspaceDir
	if appCfg.Mcp != nil && len(appCfg.Mcp.Servers) > 0 {
		var mcpServers []tool.McpServerDef
		for name, sc := range appCfg.Mcp.Servers {
			cmd := sc.Command
			if len(cmd) == 0 {
				log.Printf("MCP: server %s has no command, skipping", name)
				continue
			}
			mcpServers = append(mcpServers, tool.McpServerDef{
				Name:        name,
				Command:     cmd[0],
				Args:        cmd[1:],
				Environment: sc.Environment,
				Disabled:    sc.Disabled,
			})
		}
		if len(mcpServers) > 0 {
			mcpCtx, mcpCancel := context.WithCancel(context.Background())
			// Store MCP cancel separately (not engineCancel, which is for /stop)
			mcpManager.ConnectServers(mcpCtx, mcpServers)
			log.Printf("MCP: connecting to %d server(s)", len(mcpServers))
			// Cancel MCP on shutdown
			defer mcpCancel()
		}
	}
	defer mcpManager.Close()


	activeConversation = "session_" + fmt.Sprintf("%d", time.Now().Unix())

	pr := loadProjectRegistry()
	if pr.LastActive != "" {
		workspaceDir = pr.LastActive
		os.Setenv("WORKSPACE_DIR", workspaceDir)
		initWorkspace(workspaceDir)
		if mcpManager != nil {
			mcpManager.Workspace = workspaceDir
		}
	} else if len(pr.Projects) > 0 {
		workspaceDir = pr.Projects[0].Path
		os.Setenv("WORKSPACE_DIR", workspaceDir)
		initWorkspace(workspaceDir)
		if mcpManager != nil {
			mcpManager.Workspace = workspaceDir
		}
	}

	app := fiber.New(fiber.Config{
		DisableStartupMessage: true,
	})
	app.Use(cors.New())
	app.Use(authMiddleware)
	app.Use("/public", filesystem.New(filesystem.Config{
		Root:       http.FS(publicFiles),
		PathPrefix: "public",
		Browse:     false,
	}))

	app.Get("/", func(c *fiber.Ctx) error {
		file, err := publicFiles.ReadFile("public/index.html")
		if err != nil {
			return c.Status(500).SendString("index.html not found in binary")
		}
		c.Set("Content-Type", "text/html")
		return c.Send(file)
	})

	api := app.Group("/api")

	setupAuthRoutes(app, api)
	setupHealthRoutes(api)
	setupChatRoutes(api)
	setupConfigRoutes(api)
	setupProjectRoutes(api)
	setupWorkspaceRoutes(api)
	setupToolRoutes(api)
	setupMiscRoutes(api)
	setupStreamRoutes(api)

	sslCert, sslKey := loadRawSSLConfig()
	if flagSSLCert != "" {
		sslCert = flagSSLCert
	}
	if flagSSLKey != "" {
		sslKey = flagSSLKey
	}
	hasSSLCfg := sslCert != "" && sslKey != ""
	port := 80
	if appCfg.Port != nil {
		port = *appCfg.Port
	}
	if flagPort > 0 {
		port = flagPort
	}
	if hasSSLCfg {
		port = 443
		if appCfg.SSLPort != nil {
			port = *appCfg.SSLPort
		}
		if flagSSLPort > 0 {
			port = flagSSLPort
		}
	}
	addr := fmt.Sprintf("0.0.0.0:%d", port)
	log.Printf("QuietForge server starting on %s", addr)

	if hasSSLCfg {
		sslOptions := loadSSLCertificates(sslCert, sslKey)
		if sslOptions != nil {
			listener, err := tls.Listen("tcp", addr, sslOptions)
			if err != nil {
				log.Fatalf("Failed to start TLS listener: %v", err)
			}
			log.Fatal(app.Listener(listener))
		}
	}
	log.Fatal(app.Listen(addr))
}

func killZombieProcesses() {
	output, err := exec.Command("netstat", "-ano").Output()
	if err != nil {
		return
	}
	pid := os.Getpid()
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.Contains(line, "127.0.0.1:8000") && !strings.Contains(line, "127.0.0.1:80") && !strings.Contains(line, "0.0.0.0:8000") && !strings.Contains(line, "0.0.0.0:80") {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) > 0 {
			last := parts[len(parts)-1]
			var zombiePID int
			if _, err := fmt.Sscanf(last, "%d", &zombiePID); err == nil && zombiePID > 0 && zombiePID != pid {
				proc, err := os.FindProcess(zombiePID)
				if err == nil {
					proc.Kill()
					log.Printf("Killed zombie server PID %d", zombiePID)
				}
			}
		}
	}
}

func loadRawSSLConfig() (string, string) {
	certFile, keyFile := "", ""
	for _, path := range config.ProjectConfigFiles(".") {
		raw := readJSONFile(path)
		if raw == nil {
			continue
		}
		if c, ok := raw["ssl_cert"].(string); ok && c != "" {
			certFile = c
		}
		if k, ok := raw["ssl_key"].(string); ok && k != "" {
			keyFile = k
		}
	}
	configDir := config.GlobalConfigDir()
	for _, name := range []string{"config.json", "quietforge.json", "quietforge.jsonc"} {
		raw := readJSONFile(filepath.Join(configDir, name))
		if raw == nil {
			continue
		}
		if c, ok := raw["ssl_cert"].(string); ok && c != "" {
			certFile = c
		}
		if k, ok := raw["ssl_key"].(string); ok && k != "" {
			keyFile = k
		}
	}
	if raw := readJSONFile("quietforge.json"); raw != nil {
		if c, ok := raw["ssl_cert"].(string); ok && c != "" {
			certFile = c
		}
		if k, ok := raw["ssl_key"].(string); ok && k != "" {
			keyFile = k
		}
	}
	return certFile, keyFile
}

func readJSONFile(path string) map[string]any {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil
	}
	return raw
}

func loadSSLCertificates(certFile, keyFile string) *tls.Config {
	if certFile == "" {
		certFile = os.Getenv("QUIETFORGE_SSL_CERT")
	}
	if keyFile == "" {
		keyFile = os.Getenv("QUIETFORGE_SSL_KEY")
	}
	if certFile == "" || keyFile == "" {
		if _, err := os.Stat("server.crt"); err == nil {
			certFile = "server.crt"
			keyFile = "server.key"
		}
	}
	if certFile == "" || keyFile == "" {
		return nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		log.Printf("Warning: Failed to load SSL certificate: %v", err)
		return nil
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}
}

func registerTools() {
	for _, t := range []tool.Tool{
		&impl.ReadTool{},
		&impl.WriteTool{},
		&impl.EditTool{},
		&impl.ApplyPatchTool{},
		&impl.SearchReplaceTool{},
		&impl.GrepTool{},
		&impl.GlobTool{},
		&impl.ShellTool{},
		&impl.WebFetchTool{},
		&impl.InvokeSubagentTool{
			SpawnFunc: func(prompt, agentType, parentSessionID string) (string, error) {
				return spawnSubagent(prompt, agentType, parentSessionID)
			},
		},
		&impl.TodoWriteTool{},
		&impl.SkillTool{},
		&impl.LspTool{},
		&impl.AstSearchTool{},
		&impl.RevertTool{},
		&impl.PlanExitTool{},
		&impl.DefineSubagentTool{},
		&impl.WriteArtifactTool{},
		&impl.InvalidTool{},
	} {
		toolRegistry.Register(t)
	}
}

var contextLimitRegex = regexp.MustCompile(`(?i)(?:maximum\s+context\s+(?:length\s+)?is\s+(\d+)|(\d+)\s*>\s*(\d+)|(?:context\s+(?:length|window|size)).{0,40}?(\d+)\s*(?:token|character)?)`)

func extractContextLimit(errStr string) int {
	matches := contextLimitRegex.FindStringSubmatch(errStr)
	if matches == nil {
		return 0
	}
	if matches[1] != "" {
		if n, err := strconv.Atoi(matches[1]); err == nil {
			return n
		}
	}
	if matches[3] != "" {
		if n, err := strconv.Atoi(matches[3]); err == nil {
			return n
		}
	}
	if matches[4] != "" {
		if n, err := strconv.Atoi(matches[4]); err == nil {
			return n
		}
	}
	return 0
}

func loadRawConfig() map[string]any {
	for _, path := range config.ProjectConfigFiles(".") {
		if raw := readJSONFile(path); raw != nil {
			return raw
		}
	}
	return nil
}

func saveRawConfig(raw map[string]any) {
	path := ""
	for _, p := range config.ProjectConfigFiles(".") {
		path = p
		break
	}
	if path == "" {
		path = filepath.Join(".", ".quietforge", "config.json")
	}
	os.MkdirAll(filepath.Dir(path), 0755)
	data, _ := json.MarshalIndent(raw, "", "  ")
	os.WriteFile(path, data, 0644)
}

func resolveAgentForMode(mode string, rawCfg map[string]any) string {
	switch mode {
	case "chat":
		return "explore"
	case "plan":
		return "plan"
	default:
		if da, ok := rawCfg["default_agent"].(string); ok && da != "" {
			return da
		}
		return "build"
	}
}

func getConfigPassword() string {
	if flagPassword != "" {
		return flagPassword
	}
	for _, path := range config.ProjectConfigFiles(".") {
		raw := readJSONFile(path)
		if raw == nil {
			continue
		}
		if pwd, ok := raw["password"].(string); ok && pwd != "" {
			return pwd
		}
	}
	raw := readJSONFile(filepath.Join(config.GlobalConfigDir(), "quietforge.json"))
	if raw != nil {
		if pwd, ok := raw["password"].(string); ok && pwd != "" {
			return pwd
		}
	}
	if raw := readJSONFile("quietforge.json"); raw != nil {
		if pwd, ok := raw["password"].(string); ok && pwd != "" {
			return pwd
		}
	}
	return ""
}

func sha256Hex(s string) string {
	h := sha256.Sum256([]byte(s))
	return fmt.Sprintf("%x", h)
}

func authMiddleware(c *fiber.Ctx) error {
	pwd := getConfigPassword()
	if pwd == "" {
		debugLog("auth: no password configured, skipping auth for %s", c.Path())
		return c.Next()
	}
	path := c.Path()
	if path == "/login" || path == "/api/login" || path == "/api/logout" {
		debugLog("auth: bypassing for login/logout path: %s", path)
		return c.Next()
	}
	if strings.HasPrefix(path, "/public/") {
		debugLog("auth: bypassing for public path: %s", path)
		return c.Next()
	}
	expectedHash := sha256Hex(pwd)
	if c.Cookies("qf_auth") == expectedHash {
		debugLog("auth: valid cookie for %s", path)
		return c.Next()
	}
	debugLog("auth: invalid or missing cookie for %s", path)
	if strings.HasPrefix(path, "/api/") {
		debugLog("auth: returning 401 for %s", path)
		return c.Status(401).JSON(fiber.Map{"error": "Unauthorized"})
	}
	debugLog("auth: redirecting to /login for %s", path)
	return c.Redirect("/login")
}

func setupAuthRoutes(app *fiber.App, api fiber.Router) {
	app.Get("/login", func(c *fiber.Ctx) error {
		loginPath := "./public/login.html"
		if _, err := os.Stat(loginPath); os.IsNotExist(err) {
			debugLog("login.html not found at %s", loginPath)
			return c.Status(404).SendString("login.html not found.")
		}
		debugLog("serving login.html")
		return c.SendFile(loginPath)
	})
	api.Post("/login", func(c *fiber.Ctx) error {
		payload := new(struct {
			Password string `json:"password"`
		})
		c.BodyParser(payload)
		pwd := getConfigPassword()
		debugLog("login attempt: password configured=%v", pwd != "")
		if pwd == "" {
			debugLog("login: no password configured, returning ok=true")
			return c.JSON(fiber.Map{"ok": true})
		}
		if payload.Password == pwd {
			hashed := sha256Hex(pwd)
			c.Cookie(&fiber.Cookie{
				Name:     "qf_auth",
				Value:    hashed,
				HTTPOnly: true,
				Path:     "/",
			})
			debugLog("login successful, cookie set")
			return c.JSON(fiber.Map{"ok": true})
		}
		debugLog("login failed: invalid password")
		return c.Status(401).JSON(fiber.Map{"error": "Invalid password"})
	})
	api.Post("/logout", func(c *fiber.Ctx) error {
		debugLog("logout called")
		c.Cookie(&fiber.Cookie{
			Name:     "qf_auth",
			Value:    "",
			HTTPOnly: true,
			Path:     "/",
			Expires:  time.Now().Add(-1 * time.Hour),
		})
		debugLog("logout: cookie cleared")
		return c.JSON(fiber.Map{"ok": true})
	})
}

func setupHealthRoutes(api fiber.Router) {
	api.Get("/status", func(c *fiber.Ctx) error {
		engineMu.Lock()
		running := engineRunning
		engineMu.Unlock()
		activePath := workspaceDir
		cfg := loadCfg()
		auth := getConfigPassword() != ""
		full := c.Query("full") == "true" || needsFullRefresh
		needsFullRefresh = false
		mode := "build"
		if rawCfg := loadRawConfig(); rawCfg != nil {
			if m, ok := rawCfg["intent_mode"].(string); ok {
				mode = m
			} else if m, ok := rawCfg["mode"].(string); ok {
				mode = m
			}
		}
		agentID := mode // display name matches mode, not internal agent ID
		if agentID == "" {
			agentID = "build"
		}
		model := "gpt-4o"
		if len(cfg.EnabledProviders) > 0 {
			primaryID := cfg.EnabledProviders[0]
			if pc, ok := cfg.Provider[primaryID]; ok && pc.Model != nil && *pc.Model != "" {
				model = *pc.Model
			}
		}
		if model == "gpt-4o" && cfg.Model != nil && *cfg.Model != "" {
			model = *cfg.Model
		}

		resp := fiber.Map{
			"status":                 "running",
			"running":                running,
			"agent_status":           "V3 Engine Ready",
			"provider":               "openai_compatible",
			"model":                  model,
			"workspace":              activePath,
			"project":                fiber.Map{"workspace": activePath},
			"active_conversation_id": activeConversation,
			"auth_enabled":           auth,
			"mode":                   mode,
			"agent":                  agentID,
			"total_prompt_tokens":    getPromptTokens(),
			"total_completion_tokens": getCompletionTokens(),
			"input_token_price":      2.50,
			"output_token_price":     10.00,
			"features":               fiber.Map{},
			"stop_requested":         stopRequested,
			"backend_diagnostics":    fiber.Map{},
		}

		if !running {
			resp["status"] = "idle"
		}

		if full {
			pr := loadProjectRegistry()
			projects := pr.Projects
			for i, p := range projects {
				pPath := p.Path
				p.Active = activePath != "" && isAbsPathEqual(pPath, activePath)
				if _, err := os.Stat(pPath); err == nil {
					convs, err := listProjectConversations(pPath)
					if err == nil {
						p.Convs = convs
					}
				}
				projects[i] = p
			}
			resp["projects"] = projects
			resp["session_log"] = getSessionLog()
			eventsMu.Lock()
			eventsCopy := make([]map[string]any, len(liveEvents))
			copy(eventsCopy, liveEvents)
			eventsMu.Unlock()
			resp["live_events"] = eventsCopy
			resp["artifacts"] = getArtifactsForUI(activePath)
			resp["events"] = eventsCopy
		}

		return c.JSON(resp)
	})
}

func configToDict(cfg config.Config) map[string]any {
	d := make(map[string]any)
	if cfg.Model != nil {
		d["model"] = *cfg.Model
	}
	if len(cfg.Provider) > 0 {
		providers := make(map[string]any, len(cfg.Provider))
		for k, v := range cfg.Provider {
			pd := make(map[string]any)
			if v.APIKey != nil {
				pd["api_key"] = *v.APIKey
			}
			if v.BaseURL != nil {
				pd["base_url"] = *v.BaseURL
			}
			pd["options"] = v.Options
			providers[k] = pd
		}
		d["provider"] = providers
	}
	if len(cfg.Agent) > 0 {
		d["agent"] = cfg.Agent
	}
	if len(cfg.Permission) > 0 {
		d["permission"] = cfg.Permission
	}
	if cfg.Shell != nil {
		d["shell"] = *cfg.Shell
	}
	if cfg.Username != nil {
		d["username"] = *cfg.Username
	}
	if len(cfg.Instructions) > 0 {
		d["instructions"] = cfg.Instructions
	}
	if cfg.DefaultAgent != nil {
		d["default_agent"] = *cfg.DefaultAgent
	}
	if len(cfg.Mode) > 0 {
		d["mode"] = cfg.Mode
	}
	return d
}

func setupChatRoutes(api fiber.Router) {
	api.Post("/chat/new", func(c *fiber.Ctx) error {
		if isEngineRunning() {
			debugLog("/chat/new: engine running, rejecting")
			return c.Status(409).JSON(fiber.Map{"error": "Cannot create conversation while running."})
		}
		payload := new(struct {
			AgentID string `json:"agent_id"`
		})
		c.BodyParser(payload)
		if payload.AgentID == "" {
			if rawCfg := loadRawConfig(); rawCfg != nil {
				mode := "build"
				if m, ok := rawCfg["intent_mode"].(string); ok {
					mode = m
				}
				payload.AgentID = resolveAgentForMode(mode, rawCfg)
			} else {
				payload.AgentID = "build"
			}
		}
		activeConversation = "session_" + fmt.Sprintf("%d", time.Now().Unix())
		sessionMu.Lock()
		activeSession = nil
		sessionMu.Unlock()
		debugLog("/chat/new: conversation=%s agent=%s", activeConversation, payload.AgentID)
		addLiveEvent("new_conversation", map[string]any{
			"conversation_id": activeConversation,
			"agent_id":        payload.AgentID,
		})
		return c.JSON(fiber.Map{
			"ok":              true,
			"conversation_id": activeConversation,
			"session_log":     []map[string]any{},
		})
	})

	api.Post("/chat/switch", func(c *fiber.Ctx) error {
		if isEngineRunning() {
			return c.Status(409).JSON(fiber.Map{"error": "Cannot switch conversations while running."})
		}
		payload := new(struct {
			ConversationID string `json:"conversation_id"`
			ID             string `json:"id"`
		})
		c.BodyParser(payload)
		cid := payload.ID
		if cid == "" {
			cid = payload.ConversationID
		}
		if cid == "" {
			cid = activeConversation
		}
		debugLog("/chat/switch: target=%s current=%s", cid, activeConversation)
		activeConversation = cid
		activeSession = nil
		sessionLog := []map[string]any{}
		tryLoad := func(workspace string) (bool, error) {
			if workspace == "" {
				return false, nil
			}
			dbPath := filepath.Join(workspace, ".agent", "sessions.db")
			if !fileExists(dbPath) {
				return false, nil
			}
			d, err := storage.NewDatabase(dbPath)
			if err != nil {
				return false, err
			}
			defer d.Close()
			r := storage.NewRepository(d)
			s := session.NewSession(cid, r, "build", configToDict(appCfg), workspace)
			if err := s.Load(); err != nil {
				return false, err
			}
			if len(s.GetHistory()) == 0 {
				return false, nil
			}
			activeSession = s
			sessionLog = getSessionLog()
			os.Setenv("WORKSPACE_DIR", workspace)
			workspaceDir = workspace
			return true, nil
		}
		// Try current workspace first
		ws := os.Getenv("WORKSPACE_DIR")
		found := false
		if ws != "" {
			found, _ = tryLoad(ws)
			debugLog("/chat/switch: current workspace %s found=%v", ws, found)
		}
		// Search all project databases
		if !found {
			pr := loadProjectRegistry()
			debugLog("/chat/switch: searching %d projects", len(pr.Projects))
			for _, p := range pr.Projects {
				if p.Path == ws {
					continue
				}
				if ok, _ := tryLoad(p.Path); ok {
					debugLog("/chat/switch: found in project %s", p.Path)
					break
				}
			}
		}
		debugLog("/chat/switch: result found=%v sessionLog=%d entries", activeSession != nil, len(sessionLog))
		return c.JSON(fiber.Map{
			"ok":              true,
			"conversation_id": activeConversation,
			"session_log":     sessionLog,
		})
	})

	api.Post("/chat/delete", func(c *fiber.Ctx) error {
		if isEngineRunning() {
			return c.Status(409).JSON(fiber.Map{"error": "Cannot delete while running."})
		}
		body := c.Body()
		var payload struct {
			ID string `json:"id"`
		}
		json.Unmarshal(body, &payload)
		convID := payload.ID
		if convID == "" {
			convID = activeConversation
		}
		debugLog("/chat/delete: convID=%s", convID)
		// Search all registered projects for the session to delete
		pr := loadProjectRegistry()
		for _, p := range pr.Projects {
			dbPath := filepath.Join(p.Path, ".agent", "sessions.db")
			if !fileExists(dbPath) {
				continue
			}
			d, err := storage.NewDatabase(dbPath)
			if err != nil {
				continue
			}
			r := storage.NewRepository(d)
			debugLog("/chat/delete: deleting from project %s", p.Path)
			r.DeleteSession(convID)
			d.Close()
		}
		if convID == activeConversation {
			activeSession = nil
			// Find next conversation
			found := false
			for _, p := range pr.Projects {
				dbPath := filepath.Join(p.Path, ".agent", "sessions.db")
				if !fileExists(dbPath) {
					continue
				}
				d, err := storage.NewDatabase(dbPath)
				if err != nil {
					continue
				}
				r := storage.NewRepository(d)
				remaining, _ := r.ListSessions(1, p.Path)
				d.Close()
				if len(remaining) > 0 {
					activeConversation = remaining[0].ID
					debugLog("/chat/delete: next conversation %s", activeConversation)
					found = true
					break
				}
			}
			if !found {
				activeConversation = "session_" + fmt.Sprintf("%d", time.Now().Unix())
				debugLog("/chat/delete: no conversations left, new ID %s", activeConversation)
			}
		}
		needsFullRefresh = true
		debugLog("/chat/delete: needsFullRefresh set to true")
		return c.JSON(fiber.Map{"ok": true, "session_log": []any{}})
	})

	api.Post("/chat/revert", func(c *fiber.Ctx) error {
		payload := new(struct {
			MessageID string `json:"message_id"`
		})
		if err := c.BodyParser(payload); err != nil || payload.MessageID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "message_id is required"})
		}
		if activeSession == nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
		}
		debugLog("/chat/revert: messageID=%s", payload.MessageID)
		msgs := activeSession.GetHistory()
		var targetMsg *session.Message
		for _, m := range msgs {
			if m.ID == payload.MessageID {
				targetMsg = &m
				break
			}
		}
		if targetMsg == nil {
			return c.Status(404).JSON(fiber.Map{"error": "Message not found"})
		}
		snapRaw, ok := targetMsg.Metadata["snapshot"]
		if !ok {
			return c.Status(404).JSON(fiber.Map{"error": "No snapshot for this message"})
		}
		snapHash, ok := snapRaw.(string)
		if !ok || snapHash == "" {
			return c.Status(404).JSON(fiber.Map{"error": "No snapshot for this message"})
		}
		ws := activeSession.Workspace
		if ws == "" {
			ws = workspaceDir
		}
		sm := util.NewSnapshotManager(ws)
		if !sm.Restore(snapHash) {
			return c.Status(500).JSON(fiber.Map{"error": "Failed to restore workspace"})
		}
		// Delete files created by messages after the snapshot
		for _, m := range msgs {
			if m.CreatedAt >= targetMsg.CreatedAt && m.Role == "assistant" {
				if runMetaRaw, ok := m.Metadata["run_meta"]; ok {
					if runMeta, ok := runMetaRaw.(map[string]any); ok {
						if changesRaw, ok := runMeta["workspace_changes"]; ok {
							if changes, ok := changesRaw.(map[string]any); ok {
								if createdRaw, ok := changes["created"]; ok {
									if created, ok := createdRaw.([]any); ok {
										for _, f := range created {
											os.Remove(filepath.Join(ws, fmt.Sprintf("%v", f)))
										}
									}
								}
							}
						}
					}
				}
			}
		}
		if repo != nil {
			repo.DeleteMessagesAfter(activeSession.SessionID, targetMsg.CreatedAt)
		}
		activeSession.Load()
		return c.JSON(fiber.Map{"ok": true, "session_log": getSessionLog()})
	})

	api.Post("/chat/revert-file", func(c *fiber.Ctx) error {
		payload := new(struct {
			Path      string `json:"path"`
			MessageID string `json:"message_id"`
		})
		if err := c.BodyParser(payload); err != nil || payload.Path == "" || payload.MessageID == "" {
			return c.Status(400).JSON(fiber.Map{"error": "message_id and path are required"})
		}
		if activeSession == nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
		}
		msgs := activeSession.GetHistory()
		var targetMsg *session.Message
		for _, m := range msgs {
			if m.ID == payload.MessageID {
				targetMsg = &m
				break
			}
		}
		if targetMsg == nil {
			return c.Status(404).JSON(fiber.Map{"error": "Message not found"})
		}
		snapRaw, ok := targetMsg.Metadata["snapshot"]
		if !ok {
			return c.Status(404).JSON(fiber.Map{"error": "No snapshot for this message"})
		}
		snapHash, ok := snapRaw.(string)
		if !ok || snapHash == "" {
			return c.Status(404).JSON(fiber.Map{"error": "No snapshot for this message"})
		}
		ws := activeSession.Workspace
		if ws == "" {
			ws = workspaceDir
		}
		sm := util.NewSnapshotManager(ws)
		if sm.RestoreFile(snapHash, payload.Path) {
			return c.JSON(fiber.Map{"ok": true})
		}
		return c.Status(500).JSON(fiber.Map{"error": "Failed to revert file"})
	})
}

func setupConfigRoutes(api fiber.Router) {
	api.Get("/config/llm", func(c *fiber.Ctx) error {
		cfg := loadCfg()
		
		type providerInfo struct {
			ID            string `json:"id"`
			Model         string `json:"model"`
			BaseURL       string `json:"base_url"`
			APIKey        string `json:"api_key"`
			DisableVision bool   `json:"disable_vision"`
			ContextWindow int    `json:"context_window"`
			MaxMessages   int    `json:"max_messages"`
		}
		
		var providers []providerInfo
		
		// Helper to add provider
		addProv := func(pid string, pc config.ProviderConfig) {
			key := ""
			if pc.APIKey != nil {
				key = *pc.APIKey
			}
			masked := ""
			if len(key) > 8 {
				masked = key[:8] + "..."
			} else if len(key) > 0 {
				masked = "***"
			}
			base := ""
			if pc.BaseURL != nil {
				base = *pc.BaseURL
			}
			mdl := ""
			if pc.Model != nil {
				mdl = *pc.Model
			}
			dv := false
			if pc.DisableVision != nil {
				dv = *pc.DisableVision
			}
			cw := 0
			if pc.ContextWindow != nil {
				cw = *pc.ContextWindow
			}
			mm := 0
			if pc.MaxMessages != nil {
				mm = *pc.MaxMessages
			}
			providers = append(providers, providerInfo{
				ID:            pid,
				Model:         mdl,
				BaseURL:       base,
				APIKey:        masked,
				DisableVision: dv,
				ContextWindow: cw,
				MaxMessages:   mm,
			})
		}
		
		model := "gpt-4o"
		if cfg.Model != nil {
			model = *cfg.Model
		}
		temperature := 0.0
		maxMessages := 200
		contextWindow := 0
		if cfg.Mode != nil {
			if v, ok := cfg.Mode["temperature"].(float64); ok {
				temperature = v
			}
			if v, ok := cfg.Mode["max_messages"].(float64); ok {
				maxMessages = int(v)
			} else if rawCfg := loadRawConfig(); rawCfg != nil {
				if v, ok := rawCfg["max_messages"].(float64); ok {
					maxMessages = int(v)
				}
			}
			if v, ok := cfg.Mode["context_window"].(float64); ok {
				contextWindow = int(v)
			} else if rawCfg := loadRawConfig(); rawCfg != nil {
				if v, ok := rawCfg["context_window"].(float64); ok {
					contextWindow = int(v)
				}
			}
		} else if rawCfg := loadRawConfig(); rawCfg != nil {
			if v, ok := rawCfg["max_messages"].(float64); ok {
				maxMessages = int(v)
			}
			if v, ok := rawCfg["context_window"].(float64); ok {
				contextWindow = int(v)
			}
		}

		// Add in order of EnabledProviders if present
		added := make(map[string]bool)
		for _, pid := range cfg.EnabledProviders {
			if pc, ok := cfg.Provider[pid]; ok {
				addProv(pid, pc)
				added[pid] = true
			}
		}
		
		// Add remaining
		for pid, pc := range cfg.Provider {
			if !added[pid] {
				addProv(pid, pc)
			}
		}
		
		if len(providers) == 0 {
			providers = append(providers, providerInfo{ID: "openai_compatible"})
		}

		// Apply global defaults to any provider missing them
		for i := range providers {
			if providers[i].Model == "" {
				providers[i].Model = model
			}
			if providers[i].MaxMessages == 0 {
				providers[i].MaxMessages = maxMessages
			}
			if providers[i].ContextWindow == 0 {
				providers[i].ContextWindow = contextWindow
			}
		}

		shellAccess := "allow"
		if cfg.Permission != nil {
			if v, ok := cfg.Permission["shell"].(map[string]any); ok {
				if act, ok := v["action"].(string); ok && act == "ask" {
					shellAccess = "ask"
				}
			}
		}

		debugLog("GET /config/llm: providers=%d model=%s shell_access=%s", len(providers), model, shellAccess)
		return c.JSON(fiber.Map{
			"providers":      providers,
			"model":          model,
			"temperature":    temperature,
			"max_messages":   maxMessages,
			"context_window": contextWindow,
			"shell_access":   shellAccess,
		})
	})

	api.Post("/config/llm", func(c *fiber.Ctx) error {
		type provPayload struct {
			ID            string `json:"id"`
			Model         string `json:"model"`
			APIKey        string `json:"api_key"`
			BaseURL       string `json:"base_url"`
			DisableVision bool   `json:"disable_vision"`
			ContextWindow int    `json:"context_window"`
			MaxMessages   int    `json:"max_messages"`
		}
		payload := new(struct {
			Model         string        `json:"model"`
			Providers     []provPayload `json:"providers"`
			MaxMessages   int           `json:"max_messages"`
			ContextWindow int           `json:"context_window"`
			ShellAccess   string        `json:"shell_access"`
		})
		c.BodyParser(payload)
		debugLog("POST /config/llm: model=%s providers=%d max_messages=%d shell_access=%s", payload.Model, len(payload.Providers), payload.MaxMessages, payload.ShellAccess)

		rawCfg := loadRawConfig()
		if rawCfg == nil {
			rawCfg = make(map[string]any)
		}
		if payload.Model != "" {
			rawCfg["model"] = payload.Model
		}

		if len(payload.Providers) > 0 {
			if _, ok := rawCfg["provider"].(map[string]any); !ok {
				rawCfg["provider"] = make(map[string]any)
			}
			provMap := rawCfg["provider"].(map[string]any)
			
			newProvMap := make(map[string]any)
			var enabledProviders []string
			
			for i, p := range payload.Providers {
				var newID string
				if i == 0 {
					newID = "primary"
				} else {
					newID = fmt.Sprintf("fallback_%d", i)
				}
				enabledProviders = append(enabledProviders, newID)
				
				pCfg := make(map[string]any)
				if existing, ok := provMap[p.ID].(map[string]any); ok && p.ID != "" {
					for k, v := range existing {
						pCfg[k] = v
					}
				}
				
				if p.APIKey != "" && !strings.HasSuffix(p.APIKey, "...") {
					pCfg["api_key"] = p.APIKey
					if i == 0 {
						os.Setenv("OPENAI_API_KEY", p.APIKey)
					}
				}
				if p.BaseURL != "" {
					pCfg["base_url"] = p.BaseURL
				} else {
					pCfg["base_url"] = "https://api.openai.com/v1"
				}
				if p.Model != "" {
					pCfg["model"] = p.Model
				}
				pCfg["disable_vision"] = p.DisableVision
				if p.ContextWindow > 0 {
					pCfg["context_window"] = p.ContextWindow
				} else {
					delete(pCfg, "context_window")
				}
				if p.MaxMessages > 0 {
					pCfg["max_messages"] = p.MaxMessages
				} else {
					delete(pCfg, "max_messages")
				}
				
				newProvMap[newID] = pCfg
			}
			
			rawCfg["provider"] = newProvMap

			rawCfg["enabled_providers"] = enabledProviders
		}

		if payload.MaxMessages > 0 {
			rawCfg["max_messages"] = payload.MaxMessages
		}
		if payload.ContextWindow > 0 {
			rawCfg["context_window"] = payload.ContextWindow
		}
		if payload.ShellAccess != "" {
			if _, ok := rawCfg["permission"].(map[string]any); !ok {
				rawCfg["permission"] = make(map[string]any)
			}
			perm := rawCfg["permission"].(map[string]any)
			if payload.ShellAccess == "ask" {
				perm["shell"] = map[string]any{"action": "ask"}
			} else {
				perm["shell"] = map[string]any{"action": "allow"}
			}
		}
		saveRawConfig(rawCfg)
		appCfg = loadCfg()

		debugLog("POST /config/llm: saved to project config")
		return c.JSON(fiber.Map{"ok": true})
	})

	api.Post("/config/mode", func(c *fiber.Ctx) error {
		payload := new(struct {
			Mode string `json:"mode"`
		})
		if err := c.BodyParser(payload); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid request"})
		}
		mode := payload.Mode
		debugLog("POST /config/mode: mode=%s", mode)
		if mode != "chat" && mode != "plan" && mode != "build" {
			debugLog("POST /config/mode: invalid mode %s", mode)
			return c.Status(400).JSON(fiber.Map{"error": "Invalid mode"})
		}
		rawCfg := loadRawConfig()
		if rawCfg == nil {
			rawCfg = make(map[string]any)
		}
		rawCfg["intent_mode"] = mode
		saveRawConfig(rawCfg)
		appCfg = loadCfg()
		agentID := resolveAgentForMode(mode, rawCfg)
		debugLog("POST /config/mode: resolved agent=%s", agentID)
		if activeSession != nil && activeSession.AgentID != agentID {
			activeSession.SetAgent(agentID)
			debugLog("POST /config/mode: active session agent updated to %s", agentID)
		}
		addLiveEvent("mode_change", fiber.Map{"mode": mode, "agent": agentID})
		return c.JSON(fiber.Map{"ok": true, "mode": mode, "agent": agentID})
	})

	api.Get("/config/mcp", func(c *fiber.Ctx) error {
		cfg := loadCfg()
		if cfg.Mcp != nil {
			return c.JSON(cfg.Mcp)
		}
		return c.JSON(fiber.Map{"servers": map[string]any{}})
	})

	api.Post("/config/mcp", func(c *fiber.Ctx) error {
		payload := new(config.McpConfig)
		if err := c.BodyParser(payload); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid payload"})
		}
		
		rawCfg := loadRawConfig()
		if rawCfg == nil {
			rawCfg = make(map[string]any)
		}
		
		if payload.Servers != nil {
			rawCfg["mcp"] = map[string]any{
				"servers": payload.Servers,
			}
		} else {
			delete(rawCfg, "mcp")
		}
		
		saveRawConfig(rawCfg)
		appCfg = loadCfg()
		
		// If engine is running, we might need to notify or restart MCP clients.
		// For now, returning ok will prompt the UI to restart or they will be loaded on next run.
		debugLog("POST /config/mcp: saved MCP settings")

		if isEngineRunning() && mcpManager != nil {
			var mcpServers []tool.McpServerDef
			if payload.Servers != nil {
				for name, sc := range payload.Servers {
					if len(sc.Command) == 0 {
						continue
					}
					mcpServers = append(mcpServers, tool.McpServerDef{
						Name:        name,
						Command:     sc.Command[0],
						Args:        sc.Command[1:],
						Environment: sc.Environment,
						Disabled:    sc.Disabled,
					})
				}
			}
			go mcpManager.RestartServers(context.Background(), mcpServers)
		}

		return c.JSON(fiber.Map{"ok": true})
	})

	api.Get("/config/compaction", func(c *fiber.Ctx) error {
		cfg := loadCfg()
		if cfg.Compaction != nil {
			return c.JSON(cfg.Compaction)
		}
		// Default values
		return c.JSON(fiber.Map{
			"auto":                   false,
			"tail_turns":             10,
			"preserve_recent_tokens": 1000,
			"reserved":               2000,
			"prune":                  false,
			"tool_truncation_limit":  10000,
		})
	})

	api.Post("/config/compaction", func(c *fiber.Ctx) error {
		payload := new(config.CompactionConfig)
		if err := c.BodyParser(payload); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid payload"})
		}
		
		rawCfg := loadRawConfig()
		if rawCfg == nil {
			rawCfg = make(map[string]any)
		}
		
		rawCfg["compaction"] = map[string]any{
			"auto":                   payload.Auto,
			"tail_turns":             payload.TailTurns,
			"preserve_recent_tokens": payload.PreserveRecentTokens,
			"reserved":               payload.Reserved,
			"prune":                  payload.Prune,
			"tool_truncation_limit":  payload.ToolTruncationLimit,
		}
		
		saveRawConfig(rawCfg)
		appCfg = loadCfg()
		
		debugLog("POST /config/compaction: saved Compaction settings")
		return c.JSON(fiber.Map{"ok": true})
	})
}

func setupProjectRoutes(api fiber.Router) {
	api.Get("/projects", func(c *fiber.Ctx) error {
		pr := loadProjectRegistry()
		activePath := workspaceDir
		projects := pr.Projects
		for i, p := range projects {
			pPath := p.Path
			p.Active = activePath != "" && isAbsPathEqual(pPath, activePath)
			if _, err := os.Stat(pPath); err == nil {
				convs, err := listProjectConversations(pPath)
				if err == nil {
					p.Convs = convs
				}
			}
			projects[i] = p
		}
		return c.JSON(fiber.Map{"projects": projects, "active": activePath})
	})

	api.Get("/projects/available", func(c *fiber.Ctx) error {
		entries, err := os.ReadDir(util.GlobalWorkspacesRoot)
		if err != nil {
			return c.JSON(fiber.Map{"error": err.Error(), "folders": []string{}})
		}
		
		var available []string
		pr := loadProjectRegistry()
		
		for _, entry := range entries {
			if entry.IsDir() {
				isRegistered := false
				for _, proj := range pr.Projects {
					if proj.Name == entry.Name() {
						isRegistered = true
						break
					}
				}
				if !isRegistered {
					available = append(available, entry.Name())
				}
			}
		}
		return c.JSON(fiber.Map{"folders": available})
	})

	api.Post("/projects/create", func(c *fiber.Ctx) error {
		if isEngineRunning() {
			return c.Status(409).JSON(fiber.Map{"error": "Cannot create projects while running."})
		}
		payload := new(struct {
			Folders []string `json:"folders"`
		})
		c.BodyParser(payload)
		if len(payload.Folders) == 0 {
			return c.Status(400).JSON(fiber.Map{"error": "At least one folder path is required."})
		}
		targetFolder, err := util.JailPath(util.GlobalWorkspacesRoot, filepath.Base(payload.Folders[0]))
		if err != nil {
			return c.Status(403).JSON(fiber.Map{"error": err.Error()})
		}
		os.Setenv("WORKSPACE_DIR", targetFolder)
		workspaceDir = targetFolder
		if mcpManager != nil {
			mcpManager.Workspace = targetFolder
		}
		initWorkspace(targetFolder)
		
		pr := loadProjectRegistry()
		pr.LastActive = targetFolder
		saveProjectRegistry(pr)
		
		activeSession = nil
		project := registerProject(targetFolder)
		loadLatestSession(targetFolder)
		sessionLog := getSessionLog()
		return c.JSON(fiber.Map{
			"ok":          true,
			"created":     []projectEntry{project},
			"project":     project,
			"session_log": sessionLog,
		})
	})

	api.Post("/projects/select", func(c *fiber.Ctx) error {
		if isEngineRunning() {
			return c.Status(409).JSON(fiber.Map{"error": "Cannot switch projects while running."})
		}
		payload := new(struct {
			Path string `json:"path"`
		})
		if err := c.BodyParser(payload); err != nil || payload.Path == "" {
			return c.Status(400).JSON(fiber.Map{"error": "Project path is required."})
		}
		
		targetFolder, err := util.JailPath(util.GlobalWorkspacesRoot, filepath.Base(payload.Path))
		if err != nil {
			return c.Status(403).JSON(fiber.Map{"error": err.Error()})
		}
		
		os.Setenv("WORKSPACE_DIR", targetFolder)
		workspaceDir = targetFolder
		if mcpManager != nil {
			mcpManager.Workspace = targetFolder
		}
		initWorkspace(targetFolder)
		
		pr := loadProjectRegistry()
		pr.LastActive = targetFolder
		saveProjectRegistry(pr)
		
		activeSession = nil
		// Load live events from disk
		eventsMu.Lock()
		liveEvents = nil
		eventsMu.Unlock()
		registerProject(targetFolder)
		loadLatestSession(targetFolder)
		sessionLog := getSessionLog()

		pr = loadProjectRegistry()
		projects := pr.Projects
		for i, p := range projects {
			pPath := p.Path
			p.Active = isAbsPathEqual(pPath, targetFolder)
			if _, err := os.Stat(pPath); err == nil {
				convs, err := listProjectConversations(pPath)
				if err == nil {
					p.Convs = convs
				}
			}
			projects[i] = p
		}
		return c.JSON(fiber.Map{
			"ok":          true,
			"project":     projectEntry{Path: targetFolder, Label: filepath.Base(targetFolder)},
			"session_log": sessionLog,
			"projects":    projects,
		})
	})

	api.Post("/projects/remove", func(c *fiber.Ctx) error {
		if isEngineRunning() {
			return c.Status(409).JSON(fiber.Map{"error": "Cannot remove projects while running."})
		}
		payload := new(struct {
			Path string `json:"path"`
		})
		if err := c.BodyParser(payload); err != nil || payload.Path == "" {
			return c.Status(400).JSON(fiber.Map{"error": "Project path is required."})
		}
		
		// Remove it from the registry by its raw path so legacy projects can be removed.
		warning := unregisterProject(payload.Path)

		// Close workspace DB before deleting .agent to avoid lock on Windows
		if workspaceDir != "" && isAbsPathEqual(workspaceDir, payload.Path) {
			if activeSession != nil && activeSession.Repo != nil {
				activeSession.Repo.Close()
			}
			activeSession = nil
			workspaceDir = ""
			os.Unsetenv("WORKSPACE_DIR")
			activeConversation = "session_" + fmt.Sprintf("%d", time.Now().Unix())
			eventsMu.Lock()
			liveEvents = nil
			eventsMu.Unlock()
		}
		
		return c.JSON(fiber.Map{"ok": true, "warning": warning, "session_log": []any{}})
	})
}

func setupWorkspaceRoutes(api fiber.Router) {
	api.Get("/files", func(c *fiber.Ctx) error {
		q := strings.ToLower(c.Query("q"))
		w := workspaceDir
		if w == "" || !dirExists(w) {
			return c.JSON([]any{})
		}
		var results []map[string]any
		ignored := map[string]bool{
			".git": true, "node_modules": true, ".venv": true, "venv": true,
			"__pycache__": true, ".agent": true, ".pytest_cache": true,
			".opencode": true, "target": true, "build": true, "dist": true,
			".next": true, ".idea": true,
		}
		filepath.Walk(w, func(path string, info os.FileInfo, err error) error {
			if err != nil || info.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(w, path)
			rel = strings.ReplaceAll(rel, "\\", "/")
			parts := strings.SplitN(rel, "/", 2)
			if len(parts) > 0 && ignored[parts[0]] {
				return nil
			}
			if strings.HasPrefix(filepath.Base(rel), ".") {
				return nil
			}
			if filepath.Base(rel) == ".DS_Store" || filepath.Base(rel) == "Thumbs.db" || filepath.Base(rel) == ".gitignore" {
				return nil
			}
			if q != "" && !strings.Contains(strings.ToLower(rel), q) {
				return nil
			}
			ext := strings.TrimPrefix(filepath.Ext(info.Name()), ".")
			if ext == "" {
				ext = "file"
			}
			label := filepath.Dir(rel)
			if label == "." {
				label = "root"
			}
			results = append(results, map[string]any{
				"value": rel,
				"type":  ext,
				"label": label,
			})
			if len(results) >= 50 {
				return filepath.SkipAll
			}
			return nil
		})
		sort.Slice(results, func(i, j int) bool {
			return results[i]["value"].(string) < results[j]["value"].(string)
		})
		return c.JSON(results)
	})

	api.Post("/workspace/index", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": true, "status": "indexed", "detail": "V3: codebase-memory-mcp not used"})
	})

	api.Get("/workspace/tree", func(c *fiber.Ctx) error {
		w := workspaceDir
		if w == "" || !dirExists(w) {
			return c.JSON([]any{})
		}
		return c.JSON(buildTree(w, w))
	})

	api.Get("/workspace/file", func(c *fiber.Ctx) error {
		path := c.Query("path")
		if path == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No path provided"})
		}
		w := workspaceDir
		if w == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No workspace"})
		}
		fullPath := filepath.Join(w, path)
		if !pathSafe(fullPath, w) {
			return c.Status(403).JSON(fiber.Map{"error": "Access denied"})
		}
		data, err := os.ReadFile(fullPath)
		if err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"content": string(data)})
	})

	api.Post("/workspace/create-file", func(c *fiber.Ctx) error {
		payload := new(struct {
			Path string `json:"path"`
		})
		if err := c.BodyParser(payload); err != nil || payload.Path == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No path provided"})
		}
		w := workspaceDir
		if w == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No workspace"})
		}
		fullPath := filepath.Join(w, payload.Path)
		if !pathSafe(fullPath, w) {
			return c.Status(403).JSON(fiber.Map{"error": "Access denied"})
		}
		os.MkdirAll(filepath.Dir(fullPath), 0755)
		if err := os.WriteFile(fullPath, []byte{}, 0644); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	api.Post("/workspace/create-folder", func(c *fiber.Ctx) error {
		payload := new(struct {
			Path string `json:"path"`
		})
		if err := c.BodyParser(payload); err != nil || payload.Path == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No path provided"})
		}
		w := workspaceDir
		if w == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No workspace"})
		}
		fullPath := filepath.Join(w, payload.Path)
		if !pathSafe(fullPath, w) {
			return c.Status(403).JSON(fiber.Map{"error": "Access denied"})
		}
		if err := os.MkdirAll(fullPath, 0755); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	api.Post("/workspace/save-file", func(c *fiber.Ctx) error {
		payload := new(struct {
			Path    string `json:"path"`
			Content string `json:"content"`
		})
		if err := c.BodyParser(payload); err != nil || payload.Path == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No path or content"})
		}
		w := workspaceDir
		if w == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No workspace"})
		}
		fullPath := filepath.Join(w, payload.Path)
		if !pathSafe(fullPath, w) {
			return c.Status(403).JSON(fiber.Map{"error": "Access denied"})
		}
		if err := os.WriteFile(fullPath, []byte(payload.Content), 0644); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	api.Post("/workspace/delete", func(c *fiber.Ctx) error {
		payload := new(struct {
			Path string `json:"path"`
		})
		if err := c.BodyParser(payload); err != nil || payload.Path == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No path provided"})
		}
		w := workspaceDir
		if w == "" {
			return c.Status(400).JSON(fiber.Map{"error": "No workspace"})
		}
		fullPath := filepath.Join(w, payload.Path)
		if !pathSafe(fullPath, w) {
			return c.Status(403).JSON(fiber.Map{"error": "Access denied"})
		}
		if _, err := os.Stat(fullPath); os.IsNotExist(err) {
			return c.Status(404).JSON(fiber.Map{"error": "Not found"})
		}
		if err := os.RemoveAll(fullPath); err != nil {
			return c.Status(500).JSON(fiber.Map{"error": err.Error()})
		}
		return c.JSON(fiber.Map{"success": true})
	})

	api.Post("/tool/approve", func(c *fiber.Ctx) error {
		payload := new(struct {
			CallID  string `json:"call_id"`
			Approve bool   `json:"approve"`
		})
		if err := c.BodyParser(payload); err != nil {
			return c.Status(400).JSON(fiber.Map{"error": "Invalid payload"})
		}
		if ch, ok := pendingToolApprovals.Load(payload.CallID); ok {
			select {
			case ch.(chan bool) <- payload.Approve:
			default:
			}
			return c.JSON(fiber.Map{"success": true})
		}
		return c.Status(404).JSON(fiber.Map{"error": "Pending call not found"})
	})
}

func setupToolRoutes(api fiber.Router) {
	api.Get("/tools", func(c *fiber.Ctx) error {
		q := strings.ToLower(c.Query("q"))
		commands := []map[string]any{
			{"value": "plan", "type": "cmd", "label": "Plan mode — plan before implementing"},
			{"value": "build", "type": "cmd", "label": "Build mode — implement directly"},
		}
		if q != "" {
			var filtered []map[string]any
			for _, cmd := range commands {
				if strings.Contains(strings.ToLower(cmd["value"].(string)), q) {
					filtered = append(filtered, cmd)
				}
			}
			return c.JSON(filtered)
		}
		return c.JSON(commands)
	})

	api.Post("/diagnostics/model-test", func(c *fiber.Ctx) error {
		cfg := loadCfg()
		providerID := c.Query("provider")

		var client *provider.Client
		var displayName string

		if providerID != "" {
			pc, ok := cfg.Provider[providerID]
			if !ok {
				return c.Status(404).JSON(fiber.Map{"ok": false, "detail": fmt.Sprintf("Provider '%s' not found", providerID)})
			}

			globalModel := "gpt-4o"
			if cfg.Model != nil {
				globalModel = *cfg.Model
			}

			key := ""
			if pc.APIKey != nil { key = *pc.APIKey }
			base := ""
			if pc.BaseURL != nil { base = *pc.BaseURL }
			mdl := globalModel
			if pc.Model != nil { mdl = *pc.Model }
			dv := false
			if pc.DisableVision != nil { dv = *pc.DisableVision }

			inst := buildProviderInstance(providerID, key, base, mdl, dv)
			if inst == nil {
				return c.JSON(fiber.Map{"ok": false, "detail": "No API key configured for this provider"})
			}
			client = provider.NewMultiClient([]provider.ProviderInstance{*inst}, mdl)
			displayName = providerID + " (" + mdl + ")"
		} else {
			client = clientFromCfg(cfg)
			displayName = "primary"
		}

		start := time.Now()
		prompt := c.Query("prompt")
		if prompt == "" {
			prompt = "Hello. Please reply with the word 'OK'."
		}

		raw := session.ToOpenAIMessages([]session.Message{
			{Role: "user", Parts: []session.MessagePart{{Type: "text", Content: prompt}}, ID: "test", CreatedAt: time.Now().UnixMilli()},
		}, false)

		llmResp, err := client.Generate(context.Background(), raw, nil)
		elapsed := time.Since(start).Seconds()

		if err != nil {
			return c.JSON(fiber.Map{
				"ok":              false,
				"provider":        displayName,
				"detail":          fmt.Sprintf("Connection failed: %v", err),
				"elapsed_seconds": elapsed,
			})
		}

		return c.JSON(fiber.Map{
			"ok":              true,
			"provider":        displayName,
			"detail":          fmt.Sprintf("Connection OK. Model: %s", llmResp.Model),
			"elapsed_seconds": elapsed,
		})
	})
}

func setupMiscRoutes(api fiber.Router) {
	api.Get("/timeline/export", func(c *fiber.Ctx) error {
		eventsMu.Lock()
		eventsCopy := make([]map[string]any, len(liveEvents))
		copy(eventsCopy, liveEvents)
		eventsMu.Unlock()
		return c.JSON(fiber.Map{"events": eventsCopy})
	})

	api.Post("/open-file", func(c *fiber.Ctx) error {
		payload := new(struct {
			Path string `json:"path"`
		})
		c.BodyParser(payload)
		w := workspaceDir
		if payload.Path == "" || w == "" {
			return c.JSON(fiber.Map{"ok": false, "error": "File not found"})
		}
		if _, err := os.Stat(payload.Path); os.IsNotExist(err) {
			return c.JSON(fiber.Map{"ok": false, "error": "File not found"})
		}
		if isAbsPath(payload.Path, w) {
			if !pathSafe(payload.Path, w) {
				return c.JSON(fiber.Map{"ok": false, "error": "Path outside workspace"})
			}
		}
		exec.Command("cmd", "/c", "start", "", payload.Path).Start()
		return c.JSON(fiber.Map{"ok": true})
	})

	api.Post("/upload", func(c *fiber.Ctx) error {
		return c.JSON(fiber.Map{"ok": false, "error": "Uploads not supported"})
	})

	api.Get("/activity", func(c *fiber.Ctx) error {
		eventsMu.Lock()
		defer eventsMu.Unlock()
		return c.JSON(fiber.Map{"events": liveEvents})
	})

	api.Post("/activity/clear", func(c *fiber.Ctx) error {
		eventsMu.Lock()
		liveEvents = nil
		eventsMu.Unlock()
		// Also clear disk-backed history file if it exists
		if workspaceDir != "" {
			historyFile := filepath.Join(workspaceDir, ".agent", "agent_history.json")
			os.Remove(historyFile)
		}
		return c.JSON(fiber.Map{"ok": true})
	})
}

func setupStreamRoutes(api fiber.Router) {
	api.Get("/stream/activity", func(c *fiber.Ctx) error {
		c.Set("Content-Type", "text/event-stream")
		c.Set("Cache-Control", "no-cache")
		c.Set("Connection", "keep-alive")

		c.Context().SetBodyStreamWriter(func(w *bufio.Writer) {
			ch := subscribe()
			defer unsubscribe(ch)
			for evt := range ch {
				data, err := json.Marshal(evt)
				if err != nil {
					continue
				}
				_, err = fmt.Fprintf(w, "data: %s\n\n", data)
				if err != nil {
					return
				}
				if err := w.Flush(); err != nil {
					return
				}
			}
		})
		return nil
	})
	api.Post("/chat/followup", func(c *fiber.Ctx) error {
		if !isEngineRunning() {
			return c.Status(400).JSON(fiber.Map{"error": "Engine is not running. Use /run instead."})
		}
		if activeSession == nil {
			return c.Status(400).JSON(fiber.Map{"error": "No active session."})
		}
		payload := new(struct {
			Prompt string `json:"prompt"`
		})
		c.BodyParser(payload)
		
		activeSession.QueueFollowup(payload.Prompt)
		activeSession.Save()
		
		return c.JSON(fiber.Map{"ok": true, "message": "Follow-up prompt queued!"})
	})

	api.Post("/run", func(c *fiber.Ctx) error {
		if isEngineRunning() {
			debugLog("/run: engine already running, returning 409")
			return c.Status(409).JSON(fiber.Map{"error": "Engine is already running."})
		}
		payload := new(struct {
			Message      string `json:"message"`
			Prompt       string `json:"prompt"`
			Agent        string `json:"agent"`
			SystemPrompt string `json:"system_prompt"`
		})
		c.BodyParser(payload)
		if payload.Message == "" && payload.Prompt == "" {
			return c.Status(400).JSON(fiber.Map{"error": "Message is required"})
		}
		msg := payload.Message
		if msg == "" {
			msg = payload.Prompt
		}
		if payload.Agent == "" {
			if rawCfg := loadRawConfig(); rawCfg != nil {
				mode := "build"
				if m, ok := rawCfg["intent_mode"].(string); ok {
					mode = m
				}
				payload.Agent = resolveAgentForMode(mode, rawCfg)
			} else {
				payload.Agent = "build"
			}
		}
		debugLog("/run: message=%.80s agent=%s systemPrompt_len=%d", msg, payload.Agent, len(payload.SystemPrompt))

		eventsMu.Lock()
		liveEvents = nil
		eventsMu.Unlock()
		debugLog("/run: liveEvents cleared")

		engineMu.Lock()
		engineRunning = true
		stopRequested = false
		engineMu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())
		engineCancel = cancel

		debugLog("/run: launching runEngine goroutine")
		go runEngine(ctx, msg, payload.Agent, payload.SystemPrompt)

		return c.JSON(fiber.Map{"ok": true})
	})

	api.Post("/stop", func(c *fiber.Ctx) error {
		engineMu.Lock()
		defer engineMu.Unlock()
		debugLog("/stop: engineRunning=%v cancel=%v", engineRunning, engineCancel != nil)
		stopRequested = true
		if engineRunning && engineCancel != nil {
			engineCancel()
			engineRunning = false
			debugLog("/stop: engine cancelled, engineRunning=false")
		}
		return c.JSON(fiber.Map{"ok": true})
	})
}

func formatToolSummary(toolName, argsStr string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(argsStr), &args); err != nil {
		if len(argsStr) > 80 {
			return argsStr[:80] + "..."
		}
		return argsStr
	}
	var parts []string
	extractStr := func(key string) string {
		if val, ok := args[key]; ok {
			return fmt.Sprintf("%v", val)
		}
		return ""
	}
	switch toolName {
	case "read", "write", "edit", "ast_search", "lsp":
		if fp := extractStr("filePath"); fp != "" {
			parts = append(parts, filepath.Base(fp))
		}
		for _, k := range []string{"limit", "offset", "startLine", "action", "line", "query"} {
			if v := extractStr(k); v != "" {
				if len(v) > 20 {
					v = v[:20] + "..."
				}
				parts = append(parts, v)
			}
		}
	case "todowrite":
		for _, k := range []string{"action", "id", "content"} {
			if v := extractStr(k); v != "" {
				if len(v) > 40 {
					v = v[:40] + "..."
				}
				parts = append(parts, v)
			}
		}
	case "grep", "glob":
		if v := extractStr("pattern"); v != "" {
			parts = append(parts, v)
		}
		if v := extractStr("path"); v != "" {
			parts = append(parts, filepath.Base(v))
		}
	case "shell":
		if v := extractStr("command"); v != "" {
			parts = append(parts, v)
		}
	default:
		for _, v := range args {
			strV := fmt.Sprintf("%v", v)
			if len(strV) > 20 {
				strV = strV[:20] + "..."
			}
			parts = append(parts, strV)
		}
	}
	summary := strings.Join(parts, " ")
	summary = strings.ReplaceAll(summary, "\n", " ")
	summary = strings.TrimSpace(summary)
	if len(summary) > 80 {
		summary = summary[:77] + "..."
	}
	if summary == "" {
		if len(argsStr) > 80 {
			return argsStr[:80] + "..."
		}
		return argsStr
	}
	return summary
}
func spawnSubagent(prompt, agentType, parentSessionID string) (string, error) {
	newSessionID := fmt.Sprintf("sub-%d", time.Now().UnixNano())

	workspace := os.Getenv("WORKSPACE_DIR")
	wm := util.NewWorktreeManager(workspace)
	branchName := newSessionID
	worktreePath := workspace
	if targetPath := wm.Spawn(branchName); targetPath != nil {
		worktreePath = *targetPath
	}

	var sessionRepo *storage.Repository
	if workspace != "" {
		dbPath := filepath.Join(workspace, ".agent", "sessions.db")
		d, err := storage.NewDatabase(dbPath)
		if err == nil {
			sessionRepo = storage.NewRepository(d)
		}
	}
	if sessionRepo == nil {
		sessionRepo = repo
	}

	subSession := session.NewSession(newSessionID, sessionRepo, agentType, configToDict(appCfg), worktreePath)
	if err := subSession.Save(); err != nil {
		return "", err
	}

	go func() {
		ctx := context.Background()
		runSubEngine(ctx, subSession, prompt, agentType, parentSessionID, worktreePath, wm, branchName)
	}()

	return newSessionID, nil
}

func runSubEngine(ctx context.Context, subSession *session.Session, message, agentID, parentSessionID, workspace string, wm *util.WorktreeManager, branchName string) {
	debugLog("runSubEngine starting: sessionID=%s agentID=%s message=%.80s", subSession.SessionID, agentID, message)
	defer func() {
		if r := recover(); r != nil {
			debugLog("PANIC in runSubEngine: %v", r)
			subSession.Save()
		}
	}()
	var snapHash string
	agentModifiedFiles := make(map[string]bool)

	startTime := time.Now()
	defer func() {
		durationMs := time.Since(startTime).Milliseconds()
		wsChanges := map[string]any{"created": []string{}, "modified": []string{}, "deleted": []string{}}
		if snapHash != "" && workspace != "" {
			wsChanges = createArtifacts(workspace, snapHash, agentModifiedFiles)
		}
		
		runMeta := map[string]any{
			"live_events":       []map[string]any{},
			"duration_ms":       durationMs,
			"workspace_changes": wsChanges,
		}
		
		for i := len(subSession.Messages) - 1; i >= 0; i-- {
			if subSession.Messages[i].Role == "assistant" {
				msg := &subSession.Messages[i]
				if msg.Metadata == nil {
					msg.Metadata = make(map[string]any)
				}
				msg.Metadata["run_meta"] = runMeta
				if subSession.Repo != nil {
					subSession.Repo.UpdateMessageMetadata(msg.ID, msg.Metadata)
				}
				break
			}
		}
		subSession.Save()
		
		if parentSessionID != "" && subSession.Repo != nil {
			parentMsg := session.Message{
				ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
				SessionID: parentSessionID,
				Role:      "user",
				CreatedAt: time.Now().UnixMilli(),
				Parts:     []session.MessagePart{{Type: "text", Content: fmt.Sprintf("Subagent %s has finished its task. Review its conversation history if needed.", subSession.SessionID)}},
			}
			parentSession := session.NewSession(parentSessionID, subSession.Repo, "", configToDict(appCfg), workspace)
			if err := parentSession.Load(); err == nil {
				parentSession.AddMessage(parentMsg)
				parentSession.Save()
			}
			engineMu.Lock()
			if activeSession != nil && activeSession.SessionID == parentSessionID {
				activeSession.Load()
			}
			engineMu.Unlock()
		}
		if wm != nil && branchName != "" && workspace != os.Getenv("WORKSPACE_DIR") {
			wm.Cleanup(branchName)
		}
	}()

	if err := subSession.Load(); err != nil {
		debugLog("runSubEngine: load err=%v", err)
	}

	if workspace != "" {
		if snapHash = createGitSnapshot(workspace); snapHash != "" {
			debugLog("runSubEngine: git snapshot %s", snapHash)
		}
	}

	userMsg := session.Message{
		ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		SessionID: subSession.SessionID,
		Role:      "user",
		CreatedAt: time.Now().UnixMilli(),
		Parts:     []session.MessagePart{{Type: "text", Content: message}},
	}
	if snapHash != "" {
		userMsg.Metadata = map[string]any{"snapshot": snapHash}
	}
	subSession.AddMessage(userMsg)

	cfg := loadCfg()
	client := clientFromCfg(cfg)
	pm := session.NewPromptManager(subSession, configToDict(appCfg))

	permRules := permission.FromConfig(cfg.Permission)
	if len(permRules) == 0 {
		permRules = permission.AllowAll
	}
	sp := session.NewSessionProcessor(toolRegistry, permRules, askPermissionCallback, workspace)
	sp.SnapHash = snapHash
	var fullContent string

	for i := 0; i < 50; i++ {
		debugLog("runSubEngine: cycle %d/50 starting", i)
		select {
		case <-ctx.Done():
			subSession.Save()
			return
		default:
		}

		pendingMsg := subSession.PopFollowup()
		if pendingMsg != "" {
			followup := session.Message{
				ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
				SessionID: subSession.SessionID,
				Role:      "user",
				CreatedAt: time.Now().UnixMilli(),
				Parts:     []session.MessagePart{{Type: "text", Content: pendingMsg}},
			}
			subSession.AddMessage(followup)
			subSession.Save()
		}

		schemas := buildToolSchemas(agentID)
		pm.SystemPrompt = pm.BuildSystemPrompt(agentID, schemas, workspace)

		ctxWindow := 2000000
		
		if len(appCfg.EnabledProviders) > 0 {
			primaryID := appCfg.EnabledProviders[0]
			if pCfg, ok := appCfg.Provider[primaryID]; ok && pCfg.ContextWindow != nil && *pCfg.ContextWindow > 0 {
				ctxWindow = *pCfg.ContextWindow
			}
		}
		
		if ctxWindow == 2000000 && appCfg.ContextWindow != nil && *appCfg.ContextWindow > 0 {
			ctxWindow = *appCfg.ContextWindow
		} else if ctxWindow == 2000000 {
			if mInfo, ok := provider.ResolveModel(*appCfg.Model); ok {
				ctxWindow = mInfo.Context
			}
		}

		history := pm.PrepareMessages(ctx, agentID, ctxWindow, client, func(state string) {})
		oaMsgs := session.ToOpenAIMessages(history, false)
		
		toolDefs := buildOpenAIToolDefs(agentID)
		streamCh, err := client.Stream(ctx, oaMsgs, toolDefs)
		if err != nil {
			debugLog("runSubEngine: cycle %d stream failed: %v", i, err)
		}
		
		var cycleContent string
		var toolCalls []provider.ToolCall

		if err == nil {
		streamLoop:
			for {
				select {
				case evt, ok := <-streamCh:
					if !ok {
						break streamLoop
					}
					switch evt.Type {
					case "text":
						cycleContent += evt.Text
						fullContent += evt.Text
					case "tool_use":
						if evt.ToolCall != nil {
							toolCalls = append(toolCalls, *evt.ToolCall)
						}
					}
				case <-ctx.Done():
					break streamLoop
				}
			}
		}

		cycleContent = strings.TrimSpace(cycleContent)
		astMsg := session.Message{
			ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
			SessionID: subSession.SessionID,
			Role:      "assistant",
			CreatedAt: time.Now().UnixMilli(),
		}
		if cycleContent != "" {
			astMsg.Parts = append(astMsg.Parts, session.MessagePart{Type: "text", Content: cycleContent})
		}
		for _, tc := range toolCalls {
			astMsg.Parts = append(astMsg.Parts, session.MessagePart{
				Type:       "tool_use",
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Arguments:  tc.Arguments,
			})
		}
		
		if cycleContent == "" && len(toolCalls) == 0 && err != nil {
			astMsg.Parts = append(astMsg.Parts, session.MessagePart{Type: "text", Content: "API error: " + err.Error()})
		}
		subSession.AddMessage(astMsg)
		promoteFallbackProvider(client, nil)
		subSession.Save()

		if len(toolCalls) == 0 {
			debugLog("runSubEngine: cycle %d finished (no tool calls)", i)
			break
		}

		for _, tc := range toolCalls {
			result := sp.ProcessToolCall(tc, subSession, agentID)
			
			resMsg := session.Message{
				ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
				SessionID: subSession.SessionID,
				Role:      "tool",
				CreatedAt: time.Now().UnixMilli(),
			}
			
			if result.Error != "" && result.Output == "" {
				resMsg.Parts = append(resMsg.Parts, session.MessagePart{
					Type:        "tool_result",
					ToolCallID:  tc.ID,
					ToolName:    tc.Name,
					Content:     fmt.Sprintf("Error: %s", result.Error),
					Attachments: result.Attachments,
				})
			} else {
				resMsg.Parts = append(resMsg.Parts, session.MessagePart{
					Type:        "tool_result",
					ToolCallID:  tc.ID,
					ToolName:    tc.Name,
					Content:     result.Output,
					Attachments: result.Attachments,
				})
			}
			subSession.AddMessage(resMsg)
		}
		subSession.Save()
	}
}

func runEngine(ctx context.Context, message, agentID, systemPrompt string) {
	debugLog("runEngine starting: agentID=%s message=%.80s systemPrompt_len=%d", agentID, message, len(systemPrompt))
	defer func() {
		stopBackgroundProcesses()
		if r := recover(); r != nil {
			errMsg := fmt.Sprintf("engine panic: %v", r)
			debugLog("PANIC in runEngine: %v", r)
			addLiveEvent("complete", map[string]any{"response": errMsg, "reason": "panic"})
			activeSession.Save()
		}
		engineMu.Lock()
		engineRunning = false
		engineMu.Unlock()
		debugLog("runEngine: engineRunning set to false")
	}()
	workspace := os.Getenv("WORKSPACE_DIR")
	var snapHash string
	agentModifiedFiles := make(map[string]bool)

	startTime := time.Now()
	defer func() {
		durationMs := time.Since(startTime).Milliseconds()
		wsChanges := map[string]any{"created": []string{}, "modified": []string{}, "deleted": []string{}}
		// workspace and snapHash might not be captured if defer evaluates them late, but they are variables in the outer scope
		if snapHash != "" && workspace != "" {
			wsChanges = createArtifacts(workspace, snapHash, agentModifiedFiles)
		}
		eventsMu.Lock()
		liveSnap := make([]map[string]any, len(liveEvents))
		copy(liveSnap, liveEvents)
		eventsMu.Unlock()
		var runEvents []map[string]any
		for _, evt := range liveSnap {
			typ, _ := evt["type"].(string)
			if typ != "token" && typ != "complete" && typ != "done" && typ != "stopped" {
				runEvents = append(runEvents, evt)
			}
		}
		runMeta := map[string]any{
			"live_events":       runEvents,
			"duration_ms":       durationMs,
			"workspace_changes": wsChanges,
		}
		if activeSession != nil {
			for i := len(activeSession.Messages) - 1; i >= 0; i-- {
				if activeSession.Messages[i].Role == "assistant" {
					msg := &activeSession.Messages[i]
					if msg.Metadata == nil {
						msg.Metadata = make(map[string]any)
					}
					msg.Metadata["run_meta"] = runMeta
					debugLog("runEngine: run_meta attached to msg %s with %d live_events", msg.ID, len(runEvents))
					if activeSession.Repo != nil {
						activeSession.Repo.UpdateMessageMetadata(msg.ID, msg.Metadata)
					}
					break
				}
			}
			activeSession.Save()
			debugLog("runEngine: session saved on exit")
		}
	}()

	var sessionRepo *storage.Repository
	if workspace != "" {
		dbPath := filepath.Join(workspace, ".agent", "sessions.db")
		os.MkdirAll(filepath.Join(workspace, ".agent"), 0755)
		d, err := storage.NewDatabase(dbPath)
		if err == nil {
			sessionRepo = storage.NewRepository(d)
		}
	}
	if sessionRepo == nil {
		sessionRepo = repo
	}
	sessionMu.Lock()
	activeSession = session.NewSession(activeConversation, sessionRepo, agentID, configToDict(appCfg), workspace)
	debugLog("runEngine: workspace=%s sessionID=%s", workspace, activeSession.SessionID)
	if err := activeSession.Load(); err != nil {
		debugLog("runEngine: new session (load err=%v)", err)
	} else {
		debugLog("runEngine: existing session loaded, %d messages", len(activeSession.Messages))
	}
	if agentID != "" {
		activeSession.SetAgent(agentID)
	}
	sessionMu.Unlock()

	// Create git snapshot before run
	if workspace != "" {
		if snapHash = createGitSnapshot(workspace); snapHash != "" {
			debugLog("runEngine: git snapshot %s", snapHash)
		}
	}

	userMsg := session.Message{
		ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
		SessionID: activeSession.SessionID,
		Role:      "user",
		CreatedAt: time.Now().UnixMilli(),
		Parts:     []session.MessagePart{{Type: "text", Content: message}},
	}
	if snapHash != "" {
		userMsg.Metadata = map[string]any{"snapshot": snapHash}
	}
	activeSession.AddMessage(userMsg)

	cfg := loadCfg()
	client := clientFromCfg(cfg)
	pm := session.NewPromptManager(activeSession, configToDict(appCfg))

	permRules := permission.FromConfig(cfg.Permission)
	if len(permRules) == 0 {
		permRules = permission.AllowAll
	}
	sp := session.NewSessionProcessor(toolRegistry, permRules, askPermissionCallback, workspace)
	sp.SnapHash = snapHash
	var fullContent string
	var originalCtxWindow int

	var i int
	for i = 0; i < 150; i++ {
		debugLog("runEngine: cycle %d/150 starting", i)
	retryCycle:
		select {
		case <-ctx.Done():
			debugLog("runEngine: cycle %d context cancelled", i)
			addLiveEvent("complete", map[string]any{"reason": "cancelled", "response": fullContent})
			activeSession.Save()
			return
		default:
		}

		pendingMsg := activeSession.PopFollowup()

		if pendingMsg != "" {
			followup := session.Message{
				ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
				SessionID: activeSession.SessionID,
				Role:      "user",
				CreatedAt: time.Now().UnixMilli(),
				Parts:     []session.MessagePart{{Type: "text", Content: pendingMsg}},
			}
			activeSession.AddMessage(followup)
			activeSession.Save()
			addLiveEvent("followup", map[string]any{"status": "processed"})
		}

		sysPrompt := systemPrompt
		if sysPrompt == "" {
			schemas := buildToolSchemas(agentID)
			sysPrompt = pm.BuildSystemPrompt(agentID, schemas, workspaceDir)
		}
		pm.SystemPrompt = sysPrompt

		ctxWindow := 2000000
		
		if len(appCfg.EnabledProviders) > 0 {
			primaryID := appCfg.EnabledProviders[0]
			if pCfg, ok := appCfg.Provider[primaryID]; ok && pCfg.ContextWindow != nil && *pCfg.ContextWindow > 0 {
				ctxWindow = *pCfg.ContextWindow
			}
		}
		
		if ctxWindow == 2000000 && appCfg.ContextWindow != nil && *appCfg.ContextWindow > 0 {
			ctxWindow = *appCfg.ContextWindow
		} else if ctxWindow == 2000000 {
			if mInfo, ok := provider.ResolveModel(*appCfg.Model); ok {
				ctxWindow = mInfo.Context
			}
		}

		if i == 0 {
			originalCtxWindow = ctxWindow
		}

		history := pm.PrepareMessages(ctx, agentID, ctxWindow, client, func(state string) {
			addLiveEvent("activity", map[string]any{"event": state})
		})
		oaMsgs := session.ToOpenAIMessages(history, false)
		debugLog("runEngine: cycle %d prepared %d messages", i, len(oaMsgs))

		var cycleContent string
		var thoughtBuffer string
		var toolCalls []provider.ToolCall
		flushThought := func() {
			if tb := strings.TrimSpace(thoughtBuffer); tb != "" {
				addLiveEvent("think", map[string]any{"text": tb, "event": tb})
				thoughtBuffer = ""
			}
		}

		// Try streaming first
		toolDefs := buildOpenAIToolDefs(agentID)
		debugLog("runEngine: cycle %d streaming with %d tools", i, len(toolDefs))
		streamCh, err := client.Stream(ctx, oaMsgs, toolDefs)
		hasToolCall := false
		if err != nil {
			debugLog("runEngine: cycle %d stream failed: %v", i, err)
		}
		if err == nil {
			isReasoning := false
			hasReceivedReasoning := false
		streamLoop:
			for {
				select {
				case evt, ok := <-streamCh:
					if !ok {
						if isReasoning {
							isReasoning = false
							cycleContent += "\n</think>\n"
							fullContent += "\n</think>\n"
							thoughtBuffer += "\n</think>\n"
						}
						break streamLoop
					}
					switch evt.Type {
					case "reasoning":
						if !isReasoning {
							isReasoning = true
							cycleContent += "<think>\n"
							fullContent += "<think>\n"
						}
						hasReceivedReasoning = true
						cycleContent += evt.Text
						fullContent += evt.Text
						thoughtBuffer += evt.Text
					case "text":
						if isReasoning {
							isReasoning = false
							cycleContent += "\n</think>\n"
							fullContent += "\n</think>\n"
						}
						cycleContent += evt.Text
						fullContent += evt.Text
						if !hasReceivedReasoning {
							thoughtBuffer += evt.Text
						}
					case "tool_use":
						if !hasToolCall {
							hasToolCall = true
							flushThought()
						}
						if evt.ToolCall != nil {
							toolCalls = append(toolCalls, *evt.ToolCall)
						}
					case "usage":
						if evt.Usage != nil && activeSession != nil {
							activeSession.AddTokens(evt.Usage.InputTokens, evt.Usage.OutputTokens)
							addLiveEvent("token_usage", map[string]any{
								"total_prompt":     activeSession.PromptTokens,
								"total_completion": activeSession.CompletionTokens,
							})
							debugLog("runEngine: cycle %d token usage: prompt=%d completion=%d",
								i, evt.Usage.InputTokens, evt.Usage.OutputTokens)
						}
					}
				case <-ctx.Done():
					break streamLoop
				}
			}
			if isReasoning {
				isReasoning = false
				cycleContent += "\n</think>\n"
				fullContent += "\n</think>\n"
			}
		}
		
		// Deduplicate proxy <think> mirroring
		if strings.Count(cycleContent, "<think>") > 1 {
			// If the proxy mirrored reasoning_content into content, we strip the second one
			cycleContent = regexp.MustCompile(`(?s)(<think>.*?</think>).*?<think>.*?</think>`).ReplaceAllString(cycleContent, "$1")
			thoughtBuffer = regexp.MustCompile(`(?s)(<think>.*?</think>).*?<think>.*?</think>`).ReplaceAllString(thoughtBuffer, "$1")
		}

		// Fix DeepSeek R1 random <think> omissions
		if !strings.Contains(cycleContent, "<think>") && len(cycleContent) > 0 {
			cycleContent = "<think>\n[Thought process omitted for context limits]\n</think>\n" + cycleContent
		}

		debugLog("runEngine: cycle %d stream done: content=%d toolCalls=%d", i, len(cycleContent), len(toolCalls))

		select {
		case <-ctx.Done():
			debugLog("runEngine: cycle %d context cancelled after stream", i)
			addLiveEvent("complete", map[string]any{"reason": "cancelled", "response": fullContent})
			activeSession.Save()
			return
		default:
		}

		if hasToolCall {
			flushThought()
		} else if thoughtBuffer != "" {
			addLiveEvent("token", map[string]any{"event": cycleContent})
		}

		if cycleContent == "" && len(toolCalls) == 0 {
			// Stream produced nothing, fall back to non-streaming
			debugLog("runEngine: cycle %d stream empty, falling back to Generate()", i)
			resp, gErr := client.Generate(ctx, oaMsgs, toolDefs)
			if gErr != nil {
				errStr := strings.ToLower(gErr.Error())
				if (strings.Contains(errStr, "1214") || strings.Contains(errStr, "context length") || strings.Contains(errStr, "maximum context") || strings.Contains(errStr, "context window") || strings.Contains(errStr, "max length") || strings.Contains(errStr, "prompt exceeds")) && i < 49 {
					debugLog("runEngine: cycle %d context window exceeded, reducing ctxWindow and retrying", i)
					addLiveEvent("activity", map[string]any{"event": "⚠️ Context Window Exceeded! Reducing context window and retrying..."})
					if limit := extractContextLimit(errStr); limit > 0 && limit < ctxWindow {
						ctxWindow = limit
					} else {
						ctxWindow = ctxWindow / 2
					}
					if ctxWindow < 10000 {
						ctxWindow = 10000
					}
					history := pm.PrepareMessages(ctx, agentID, ctxWindow, client, func(state string) {
						addLiveEvent("activity", map[string]any{"event": state})
					})
					oaMsgs = session.ToOpenAIMessages(history, false)
					debugLog("runEngine: cycle %d reduced ctxWindow to %d, retrying", i, ctxWindow)
					goto retryCycle
				}
				errMsg := fmt.Sprintf("LLM Error: %s", gErr.Error())
				debugLog("runEngine: cycle %d Generate() error: %v", i, gErr)
				addLiveEvent("error", map[string]any{"error": errMsg, "cycle": i})
				addLiveEvent("complete", map[string]any{"response": errMsg, "reason": "error"})

				// Append whatever we managed to stream/generate before the error
				assistantMsg := session.Message{
					ID:        fmt.Sprintf("msg-%d-err", time.Now().UnixNano()),
					SessionID: activeSession.SessionID,
					Role:      "assistant",
					CreatedAt: time.Now().UnixMilli(),
				}
				if cycleContent != "" {
					assistantMsg.Parts = append(assistantMsg.Parts, session.MessagePart{
						Type:    "text",
						Content: cycleContent,
					})
				}
				for _, tc := range toolCalls {
					assistantMsg.Parts = append(assistantMsg.Parts, session.MessagePart{
						Type:       "tool_use",
						ToolCallID: tc.ID,
						ToolName:   tc.Name,
						Arguments:  tc.Arguments,
					})
				}
				if len(assistantMsg.Parts) == 0 {
					assistantMsg.Parts = append(assistantMsg.Parts, session.MessagePart{
						Type:    "text",
						Content: "❌ " + errMsg,
					})
				}
				activeSession.AddMessage(assistantMsg)
				return
			}
			cycleContent = ""
			if resp.Reasoning != "" {
				cycleContent += "<think>\n" + resp.Reasoning + "\n</think>\n"
			}
			cycleContent += resp.Content
			
			// Deduplicate proxy <think> mirroring
			if strings.Count(cycleContent, "<think>") > 1 {
				cycleContent = regexp.MustCompile(`(?s)(<think>.*?</think>).*?<think>.*?</think>`).ReplaceAllString(cycleContent, "$1")
			}
			
			// Fix DeepSeek R1 random <think> omissions
			if !strings.Contains(cycleContent, "<think>") && len(cycleContent) > 0 {
				cycleContent = "<think>\n[Thought process omitted for context limits]\n</think>\n" + cycleContent
			}
			
			fullContent += cycleContent
			hasToolCall = len(resp.ToolCalls) > 0
			debugLog("runEngine: cycle %d Generate() response: content=%d toolCalls=%d", i, len(cycleContent), len(resp.ToolCalls))
			if hasToolCall {
				tb := resp.Reasoning
				if tb == "" {
					tb = resp.Content
				}
				if tb != "" {
					addLiveEvent("think", map[string]any{"text": tb, "event": tb})
				}
			} else {
				if cycleContent != "" {
					addLiveEvent("token", map[string]any{"event": cycleContent})
				}
			}
			toolCalls = append(toolCalls, resp.ToolCalls...)
		}

		assistantMsg := session.Message{
			ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
			SessionID: activeSession.SessionID,
			Role:      "assistant",
			CreatedAt: time.Now().UnixMilli(),
		}
		if cycleContent != "" {
			assistantMsg.Parts = append(assistantMsg.Parts, session.MessagePart{
				Type:    "text",
				Content: cycleContent,
			})
		}
		for _, tc := range toolCalls {
			assistantMsg.Parts = append(assistantMsg.Parts, session.MessagePart{
				Type:       "tool_use",
				ToolCallID: tc.ID,
				ToolName:   tc.Name,
				Arguments:  tc.Arguments,
			})
		}
		activeSession.AddMessage(assistantMsg)
		promoteFallbackProvider(client, addLiveEvent)

		if len(toolCalls) == 0 {
			debugLog("runEngine: cycle %d no tool calls, breaking loop", i)
			if originalCtxWindow > 0 && ctxWindow < originalCtxWindow {
				debugLog("runEngine: saving reduced ctxWindow=%d to config", ctxWindow)
				if rawCfg := loadRawConfig(); rawCfg != nil {
					rawCfg["context_window"] = ctxWindow
					saveRawConfig(rawCfg)
					appCfg = loadCfg()
				}
			}
			break
		}

		for _, tc := range toolCalls {
			debugLog("runEngine: executing tool %s args=%.60s", tc.Name, tc.Arguments)
			select {
			case <-ctx.Done():
				debugLog("runEngine: tool %s cancelled mid-execution", tc.Name)
				addLiveEvent("complete", map[string]any{"reason": "cancelled", "response": fullContent})
				activeSession.Save()
				return
			default:
			}

			argsStr := formatToolSummary(tc.Name, tc.Arguments)
			execText := fmt.Sprintf("Executing: %s %s", tc.Name, argsStr)
			addLiveEvent("action", map[string]any{
				"text":  execText,
				"event": execText,
			})
			var beforeSnap map[string]int64
			if tc.Name == "shell" && workspace != "" {
				beforeSnap = util.TakeDirSnapshot(workspace)
			}

			result := sp.ProcessToolCall(tc, activeSession, agentID)
			debugLog("runEngine: tool %s result: error=%q output_len=%d", tc.Name, result.Error, len(result.Output))

			if tc.Name == "shell" && workspace != "" {
				afterSnap := util.TakeDirSnapshot(workspace)
				changed := util.GetChangedFiles(beforeSnap, afterSnap)
				for _, f := range changed {
					agentModifiedFiles[f] = true
				}
			}

			if result.Error == "" {
				if tc.Name == "write" || tc.Name == "edit" || tc.Name == "apply_patch" {
					var args map[string]any
					if err := json.Unmarshal([]byte(tc.Arguments), &args); err == nil {
						if fp, ok := args["filePath"].(string); ok && workspace != "" {
							if jailed, err := util.JailPath(workspace, fp); err == nil {
								if rel, err := filepath.Rel(workspace, jailed); err == nil {
									agentModifiedFiles[filepath.ToSlash(rel)] = true
								}
							}
						}
					}
				}
			}
			if result.Error != "" {
				addLiveEvent("action", map[string]any{
					"text":  fmt.Sprintf("✗ %s: %.300s", tc.Name, result.Output),
					"event": fmt.Sprintf("✗ %s: %.300s", tc.Name, result.Output),
				})
			} else {
				addLiveEvent("action", map[string]any{
					"text":  fmt.Sprintf("✓ %s", tc.Name),
					"event": fmt.Sprintf("✓ %s", tc.Name),
				})
			}

			resultMsg := session.Message{
				ID:        fmt.Sprintf("msg-%d", time.Now().UnixNano()),
				SessionID: activeSession.SessionID,
				Role:      "tool",
				CreatedAt: time.Now().UnixMilli(),
			}
			resultMsg.Parts = append(resultMsg.Parts, session.MessagePart{
				Type:        "tool_result",
				ToolCallID:  tc.ID,
				ToolName:    tc.Name,
				Content:     result.Output,
				Attachments: result.Attachments,
			})
			activeSession.AddMessage(resultMsg)
		}
	}

	// Detect changed files and create artifact diffs
	workspaceChanges := map[string]any{"created": []string{}, "modified": []string{}, "deleted": []string{}}
	if snapHash != "" && workspace != "" {
		workspaceChanges = createArtifacts(workspace, snapHash, agentModifiedFiles)
		debugLog("runEngine: workspace changes: %+v", workspaceChanges)
	}

	elapsedMs := time.Since(startTime).Milliseconds()
	durationMs := elapsedMs
	if durationMs < 1 {
		durationMs = 1
	}

	reason := "finished"
	if i >= 150 {
		reason = "iteration_limit_reached"
	}

	addLiveEvent("complete", map[string]any{
		"response":          fullContent,
		"reason":            reason,
		"duration_ms":       durationMs,
		"workspace_changes": workspaceChanges,
	})
	debugLog("runEngine: final complete event")
}

var debugMode bool

func debugLog(format string, args ...any) {
	if debugMode {
		log.Printf("[DEBUG] "+format, args...)
	}
}



func stopBackgroundProcesses() {
	bgProcessesMu.Lock()
	for id, cancel := range bgProcesses {
		cancel()
		delete(bgProcesses, id)
	}
	bgProcessesMu.Unlock()
	impl.KillBackgroundShells()
}

func getPromptTokens() int {
	sessionMu.RLock()
	s := activeSession
	sessionMu.RUnlock()
	if s != nil {
		return s.PromptTokens
	}
	return 0
}

func getCompletionTokens() int {
	sessionMu.RLock()
	s := activeSession
	sessionMu.RUnlock()
	if s != nil {
		return s.CompletionTokens
	}
	return 0
}

func pathSafe(fullPath, workspace string) bool {
	absFull, err := filepath.Abs(fullPath)
	if err != nil {
		return false
	}
	absWS, err := filepath.Abs(workspace)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(absWS, absFull)
	if err != nil {
		return false
	}
	return !strings.HasPrefix(rel, "..")
}

func isAbsPathEqual(a, b string) bool {
	aa, _ := filepath.Abs(a)
	bb, _ := filepath.Abs(b)
	return strings.EqualFold(aa, bb)
}

func isAbsPath(path, workspace string) bool {
	return filepath.IsAbs(path)
}

func getArtifactsForUI(workspace string) []map[string]any {
	if workspace == "" {
		return nil
	}
	artifactsDir := filepath.Join(workspace, ".agent", "artifacts")
	if _, err := os.Stat(artifactsDir); os.IsNotExist(err) {
		return nil
	}
	var results []map[string]any
	entries, err := os.ReadDir(artifactsDir)
	if err != nil {
		return nil
	}
	for _, f := range entries {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".md") {
			continue
		}
		path := filepath.Join(artifactsDir, f.Name())
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		versionsDir := filepath.Join(artifactsDir, ".versions", f.Name())
		var versions []map[string]any
		if vEntries, err := os.ReadDir(versionsDir); err == nil {
			for _, v := range vEntries {
				if strings.HasSuffix(v.Name(), ".md") {
					versions = append(versions, map[string]any{
						"id":       strings.TrimSuffix(v.Name(), ".md"),
						"filename": v.Name(),
					})
				}
			}
		}
		info, _ := os.Stat(path)
		results = append(results, map[string]any{
			"title":         f.Name(),
			"content":       string(data),
			"id":            strings.TrimSuffix(f.Name(), ".md"),
			"filename":      f.Name(),
			"versions":      versions,
			"version_count": len(versions),
			"mtime":         info.ModTime().Unix(),
		})
	}
	sort.Slice(results, func(i, j int) bool {
		mi := results[i]["mtime"].(int64)
		mj := results[j]["mtime"].(int64)
		if mi != mj {
			return mi < mj
		}
		return results[i]["title"].(string) < results[j]["title"].(string)
	})
	return results
}

func createGitSnapshot(workspace string) string {
	// Check if inside a git work tree
	cmd := exec.Command("git", "rev-parse", "--is-inside-work-tree")
	cmd.Dir = workspace
	if err := cmd.Run(); err != nil {
		return ""
	}
	// Try git stash create
	cmd = exec.Command("git", "stash", "create", "-m", "QuietForge Snapshot")
	cmd.Dir = workspace
	out, err := cmd.Output()
	if err == nil && strings.TrimSpace(string(out)) != "" {
		return strings.TrimSpace(string(out))
	}
	// Fallback to HEAD
	cmd = exec.Command("git", "rev-parse", "HEAD")
	cmd.Dir = workspace
	out, err = cmd.Output()
	if err == nil {
		return strings.TrimSpace(string(out))
	}
	return ""
}

func createArtifacts(workspace, snapHash string, agentModifiedFiles map[string]bool) map[string]any {
	changes := map[string]any{"created": []string{}, "modified": []string{}, "deleted": []string{}}

	// Get changed files via git diff
	cmd := exec.Command("git", "diff", "--name-only", snapHash)
	cmd.Dir = workspace
	out, err := cmd.Output()
	if err != nil {
		return changes
	}
	changedSet := map[string]bool{}
	for _, f := range strings.Fields(string(out)) {
		f = strings.TrimSpace(f)
		if f != "" && agentModifiedFiles[filepath.ToSlash(f)] {
			changedSet[f] = true
		}
	}

	// Get untracked files
	cmd = exec.Command("git", "ls-files", "--others", "--exclude-standard")
	cmd.Dir = workspace
	out, err = cmd.Output()
	if err == nil {
		for _, f := range strings.Fields(string(out)) {
			f = strings.TrimSpace(f)
			if f != "" && !changedSet[f] && agentModifiedFiles[filepath.ToSlash(f)] {
				changedSet[f] = true
			}
		}
	}

	// Also explicitly include files that were modified by the agent but might have been entirely deleted
	// (git diff catches deleted tracked files, but maybe not untracked deleted files)
	for f := range agentModifiedFiles {
		changedSet[f] = true
	}

	if len(changedSet) == 0 {
		return changes
	}

	// Limit to 50 files
	var changedFiles []string
	for f := range changedSet {
		changedFiles = append(changedFiles, f)
	}
	sort.Strings(changedFiles)
	if len(changedFiles) > 50 {
		changedFiles = changedFiles[:50]
	}

	artifactsDir := filepath.Join(workspace, ".agent", "artifacts")
	os.MkdirAll(artifactsDir, 0755)

	var created, modified, deleted []string

	for _, relPath := range changedFiles {
		absPath := filepath.Join(workspace, relPath)

		// Get git diff for this file
		cmd = exec.Command("git", "diff", snapHash, "--", relPath)
		cmd.Dir = workspace
		dOut, dErr := cmd.Output()
		diffContent := ""
		if dErr == nil {
			diffContent = strings.TrimSpace(string(dOut))
		}

		// If no git diff but file exists, it's a new untracked file
		if diffContent == "" {
			info, sErr := os.Stat(absPath)
			if sErr == nil && info.Size() < 100*1024 {
				data, rErr := os.ReadFile(absPath)
				if rErr == nil {
					lines := strings.Split(string(data), "\n")
					diffContent = fmt.Sprintf("--- /dev/null\n+++ b/%s\n@@ -0,0 +1,%d @@\n", relPath, len(lines))
					for _, line := range lines {
						diffContent += "+" + line + "\n"
					}
				}
			}
		}

		if diffContent != "" {
			if len(diffContent) > 10000 {
				diffContent = diffContent[:10000]
			}
			safeName := strings.NewReplacer("\\", "_", "/", "_", ":", "_").Replace(relPath)
			artifactPath := filepath.Join(artifactsDir, "Diff_"+safeName+".md")
			os.WriteFile(artifactPath, []byte("```diff\n"+diffContent+"\n```\n"), 0644)

			if _, statErr := os.Stat(absPath); os.IsNotExist(statErr) {
				deleted = append(deleted, relPath)
			} else if dErr != nil || string(dOut) == "" {
				created = append(created, relPath)
			} else {
				modified = append(modified, relPath)
			}
		} else {
			created = append(created, relPath)
		}
	}

	if created == nil {
		created = []string{}
	}
	if modified == nil {
		modified = []string{}
	}
	if deleted == nil {
		deleted = []string{}
	}
	changes["created"] = created
	changes["modified"] = modified
	changes["deleted"] = deleted
	return changes
}

func dirExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && info.IsDir()
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ensureProjectInit() {
	qfDir := ".quietforge"
	if _, err := os.Stat(qfDir); os.IsNotExist(err) {
		log.Printf("Initializing new QuietForge project in current directory...")
		if err := os.MkdirAll(qfDir, 0755); err != nil {
			log.Printf("Warning: failed to create %s: %v", qfDir, err)
			return
		}
		
		configPath := filepath.Join(qfDir, "config.json")
		if _, err := os.Stat(configPath); os.IsNotExist(err) {
			defaultConfig := []byte(`{
  "agent": {
    "build": {
      "description": "Primary coding agent with full tool access"
    },
    "explore": {
      "description": "Fast codebase exploration agent"
    },
    "plan": {
      "description": "Research and planning agent (read-only tools)"
    }
  },
  "compaction": {
    "auto": true,
    "preserve_recent_tokens": 4000,
    "prune": true,
    "reserved": 4000,
    "tail_turns": 3,
    "tool_truncation_limit": 2000
  },
  "context_window": 1000000,
  "default_agent": "build",
  "disable_vision": true,
  "instructions": [],
  "intent_mode": "plan",
  "max_messages": 200,
  "mcp": {
    "servers": {
      "playwright": {
        "command": [
          "npx",
          "-y",
          "@playwright/mcp@latest",
          "--isolated"
        ],
        "type": "local"
      }
    }
  },
  "model": "gpt-4o",
  "permission": {
    "apply_patch": "allowed",
    "ast_search": "allowed",
    "edit": "allowed",
    "glob": "allowed",
    "grep": "allowed",
    "invalid": "allowed",
    "lsp": "allowed",
    "plan_exit": "allowed",
    "playwright__*": "allowed",
    "question": "allowed",
    "read": "allowed",
    "shell": "allowed",
    "skill": "allowed",
    "task": "allowed",
    "todowrite": "allowed",
    "webfetch": "allowed",
    "websearch": "allowed",
    "write": "allowed"
  },
  "shell": {
    "cwd": null,
    "timeout": 120000
  },
  "username": null
}`)
			os.WriteFile(configPath, defaultConfig, 0644)
		}
		
		wsDir := "workspace"
		if _, err := os.Stat(wsDir); os.IsNotExist(err) {
			os.MkdirAll(wsDir, 0755)
		}
	}
}
