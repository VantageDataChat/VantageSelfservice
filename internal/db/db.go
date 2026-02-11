// Package db provides SQLite database initialization and migration for the helpdesk system.
package db

import (
	"database/sql"
	"fmt"

	_ "github.com/mattn/go-sqlite3"
)

// InitDB opens a SQLite database connection at dbPath, enables WAL mode and
// foreign keys, and creates all required tables idempotently.
func InitDB(dbPath string) (*sql.DB, error) {
	db, err := sql.Open("sqlite3", dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err := db.Ping(); err != nil {
		db.Close()
		return nil, fmt.Errorf("failed to ping database: %w", err)
	}

	if err := configurePragmas(db); err != nil {
		db.Close()
		return nil, err
	}

	if err := createTables(db); err != nil {
		db.Close()
		return nil, err
	}

	return db, nil
}

func configurePragmas(db *sql.DB) error {
	pragmas := []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA foreign_keys=ON",
	}
	for _, p := range pragmas {
		if _, err := db.Exec(p); err != nil {
			return fmt.Errorf("failed to execute %s: %w", p, err)
		}
	}
	return nil
}

func createTables(db *sql.DB) error {
	tables := []string{
		`CREATE TABLE IF NOT EXISTS documents (
			id         TEXT PRIMARY KEY,
			name       TEXT NOT NULL,
			type       TEXT NOT NULL,
			status     TEXT NOT NULL,
			error      TEXT,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		)`,
		`CREATE TABLE IF NOT EXISTS chunks (
			id            TEXT PRIMARY KEY,
			document_id   TEXT NOT NULL,
			document_name TEXT NOT NULL,
			chunk_index   INTEGER NOT NULL,
			chunk_text    TEXT NOT NULL,
			embedding     BLOB NOT NULL,
			created_at    DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (document_id) REFERENCES documents(id)
		)`,
		`CREATE TABLE IF NOT EXISTS pending_questions (
			id          TEXT PRIMARY KEY,
			question    TEXT NOT NULL,
			user_id     TEXT NOT NULL,
			status      TEXT NOT NULL,
			answer      TEXT,
			llm_answer  TEXT,
			created_at  DATETIME DEFAULT CURRENT_TIMESTAMP,
			answered_at DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS users (
			id             TEXT PRIMARY KEY,
			email          TEXT UNIQUE,
			name           TEXT,
			provider       TEXT NOT NULL,
			provider_id    TEXT NOT NULL,
			password_hash  TEXT,
			email_verified INTEGER DEFAULT 0,
			created_at     DATETIME DEFAULT CURRENT_TIMESTAMP,
			last_login     DATETIME
		)`,
		`CREATE TABLE IF NOT EXISTS email_tokens (
			id         TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL,
			token      TEXT NOT NULL UNIQUE,
			type       TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
		`CREATE TABLE IF NOT EXISTS sessions (
			id         TEXT PRIMARY KEY,
			user_id    TEXT NOT NULL,
			expires_at DATETIME NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP,
			FOREIGN KEY (user_id) REFERENCES users(id)
		)`,
	}

	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}

	for _, ddl := range tables {
		if _, err := tx.Exec(ddl); err != nil {
			tx.Rollback()
			return fmt.Errorf("failed to create table: %w", err)
		}
	}

	return tx.Commit()
}
