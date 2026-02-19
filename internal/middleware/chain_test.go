package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

// TestChainEmpty verifies that Chain with no middlewares passes through to the handler directly.
func TestChainEmpty(t *testing.T) {
	called := false
	handler := func(w http.ResponseWriter, r *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
	}

	chained := Chain()(handler)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	chained(rec, req)

	if !called {
		t.Fatal("handler was not called with empty chain")
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("expected status 200, got %d", rec.Code)
	}
}

// TestChainSingleMiddleware verifies a single middleware wraps the handler correctly.
func TestChainSingleMiddleware(t *testing.T) {
	var order []string

	m1 := func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "m1-before")
			next(w, r)
			order = append(order, "m1-after")
		}
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}

	chained := Chain(m1)(handler)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	chained(rec, req)

	expected := []string{"m1-before", "handler", "m1-after"}
	if len(order) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, order)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Fatalf("at index %d: expected %q, got %q", i, v, order[i])
		}
	}
}

// TestChainOnionOrder verifies the onion model execution order with multiple middlewares.
// Chain(m1, m2, m3) should execute: m1-before → m2-before → m3-before → handler → m3-after → m2-after → m1-after
func TestChainOnionOrder(t *testing.T) {
	var order []string

	makeMiddleware := func(name string) Middleware {
		return func(next http.HandlerFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				order = append(order, name+"-before")
				next(w, r)
				order = append(order, name+"-after")
			}
		}
	}

	handler := func(w http.ResponseWriter, r *http.Request) {
		order = append(order, "handler")
	}

	chained := Chain(makeMiddleware("m1"), makeMiddleware("m2"), makeMiddleware("m3"))(handler)
	rec := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	chained(rec, req)

	expected := []string{
		"m1-before", "m2-before", "m3-before",
		"handler",
		"m3-after", "m2-after", "m1-after",
	}
	if len(order) != len(expected) {
		t.Fatalf("expected %v, got %v", expected, order)
	}
	for i, v := range expected {
		if order[i] != v {
			t.Fatalf("at index %d: expected %q, got %q", i, v, order[i])
		}
	}
}
