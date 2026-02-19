package middleware

import (
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

// RateLimiter provides per-IP rate limiting using a sliding window counter.
type RateLimiter struct {
	mu       sync.Mutex
	requests map[string][]time.Time
	limit    int           // max requests per window
	window   time.Duration // time window
	stopCh   chan struct{} // signal to stop the cleanup goroutine
}

// NewRateLimiter creates a RateLimiter instance and starts a background
// goroutine that cleans up stale entries every 5 minutes.
func NewRateLimiter(limit int, window time.Duration) *RateLimiter {
	rl := &RateLimiter{
		requests: make(map[string][]time.Time),
		limit:    limit,
		window:   window,
		stopCh:   make(chan struct{}),
	}
	// Background cleanup of stale entries every 5 minutes
	go func() {
		defer func() {
			if r := recover(); r != nil {
				log.Printf("[RateLimiter] panic in cleanup goroutine: %v", r)
			}
		}()
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				rl.cleanup()
			case <-rl.stopCh:
				return
			}
		}
	}()
	return rl
}

// Stop terminates the background cleanup goroutine.
func (rl *RateLimiter) Stop() {
	select {
	case <-rl.stopCh:
		// already closed
	default:
		close(rl.stopCh)
	}
}

// Allow checks whether the given IP is allowed to make a request
// under the configured rate limit.
func (rl *RateLimiter) Allow(ip string) bool {
	rl.mu.Lock()
	defer rl.mu.Unlock()

	now := time.Now()
	cutoff := now.Add(-rl.window)

	// Prevent memory exhaustion: if too many unique IPs, force cleanup
	if len(rl.requests) > 100000 {
		for k := range rl.requests {
			delete(rl.requests, k)
			if len(rl.requests) <= 50000 {
				break
			}
		}
	}

	// Filter out expired entries
	times := rl.requests[ip]
	valid := times[:0]
	for _, t := range times {
		if t.After(cutoff) {
			valid = append(valid, t)
		}
	}

	if len(valid) >= rl.limit {
		rl.requests[ip] = valid
		return false
	}

	rl.requests[ip] = append(valid, now)
	return true
}

// cleanup removes expired entries from the requests map.
func (rl *RateLimiter) cleanup() {
	rl.mu.Lock()
	defer rl.mu.Unlock()
	cutoff := time.Now().Add(-rl.window)
	for ip, times := range rl.requests {
		valid := times[:0]
		for _, t := range times {
			if t.After(cutoff) {
				valid = append(valid, t)
			}
		}
		if len(valid) == 0 {
			delete(rl.requests, ip)
		} else {
			rl.requests[ip] = valid
		}
	}
}

// GetClientIP extracts the client IP from the request, respecting X-Forwarded-For
// but only using the first (leftmost) IP to avoid spoofing.
func GetClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		// Use only the first IP (client IP set by the first proxy)
		if idx := strings.IndexByte(xff, ','); idx != -1 {
			return strings.TrimSpace(xff[:idx])
		}
		return strings.TrimSpace(xff)
	}
	if xri := r.Header.Get("X-Real-Ip"); xri != "" {
		return strings.TrimSpace(xri)
	}
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// Limit returns a Middleware that enforces the rate limit.
// When the limit is exceeded, it responds with 429 Too Many Requests.
func (rl *RateLimiter) Limit() Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			ip := GetClientIP(r)
			if !rl.Allow(ip) {
				w.Header().Set("Content-Type", "application/json")
				w.Header().Set("Retry-After", "60")
				w.WriteHeader(http.StatusTooManyRequests)
				w.Write([]byte(`{"error":"请求过于频繁，请稍后再试"}`))
				return
			}
			next(w, r)
		}
	}
}
