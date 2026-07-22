package session

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
)

func LoadPromptTemplate(name string) string {
	promptDir := filepath.Join("tool", "prompts")
	for _, ext := range []string{".txt", ".md"} {
		p := filepath.Join(promptDir, name+ext)
		if data, err := os.ReadFile(p); err == nil {
			return string(data)
		}
	}
	return ""
}

func BuildSystemPrompt(agentID string, tools []map[string]any, env map[string]string, extraInstructions []string, workspace string, globalArch string) string {
	templateName := agentID + "_system"

	system := LoadPromptTemplate(templateName)
	if system == "" {
		system = LoadPromptTemplate("default_system")
	}

	sections := []string{system}

	if len(tools) > 0 {
		toolLines := []string{"# Available Tools\n"}

		for _, t := range tools {
			name := "unknown"
			desc := ""

			if fn, ok := t["function"].(map[string]any); ok {
				if v, ok := fn["name"].(string); ok {
					name = v
				}
				if v, ok := fn["description"].(string); ok {
					desc = v
				}
			}

			toolLines = append(toolLines,
				fmt.Sprintf("- %s: %s", name, desc),
			)
		}

		sections = append(sections, strings.Join(toolLines, "\n"))
	}

	if env == nil {
		env = map[string]string{}
	}

	envInfo := buildEnvSection(env, workspace)
	if envInfo != "" {
		sections = append(sections, envInfo)
	}

	if len(extraInstructions) > 0 {
		lines := make([]string, len(extraInstructions))
		for i, s := range extraInstructions {
			lines[i] = "- " + s
		}

		sections = append(
			sections,
			"# Additional Instructions\n"+strings.Join(lines, "\n"),
		)
	}

	if workspace != "" {
		agentsMD := filepath.Join(workspace, ".agents", "AGENTS.md")
		if stat, err := os.Stat(agentsMD); err != nil || stat.IsDir() {
			agentsMD = filepath.Join(workspace, "AGENTS.md")
		}

		if stat, err := os.Stat(agentsMD); err == nil && !stat.IsDir() {
			if data, err := os.ReadFile(agentsMD); err == nil {
				relPath, relErr := filepath.Rel(workspace, agentsMD)
				if relErr != nil {
					relPath = "AGENTS.md"
				}
				relPath = filepath.ToSlash(relPath)
				sections = append(
					sections,
					fmt.Sprintf("# Workspace Rules (%s)\n%s", relPath, string(data)),
				)
			}
		}
	}

	hasShell := false
	for _, t := range tools {
		if fn, ok := t["function"].(map[string]any); ok {
			if name, ok := fn["name"].(string); ok && name == "shell" {
				hasShell = true
				break
			}
		}
	}

	if hasShell {
		shellRule := "You are running in a non-interactive headless shell. YOU MUST NEVER run commands that wait for user confirmation (e.g., waiting for [Y/n]). Always append `-y`, `--quiet`, or `CI=true` to your commands. If your command hangs, it is likely waiting for input you cannot provide."
		sections = append(sections, "# Interactive Shell Guidance\n"+shellRule)
	}

	return strings.Join(sections, "\n\n")
}

func buildEnvSection(env map[string]string, workspace string) string {
	var info []string

	info = append(info, "- Operating System: "+runtime.GOOS)

	cwd := workspace
	if cwd == "" {
		if dir, err := os.Getwd(); err == nil {
			cwd = dir
		}
	}

	info = append(info, "- Working directory: "+cwd)

	for _, key := range []string{
		"CI",
		"GITHUB_ACTIONS",
		"GITPOD_WORKSPACE_ID",
		"CODESPACES",
	} {
		if value, ok := env[key]; ok {
			info = append(info, fmt.Sprintf("- %s: %s", key, value))
		}
	}

	shell := env["SHELL"]
	if shell == "" {
		if runtime.GOOS == "windows" {
			shell = "powershell.exe"
		} else {
			shell = "/bin/sh"
		}
	}
	info = append(info, "- Default Shell: "+shell)

	if len(info) == 0 {
		return ""
	}

	return "# Environment\n" + strings.Join(info, "\n")
}