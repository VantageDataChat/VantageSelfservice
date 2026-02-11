// Package query implements the RAG query engine that coordinates
// embedding, vector search, and LLM response generation.
package query

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"helpdesk/internal/config"
	"helpdesk/internal/embedding"
	"helpdesk/internal/llm"
	"helpdesk/internal/vectorstore"
)

// QueryRequest represents a user's question submission.
type QueryRequest struct {
	Question string `json:"question"`
	UserID   string `json:"user_id"`
}

// QueryResponse represents the result of a RAG query.
type QueryResponse struct {
	Answer  string      `json:"answer"`
	Sources []SourceRef `json:"sources"`
	IsPending bool   `json:"is_pending"`
	Message   string `json:"message,omitempty"`
}

// SourceRef represents a reference to a source document chunk.
type SourceRef struct {
	DocumentName string `json:"document_name"`
	ChunkIndex   int    `json:"chunk_index"`
	Snippet      string `json:"snippet"`
}

// QueryEngine orchestrates the RAG query flow: embed → search → LLM generate or pending.
type QueryEngine struct {
	embeddingService embedding.EmbeddingService
	vectorStore      vectorstore.VectorStore
	llmService       llm.LLMService
	db               *sql.DB
	config           *config.Config
}

// NewQueryEngine creates a new QueryEngine with the given dependencies.
func NewQueryEngine(
	embeddingService embedding.EmbeddingService,
	vectorStore vectorstore.VectorStore,
	llmService llm.LLMService,
	db *sql.DB,
	cfg *config.Config,
) *QueryEngine {
	return &QueryEngine{
		embeddingService: embeddingService,
		vectorStore:      vectorStore,
		llmService:       llmService,
		db:               db,
		config:           cfg,
	}
}

// Query executes the full RAG pipeline:
// 1. Embed the question
// 2. Search the vector store for relevant chunks
// 3. If results found, call LLM to generate an answer with source references
// 4. If no results, create a pending question and notify the user
func (qe *QueryEngine) Query(req QueryRequest) (*QueryResponse, error) {
	// Step 1: Embed the question
	queryVector, err := qe.embeddingService.Embed(req.Question)
	if err != nil {
		return nil, fmt.Errorf("failed to embed question: %w", err)
	}

	// Step 2: Search vector store
	topK := qe.config.Vector.TopK
	threshold := qe.config.Vector.Threshold
	results, err := qe.vectorStore.Search(queryVector, topK, threshold)
	if err != nil {
		return nil, fmt.Errorf("failed to search vector store: %w", err)
	}

	// Step 3: If no results above threshold, create pending question
	if len(results) == 0 {
		if err := qe.createPendingQuestion(req.Question, req.UserID); err != nil {
			return nil, fmt.Errorf("failed to create pending question: %w", err)
		}
		return &QueryResponse{
			IsPending: true,
			Message:   "该问题已转交人工处理，请稍后查看回复",
		}, nil
	}

	// Step 4: Build context from search results and call LLM
	context := make([]string, len(results))
	for i, r := range results {
		context[i] = r.ChunkText
	}

	answer, err := qe.llmService.Generate("", context, req.Question)
	if err != nil {
		return nil, fmt.Errorf("failed to generate answer: %w", err)
	}

	// Step 5: Build source references
	sources := make([]SourceRef, len(results))
	for i, r := range results {
		snippet := r.ChunkText
		if len(snippet) > 100 {
			snippet = snippet[:100]
		}
		sources[i] = SourceRef{
			DocumentName: r.DocumentName,
			ChunkIndex:   r.ChunkIndex,
			Snippet:      snippet,
		}
	}

	return &QueryResponse{
		Answer:  answer,
		Sources: sources,
	}, nil
}

// createPendingQuestion inserts a new pending question record into the database.
func (qe *QueryEngine) createPendingQuestion(question, userID string) error {
	id, err := generateID()
	if err != nil {
		return err
	}
	_, err = qe.db.Exec(
		`INSERT INTO pending_questions (id, question, user_id, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, question, userID, "pending", time.Now().UTC(),
	)
	return err
}

// generateID creates a random hex string for use as a unique identifier.
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}
