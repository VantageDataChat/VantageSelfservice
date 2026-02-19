package handler

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"testing/quick"
)

// Feature: architecture-optimization, Property 1: JSON 辅助函数 round-trip
// For any JSON-serializable Go value, calling WriteJSON to write to a ResponseRecorder,
// then JSON-decoding from the response body should recover an equivalent value.
// The response Content-Type should be "application/json".
// Validates: Requirements 1.4

func TestProperty1_WriteJSON_RoundTrip_String(t *testing.T) {
	f := func(s string) bool {
		rec := httptest.NewRecorder()
		WriteJSON(rec, http.StatusOK, s)

		// Content-Type must be application/json
		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Logf("expected Content-Type application/json, got %q", ct)
			return false
		}

		// Status code must match
		if rec.Code != http.StatusOK {
			t.Logf("expected status 200, got %d", rec.Code)
			return false
		}

		// Round-trip: decode and compare
		var decoded string
		if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
			t.Logf("decode error: %v", err)
			return false
		}
		if decoded != s {
			t.Logf("round-trip mismatch: input=%q decoded=%q", s, decoded)
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

func TestProperty1_WriteJSON_RoundTrip_Int(t *testing.T) {
	f := func(n int) bool {
		rec := httptest.NewRecorder()
		WriteJSON(rec, http.StatusOK, n)

		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Logf("expected Content-Type application/json, got %q", ct)
			return false
		}

		// Use json.Number to avoid float64 precision loss for large integers
		dec := json.NewDecoder(rec.Body)
		dec.UseNumber()
		var num json.Number
		if err := dec.Decode(&num); err != nil {
			t.Logf("decode error: %v", err)
			return false
		}
		decoded, err := num.Int64()
		if err != nil {
			t.Logf("Int64 conversion error: %v", err)
			return false
		}
		if int(decoded) != n {
			t.Logf("round-trip mismatch: input=%d decoded=%d", n, decoded)
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

func TestProperty1_WriteJSON_RoundTrip_Map(t *testing.T) {
	f := func(key, value string) bool {
		input := map[string]string{key: value}
		rec := httptest.NewRecorder()
		WriteJSON(rec, http.StatusOK, input)

		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Logf("expected Content-Type application/json, got %q", ct)
			return false
		}

		var decoded map[string]string
		if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
			t.Logf("decode error: %v", err)
			return false
		}
		if decoded[key] != value {
			t.Logf("round-trip mismatch: input[%q]=%q decoded[%q]=%q", key, value, key, decoded[key])
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

// testPayload is a struct used to verify struct round-trip through WriteJSON.
type testPayload struct {
	Name  string `json:"name"`
	Count int    `json:"count"`
	Active bool  `json:"active"`
}

func TestProperty1_WriteJSON_RoundTrip_Struct(t *testing.T) {
	f := func(name string, count int, active bool) bool {
		input := testPayload{Name: name, Count: count, Active: active}
		rec := httptest.NewRecorder()
		WriteJSON(rec, http.StatusOK, input)

		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Logf("expected Content-Type application/json, got %q", ct)
			return false
		}

		var decoded testPayload
		if err := json.NewDecoder(rec.Body).Decode(&decoded); err != nil {
			t.Logf("decode error: %v", err)
			return false
		}
		if decoded != input {
			t.Logf("round-trip mismatch: input=%+v decoded=%+v", input, decoded)
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

func TestProperty1_WriteJSON_StatusCode(t *testing.T) {
	// Verify that WriteJSON correctly sets arbitrary status codes.
	f := func(code uint8) bool {
		// Constrain to valid HTTP status codes (200-599)
		status := int(code)%400 + 200
		rec := httptest.NewRecorder()
		WriteJSON(rec, status, "ok")

		if rec.Code != status {
			t.Logf("expected status %d, got %d", status, rec.Code)
			return false
		}

		if ct := rec.Header().Get("Content-Type"); ct != "application/json" {
			t.Logf("expected Content-Type application/json, got %q", ct)
			return false
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}
