package auth

import (
	"crypto/rand"
	"database/sql"
	"encoding/hex"
	"fmt"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// DefaultSessionExpiry is the default session duration (24 hours).
const DefaultSessionExpiry = 24 * time.Hour

// sessionCacheSize is the maximum number of sessions to cache in memory.
const sessionCacheSize = 1024

// Session represents a user session stored in the database.
type Session struct {
	ID        string    `json:"id"`
	UserID    string    `json:"user_id"`
	ExpiresAt time.Time `json:"expires_at"`
	CreatedAt time.Time `json:"created_at"`
}

// sessionCacheEntry wraps a cached session with a fetch timestamp for TTL.
type sessionCacheEntry struct {
	session  *Session
	cachedAt time.Time
}

// SessionManager handles session creation, validation, and cleanup.
type SessionManager struct {
	readDB  *sql.DB
	writeDB *sql.DB
	expiry  time.Duration

	// In-memory LRU-like cache for ValidateSession hot path.
	// Key: session ID, Value: sessionCacheEntry.
	cacheMu sync.RWMutex
	cache   map[string]sessionCacheEntry
	// cacheTTL controls how long a cached session is considered fresh.
	cacheTTL time.Duration
}

// NewSessionManager creates a SessionManager with the given database and expiry duration.
// If expiry is zero, DefaultSessionExpiry is used.
func NewSessionManager(readDB, writeDB *sql.DB, expiry time.Duration) *SessionManager {
	if expiry <= 0 {
		expiry = DefaultSessionExpiry
	}
	return &SessionManager{
		readDB:   readDB,
		writeDB:  writeDB,
		expiry:   expiry,
		cache:    make(map[string]sessionCacheEntry, sessionCacheSize),
		cacheTTL: 2 * time.Minute,
	}
}

// CreateSession creates a new session for the given user ID and stores it in the database.
func (sm *SessionManager) CreateSession(userID string) (*Session, error) {
	id, err := generateSessionID()
	if err != nil {
		return nil, err
	}

	now := time.Now().UTC()
	expiresAt := now.Add(sm.expiry)

	_, err = sm.writeDB.Exec(
		"INSERT INTO sessions (id, user_id, expires_at, created_at) VALUES (?, ?, ?, ?)",
		id, userID, expiresAt.Format(time.RFC3339), now.Format(time.RFC3339),
	)
	if err != nil {
		return nil, fmt.Errorf("insert session: %w", err)
	}

	s := &Session{
		ID:        id,
		UserID:    userID,
		ExpiresAt: expiresAt,
		CreatedAt: now,
	}

	// Pre-populate cache for the new session
	sm.cacheSet(id, s)

	return s, nil
}

// ValidateSession checks if a session exists and has not expired.
// Returns the session if valid, or an error if not found or expired.
// Uses an in-memory cache to avoid DB hits on every authenticated request.
func (sm *SessionManager) ValidateSession(sessionID string) (*Session, error) {
	// Check cache first
	if s, ok := sm.cacheGet(sessionID); ok {
		// Re-validate expiry on cached session
		if time.Now().UTC().After(s.ExpiresAt) {
			sm.cacheDelete(sessionID)
			return nil, fmt.Errorf("session expired")
		}
		const maxSessionAge = 7 * 24 * time.Hour
		if time.Now().UTC().Sub(s.CreatedAt) > maxSessionAge {
			sm.cacheDelete(sessionID)
			sm.writeDB.Exec("DELETE FROM sessions WHERE id = ?", sessionID)
			return nil, fmt.Errorf("session expired (max age)")
		}
		// Sliding window: extend session expiry on each successful validation
		remaining := time.Until(s.ExpiresAt)
		if remaining < sm.expiry/2 {
			newExpiry := time.Now().UTC().Add(sm.expiry)
			sm.writeDB.Exec("UPDATE sessions SET expires_at = ? WHERE id = ?",
				newExpiry.Format(time.RFC3339), sessionID)
			s.ExpiresAt = newExpiry
			sm.cacheSet(sessionID, s)
		}
		return s, nil
	}

	// Cache miss: query from read DB
	var s Session
	var expiresAtStr, createdAtStr string

	err := sm.readDB.QueryRow(
		"SELECT id, user_id, expires_at, created_at FROM sessions WHERE id = ?",
		sessionID,
	).Scan(&s.ID, &s.UserID, &expiresAtStr, &createdAtStr)
	if err == sql.ErrNoRows {
		return nil, fmt.Errorf("session not found")
	}
	if err != nil {
		return nil, fmt.Errorf("query session: %w", err)
	}

	expiresAt, err := time.Parse(time.RFC3339, expiresAtStr)
	if err != nil {
		expiresAt, err = time.Parse("2006-01-02T15:04:05Z", expiresAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse expires_at: %w", err)
		}
	}
	s.ExpiresAt = expiresAt

	createdAt, err := time.Parse(time.RFC3339, createdAtStr)
	if err != nil {
		createdAt, err = time.Parse("2006-01-02T15:04:05Z", createdAtStr)
		if err != nil {
			return nil, fmt.Errorf("parse created_at: %w", err)
		}
	}
	s.CreatedAt = createdAt

	if time.Now().UTC().After(s.ExpiresAt) {
		return nil, fmt.Errorf("session expired")
	}

	const maxSessionAge = 7 * 24 * time.Hour
	if time.Now().UTC().Sub(s.CreatedAt) > maxSessionAge {
		sm.writeDB.Exec("DELETE FROM sessions WHERE id = ?", sessionID)
		return nil, fmt.Errorf("session expired (max age)")
	}

	// Sliding window: extend session expiry on each successful validation
	remaining := time.Until(s.ExpiresAt)
	if remaining < sm.expiry/2 {
		newExpiry := time.Now().UTC().Add(sm.expiry)
		sm.writeDB.Exec("UPDATE sessions SET expires_at = ? WHERE id = ?",
			newExpiry.Format(time.RFC3339), sessionID)
		s.ExpiresAt = newExpiry
	}

	// Cache the valid session
	sm.cacheSet(sessionID, &s)

	return &s, nil
}

// CleanExpired removes all expired sessions from the database.
// Returns the number of sessions removed.
func (sm *SessionManager) CleanExpired() (int64, error) {
	result, err := sm.writeDB.Exec(
		"DELETE FROM sessions WHERE expires_at <= ?",
		time.Now().UTC().Format(time.RFC3339),
	)
	if err != nil {
		return 0, fmt.Errorf("delete expired sessions: %w", err)
	}
	// Flush cache on bulk cleanup since we can't know which entries were deleted
	sm.cacheFlush()
	return result.RowsAffected()
}

// DeleteSession removes a specific session by ID.
func (sm *SessionManager) DeleteSession(sessionID string) error {
	sm.cacheDelete(sessionID)
	_, err := sm.writeDB.Exec("DELETE FROM sessions WHERE id = ?", sessionID)
	if err != nil {
		return fmt.Errorf("delete session: %w", err)
	}
	return nil
}

// DeleteSessionsByUserID removes all sessions for a given user ID.
// Used for session rotation on login and user cleanup.
func (sm *SessionManager) DeleteSessionsByUserID(userID string) error {
	_, err := sm.writeDB.Exec("DELETE FROM sessions WHERE user_id = ?", userID)
	if err != nil {
		return fmt.Errorf("delete sessions by user ID: %w", err)
	}
	// Flush cache since we can't efficiently find all sessions for a user
	sm.cacheFlush()
	return nil
}

// VerifyAdminPassword checks if the provided password matches the stored bcrypt hash.
// Returns nil if the password is correct, or an error otherwise.
func VerifyAdminPassword(password, passwordHash string) error {
	if passwordHash == "" {
		return fmt.Errorf("admin password not configured")
	}
	err := bcrypt.CompareHashAndPassword([]byte(passwordHash), []byte(password))
	if err != nil {
		return fmt.Errorf("密码错误")
	}
	return nil
}

// HashPassword generates a bcrypt hash for the given password.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("hash password: %w", err)
	}
	return string(hash), nil
}

// generateSessionID creates a cryptographically random hex string for session IDs.
// Uses 32 bytes (256 bits) of entropy for strong session security.
func generateSessionID() (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate session ID: %w", err)
	}
	return hex.EncodeToString(b), nil
}

// --- Session cache helpers ---

// cacheGet returns a cached session if it exists and hasn't expired the cache TTL.
func (sm *SessionManager) cacheGet(sessionID string) (*Session, bool) {
	sm.cacheMu.RLock()
	entry, ok := sm.cache[sessionID]
	sm.cacheMu.RUnlock()
	if !ok {
		return nil, false
	}
	if time.Since(entry.cachedAt) > sm.cacheTTL {
		// Stale entry — remove it
		sm.cacheDelete(sessionID)
		return nil, false
	}
	// Return a copy to avoid data races on the caller side
	s := *entry.session
	return &s, true
}

// cacheSet stores a session in the cache. If the cache is full, it evicts
// a random entry (simple probabilistic eviction, good enough for a bounded cache).
// Stores a copy of the session to prevent external mutation of cached data.
func (sm *SessionManager) cacheSet(sessionID string, s *Session) {
	// Make a copy so the caller can't mutate the cached entry
	sCopy := *s
	sm.cacheMu.Lock()
	defer sm.cacheMu.Unlock()
	// Simple eviction: if at capacity, delete one random entry
	if len(sm.cache) >= sessionCacheSize {
		for k := range sm.cache {
			delete(sm.cache, k)
			break
		}
	}
	sm.cache[sessionID] = sessionCacheEntry{session: &sCopy, cachedAt: time.Now()}
}

// cacheDelete removes a session from the cache.
func (sm *SessionManager) cacheDelete(sessionID string) {
	sm.cacheMu.Lock()
	delete(sm.cache, sessionID)
	sm.cacheMu.Unlock()
}

// cacheFlush clears the entire session cache.
func (sm *SessionManager) cacheFlush() {
	sm.cacheMu.Lock()
	sm.cache = make(map[string]sessionCacheEntry, sessionCacheSize)
	sm.cacheMu.Unlock()
}
