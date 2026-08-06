package workspace

import (
	"log"
	"path/filepath"
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
	RepoResolver func(workspace string) (*storage.Repository, error)
	eventCh      chan Event
	workers      int
	wg           sync.WaitGroup
}

func NewObserver(resolver func(workspace string) (*storage.Repository, error), workers int) *Observer {
	obs := &Observer{
		RepoResolver: resolver,
		eventCh:      make(chan Event, 1000), // Buffered
		workers:      workers,
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
	
	// Ensure path is relative to workspace for consistency
	cleanPath := filepath.ToSlash(path)
	cleanWorkspace := filepath.ToSlash(workspace)
	if strings.HasPrefix(cleanPath, cleanWorkspace) {
		path = strings.TrimPrefix(cleanPath, cleanWorkspace)
		path = strings.TrimPrefix(path, "/")
	}
	
	select {
	case o.eventCh <- Event{Type: eventType, Workspace: workspace, Path: path}:
	default:
		log.Printf("Observer queue full, dropping event: %s", path)
	}
}

func (o *Observer) worker() {
	defer o.wg.Done()
	for ev := range o.eventCh {
		log.Printf("Observer received event: %s %s %s", ev.Type, ev.Workspace, ev.Path)
		repo, err := o.RepoResolver(ev.Workspace)
		if err != nil || repo == nil {
			log.Printf("Observer error resolving repo for %s: %v", ev.Workspace, err)
			continue // Could not resolve repository
		}
		switch ev.Type {
		case "file_modified", "created", "modified":
			if err := UpdateFile(repo, ev.Workspace, ev.Path); err != nil {
				log.Printf("Observer error updating %s: %v", ev.Path, err)
			} else {
				log.Printf("Observer successfully updated %s", ev.Path)
			}
		case "file_deleted", "deleted":
			if err := DeleteFile(repo, ev.Workspace, ev.Path); err != nil {
				log.Printf("Observer error deleting %s: %v", ev.Path, err)
			} else {
				log.Printf("Observer successfully deleted %s", ev.Path)
			}
		}
	}
}
