package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestRequestIDSetsHeader verifies that the RequestID middleware sets a non-empty X-Request-Id header.
func TestRequestIDSetsHeader(t *testing.T) {
	mw := RequestID()
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler(rr, req)

	reqID := rr.Header().Get("X-Request-Id")
	if reqID == "" {
		t.Fatal("expected X-Request-Id header to be set, got empty")
	}
	// 8 bytes = 16 hex characters
	if len(reqID) != 16 {
		t.Fatalf("expected X-Request-Id to be 16 hex chars, got %d chars: %q", len(reqID), reqID)
	}
}

// TestRequestIDUniqueness verifies that consecutive requests produce different IDs.
func TestRequestIDUniqueness(t *testing.T) {
	mw := RequestID()
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	seen := make(map[string]bool)
	for i := 0; i < 100; i++ {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		handler(rr, req)

		id := rr.Header().Get("X-Request-Id")
		if seen[id] {
			t.Fatalf("duplicate request ID on iteration %d: %q", i, id)
		}
		seen[id] = true
	}
}

// TestRequestIDCallsNext verifies that the middleware calls the next handler.
func TestRequestIDCallsNext(t *testing.T) {
	called := false
	mw := RequestID()
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		called = true
	})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	handler(rr, req)

	if !called {
		t.Fatal("expected next handler to be called")
	}
}
