package workspace

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"sync"
	"time"

	"quietforge/config"
	"quietforge/storage"

	openai "github.com/sashabaranov/go-openai"
)

type EmbeddingRecord struct {
	ID         string
	Workspace  string
	Kind       string
	ObjectID   string
	ChunkIndex int
	Model      string
	Dimension  int
	Hash       string
	Embedding  []float32
}

var (
	cacheMu     sync.RWMutex
	vectorCache = make(map[string][]EmbeddingRecord) // map[workspace][]EmbeddingRecord
)

func LoadWorkspaceEmbeddings(workspace string, repo *storage.Repository) error {
	cacheMu.Lock()
	defer cacheMu.Unlock()

	rows, err := repo.DB.Conn.Query(`
		SELECT id, kind, object_id, chunk_index, model, dimension, hash, embedding
		FROM workspace_embeddings
		WHERE workspace = ?
	`, workspace)
	if err != nil {
		return err
	}
	defer rows.Close()

	var records []EmbeddingRecord
	for rows.Next() {
		var rec EmbeddingRecord
		var embBlob []byte
		rec.Workspace = workspace
		if err := rows.Scan(&rec.ID, &rec.Kind, &rec.ObjectID, &rec.ChunkIndex, &rec.Model, &rec.Dimension, &rec.Hash, &embBlob); err != nil {
			continue
		}
		
		var emb []float32
		if err := json.Unmarshal(embBlob, &emb); err != nil {
			continue
		}
		rec.Embedding = emb
		records = append(records, rec)
	}

	vectorCache[workspace] = records
	return nil
}

func GetWorkspaceEmbeddings(workspace string) []EmbeddingRecord {
	cacheMu.RLock()
	defer cacheMu.RUnlock()
	return vectorCache[workspace]
}

// Queue chunks for batch processing
type Chunk struct {
	Workspace  string
	Kind       string
	ObjectID   string
	ChunkIndex int
	Content    string
	Hash       string
}

var (
	batchMu    sync.Mutex
	chunkBatch []Chunk
	batchTimer *time.Timer
)

func EmbedChunk(workspace, kind, objectID string, chunkIndex int, content string, cfg *config.EmbeddingConfig, repo *storage.Repository) {
	if cfg == nil || !cfg.Enabled {
		return
	}

	hash := fmt.Sprintf("%x", sha256.Sum256([]byte(content)))
	model := "text-embedding-3-small"
	if cfg.Model != nil && *cfg.Model != "" {
		model = *cfg.Model
	}

	// Check if hash matches
	cacheMu.RLock()
	cache := vectorCache[workspace]
	cacheMu.RUnlock()
	
	for _, rec := range cache {
		if rec.Kind == kind && rec.ObjectID == objectID && rec.ChunkIndex == chunkIndex {
			if rec.Hash == hash && rec.Model == model {
				return // No change, skip
			}
		}
	}

	batchMu.Lock()
	defer batchMu.Unlock()
	
	chunkBatch = append(chunkBatch, Chunk{
		Workspace:  workspace,
		Kind:       kind,
		ObjectID:   objectID,
		ChunkIndex: chunkIndex,
		Content:    content,
		Hash:       hash,
	})

	if len(chunkBatch) >= 50 {
		if batchTimer != nil {
			batchTimer.Stop()
		}
		go processBatch(append([]Chunk(nil), chunkBatch...), cfg, repo)
		chunkBatch = nil
	} else if len(chunkBatch) == 1 {
		batchTimer = time.AfterFunc(2*time.Second, func() {
			batchMu.Lock()
			if len(chunkBatch) > 0 {
				go processBatch(append([]Chunk(nil), chunkBatch...), cfg, repo)
				chunkBatch = nil
			}
			batchMu.Unlock()
		})
	}
}

func processBatch(chunks []Chunk, cfg *config.EmbeddingConfig, repo *storage.Repository) {
	if len(chunks) == 0 {
		return
	}

	apiKey := ""
	if cfg.APIKey != nil {
		apiKey = *cfg.APIKey
	}
	
	clientConfig := openai.DefaultConfig(apiKey)
	if cfg.BaseURL != nil && *cfg.BaseURL != "" {
		clientConfig.BaseURL = *cfg.BaseURL
	}
	client := openai.NewClientWithConfig(clientConfig)

	var inputs []string
	for _, c := range chunks {
		inputs = append(inputs, c.Content)
	}

	model := openai.SmallEmbedding3
	if cfg.Model != nil && *cfg.Model != "" {
		model = openai.EmbeddingModel(*cfg.Model)
	}

	req := openai.EmbeddingRequest{
		Input: inputs,
		Model: model,
	}

	resp, err := client.CreateEmbeddings(context.Background(), req)
	if err != nil {
		fmt.Printf("Embedding error: %v\n", err)
		return
	}

	var newRecords []EmbeddingRecord

	for i, data := range resp.Data {
		c := chunks[i]
		
		embBlob, _ := json.Marshal(data.Embedding)
		
		id := fmt.Sprintf("%s_%s_%s_%d", c.Workspace, c.Kind, c.ObjectID, c.ChunkIndex)
		
		repo.DB.Conn.Exec(`
			INSERT INTO workspace_embeddings (id, workspace, kind, object_id, chunk_index, model, dimension, hash, embedding, updated_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(id) DO UPDATE SET
			model=excluded.model, dimension=excluded.dimension, hash=excluded.hash, embedding=excluded.embedding, updated_at=excluded.updated_at
		`, id, c.Workspace, c.Kind, c.ObjectID, c.ChunkIndex, string(model), len(data.Embedding), c.Hash, embBlob, time.Now().Unix())

		newRecords = append(newRecords, EmbeddingRecord{
			ID:         id,
			Workspace:  c.Workspace,
			Kind:       c.Kind,
			ObjectID:   c.ObjectID,
			ChunkIndex: c.ChunkIndex,
			Model:      string(model),
			Dimension:  len(data.Embedding),
			Hash:       c.Hash,
			Embedding:  data.Embedding,
		})
	}

	// Update cache
	cacheMu.Lock()
	defer cacheMu.Unlock()
	for _, newRec := range newRecords {
		cache := vectorCache[newRec.Workspace]
		updated := false
		for i, rec := range cache {
			if rec.ID == newRec.ID {
				cache[i] = newRec
				updated = true
				break
			}
		}
		if !updated {
			vectorCache[newRec.Workspace] = append(cache, newRec)
		}
	}
}

func GenerateSingleEmbedding(text string, cfg *config.EmbeddingConfig) ([]float32, error) {
	apiKey := ""
	if cfg.APIKey != nil {
		apiKey = *cfg.APIKey
	}
	
	clientConfig := openai.DefaultConfig(apiKey)
	if cfg.BaseURL != nil && *cfg.BaseURL != "" {
		clientConfig.BaseURL = *cfg.BaseURL
	}
	client := openai.NewClientWithConfig(clientConfig)

	model := openai.SmallEmbedding3
	if cfg.Model != nil && *cfg.Model != "" {
		model = openai.EmbeddingModel(*cfg.Model)
	}

	req := openai.EmbeddingRequest{
		Input: []string{text},
		Model: model,
	}

	resp, err := client.CreateEmbeddings(context.Background(), req)
	if err != nil {
		return nil, err
	}
	return resp.Data[0].Embedding, nil
}
