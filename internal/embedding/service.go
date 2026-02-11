// Package embedding provides the Embedding service client for converting text
// to vector representations via OpenAI-compatible API endpoints.
package embedding

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

// EmbeddingService defines the interface for text embedding operations.
type EmbeddingService interface {
	Embed(text string) ([]float64, error)
	EmbedBatch(texts []string) ([][]float64, error)
}

// APIEmbeddingService implements EmbeddingService using an OpenAI-compatible API.
type APIEmbeddingService struct {
	Endpoint  string
	APIKey    string
	ModelName string
	client    *http.Client
}

// NewAPIEmbeddingService creates a new APIEmbeddingService with the given configuration.
func NewAPIEmbeddingService(endpoint, apiKey, modelName string) *APIEmbeddingService {
	return &APIEmbeddingService{
		Endpoint:  endpoint,
		APIKey:    apiKey,
		ModelName: modelName,
		client: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

// embeddingRequest is the request body for the OpenAI-compatible embedding API.
type embeddingRequest struct {
	Model string      `json:"model"`
	Input interface{} `json:"input"`
}

// embeddingResponse is the response body from the OpenAI-compatible embedding API.
type embeddingResponse struct {
	Data  []embeddingData `json:"data"`
	Error *apiError       `json:"error,omitempty"`
}

// embeddingData represents a single embedding result.
type embeddingData struct {
	Embedding []float64 `json:"embedding"`
	Index     int       `json:"index"`
}

// apiError represents an error returned by the API.
type apiError struct {
	Message string `json:"message"`
	Type    string `json:"type"`
}

// Embed converts a single text string into an embedding vector.
func (s *APIEmbeddingService) Embed(text string) ([]float64, error) {
	results, err := s.callAPI(text)
	if err != nil {
		return nil, err
	}
	if len(results) == 0 {
		return nil, fmt.Errorf("embedding API returned no results")
	}
	return results[0].Embedding, nil
}

// EmbedBatch converts multiple text strings into embedding vectors.
func (s *APIEmbeddingService) EmbedBatch(texts []string) ([][]float64, error) {
	if len(texts) == 0 {
		return nil, nil
	}
	results, err := s.callAPI(texts)
	if err != nil {
		return nil, err
	}
	if len(results) != len(texts) {
		return nil, fmt.Errorf("embedding API returned %d results, expected %d", len(results), len(texts))
	}

	// Sort results by index to ensure correct ordering.
	embeddings := make([][]float64, len(texts))
	for _, d := range results {
		if d.Index < 0 || d.Index >= len(texts) {
			return nil, fmt.Errorf("embedding API returned invalid index %d", d.Index)
		}
		embeddings[d.Index] = d.Embedding
	}
	return embeddings, nil
}

// callAPI sends the embedding request to the API and returns the parsed response data.
func (s *APIEmbeddingService) callAPI(input interface{}) ([]embeddingData, error) {
	reqBody := embeddingRequest{
		Model: s.ModelName,
		Input: input,
	}
	bodyBytes, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	url := s.Endpoint + "/embeddings"
	req, err := http.NewRequest(http.MethodPost, url, bytes.NewReader(bodyBytes))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if s.APIKey != "" {
		req.Header.Set("Authorization", "Bearer "+s.APIKey)
	}

	resp, err := s.client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("embedding API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		var errResp embeddingResponse
		if json.Unmarshal(respBody, &errResp) == nil && errResp.Error != nil {
			return nil, fmt.Errorf("embedding API error (HTTP %d): %s", resp.StatusCode, errResp.Error.Message)
		}
		return nil, fmt.Errorf("embedding API error (HTTP %d): %s", resp.StatusCode, string(respBody))
	}

	var result embeddingResponse
	if err := json.Unmarshal(respBody, &result); err != nil {
		return nil, fmt.Errorf("failed to decode response: %w", err)
	}
	if result.Error != nil {
		return nil, fmt.Errorf("embedding API error: %s", result.Error.Message)
	}

	return result.Data, nil
}
