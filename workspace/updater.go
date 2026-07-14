package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
	"time"

	"quietforge/config"
	"quietforge/storage"
)

func UpdateFile(repo *storage.Repository, workspace, path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return err
	}

	hashBytes := sha256.Sum256(data)
	hash := hex.EncodeToString(hashBytes[:])

	// Optional: Check if hash changed before parsing (requires a GetWorkspaceFile func, omitting for simplicity/robustness)

	cfg := config.LoadConfig(workspace)

	// Check if this is a brain markdown file
	if strings.HasSuffix(path, ".md") {
		brainDir := ".agent"
		if cfg.Embedding != nil && cfg.Embedding.BrainDir != nil {
			brainDir = *cfg.Embedding.BrainDir
		}
		
		cleanPath := filepath.ToSlash(path)
		cleanBrainDir := filepath.ToSlash(brainDir)
		
		// Simple contains check to handle both absolute and relative paths robustly
		if strings.Contains(cleanPath, cleanBrainDir+"/") {
			baseName := strings.ToLower(filepath.Base(cleanPath))
			if strings.Contains(baseName, "diff") || strings.Contains(baseName, "patch") {
				// Skip diffs and patches to avoid polluting the concept index with raw code changes
				return repo.UpsertWorkspaceFile(storage.WorkspaceFileRow{
					Workspace: workspace, Path: path, FileHash: hash, UpdatedAt: time.Now().Unix(),
				})
			}

			chunks := strings.Split(string(data), "\n\n")
			for i, chunk := range chunks {
				chunk = strings.TrimSpace(chunk)
				if len(chunk) > 20 { // Ignore tiny fragments
					EmbedChunk(workspace, "brain", path, i, chunk, cfg.Embedding, repo)
				}
			}
			return repo.UpsertWorkspaceFile(storage.WorkspaceFileRow{
				Workspace: workspace, Path: path, FileHash: hash, UpdatedAt: time.Now().Unix(),
			})
		}
	}

	tree, err := ParseAST(path, data)
	if err != nil {
		return err // Parse error
	}
	if tree == nil {
		// Unsupported language, just track the hash
		return repo.UpsertWorkspaceFile(storage.WorkspaceFileRow{
			Workspace: workspace, Path: path, FileHash: hash, UpdatedAt: time.Now().Unix(),
		})
	}
	defer tree.Close()

	symbols, edges := ExtractFacts(workspace, path, data, tree)

	// Ensure updated_at is populated
	now := time.Now().Unix()
	if len(symbols) > 0 { symbols[0].UpdatedAt = now }
	
	// Queue embeddings
	lines := strings.Split(string(data), "\n")
	for i, sym := range symbols {
		start := sym.LineStart - 1
		end := sym.LineEnd
		if start < 0 { start = 0 }
		if end > len(lines) { end = len(lines) }
		if start < end {
			chunkContent := strings.Join(lines[start:end], "\n")
			EmbedChunk(workspace, "symbol", path+":"+sym.Name, i, chunkContent, cfg.Embedding, repo)
		}
	}

	return repo.SyncWorkspaceFacts(workspace, path, hash, symbols, edges)
}

func DeleteFile(repo *storage.Repository, workspace, path string) error {
	return repo.DeleteWorkspaceFileAndFacts(workspace, path)
}
