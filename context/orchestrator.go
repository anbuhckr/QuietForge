package context

import (
	"quietforge/storage"
)

type Orchestrator struct {
	Repo      *storage.Repository
	Cache     *WorkingSetCache
	Builder   *PromptBuilder
	Providers []ContextProvider
}

func NewOrchestrator(repo *storage.Repository) *Orchestrator {
	o := &Orchestrator{
		Repo:    repo,
		Cache:   NewWorkingSetCache(),
		Builder: NewPromptBuilder(2000), // Global token budget
		Providers: []ContextProvider{}, 
	}
	o.AddProvider(&ArchitectureProvider{Repo: repo})
	o.AddProvider(&TaskProvider{Repo: repo})
	o.AddProvider(&RetrievalProvider{Repo: repo})
	o.AddProvider(&DiagnosticProvider{Repo: repo})
	return o
}

// AddProvider registers a new context provider
func (o *Orchestrator) AddProvider(p ContextProvider) {
	o.Providers = append(o.Providers, p)
}

// GatherContext runs all providers and builds a unified context JSON string
func (o *Orchestrator) GatherContext(req ContextRequest) string {
	var allFragments []ContextFragment
	for _, p := range o.Providers {
		fragments, err := p.Gather(req)
		if err == nil && len(fragments) > 0 {
			allFragments = append(allFragments, fragments...)
		}
	}
	
	// Increment cache turn
	o.Cache.AdvanceTurn()

	return o.Builder.Build(o.Providers, allFragments, o.Cache)
}

// The old hardcoded hooks now wrap the robust architecture
func (o *Orchestrator) GetGlobalArchitecture(workspace string) string {
	// We will handle injection fully via EnrichUserPrompt to keep it centralized.
	// We could run GatherContext, but system prompt might want it injected separately, or we can just return it via prompt builder.
	// Actually, the new architecture can inject everything directly into the user prompt or system prompt based on builder output.
	// For simplicity, we just use GatherContext specifically filtering for architecture?
	// Let's rely on GatherContext for User prompts and Tools, and let system prompt remain static for now, OR we can push Arch via User prompt!
	// Wait, the user wants Architecture injected into System prompt? The Provider architecture makes it easy to just run GatherContext and inject.
	
	// Let's implement a targeted gather just for System Prompt global architecture if needed, or use the robust GatherContext for everything.
	return "" // We will handle injection fully via EnrichUserPrompt to keep it centralized.
}

func (o *Orchestrator) EnrichUserPrompt(rawPrompt string, workspace string) string {
	req := ContextRequest{
		Workspace: workspace,
		Prompt:    rawPrompt,
	}
	ctxBlock := o.GatherContext(req)
	
	if ctxBlock == "" {
		return rawPrompt
	}
	
	return ctxBlock + "\n\n" + rawPrompt
}

func (o *Orchestrator) EnrichToolOutput(toolName string, output string, workspace string) string {
	req := ContextRequest{
		Workspace: workspace,
		ToolName:  toolName,
		Output:    output,
	}
	ctxBlock := o.GatherContext(req)
	
	if ctxBlock == "" {
		return output
	}
	
	return output + "\n\n[Engine Diagnostic Hints]\n" + ctxBlock
}
