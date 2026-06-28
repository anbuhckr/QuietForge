package plugins

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"quietforge/tool"

	"github.com/traefik/yaegi/interp"
	"github.com/traefik/yaegi/stdlib"
)

func DiscoverPluginTools(searchDir string) map[string]tool.Tool {
	tools := make(map[string]tool.Tool)
	seen := make(map[string]bool)

	searchDirs := resolveSearchDirs(searchDir)

	for _, d := range searchDirs {
		entries, err := os.ReadDir(d)
		if err != nil {
			continue
		}
		for _, entry := range entries {
			if entry.IsDir() || strings.HasPrefix(entry.Name(), "_") {
				continue
			}
			if filepath.Ext(entry.Name()) != ".go" {
				continue
			}

			fullPath := filepath.Join(d, entry.Name())
			loaded, err := loadGoPlugin(fullPath)
			if err != nil {
				continue
			}
			if _, dup := seen[loaded.ID()]; !dup {
				seen[loaded.ID()] = true
				tools[loaded.ID()] = loaded
			}
		}
	}

	return tools
}

func resolveSearchDirs(searchDir string) []string {
	var dirs []string

	if searchDir != "" {
		dirs = append(dirs, searchDir)
	}

	cwd, _ := os.Getwd()
	for _, sub := range []string{".quietforge/tools", ".quietforge/tool"} {
		p := filepath.Join(cwd, sub)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			dirs = append(dirs, p)
		}
	}

	homeDir, _ := os.UserHomeDir()
	var configBase string
	if runtime.GOOS == "windows" {
		configBase = os.Getenv("APPDATA")
		if configBase == "" {
			configBase = filepath.Join(homeDir, "AppData", "Roaming")
		}
	} else {
		configBase = os.Getenv("XDG_CONFIG_HOME")
		if configBase == "" {
			configBase = filepath.Join(homeDir, ".config")
		}
	}
	configDir := filepath.Join(configBase, "quietforge")
	for _, sub := range []string{"tools", "tool"} {
		p := filepath.Join(configDir, sub)
		if info, err := os.Stat(p); err == nil && info.IsDir() {
			dirs = append(dirs, p)
		}
	}

	return dirs
}

func loadGoPlugin(path string) (tool.Tool, error) {
	i := interp.New(interp.Options{})
	i.Use(stdlib.Symbols)
	i.Use(interp.Symbols)

	if _, err := i.Eval(`import "quietforge/tool"`); err != nil {
		return nil, fmt.Errorf("yaegi import tool: %w", err)
	}

	_, err := i.EvalPath(path)
	if err != nil {
		return nil, fmt.Errorf("yaegi eval %s: %w", path, err)
	}

	v, err := i.Eval(`PluginTool{}`)
	if err != nil {
		v, err = i.Eval(`PluginTool`)
		if err != nil {
			return nil, fmt.Errorf("plugin %s: PluginTool symbol not found: %w", path, err)
		}
		fn, ok := v.Interface().(func() tool.Tool)
		if ok {
			return fn(), nil
		}
		return nil, fmt.Errorf("plugin %s: PluginTool is not a tool.Tool or func() tool.Tool", path)
	}

	t, ok := v.Interface().(tool.Tool)
	if !ok {
		return nil, fmt.Errorf("plugin %s: PluginTool{} does not implement tool.Tool", path)
	}
	return t, nil
}
