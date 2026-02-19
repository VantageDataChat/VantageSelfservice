package auth

import (
	"database/sql"
	"testing"
	"time"

	_ "github.com/mattn/go-sqlite3"
	"golang.org/x/crypto/bcrypt"
)

// setupTestDB creates an in-memory SQLite database with the sessions table.
func setupTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite3", ":memory:")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	_, err = db.Exec(`CREATE TABLE IF NOT EXISTS sessions (
		id         TEXT PRIMARY KEY,
		user_id    TEXT NOT NULL,
		expires_at DATETIME NOT NULL,
		created_at DATETIME DEFAULT CURRENT_TIMESTAMP
	)`)
	if err != nil {
		t.Fatalf("create table: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	return db
}

func TestCreateSession(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(db, db, time.Hour)

	session, err := sm.CreateSession("user-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}
	if session.ID == "" {
		t.Error("expected non-empty session ID")
	}
	if session.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", session.UserID, "user-1")
	}
	if session.ExpiresAt.Before(time.Now()) {
		t.Error("session should not already be expired")
	}
	// Verify it's roughly 1 hour from now
	diff := time.Until(session.ExpiresAt)
	if diff < 59*time.Minute || diff > 61*time.Minute {
		t.Errorf("expiry diff = %v, want ~1h", diff)
	}
}

func TestCreateSession_UniqueIDs(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(db, db, time.Hour)

	s1, err := sm.CreateSession("user-1")
	if err != nil {
		t.Fatalf("CreateSession 1: %v", err)
	}
	s2, err := sm.CreateSession("user-1")
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}
	if s1.ID == s2.ID {
		t.Error("expected unique session IDs")
	}
}

func TestValidateSession_Valid(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(db, db, time.Hour)

	created, err := sm.CreateSession("user-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	validated, err := sm.ValidateSession(created.ID)
	if err != nil {
		t.Fatalf("ValidateSession: %v", err)
	}
	if validated.UserID != "user-1" {
		t.Errorf("UserID = %q, want %q", validated.UserID, "user-1")
	}
}

func TestValidateSession_Expired(t *testing.T) {
	db := setupTestDB(t)
	// Use a very short expiry so the session is already expired
	sm := NewSessionManager(db, db, time.Millisecond)

	created, err := sm.CreateSession("user-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	// Wait for expiry
	time.Sleep(5 * time.Millisecond)

	_, err = sm.ValidateSession(created.ID)
	if err == nil {
		t.Fatal("expected error for expired session")
	}
	if err.Error() != "session expired" {
		t.Errorf("error = %q, want %q", err.Error(), "session expired")
	}
}

func TestValidateSession_NotFound(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(db, db, time.Hour)

	_, err := sm.ValidateSession("nonexistent-id")
	if err == nil {
		t.Fatal("expected error for nonexistent session")
	}
	if err.Error() != "session not found" {
		t.Errorf("error = %q, want %q", err.Error(), "session not found")
	}
}

func TestDeleteSession(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(db, db, time.Hour)

	created, err := sm.CreateSession("user-1")
	if err != nil {
		t.Fatalf("CreateSession: %v", err)
	}

	err = sm.DeleteSession(created.ID)
	if err != nil {
		t.Fatalf("DeleteSession: %v", err)
	}

	_, err = sm.ValidateSession(created.ID)
	if err == nil {
		t.Fatal("expected error after deleting session")
	}
}

func TestCleanExpired(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(db, db, time.Millisecond)

	// Create sessions that will expire immediately
	_, err := sm.CreateSession("user-1")
	if err != nil {
		t.Fatalf("CreateSession 1: %v", err)
	}
	_, err = sm.CreateSession("user-2")
	if err != nil {
		t.Fatalf("CreateSession 2: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	// Create a session that won't expire
	smLong := NewSessionManager(db, db, time.Hour)
	_, err = smLong.CreateSession("user-3")
	if err != nil {
		t.Fatalf("CreateSession 3: %v", err)
	}

	removed, err := sm.CleanExpired()
	if err != nil {
		t.Fatalf("CleanExpired: %v", err)
	}
	if removed != 2 {
		t.Errorf("removed = %d, want 2", removed)
	}

	// The non-expired session should still be valid
	var count int
	db.QueryRow("SELECT COUNT(*) FROM sessions").Scan(&count)
	if count != 1 {
		t.Errorf("remaining sessions = %d, want 1", count)
	}
}

func TestNewSessionManager_DefaultExpiry(t *testing.T) {
	db := setupTestDB(t)
	sm := NewSessionManager(db, db, 0)
	if sm.expiry != DefaultSessionExpiry {
		t.Errorf("expiry = %v, want %v", sm.expiry, DefaultSessionExpiry)
	}
}

func TestVerifyAdminPassword_Correct(t *testing.T) {
	password := "admin-secret-123"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate hash: %v", err)
	}

	err = VerifyAdminPassword(password, string(hash))
	if err != nil {
		t.Fatalf("expected nil error for correct password, got: %v", err)
	}
}

func TestVerifyAdminPassword_Wrong(t *testing.T) {
	password := "admin-secret-123"
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		t.Fatalf("generate hash: %v", err)
	}

	err = VerifyAdminPassword("wrong-password", string(hash))
	if err == nil {
		t.Fatal("expected error for wrong password")
	}
	if err.Error() != "密码错误" {
		t.Errorf("error = %q, want %q", err.Error(), "密码错误")
	}
}

func TestVerifyAdminPassword_EmptyHash(t *testing.T) {
	err := VerifyAdminPassword("any-password", "")
	if err == nil {
		t.Fatal("expected error for empty hash")
	}
	if err.Error() != "admin password not configured" {
		t.Errorf("error = %q, want %q", err.Error(), "admin password not configured")
	}
}

func TestHashPassword(t *testing.T) {
	password := "test-password-456"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword: %v", err)
	}
	if hash == "" {
		t.Fatal("expected non-empty hash")
	}
	if hash == password {
		t.Error("hash should not equal plaintext password")
	}

	// Verify the hash works with bcrypt
	err = bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	if err != nil {
		t.Errorf("hash verification failed: %v", err)
	}
}

func TestHashPassword_DifferentHashes(t *testing.T) {
	password := "same-password"
	h1, _ := HashPassword(password)
	h2, _ := HashPassword(password)
	if h1 == h2 {
		t.Error("expected different hashes for same password (bcrypt uses random salt)")
	}
}
