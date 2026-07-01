package tool

import (
	"fmt"
)

type Registry struct {
	tools map[string]Tool
}

func NewRegistry() *Registry {
	return &Registry{
		tools: make(map[string]Tool),
	}
}

func (r *Registry) Register(tool Tool) {
	r.tools[tool.ID()] = tool
}

func (r *Registry) RemoveTool(id string) {
	delete(r.tools, id)
}

func (r *Registry) GetTool(id string) (Tool, error) {
	tool, exists := r.tools[id]
	if !exists {
		return nil, fmt.Errorf("tool '%s' not found", id)
	}
	return tool, nil
}

func (r *Registry) GetAll() []Tool {
	var all []Tool
	for _, t := range r.tools {
		all = append(all, t)
	}
	return all
}
