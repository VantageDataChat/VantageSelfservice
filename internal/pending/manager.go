// Package pending manages questions that could not be automatically answered.
// It supports creating, listing, and answering pending questions.
package pending

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"time"

	"helpdesk/internal/chunker"
	"helpdesk/internal/embedding"
	"helpdesk/internal/llm"
	"helpdesk/internal/vectorstore"
)

// PendingQuestion represents a user question awaiting admin response.
type PendingQuestion struct {
	ID        string    `json:"id"`
	Question  string    `json:"question"`
	UserID    string    `json:"user_id"`
	Status    string    `json:"status"` // "pending", "answered"
	Answer    string    `json:"answer,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// AdminAnswerRequest represents an admin's answer to a pending question.
type AdminAnswerRequest struct {
	QuestionID string `json:"question_id"`
	Text       string `json:"text,omitempty"`
	ImageData  []byte `json:"image_data,omitempty"`
	URL        string `json:"url,omitempty"`
}

// PendingQuestionManager handles the lifecycle of pending questions.
type PendingQuestionManager struct {
	db               *sql.DB
	chunker          *chunker.TextChunker
	embeddingService embedding.EmbeddingService
	vectorStore      vectorstore.VectorStore
	llmService       llm.LLMService
}

// NewPendingQuestionManager creates a new PendingQuestionManager with the given dependencies.
func NewPendingQuestionManager(
	db *sql.DB,
	c *chunker.TextChunker,
	es embedding.EmbeddingService,
	vs vectorstore.VectorStore,
	ls llm.LLMService,
) *PendingQuestionManager {
	return &PendingQuestionManager{
		db:               db,
		chunker:          c,
		embeddingService: es,
		vectorStore:      vs,
		llmService:       ls,
	}
}

// generateID creates a random hex string for use as a unique identifier.
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// CreatePending inserts a new pending question record with status="pending".
func (pm *PendingQuestionManager) CreatePending(question string, userID string) (*PendingQuestion, error) {
	id, err := generateID()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	_, err = pm.db.Exec(
		`INSERT INTO pending_questions (id, question, user_id, status, created_at) VALUES (?, ?, ?, ?, ?)`,
		id, question, userID, "pending", now,
	)
	if err != nil {
		return nil, fmt.Errorf("failed to insert pending question: %w", err)
	}

	return &PendingQuestion{
		ID:        id,
		Question:  question,
		UserID:    userID,
		Status:    "pending",
		CreatedAt: now,
	}, nil
}

// ListPending returns pending questions filtered by status (or all if status is empty),
// ordered by created_at DESC.
func (pm *PendingQuestionManager) ListPending(status string) ([]PendingQuestion, error) {
	var rows *sql.Rows
	var err error

	if status == "" {
		rows, err = pm.db.Query(
			`SELECT id, question, user_id, status, answer, created_at FROM pending_questions ORDER BY created_at DESC`,
		)
	} else {
		rows, err = pm.db.Query(
			`SELECT id, question, user_id, status, answer, created_at FROM pending_questions WHERE status = ? ORDER BY created_at DESC`,
			status,
		)
	}
	if err != nil {
		return nil, fmt.Errorf("failed to query pending questions: %w", err)
	}
	defer rows.Close()

	var questions []PendingQuestion
	for rows.Next() {
		var q PendingQuestion
		var answer sql.NullString
		var createdAt sql.NullTime
		if err := rows.Scan(&q.ID, &q.Question, &q.UserID, &q.Status, &answer, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan pending question row: %w", err)
		}
		if answer.Valid {
			q.Answer = answer.String
		}
		if createdAt.Valid {
			q.CreatedAt = createdAt.Time
		}
		questions = append(questions, q)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pending question rows: %w", err)
	}
	return questions, nil
}

// AnswerQuestion processes an admin's answer to a pending question:
// 1. Retrieves the question from DB
// 2. Stores the answer text in the pending_questions record
// 3. Chunks the answer text → embeds → stores in vector store (knowledge base)
// 4. Calls LLM to generate a summary answer based on the admin's answer
// 5. Updates the record with llm_answer, status="answered", answered_at=now
func (pm *PendingQuestionManager) AnswerQuestion(req AdminAnswerRequest) error {
	// Step 1: Get the question from DB
	var question string
	var status string
	err := pm.db.QueryRow(
		`SELECT question, status FROM pending_questions WHERE id = ?`, req.QuestionID,
	).Scan(&question, &status)
	if err == sql.ErrNoRows {
		return fmt.Errorf("pending question not found: %s", req.QuestionID)
	}
	if err != nil {
		return fmt.Errorf("failed to query pending question: %w", err)
	}
	if status == "answered" {
		return fmt.Errorf("question already answered: %s", req.QuestionID)
	}

	// Step 2: Store the answer text in the record
	answerText := req.Text
	_, err = pm.db.Exec(
		`UPDATE pending_questions SET answer = ? WHERE id = ?`,
		answerText, req.QuestionID,
	)
	if err != nil {
		return fmt.Errorf("failed to update answer text: %w", err)
	}

	// Step 3: Chunk the answer → embed → store in vector store
	if answerText != "" {
		docID := "pending-answer-" + req.QuestionID
		docName := "管理员回答: " + truncate(question, 50)

		chunks := pm.chunker.Split(answerText, docID)
		if len(chunks) > 0 {
			texts := make([]string, len(chunks))
			for i, c := range chunks {
				texts[i] = c.Text
			}

			embeddings, err := pm.embeddingService.EmbedBatch(texts)
			if err != nil {
				return fmt.Errorf("failed to embed answer chunks: %w", err)
			}

			// Insert a document record so the chunks FK constraint is satisfied
			_, err = pm.db.Exec(
				`INSERT INTO documents (id, name, type, status, created_at) VALUES (?, ?, ?, ?, ?)`,
				docID, docName, "answer", "success", time.Now().UTC(),
			)
			if err != nil {
				return fmt.Errorf("failed to insert document record for answer: %w", err)
			}

			vectorChunks := make([]vectorstore.VectorChunk, len(chunks))
			for i, c := range chunks {
				vectorChunks[i] = vectorstore.VectorChunk{
					ChunkText:    c.Text,
					ChunkIndex:   c.Index,
					DocumentID:   docID,
					DocumentName: docName,
					Vector:       embeddings[i],
				}
			}

			if err := pm.vectorStore.Store(docID, vectorChunks); err != nil {
				return fmt.Errorf("failed to store answer in vector store: %w", err)
			}
		}
	}

	// Step 4: Call LLM to generate a summary answer
	llmAnswer, err := pm.llmService.Generate(
		"请根据管理员提供的回答内容，生成一个简洁、清晰的总结性回答。",
		[]string{answerText},
		question,
	)
	if err != nil {
		return fmt.Errorf("failed to generate LLM answer: %w", err)
	}

	// Step 5: Update record with llm_answer, status="answered", answered_at=now
	now := time.Now().UTC()
	_, err = pm.db.Exec(
		`UPDATE pending_questions SET llm_answer = ?, status = ?, answered_at = ? WHERE id = ?`,
		llmAnswer, "answered", now, req.QuestionID,
	)
	if err != nil {
		return fmt.Errorf("failed to update pending question status: %w", err)
	}

	return nil
}

// truncate shortens a string to maxLen characters, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}
