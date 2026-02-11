package embedding

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// capturedRequest stores details from an incoming HTTP request for verification.
type capturedRequest struct {
	Method      string
	Path        string
	ContentType string
	AuthHeader  string
	Body        embeddingRequest
}

// newTestServer creates an httptest server that captures the request and returns the given response.
func newTestServer(t *testing.T, statusCode int, response interface{}, captured *capturedRequest) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if captured != nil {
			captured.Method = r.Method
			captured.Path = r.URL.Path
			captured.ContentType = r.Header.Get("Content-Type")
			captured.AuthHeader = r.Header.Get("Authorization")
			body, _ := io.ReadAll(r.Body)
			json.Unmarshal(body, &captured.Body)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(statusCode)
		json.NewEncoder(w).Encode(response)
	}))
}

func TestEmbed_RequestConstruction(t *testing.T) {
	var captured capturedRequest
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{{Embedding: []float64{0.1, 0.2}, Index: 0}},
	}, &captured)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "test-api-key", "test-model")
	_, err := svc.Embed("hello world")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", captured.Method)
	}
	if captured.Path != "/embeddings" {
		t.Errorf("expected path /embeddings, got %s", captured.Path)
	}
	if captured.ContentType != "application/json" {
		t.Errorf("expected Content-Type application/json, got %s", captured.ContentType)
	}
	if captured.AuthHeader != "Bearer test-api-key" {
		t.Errorf("expected Authorization 'Bearer test-api-key', got %s", captured.AuthHeader)
	}
	if captured.Body.Model != "test-model" {
		t.Errorf("expected model 'test-model', got %s", captured.Body.Model)
	}
}

func TestEmbed_ResponseParsing(t *testing.T) {
	expected := []float64{0.1, 0.2, 0.3, 0.4, 0.5}
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{{Embedding: expected, Index: 0}},
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	result, err := svc.Embed("test text")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(result) != len(expected) {
		t.Fatalf("expected %d dimensions, got %d", len(expected), len(result))
	}
	for i, v := range expected {
		if result[i] != v {
			t.Errorf("dimension %d: expected %f, got %f", i, v, result[i])
		}
	}
}

func TestEmbed_EmptyResponse(t *testing.T) {
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{},
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	_, err := svc.Embed("test")
	if err == nil {
		t.Fatal("expected error for empty response, got nil")
	}
}

func TestEmbed_APIError(t *testing.T) {
	server := newTestServer(t, http.StatusBadRequest, embeddingResponse{
		Error: &apiError{Message: "invalid input", Type: "invalid_request_error"},
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	_, err := svc.Embed("test")
	if err == nil {
		t.Fatal("expected error for API error response, got nil")
	}
}

func TestEmbed_ServerError(t *testing.T) {
	server := newTestServer(t, http.StatusInternalServerError, map[string]string{
		"error": "internal server error",
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	_, err := svc.Embed("test")
	if err == nil {
		t.Fatal("expected error for 500 response, got nil")
	}
}

func TestEmbed_NoAPIKey(t *testing.T) {
	var captured capturedRequest
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{{Embedding: []float64{0.1}, Index: 0}},
	}, &captured)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "", "model")
	_, err := svc.Embed("test")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if captured.AuthHeader != "" {
		t.Errorf("expected no Authorization header, got %s", captured.AuthHeader)
	}
}

func TestEmbedBatch_RequestConstruction(t *testing.T) {
	var captured capturedRequest
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{
			{Embedding: []float64{0.1, 0.2}, Index: 0},
			{Embedding: []float64{0.3, 0.4}, Index: 1},
		},
	}, &captured)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "batch-key", "batch-model")
	_, err := svc.EmbedBatch([]string{"text1", "text2"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if captured.Method != http.MethodPost {
		t.Errorf("expected POST, got %s", captured.Method)
	}
	if captured.Path != "/embeddings" {
		t.Errorf("expected path /embeddings, got %s", captured.Path)
	}
	if captured.AuthHeader != "Bearer batch-key" {
		t.Errorf("expected Authorization 'Bearer batch-key', got %s", captured.AuthHeader)
	}
	if captured.Body.Model != "batch-model" {
		t.Errorf("expected model 'batch-model', got %s", captured.Body.Model)
	}
}

func TestEmbedBatch_ResponseParsing(t *testing.T) {
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{
			{Embedding: []float64{0.1, 0.2}, Index: 0},
			{Embedding: []float64{0.3, 0.4}, Index: 1},
			{Embedding: []float64{0.5, 0.6}, Index: 2},
		},
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	results, err := svc.EmbedBatch([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(results) != 3 {
		t.Fatalf("expected 3 results, got %d", len(results))
	}
	// Verify ordering by index
	if results[0][0] != 0.1 || results[1][0] != 0.3 || results[2][0] != 0.5 {
		t.Error("results not ordered correctly by index")
	}
}

func TestEmbedBatch_OutOfOrderIndices(t *testing.T) {
	// API may return results in any order; EmbedBatch should reorder by index.
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{
			{Embedding: []float64{0.5, 0.6}, Index: 2},
			{Embedding: []float64{0.1, 0.2}, Index: 0},
			{Embedding: []float64{0.3, 0.4}, Index: 1},
		},
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	results, err := svc.EmbedBatch([]string{"a", "b", "c"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results[0][0] != 0.1 {
		t.Errorf("index 0: expected 0.1, got %f", results[0][0])
	}
	if results[1][0] != 0.3 {
		t.Errorf("index 1: expected 0.3, got %f", results[1][0])
	}
	if results[2][0] != 0.5 {
		t.Errorf("index 2: expected 0.5, got %f", results[2][0])
	}
}

func TestEmbedBatch_EmptyInput(t *testing.T) {
	svc := NewAPIEmbeddingService("http://unused", "key", "model")
	results, err := svc.EmbedBatch([]string{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if results != nil {
		t.Errorf("expected nil for empty input, got %v", results)
	}
}

func TestEmbedBatch_CountMismatch(t *testing.T) {
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{
			{Embedding: []float64{0.1}, Index: 0},
		},
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	_, err := svc.EmbedBatch([]string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for count mismatch, got nil")
	}
}

func TestEmbedBatch_InvalidIndex(t *testing.T) {
	server := newTestServer(t, http.StatusOK, embeddingResponse{
		Data: []embeddingData{
			{Embedding: []float64{0.1}, Index: 0},
			{Embedding: []float64{0.2}, Index: 5}, // out of range
		},
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	_, err := svc.EmbedBatch([]string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for invalid index, got nil")
	}
}

func TestEmbedBatch_APIError(t *testing.T) {
	server := newTestServer(t, http.StatusUnauthorized, embeddingResponse{
		Error: &apiError{Message: "invalid api key", Type: "authentication_error"},
	}, nil)
	defer server.Close()

	svc := NewAPIEmbeddingService(server.URL, "bad-key", "model")
	_, err := svc.EmbedBatch([]string{"test"})
	if err == nil {
		t.Fatal("expected error for API error response, got nil")
	}
}

func TestEmbed_ConnectionError(t *testing.T) {
	// Use a server that's already closed to simulate connection failure.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	server.Close()

	svc := NewAPIEmbeddingService(server.URL, "key", "model")
	_, err := svc.Embed("test")
	if err == nil {
		t.Fatal("expected error for connection failure, got nil")
	}
}
