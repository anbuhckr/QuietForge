package workspace

import (
	"strings"
	"sync"
	"quietforge/storage"
)

type Event struct {
	Type      string
	Workspace string
	Path      string
}

type Observer struct {
	repo    *storage.Repository
	eventCh chan Event
	workers int
	wg      sync.WaitGroup
}

func NewObserver(repo *storage.Repository, workers int) *Observer {
	obs := &Observer{
		repo:    repo,
		eventCh: make(chan Event, 1000), // Buffered
		workers: workers,
	}
	obs.Start()
	return obs
}

func (o *Observer) Start() {
	for i := 0; i < o.workers; i++ {
		o.wg.Add(1)
		go o.worker()
	}
}

func (o *Observer) Stop() {
	close(o.eventCh)
	o.wg.Wait()
}

func (o *Observer) Emit(eventType, workspace, path string) {
	if strings.Contains(path, ".git") || strings.Contains(path, "node_modules") {
		return
	}
	// Allow markdown files in .agent for Brain indexing, ignore the rest (like sessions.db)
	if strings.Contains(path, ".agent") {
		if !strings.HasSuffix(path, ".md") {
			return
		}
	}
	
	// Ensure path is relative to workspace for consistency, or absolute. We use whatever the Engine uses (which is jail path usually)
	
	o.eventCh <- Event{Type: eventType, Workspace: workspace, Path: path}
}

func (o *Observer) worker() {
	defer o.wg.Done()
	for ev := range o.eventCh {
		switch ev.Type {
		case "file_modified":
			if err := UpdateFile(o.repo, ev.Workspace, ev.Path); err != nil {
				// log.Printf("Observer error updating %s: %v", ev.Path, err)
			}
		case "file_deleted":
			if err := DeleteFile(o.repo, ev.Workspace, ev.Path); err != nil {
				// log.Printf("Observer error deleting %s: %v", ev.Path, err)
			}
		}
	}
}
