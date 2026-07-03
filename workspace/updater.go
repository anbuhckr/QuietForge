package workspace

import (
	"crypto/sha256"
	"encoding/hex"
	"os"
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
	cfg := config.LoadConfig(".")
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
