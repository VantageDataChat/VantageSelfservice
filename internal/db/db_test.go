package db

import (
	"os"
	"path/filepath"
	"testing"
)

func TestInitDB_CreatesTablesSuccessfully(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Verify all expected tables exist
	expectedTables := []string{"documents", "chunks", "pending_questions", "users", "sessions"}
	for _, table := range expectedTables {
		var name string
		err := db.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name=?", table).Scan(&name)
		if err != nil {
			t.Errorf("table %q not found: %v", table, err)
		}
	}
}

func TestInitDB_WALModeEnabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("failed to query journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("expected journal_mode=wal, got %q", journalMode)
	}
}

func TestInitDB_ForeignKeysEnabled(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	var fk int
	if err := db.QueryRow("PRAGMA foreign_keys").Scan(&fk); err != nil {
		t.Fatalf("failed to query foreign_keys: %v", err)
	}
	if fk != 1 {
		t.Errorf("expected foreign_keys=1, got %d", fk)
	}
}

func TestInitDB_Idempotent(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("first InitDB failed: %v", err)
	}
	db1.Close()

	// Calling InitDB again on the same file should succeed
	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("second InitDB failed: %v", err)
	}
	defer db2.Close()
}

func TestInitDB_ForeignKeyEnforcement(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "test.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Inserting a session with a non-existent user_id should fail
	_, err = db.Exec("INSERT INTO sessions (id, user_id, expires_at) VALUES ('s1', 'nonexistent', '2099-01-01')")
	if err == nil {
		t.Error("expected foreign key violation error, got nil")
	}
}

func TestInitDB_InvalidPath(t *testing.T) {
	// A path inside a non-existent directory should fail
	dbPath := filepath.Join(os.TempDir(), "nonexistent_dir_abc123", "sub", "test.db")
	db, err := InitDB(dbPath)
	if err == nil {
		db.Close()
		t.Error("expected error for invalid path, got nil")
	}
}
