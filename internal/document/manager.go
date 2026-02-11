// Package document provides the Document Manager for handling document upload,
// processing, deletion, and listing in the helpdesk system.
package document

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"helpdesk/internal/chunker"
	"helpdesk/internal/embedding"
	"helpdesk/internal/parser"
	"helpdesk/internal/vectorstore"
)

// supportedFileTypes lists the file types accepted for upload.
var supportedFileTypes = map[string]bool{
	"pdf":   true,
	"word":  true,
	"excel": true,
	"ppt":   true,
}

// DocumentManager orchestrates document upload, processing, and lifecycle management.
type DocumentManager struct {
	parser           *parser.DocumentParser
	chunker          *chunker.TextChunker
	embeddingService embedding.EmbeddingService
	vectorStore      vectorstore.VectorStore
	db               *sql.DB
	httpClient       *http.Client
}

// DocumentInfo holds metadata about a document stored in the system.
type DocumentInfo struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	Type      string    `json:"type"`
	Status    string    `json:"status"` // "processing", "success", "failed"
	Error     string    `json:"error,omitempty"`
	CreatedAt time.Time `json:"created_at"`
}

// UploadFileRequest represents a file upload request.
type UploadFileRequest struct {
	FileName string `json:"file_name"`
	FileData []byte `json:"file_data"`
	FileType string `json:"file_type"`
}

// UploadURLRequest represents a URL upload request.
type UploadURLRequest struct {
	URL string `json:"url"`
}

// NewDocumentManager creates a new DocumentManager with the given dependencies.
func NewDocumentManager(
	p *parser.DocumentParser,
	c *chunker.TextChunker,
	es embedding.EmbeddingService,
	vs vectorstore.VectorStore,
	db *sql.DB,
) *DocumentManager {
	return &DocumentManager{
		parser:           p,
		chunker:          c,
		embeddingService: es,
		vectorStore:      vs,
		db:               db,
		httpClient: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// generateID creates a random UUID-like hex string.
func generateID() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("failed to generate ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// UploadFile validates the file type, parses the file, chunks the text,
// generates embeddings, and stores everything. The document record tracks
// processing status in the documents table.
func (dm *DocumentManager) UploadFile(req UploadFileRequest) (*DocumentInfo, error) {
	fileType := strings.ToLower(req.FileType)
	if !supportedFileTypes[fileType] {
		return nil, fmt.Errorf("不支持的文件格式")
	}

	docID, err := generateID()
	if err != nil {
		return nil, err
	}

	doc := &DocumentInfo{
		ID:        docID,
		Name:      req.FileName,
		Type:      fileType,
		Status:    "processing",
		CreatedAt: time.Now(),
	}

	if err := dm.insertDocument(doc); err != nil {
		return nil, fmt.Errorf("failed to insert document record: %w", err)
	}

	// Parse → Chunk → Embed → Store
	if err := dm.processFile(docID, req.FileName, req.FileData, fileType); err != nil {
		dm.updateDocumentStatus(docID, "failed", err.Error())
		doc.Status = "failed"
		doc.Error = err.Error()
		return doc, nil
	}

	dm.updateDocumentStatus(docID, "success", "")
	doc.Status = "success"
	return doc, nil
}

// UploadURL fetches the content at the given URL, chunks it, generates embeddings,
// and stores everything. The document type is recorded as "url".
func (dm *DocumentManager) UploadURL(req UploadURLRequest) (*DocumentInfo, error) {
	if req.URL == "" {
		return nil, fmt.Errorf("URL不能为空")
	}

	docID, err := generateID()
	if err != nil {
		return nil, err
	}

	doc := &DocumentInfo{
		ID:        docID,
		Name:      req.URL,
		Type:      "url",
		Status:    "processing",
		CreatedAt: time.Now(),
	}

	if err := dm.insertDocument(doc); err != nil {
		return nil, fmt.Errorf("failed to insert document record: %w", err)
	}

	// Fetch → Chunk → Embed → Store
	if err := dm.processURL(docID, req.URL); err != nil {
		dm.updateDocumentStatus(docID, "failed", err.Error())
		doc.Status = "failed"
		doc.Error = err.Error()
		return doc, nil
	}

	dm.updateDocumentStatus(docID, "success", "")
	doc.Status = "success"
	return doc, nil
}

// DeleteDocument removes a document's vectors from the vector store and its
// record from the documents table.
func (dm *DocumentManager) DeleteDocument(docID string) error {
	if err := dm.vectorStore.DeleteByDocID(docID); err != nil {
		return fmt.Errorf("failed to delete vectors: %w", err)
	}
	_, err := dm.db.Exec(`DELETE FROM documents WHERE id = ?`, docID)
	if err != nil {
		return fmt.Errorf("failed to delete document record: %w", err)
	}
	return nil
}

// ListDocuments returns all documents ordered by creation time descending.
func (dm *DocumentManager) ListDocuments() ([]DocumentInfo, error) {
	rows, err := dm.db.Query(`SELECT id, name, type, status, error, created_at FROM documents ORDER BY created_at DESC`)
	if err != nil {
		return nil, fmt.Errorf("failed to query documents: %w", err)
	}
	defer rows.Close()

	var docs []DocumentInfo
	for rows.Next() {
		var d DocumentInfo
		var errStr sql.NullString
		var createdAt sql.NullTime
		if err := rows.Scan(&d.ID, &d.Name, &d.Type, &d.Status, &errStr, &createdAt); err != nil {
			return nil, fmt.Errorf("failed to scan document row: %w", err)
		}
		if errStr.Valid {
			d.Error = errStr.String
		}
		if createdAt.Valid {
			d.CreatedAt = createdAt.Time
		}
		docs = append(docs, d)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating document rows: %w", err)
	}
	return docs, nil
}

// processFile parses a file, chunks the text, embeds, and stores vectors.
func (dm *DocumentManager) processFile(docID, docName string, fileData []byte, fileType string) error {
	result, err := dm.parser.Parse(fileData, fileType)
	if err != nil {
		return fmt.Errorf("parse error: %w", err)
	}
	if result.Text == "" {
		return fmt.Errorf("文档内容为空")
	}
	return dm.chunkEmbedStore(docID, docName, result.Text)
}

// processURL fetches URL content and processes it as plain text.
func (dm *DocumentManager) processURL(docID, url string) error {
	resp, err := dm.httpClient.Get(url)
	if err != nil {
		return fmt.Errorf("failed to fetch URL: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("URL returned HTTP %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("failed to read URL content: %w", err)
	}

	text := strings.TrimSpace(string(body))
	if text == "" {
		return fmt.Errorf("URL内容为空")
	}
	return dm.chunkEmbedStore(docID, url, text)
}

// chunkEmbedStore splits text into chunks, embeds them in batch, and stores vectors.
func (dm *DocumentManager) chunkEmbedStore(docID, docName, text string) error {
	chunks := dm.chunker.Split(text, docID)
	if len(chunks) == 0 {
		return fmt.Errorf("分块结果为空")
	}

	// Collect chunk texts for batch embedding
	texts := make([]string, len(chunks))
	for i, c := range chunks {
		texts[i] = c.Text
	}

	embeddings, err := dm.embeddingService.EmbedBatch(texts)
	if err != nil {
		return fmt.Errorf("embedding error: %w", err)
	}

	// Build VectorChunks for storage
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

	if err := dm.vectorStore.Store(docID, vectorChunks); err != nil {
		return fmt.Errorf("vector store error: %w", err)
	}
	return nil
}

// insertDocument inserts a new document record into the documents table.
func (dm *DocumentManager) insertDocument(doc *DocumentInfo) error {
	_, err := dm.db.Exec(
		`INSERT INTO documents (id, name, type, status, error, created_at) VALUES (?, ?, ?, ?, ?, ?)`,
		doc.ID, doc.Name, doc.Type, doc.Status, doc.Error, doc.CreatedAt,
	)
	return err
}

// updateDocumentStatus updates the status and error fields of a document.
func (dm *DocumentManager) updateDocumentStatus(docID, status, errMsg string) {
	dm.db.Exec(`UPDATE documents SET status = ?, error = ? WHERE id = ?`, status, errMsg, docID)
}
