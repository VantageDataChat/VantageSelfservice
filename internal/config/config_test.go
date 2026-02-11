package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func testKey() []byte {
	return []byte("01234567890123456789012345678901") // 32 bytes
}

func tempConfigPath(t *testing.T) string {
	t.Helper()
	return filepath.Join(t.TempDir(), "config.json")
}

func newTestManager(t *testing.T) (*ConfigManager, string) {
	t.Helper()
	path := tempConfigPath(t)
	cm, err := NewConfigManagerWithKey(path, testKey())
	if err != nil {
		t.Fatalf("NewConfigManagerWithKey: %v", err)
	}
	return cm, path
}

func TestNewConfigManagerWithKey_InvalidKeyLength(t *testing.T) {
	_, err := NewConfigManagerWithKey("test.json", []byte("short"))
	if err == nil {
		t.Fatal("expected error for short key")
	}
}

func TestLoad_CreatesDefaultOnMissing(t *testing.T) {
	cm, path := newTestManager(t)
	if err := cm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// File should be created
	if _, err := os.Stat(path); os.IsNotExist(err) {
		t.Fatal("config file was not created")
	}

	cfg := cm.Get()
	if cfg == nil {
		t.Fatal("Get returned nil")
	}

	// Verify defaults
	if cfg.Vector.ChunkSize != 512 {
		t.Errorf("ChunkSize = %d, want 512", cfg.Vector.ChunkSize)
	}
	if cfg.Vector.Overlap != 128 {
		t.Errorf("Overlap = %d, want 128", cfg.Vector.Overlap)
	}
	if cfg.Vector.TopK != 5 {
		t.Errorf("TopK = %d, want 5", cfg.Vector.TopK)
	}
	if cfg.Vector.Threshold != 0.7 {
		t.Errorf("Threshold = %f, want 0.7", cfg.Vector.Threshold)
	}
	if cfg.LLM.Temperature != 0.3 {
		t.Errorf("Temperature = %f, want 0.3", cfg.LLM.Temperature)
	}
	if cfg.LLM.MaxTokens != 2048 {
		t.Errorf("MaxTokens = %d, want 2048", cfg.LLM.MaxTokens)
	}
	if cfg.Vector.DBPath != "./data/helpdesk.db" {
		t.Errorf("DBPath = %q, want ./data/helpdesk.db", cfg.Vector.DBPath)
	}
}

func TestSaveAndLoad_RoundTrip(t *testing.T) {
	cm, path := newTestManager(t)
	if err := cm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Set some values
	cm.config.LLM.APIKey = "sk-test-secret-key-12345"
	cm.config.LLM.Endpoint = "https://api.example.com/v1"
	cm.config.Embedding.APIKey = "emb-secret-key-67890"
	cm.config.OAuth.Providers["google"] = OAuthProviderConfig{
		ClientID:     "google-client-id",
		ClientSecret: "google-secret",
		AuthURL:      "https://accounts.google.com/o/oauth2/auth",
		TokenURL:     "https://oauth2.googleapis.com/token",
		RedirectURL:  "http://localhost:8080/callback",
		Scopes:       []string{"openid", "email"},
	}

	if err := cm.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Load into a new manager
	cm2, err := NewConfigManagerWithKey(path, testKey())
	if err != nil {
		t.Fatalf("NewConfigManagerWithKey: %v", err)
	}
	if err := cm2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := cm2.Get()
	if cfg.LLM.APIKey != "sk-test-secret-key-12345" {
		t.Errorf("LLM.APIKey = %q, want sk-test-secret-key-12345", cfg.LLM.APIKey)
	}
	if cfg.LLM.Endpoint != "https://api.example.com/v1" {
		t.Errorf("LLM.Endpoint = %q", cfg.LLM.Endpoint)
	}
	if cfg.Embedding.APIKey != "emb-secret-key-67890" {
		t.Errorf("Embedding.APIKey = %q", cfg.Embedding.APIKey)
	}
	if cfg.OAuth.Providers["google"].ClientSecret != "google-secret" {
		t.Errorf("OAuth google ClientSecret = %q", cfg.OAuth.Providers["google"].ClientSecret)
	}
	if len(cfg.OAuth.Providers["google"].Scopes) != 2 {
		t.Errorf("OAuth google Scopes len = %d", len(cfg.OAuth.Providers["google"].Scopes))
	}
}

func TestSave_APIKeysEncryptedOnDisk(t *testing.T) {
	cm, path := newTestManager(t)
	if err := cm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cm.config.LLM.APIKey = "my-secret-llm-key"
	cm.config.Embedding.APIKey = "my-secret-emb-key"
	cm.config.OAuth.Providers["google"] = OAuthProviderConfig{
		ClientSecret: "my-google-secret",
	}

	if err := cm.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Read raw file and verify plaintext keys are NOT present
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	raw := string(data)

	if strings.Contains(raw, "my-secret-llm-key") {
		t.Error("LLM API key found in plaintext on disk")
	}
	if strings.Contains(raw, "my-secret-emb-key") {
		t.Error("Embedding API key found in plaintext on disk")
	}
	if strings.Contains(raw, "my-google-secret") {
		t.Error("OAuth client secret found in plaintext on disk")
	}

	// Verify encrypted prefix is present
	if !strings.Contains(raw, encryptedPrefix) {
		t.Error("encrypted prefix not found in file")
	}
}

func TestUpdate_AppliesAndPersists(t *testing.T) {
	cm, path := newTestManager(t)
	if err := cm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	updates := map[string]interface{}{
		"llm.endpoint":      "https://new-api.example.com",
		"llm.api_key":       "new-key",
		"llm.model_name":    "gpt-4o",
		"llm.temperature":   0.7,
		"llm.max_tokens":    4096,
		"vector.chunk_size":  1024,
		"vector.top_k":       10,
		"admin.password_hash": "bcrypt-hash-here",
	}
	if err := cm.Update(updates); err != nil {
		t.Fatalf("Update: %v", err)
	}

	// Verify in-memory
	cfg := cm.Get()
	if cfg.LLM.Endpoint != "https://new-api.example.com" {
		t.Errorf("LLM.Endpoint = %q", cfg.LLM.Endpoint)
	}
	if cfg.LLM.ModelName != "gpt-4o" {
		t.Errorf("LLM.ModelName = %q", cfg.LLM.ModelName)
	}
	if cfg.LLM.Temperature != 0.7 {
		t.Errorf("Temperature = %f", cfg.LLM.Temperature)
	}
	if cfg.LLM.MaxTokens != 4096 {
		t.Errorf("MaxTokens = %d", cfg.LLM.MaxTokens)
	}
	if cfg.Vector.ChunkSize != 1024 {
		t.Errorf("ChunkSize = %d", cfg.Vector.ChunkSize)
	}
	if cfg.Vector.TopK != 10 {
		t.Errorf("TopK = %d", cfg.Vector.TopK)
	}

	// Verify persisted
	cm2, err := NewConfigManagerWithKey(path, testKey())
	if err != nil {
		t.Fatalf("NewConfigManagerWithKey: %v", err)
	}
	if err := cm2.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	cfg2 := cm2.Get()
	if cfg2.LLM.Endpoint != "https://new-api.example.com" {
		t.Errorf("persisted LLM.Endpoint = %q", cfg2.LLM.Endpoint)
	}
	if cfg2.LLM.APIKey != "new-key" {
		t.Errorf("persisted LLM.APIKey = %q", cfg2.LLM.APIKey)
	}
}

func TestUpdate_UnknownKey(t *testing.T) {
	cm, _ := newTestManager(t)
	if err := cm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}
	err := cm.Update(map[string]interface{}{"unknown.key": "value"})
	if err == nil {
		t.Fatal("expected error for unknown key")
	}
}

func TestGet_ReturnsCopy(t *testing.T) {
	cm, _ := newTestManager(t)
	if err := cm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg1 := cm.Get()
	cfg1.LLM.Endpoint = "modified"

	cfg2 := cm.Get()
	if cfg2.LLM.Endpoint == "modified" {
		t.Error("Get did not return a copy — mutation leaked")
	}
}

func TestLoad_PlaintextAPIKey(t *testing.T) {
	// Simulate a manually edited config with plaintext API key
	path := tempConfigPath(t)
	raw := map[string]interface{}{
		"llm": map[string]interface{}{
			"api_key": "plaintext-key",
		},
	}
	data, _ := json.Marshal(raw)
	os.WriteFile(path, data, 0600)

	cm, err := NewConfigManagerWithKey(path, testKey())
	if err != nil {
		t.Fatalf("NewConfigManagerWithKey: %v", err)
	}
	if err := cm.Load(); err != nil {
		t.Fatalf("Load: %v", err)
	}

	cfg := cm.Get()
	if cfg.LLM.APIKey != "plaintext-key" {
		t.Errorf("APIKey = %q, want plaintext-key", cfg.LLM.APIKey)
	}
}

func TestEncryptDecrypt_EmptyString(t *testing.T) {
	cm, _ := newTestManager(t)
	encrypted := cm.encryptIfNeeded("")
	if encrypted != "" {
		t.Errorf("encryptIfNeeded empty = %q, want empty", encrypted)
	}
	decrypted, err := cm.decryptIfNeeded("")
	if err != nil {
		t.Fatalf("decryptIfNeeded: %v", err)
	}
	if decrypted != "" {
		t.Errorf("decryptIfNeeded empty = %q, want empty", decrypted)
	}
}
