package middleware

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/quick"
	"time"
)

// Feature: architecture-optimization, Property 2: 安全头完整性
// For any HTTP request, after SecurityHeaders middleware processing,
// the response should contain ALL required security headers.
// Validates: Requirements 2.1
func TestProperty2_SecurityHeaderCompleteness(t *testing.T) {
	requiredHeaders := []string{
		"X-Content-Type-Options",
		"X-Frame-Options",
		"Referrer-Policy",
		"Content-Security-Policy",
		"Permissions-Policy",
		"Cache-Control",
		"Strict-Transport-Security",
		"Cross-Origin-Opener-Policy",
	}

	mw := SecurityHeaders()
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	f := func(path string, usePost bool) bool {
		// Constrain path to valid URL paths
		safePath := "/" + strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '/' || r == '-' || r == '_' {
				return r
			}
			return -1
		}, path)

		method := http.MethodGet
		if usePost {
			method = http.MethodPost
		}

		req := httptest.NewRequest(method, safePath, nil)
		rec := httptest.NewRecorder()
		handler(rec, req)

		for _, h := range requiredHeaders {
			if rec.Header().Get(h) == "" {
				t.Logf("missing required security header: %s (path=%s, method=%s)", h, safePath, method)
				return false
			}
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

// Feature: architecture-optimization, Property 3: CORS 同源策略
// For any HTTP request with an Origin header, the CORS middleware should only
// set Access-Control-Allow-Origin when Origin matches the request Host.
// For OPTIONS method requests, should return 204 status code.
// Validates: Requirements 2.2
func TestProperty3_CORSSameOriginPolicy(t *testing.T) {
	mw := CORS()
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	f := func(host string, matchOrigin bool, useOptions bool) bool {
		// Constrain host to valid hostname-like strings
		safeHost := strings.Map(func(r rune) rune {
			if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '.' || r == '-' {
				return r
			}
			return -1
		}, strings.ToLower(host))
		if safeHost == "" {
			safeHost = "example.com"
		}

		method := http.MethodGet
		if useOptions {
			method = http.MethodOptions
		}

		req := httptest.NewRequest(method, "/", nil)
		req.Host = safeHost

		var origin string
		if matchOrigin {
			origin = "http://" + safeHost
		} else {
			origin = "http://evil-" + safeHost + ".attacker.com"
		}
		req.Header.Set("Origin", origin)

		rec := httptest.NewRecorder()
		handler(rec, req)

		acao := rec.Header().Get("Access-Control-Allow-Origin")

		// If origin matches host, ACAO should be set
		if matchOrigin && acao != origin {
			t.Logf("matching origin %q should set ACAO, got %q", origin, acao)
			return false
		}

		// If origin does NOT match host, ACAO should NOT be set
		if !matchOrigin && acao != "" {
			t.Logf("non-matching origin %q should not set ACAO, got %q", origin, acao)
			return false
		}

		// OPTIONS requests should always return 204
		if useOptions && rec.Code != http.StatusNoContent {
			t.Logf("OPTIONS request should return 204, got %d", rec.Code)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

// Feature: architecture-optimization, Property 4: 请求 ID 唯一性
// For any HTTP request, after RequestID middleware processing, the response
// should contain a non-empty X-Request-Id header, and consecutive requests
// should produce different IDs.
// Validates: Requirements 2.3
func TestProperty4_RequestIDUniqueness(t *testing.T) {
	mw := RequestID()
	handler := mw(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	})

	f := func(n uint8) bool {
		// Generate at least 2 requests, up to 257
		count := int(n) + 2
		ids := make(map[string]bool, count)

		for i := 0; i < count; i++ {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			rec := httptest.NewRecorder()
			handler(rec, req)

			id := rec.Header().Get("X-Request-Id")
			if id == "" {
				t.Logf("request %d: X-Request-Id is empty", i)
				return false
			}
			if ids[id] {
				t.Logf("duplicate request ID found: %s (on request %d)", id, i)
				return false
			}
			ids[id] = true
		}
		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

// Feature: architecture-optimization, Property 5: 限流器正确拒绝
// For any IP address and rate limit configuration (limit N, window W),
// after sending N+1 requests within window W for the same IP,
// the (N+1)th request should be rejected (429), while the first N should be allowed.
// Validates: Requirements 2.4
func TestProperty5_RateLimiterCorrectRejection(t *testing.T) {
	f := func(seed uint8) bool {
		// Constrain limit to 1..20 to keep tests fast
		limit := int(seed%20) + 1
		ip := fmt.Sprintf("10.0.%d.%d", seed/16, seed%16)

		// Use a large window so requests don't expire during the test
		rl := &RateLimiter{
			requests: make(map[string][]time.Time),
			limit:    limit,
			window:   1 * time.Minute,
		}

		// First N requests should all be allowed
		for i := 0; i < limit; i++ {
			if !rl.Allow(ip) {
				t.Logf("request %d of %d should be allowed for ip=%s", i+1, limit, ip)
				return false
			}
		}

		// The (N+1)th request should be rejected
		if rl.Allow(ip) {
			t.Logf("request %d should be rejected (limit=%d) for ip=%s", limit+1, limit, ip)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

// Feature: architecture-optimization, Property 5: 限流器正确拒绝 (HTTP middleware)
// Validates the Limit() middleware returns 429 when rate limit is exceeded.
// Validates: Requirements 2.4
func TestProperty5_RateLimiterMiddleware429(t *testing.T) {
	f := func(seed uint8) bool {
		limit := int(seed%10) + 1
		ip := fmt.Sprintf("192.168.%d.%d", seed/16, seed%16)

		rl := &RateLimiter{
			requests: make(map[string][]time.Time),
			limit:    limit,
			window:   1 * time.Minute,
		}

		handler := rl.Limit()(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})

		// Send limit requests — all should succeed
		for i := 0; i < limit; i++ {
			req := httptest.NewRequest(http.MethodGet, "/", nil)
			req.RemoteAddr = ip + ":12345"
			rec := httptest.NewRecorder()
			handler(rec, req)
			if rec.Code != http.StatusOK {
				t.Logf("request %d: expected 200, got %d", i+1, rec.Code)
				return false
			}
		}

		// The next request should be 429
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = ip + ":12345"
		rec := httptest.NewRecorder()
		handler(rec, req)
		if rec.Code != http.StatusTooManyRequests {
			t.Logf("request %d: expected 429, got %d", limit+1, rec.Code)
			return false
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}

// Feature: architecture-optimization, Property 6: 中间件链执行顺序
// For any list of middlewares [m1, m2, ..., mn], Chain(m1, m2, ..., mn)
// should execute in onion model order:
// m1 → m2 → ... → mn → handler → mn → ... → m2 → m1.
// Validates: Requirements 2.5
func TestProperty6_MiddlewareChainExecutionOrder(t *testing.T) {
	f := func(n uint8) bool {
		// Use 1..10 middlewares
		count := int(n%10) + 1
		var order []string

		// Create middlewares that record pre/post execution
		middlewares := make([]Middleware, count)
		for i := 0; i < count; i++ {
			idx := i // capture
			middlewares[idx] = func(next http.HandlerFunc) http.HandlerFunc {
				return func(w http.ResponseWriter, r *http.Request) {
					order = append(order, fmt.Sprintf("pre-%d", idx))
					next(w, r)
					order = append(order, fmt.Sprintf("post-%d", idx))
				}
			}
		}

		chained := Chain(middlewares...)(func(w http.ResponseWriter, r *http.Request) {
			order = append(order, "handler")
		})

		order = nil // reset
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		rec := httptest.NewRecorder()
		chained(rec, req)

		// Expected order: pre-0, pre-1, ..., pre-(n-1), handler, post-(n-1), ..., post-1, post-0
		expectedLen := 2*count + 1
		if len(order) != expectedLen {
			t.Logf("expected %d entries, got %d: %v", expectedLen, len(order), order)
			return false
		}

		// Verify pre-order: 0, 1, ..., n-1
		for i := 0; i < count; i++ {
			expected := fmt.Sprintf("pre-%d", i)
			if order[i] != expected {
				t.Logf("position %d: expected %q, got %q", i, expected, order[i])
				return false
			}
		}

		// Verify handler in the middle
		if order[count] != "handler" {
			t.Logf("position %d: expected 'handler', got %q", count, order[count])
			return false
		}

		// Verify post-order: n-1, n-2, ..., 0
		for i := 0; i < count; i++ {
			expected := fmt.Sprintf("post-%d", count-1-i)
			if order[count+1+i] != expected {
				t.Logf("position %d: expected %q, got %q", count+1+i, expected, order[count+1+i])
				return false
			}
		}

		return true
	}

	if err := quick.Check(f, &quick.Config{MaxCount: 100}); err != nil {
		t.Error(err)
	}
}
