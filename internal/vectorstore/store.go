// Package vectorstore provides vector storage and similarity search using SQLite.
// It stores document embeddings and supports cosine similarity based retrieval
// with an in-memory cache for fast search and concurrent similarity computation.
package vectorstore

import (
	"database/sql"
	"fmt"
	"math"
	"runtime"
	"sort"
	"sync"

	_ "github.com/mattn/go-sqlite3"
)

// VectorStore defines the interface for storing and searching document embeddings.
type VectorStore interface {
	Store(docID string, chunks []VectorChunk) error
	Search(queryVector []float64, topK int, threshold float64) ([]SearchResult, error)
	DeleteByDocID(docID string) error
}

// VectorChunk represents a document chunk with its embedding vector.
type VectorChunk struct {
	ChunkText    string    `json:"chunk_text"`
	ChunkIndex   int       `json:"chunk_index"`
	DocumentID   string    `json:"document_id"`
	DocumentName string    `json:"document_name"`
	Vector       []float64 `json:"vector"`
}

// SearchResult represents a search result with similarity score.
type SearchResult struct {
	ChunkText    string  `json:"chunk_text"`
	ChunkIndex   int     `json:"chunk_index"`
	DocumentID   string  `json:"document_id"`
	DocumentName string  `json:"document_name"`
	Score        float64 `json:"score"`
}

// cachedChunk holds a chunk's metadata and pre-computed norm for fast similarity.
type cachedChunk struct {
	chunkText    string
	chunkIndex   int
	documentID   string
	documentName string
	vector       []float64
	norm         float64 // pre-computed L2 norm
}

// SQLiteVectorStore implements VectorStore using SQLite for persistence
// with an in-memory vector cache for fast similarity search.
type SQLiteVectorStore struct {
	db    *sql.DB
	mu    sync.RWMutex
	cache []cachedChunk // in-memory vector index
	loaded bool
}

// NewSQLiteVectorStore creates a new SQLiteVectorStore with the given database connection.
func NewSQLiteVectorStore(db *sql.DB) *SQLiteVectorStore {
	return &SQLiteVectorStore{db: db}
}

// loadCache reads all chunks from the database into memory.
// Must be called with mu held for writing.
func (s *SQLiteVectorStore) loadCache() error {
	rows, err := s.db.Query(`SELECT document_id, document_name, chunk_index, chunk_text, embedding FROM chunks`)
	if err != nil {
		return fmt.Errorf("failed to query chunks: %w", err)
	}
	defer rows.Close()

	var cache []cachedChunk
	for rows.Next() {
		var docID, docName, chunkText string
		var chunkIndex int
		var embeddingBytes []byte

		if err := rows.Scan(&docID, &docName, &chunkIndex, &chunkText, &embeddingBytes); err != nil {
			return fmt.Errorf("failed to scan row: %w", err)
		}

		vec := DeserializeVector(embeddingBytes)
		cache = append(cache, cachedChunk{
			chunkText:    chunkText,
			chunkIndex:   chunkIndex,
			documentID:   docID,
			documentName: docName,
			vector:       vec,
			norm:         vectorNorm(vec),
		})
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("error iterating rows: %w", err)
	}

	s.cache = cache
	s.loaded = true
	return nil
}

// ensureCache loads the cache if not already loaded.
func (s *SQLiteVectorStore) ensureCache() error {
	if s.loaded {
		return nil
	}
	return s.loadCache()
}

// vectorNorm computes the L2 norm of a vector.
func vectorNorm(v []float64) float64 {
	var sum float64
	for _, x := range v {
		sum += x * x
	}
	return math.Sqrt(sum)
}

// Store inserts a batch of VectorChunks into the chunks table and updates the cache.
func (s *SQLiteVectorStore) Store(docID string, chunks []VectorChunk) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	stmt, err := tx.Prepare(`INSERT INTO chunks (id, document_id, document_name, chunk_index, chunk_text, embedding)
		VALUES (?, ?, ?, ?, ?, ?)`)
	if err != nil {
		tx.Rollback()
		return fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer stmt.Close()

	var newCached []cachedChunk
	for _, chunk := range chunks {
		chunkID := fmt.Sprintf("%s-%d", docID, chunk.ChunkIndex)
		embeddingBytes := SerializeVector(chunk.Vector)

		_, err := stmt.Exec(chunkID, docID, chunk.DocumentName, chunk.ChunkIndex, chunk.ChunkText, embeddingBytes)
		if err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to insert chunk %s: %w", chunkID, err)
		}

		newCached = append(newCached, cachedChunk{
			chunkText:    chunk.ChunkText,
			chunkIndex:   chunk.ChunkIndex,
			documentID:   chunk.DocumentID,
			documentName: chunk.DocumentName,
			vector:       chunk.Vector,
			norm:         vectorNorm(chunk.Vector),
		})
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	// Update cache
	if s.loaded {
		// Cache already loaded — just append new entries
		s.cache = append(s.cache, newCached...)
	} else {
		// First access — load everything from DB (includes just-inserted rows)
		if err := s.loadCache(); err != nil {
			return err
		}
	}
	return nil
}

// Search uses the in-memory cache with concurrent cosine similarity computation.
// It partitions the cache across goroutines, filters by threshold, and returns top-K.
func (s *SQLiteVectorStore) Search(queryVector []float64, topK int, threshold float64) ([]SearchResult, error) {
	// Ensure cache is loaded (needs write lock on first call)
	s.mu.Lock()
	if err := s.ensureCache(); err != nil {
		s.mu.Unlock()
		return nil, err
	}
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()

	if len(s.cache) == 0 {
		return nil, nil
	}

	queryNorm := vectorNorm(queryVector)
	if queryNorm == 0 {
		return nil, nil
	}

	// Determine concurrency level
	numWorkers := runtime.NumCPU()
	if numWorkers > len(s.cache) {
		numWorkers = len(s.cache)
	}
	if numWorkers < 1 {
		numWorkers = 1
	}

	// Partition work
	chunkSize := (len(s.cache) + numWorkers - 1) / numWorkers
	type partialResult struct {
		results []SearchResult
	}
	resultsCh := make(chan partialResult, numWorkers)

	for w := 0; w < numWorkers; w++ {
		start := w * chunkSize
		end := start + chunkSize
		if end > len(s.cache) {
			end = len(s.cache)
		}
		go func(items []cachedChunk) {
			var local []SearchResult
			for i := range items {
				c := &items[i]
				if c.norm == 0 {
					continue
				}
				// Inline dot product for speed
				var dot float64
				for j := range queryVector {
					dot += queryVector[j] * c.vector[j]
				}
				score := dot / (queryNorm * c.norm)
				if score >= threshold {
					local = append(local, SearchResult{
						ChunkText:    c.chunkText,
						ChunkIndex:   c.chunkIndex,
						DocumentID:   c.documentID,
						DocumentName: c.documentName,
						Score:        score,
					})
				}
			}
			resultsCh <- partialResult{results: local}
		}(s.cache[start:end])
	}

	// Collect results
	var allResults []SearchResult
	for w := 0; w < numWorkers; w++ {
		pr := <-resultsCh
		allResults = append(allResults, pr.results...)
	}

	sort.Slice(allResults, func(i, j int) bool {
		return allResults[i].Score > allResults[j].Score
	})

	if len(allResults) > topK {
		allResults = allResults[:topK]
	}

	return allResults, nil
}

// DeleteByDocID removes all chunks for the given document from DB and cache.
func (s *SQLiteVectorStore) DeleteByDocID(docID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	_, err := s.db.Exec(`DELETE FROM chunks WHERE document_id = ?`, docID)
	if err != nil {
		return fmt.Errorf("failed to delete chunks for document %s: %w", docID, err)
	}

	// Update cache: remove matching entries
	if s.loaded {
		filtered := s.cache[:0]
		for _, c := range s.cache {
			if c.documentID != docID {
				filtered = append(filtered, c)
			}
		}
		s.cache = filtered
	}

	return nil
}
