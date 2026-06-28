package implement

import (
	"quietforge/tool"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
)

type SkillTool struct {
	SkillsDir string
}

func (t *SkillTool) ID() string {
	return "skill"
}

func (t *SkillTool) Description() string {
	return "Load specialized skill instructions for a given task domain."
}

func (t *SkillTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"skill_name": map[string]interface{}{"type": "string", "description": "Name of the skill to load"},
			"prompt":     map[string]interface{}{"type": "string", "description": "Task prompt to send with skill context"},
		},
		"required": []string{"skill_name"},
	}
}

func (t *SkillTool) Execute(args []byte, ctx *tool.ToolContext) (*tool.ToolResult, error) {
	var params struct {
		SkillName string `json:"skill_name"`
		Prompt    string `json:"prompt"`
	}
	if err := json.Unmarshal(args, &params); err != nil {
		return &tool.ToolResult{Error: "invalid_args", Output: err.Error()}, nil
	}

	skillName := params.SkillName
	prompt := params.Prompt

	var searchDirs []string
	if t.SkillsDir != "" {
		searchDirs = append(searchDirs, t.SkillsDir)
	}
	if ctx.Workspace != "" {
		searchDirs = append(searchDirs, filepath.Join(ctx.Workspace, ".agents", "skills"))
	}

	cwd, _ := os.Getwd()
	searchDirs = append(searchDirs, filepath.Join(cwd, ".quietforge", "skills"))
	
	userProfile := os.Getenv("USERPROFILE")
	if userProfile == "" {
		userProfile = os.Getenv("HOME")
	}
	if userProfile != "" {
		searchDirs = append(searchDirs, filepath.Join(userProfile, ".config", "quietforge", "skills"))
	}

	var skillContent string
	found := false

	for _, base := range searchDirs {
		candidates := []string{
			filepath.Join(base, skillName+".md"),
			filepath.Join(base, skillName+".txt"),
			filepath.Join(base, skillName, "README.md"),
		}
		for _, p := range candidates {
			b, err := os.ReadFile(p)
			if err == nil {
				if isBinary(b) {
					return &tool.ToolResult{Error: "binary_content", Output: fmt.Sprintf("Skill file %s appears to be binary. Skill files must be text.", p)}, nil
				}
				skillContent = string(b)
				found = true
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return &tool.ToolResult{Error: "not_found", Output: fmt.Sprintf("Skill not found: %s", skillName)}, nil
	}

	output := fmt.Sprintf("Loaded skill: %s\n\n%s", skillName, skillContent)
	if prompt != "" {
		output += fmt.Sprintf("\n\n---\nTask: %s", prompt)
	}

	return &tool.ToolResult{
		Title:  fmt.Sprintf("Skill: %s", skillName),
		Output: output,
	}, nil
}
