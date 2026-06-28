package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
)

func deepMerge(base, override map[string]any) map[string]any {
	result := make(map[string]any, len(base))
	for k, v := range base {
		result[k] = v
	}
	for key, val := range override {
		if existing, ok := result[key]; ok {
			baseMap, baseOk := existing.(map[string]any)
			overrideMap, overrideOk := val.(map[string]any)
			if baseOk && overrideOk {
				result[key] = deepMerge(baseMap, overrideMap)
				continue
			}
		}
		result[key] = val
	}
	return result
}

var jsoncCommentRegex = regexp.MustCompile(`//.*?$|/\*.*?\*/`)

func loadJSONFile(path string) map[string]any {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	if filepath.Ext(path) == ".jsonc" {
		raw = jsoncCommentRegex.ReplaceAll(raw, nil)
	}
	var data map[string]any
	if err := json.Unmarshal(raw, &data); err != nil {
		return nil
	}
	return data
}

func parseConfig(data map[string]any) Config {
	cfg := Config{
		Provider:          make(map[string]ProviderConfig),
		Agent:             make(map[string]AgentConfig),
		Permission:        make(map[string]any),
		DisabledProviders: []string{},
		Instructions:      []string{},
		Mode:              make(map[string]any),
	}

	if m, ok := data["model"].(string); ok {
		cfg.Model = &m
	}

	agentsRaw, _ := data["agent"].(map[string]any)
	if agentsRaw == nil {
		agentsRaw = make(map[string]any)
	}

	modeRaw, _ := data["mode"].(map[string]any)
	if modeRaw == nil {
		modeRaw = make(map[string]any)
	}
	for name, modeCfg := range modeRaw {
		if mc, ok := modeCfg.(map[string]any); ok {
			if _, exists := agentsRaw[name]; !exists {
				clone := make(map[string]any, len(mc)+1)
				for k, v := range mc {
					clone[k] = v
				}
				clone["mode"] = "primary"
				agentsRaw[name] = clone
			}
		}
	}

	for name, cfgAny := range agentsRaw {
		if ac, ok := cfgAny.(map[string]any); ok {
			var agent AgentConfig
			if desc, ok := ac["description"].(string); ok {
				agent.Description = &desc
			}
			if mode, ok := ac["mode"].(string); ok {
				agent.Mode = &mode
			}
			if perm, ok := ac["permission"].(map[string]any); ok {
				agent.Permission = perm
			}
			if model, ok := ac["model"].(string); ok {
				agent.Model = &model
			}
			if prompt, ok := ac["prompt"].(string); ok {
				agent.Prompt = &prompt
			}
			if temp, ok := ac["temperature"].(float64); ok {
				agent.Temperature = &temp
			}
			if topP, ok := ac["top_p"].(float64); ok {
				agent.TopP = &topP
			}
			if disable, ok := ac["disable"].(bool); ok {
				agent.Disable = disable
			}
			if hidden, ok := ac["hidden"].(bool); ok {
				agent.Hidden = &hidden
			}
			if steps, ok := ac["steps"].(float64); ok {
				s := int(steps)
				agent.Steps = &s
			}
			cfg.Agent[name] = agent
		}
	}

	providerRaw, _ := data["provider"].(map[string]any)
	for pid, pAny := range providerRaw {
		pc, ok := pAny.(map[string]any)
		if !ok {
			continue
		}
		var pConf ProviderConfig
		if key, ok := pc["api_key"].(string); ok {
			pConf.APIKey = &key
		}
		if baseURL, ok := pc["base_url"].(string); ok {
			pConf.BaseURL = &baseURL
		}
		if opts, ok := pc["options"].(map[string]any); ok {
			pConf.Options = opts
		} else {
			pConf.Options = make(map[string]any)
		}
		cfg.Provider[pid] = pConf
	}

	if compactionRaw, ok := data["compaction"].(map[string]any); ok {
		var cc CompactionConfig
		if auto, ok := compactionRaw["auto"].(bool); ok {
			cc.Auto = auto
		} else {
			cc.Auto = true
		}
		if tail, ok := compactionRaw["tail_turns"].(float64); ok {
			cc.TailTurns = int(tail)
		} else {
			cc.TailTurns = 2
		}
		if preserve, ok := compactionRaw["preserve_recent_tokens"].(float64); ok {
			cc.PreserveRecentTokens = int(preserve)
		}
		if reserved, ok := compactionRaw["reserved"].(float64); ok {
			cc.Reserved = int(reserved)
		}
		if prune, ok := compactionRaw["prune"].(bool); ok {
			cc.Prune = prune
		} else {
			cc.Prune = true
		}
		if trunc, ok := compactionRaw["tool_truncation_limit"].(float64); ok {
			cc.ToolTruncationLimit = int(trunc)
		} else {
			cc.ToolTruncationLimit = 2000
		}
		cfg.Compaction = &cc
	}

	if mcpRaw, ok := data["mcp"].(map[string]any); ok {
		if serversRaw, ok := mcpRaw["servers"].(map[string]any); ok {
			mcp := McpConfig{
				Servers: make(map[string]McpServerConfig),
			}
			for name, sAny := range serversRaw {
				if sc, ok := sAny.(map[string]any); ok {
					var msc McpServerConfig
					if cmd, ok := sc["command"].([]any); ok {
						msc.Command = make([]string, len(cmd))
						for i, v := range cmd {
							msc.Command[i], _ = v.(string)
						}
					}
					if typ, ok := sc["type"].(string); ok {
						msc.Type = typ
					} else {
						msc.Type = "local"
					}
					if cwd, ok := sc["cwd"].(string); ok {
						msc.Cwd = &cwd
					}
					if env, ok := sc["environment"].(map[string]any); ok {
						msc.Environment = make(map[string]string, len(env))
						for k, v := range env {
							msc.Environment[k], _ = v.(string)
						}
					} else {
						msc.Environment = make(map[string]string)
					}
					if disabled, ok := sc["disabled"].(bool); ok {
						msc.Disabled = disabled
					}
					mcp.Servers[name] = msc
				}
			}
			cfg.Mcp = &mcp
		}
	}

	if perm, ok := data["permission"].(map[string]any); ok {
		cfg.Permission = perm
	}
	if dp, ok := data["disabled_providers"].([]any); ok {
		cfg.DisabledProviders = make([]string, len(dp))
		for i, v := range dp {
			cfg.DisabledProviders[i], _ = v.(string)
		}
	}
	if ep, ok := data["enabled_providers"].([]any); ok {
		enabled := make([]string, len(ep))
		for i, v := range ep {
			enabled[i], _ = v.(string)
		}
		cfg.EnabledProviders = enabled
	}
	if shell, ok := data["shell"].(string); ok {
		cfg.Shell = &shell
	}
	if username, ok := data["username"].(string); ok {
		cfg.Username = &username
	}
	if inst, ok := data["instructions"].([]any); ok {
		cfg.Instructions = make([]string, len(inst))
		for i, v := range inst {
			cfg.Instructions[i], _ = v.(string)
		}
	}
	if da, ok := data["default_agent"].(string); ok {
		cfg.DefaultAgent = &da
	}
	if mode, ok := data["mode"].(map[string]any); ok {
		cfg.Mode = mode
	}
	if port, ok := data["port"].(float64); ok {
		p := int(port)
		cfg.Port = &p
	}
	if sslPort, ok := data["ssl_port"].(float64); ok {
		p := int(sslPort)
		cfg.SSLPort = &p
	}

	return cfg
}


func loadGlobalConfig() Config {
	result := make(map[string]any)

	if os.Getenv("QUIETFORGE_CONFIG") == "" && os.Getenv("QUIETFORGE_CONFIG_DIR") == "" {
		configDir := GlobalConfigDir()
		os.MkdirAll(configDir, 0755)
		mainFile := GlobalConfigFile()
		if _, err := os.Stat(mainFile); os.IsNotExist(err) {
			os.WriteFile(mainFile, []byte(`{"$schema":"https://quietforge.ai/config.json"}`+"\n"), 0644)
		}
	}

	var filesToLoad []string
	configDir := GlobalConfigDir()
	for _, fname := range []string{"config.json", "quietforge.json", "quietforge.jsonc"} {
		p := filepath.Join(configDir, fname)
		if _, err := os.Stat(p); err == nil {
			filesToLoad = append(filesToLoad, p)
		}
	}

	if envPath := os.Getenv("QUIETFORGE_CONFIG"); envPath != "" {
		filesToLoad = append(filesToLoad, envPath)
	}

	for _, path := range filesToLoad {
		if data := loadJSONFile(path); data != nil {
			result = deepMerge(result, data)
		}
	}

	return parseConfig(result)
}

func loadProjectConfig(startDir string) Config {
	result := make(map[string]any)

	for _, path := range ProjectConfigFiles(startDir) {
		if data := loadJSONFile(path); data != nil {
			result = deepMerge(result, data)
		}
	}

	if envContent := os.Getenv("QUIETFORGE_CONFIG_CONTENT"); envContent != "" {
		var data map[string]any
		if err := json.Unmarshal([]byte(envContent), &data); err == nil {
			result = deepMerge(result, data)
		}
	}

	return parseConfig(result)
}

func configToDict(cfg Config) map[string]any {
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
		agents := make(map[string]AgentConfig, len(cfg.Agent))
		for k, v := range cfg.Agent {
			agents[k] = v
		}
		d["agent"] = agents
	}
	if len(cfg.Permission) > 0 {
		d["permission"] = cfg.Permission
	}
	if len(cfg.DisabledProviders) > 0 {
		d["disabled_providers"] = cfg.DisabledProviders
	}
	if cfg.EnabledProviders != nil {
		d["enabled_providers"] = cfg.EnabledProviders
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
	if cfg.Mcp != nil {
		mcpMap := make(map[string]any)
		servers := make(map[string]any)
		for name, sc := range cfg.Mcp.Servers {
			sMap := make(map[string]any)
			cmdAny := make([]any, len(sc.Command))
			for i, c := range sc.Command {
				cmdAny[i] = c
			}
			sMap["command"] = cmdAny
			sMap["type"] = sc.Type
			if len(sc.Environment) > 0 {
				sMap["environment"] = sc.Environment
			}
			if sc.Disabled {
				sMap["disabled"] = true
			}
			servers[name] = sMap
		}
		mcpMap["servers"] = servers
		d["mcp"] = mcpMap
	}
	if cfg.Port != nil {
		d["port"] = *cfg.Port
	}
	if cfg.SSLPort != nil {
		d["ssl_port"] = *cfg.SSLPort
	}
	return d
}

func LoadConfig(startDir string) Config {
	globalCfg := loadGlobalConfig()
	projectCfg := loadProjectConfig(startDir)
	mergedData := deepMerge(
		configToDict(globalCfg),
		configToDict(projectCfg),
	)
	return parseConfig(mergedData)
}
