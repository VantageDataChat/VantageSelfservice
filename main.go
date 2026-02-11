package main

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"helpdesk/internal/auth"
	"helpdesk/internal/chunker"
	"helpdesk/internal/config"
	"helpdesk/internal/db"
	"helpdesk/internal/document"
	"helpdesk/internal/email"
	"helpdesk/internal/embedding"
	"helpdesk/internal/llm"
	"helpdesk/internal/parser"
	"helpdesk/internal/pending"
	"helpdesk/internal/query"
	"helpdesk/internal/vectorstore"
)

func main() {
	// Ensure data directory exists
	if err := os.MkdirAll("./data", 0755); err != nil {
		log.Fatalf("Failed to create data directory: %v", err)
	}

	// 1. Initialize ConfigManager and load config
	configPath := "./data/config.json"
	cm, err := config.NewConfigManager(configPath)
	if err != nil {
		log.Fatalf("Failed to create config manager: %v", err)
	}
	if err := cm.Load(); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}
	cfg := cm.Get()

	// 2. Initialize database
	database, err := db.InitDB(cfg.Vector.DBPath)
	if err != nil {
		log.Fatalf("Failed to initialize database: %v", err)
	}
	defer database.Close()

	// 3. Create service instances
	vs := vectorstore.NewSQLiteVectorStore(database)
	tc := &chunker.TextChunker{ChunkSize: cfg.Vector.ChunkSize, Overlap: cfg.Vector.Overlap}
	dp := &parser.DocumentParser{}
	es := embedding.NewAPIEmbeddingService(cfg.Embedding.Endpoint, cfg.Embedding.APIKey, cfg.Embedding.ModelName)
	ls := llm.NewAPILLMService(cfg.LLM.Endpoint, cfg.LLM.APIKey, cfg.LLM.ModelName, cfg.LLM.Temperature, cfg.LLM.MaxTokens)
	dm := document.NewDocumentManager(dp, tc, es, vs, database)
	qe := query.NewQueryEngine(es, vs, ls, database, cfg)
	pm := pending.NewPendingQuestionManager(database, tc, es, vs, ls)
	oc := auth.NewOAuthClient(cfg.OAuth.Providers)
	sm := auth.NewSessionManager(database, 24*time.Hour)

	// Create email service
	emailSvc := email.NewService(func() config.SMTPConfig {
		return cm.Get().SMTP
	})

	// 4. Create App
	app := NewApp(database, qe, dm, pm, oc, sm, cm, emailSvc)

	// 5. Register HTTP API handlers
	registerAPIHandlers(app)

	// 6. Serve frontend with SPA fallback (non-API routes serve index.html)
	http.Handle("/", spaHandler("frontend/dist"))

	// 7. Start HTTP server
	addr := "0.0.0.0:8080"
	fmt.Printf("Helpdesk system starting on http://%s\n", addr)
	log.Fatal(http.ListenAndServe(addr, nil))
}

func registerAPIHandlers(app *App) {
	// OAuth
	http.HandleFunc("/api/oauth/url", handleOAuthURL(app))
	http.HandleFunc("/api/oauth/callback", handleOAuthCallback(app))

	// Admin login
	http.HandleFunc("/api/admin/login", handleAdminLogin(app))
	http.HandleFunc("/api/admin/setup", handleAdminSetup(app))
	http.HandleFunc("/api/admin/status", handleAdminStatus(app))

	// User registration & login
	http.HandleFunc("/api/auth/register", handleRegister(app))
	http.HandleFunc("/api/auth/login", handleUserLogin(app))
	http.HandleFunc("/api/auth/verify", handleVerifyEmail(app))

	// Query
	http.HandleFunc("/api/query", handleQuery(app))

	// Documents
	http.HandleFunc("/api/documents/upload", handleDocumentUpload(app))
	http.HandleFunc("/api/documents/url", handleDocumentURL(app))
	http.HandleFunc("/api/documents", handleDocuments(app))
	// DELETE /api/documents/{id} - handled by prefix match
	http.HandleFunc("/api/documents/", handleDocumentByID(app))

	// Pending questions
	http.HandleFunc("/api/pending/answer", handlePendingAnswer(app))
	http.HandleFunc("/api/pending", handlePending(app))

	// Config
	http.HandleFunc("/api/config", handleConfig(app))

	// Email test
	http.HandleFunc("/api/email/test", handleEmailTest(app))
}

// --- JSON helpers ---

func writeJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, status int, message string) {
	writeJSON(w, status, map[string]string{"error": message})
}

func readJSONBody(r *http.Request, v interface{}) error {
	defer r.Body.Close()
	return json.NewDecoder(r.Body).Decode(v)
}

// --- OAuth handlers ---

func handleOAuthURL(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		provider := r.URL.Query().Get("provider")
		if provider == "" {
			writeError(w, http.StatusBadRequest, "missing provider parameter")
			return
		}
		url, err := app.GetOAuthURL(provider)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"url": url})
	}
}

func handleOAuthCallback(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Provider string `json:"provider"`
			Code     string `json:"code"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		resp, err := app.HandleOAuthCallback(req.Provider, req.Code)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// --- Admin login handler ---

func handleAdminLogin(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		resp, err := app.AdminLogin(req.Username, req.Password)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleAdminSetup(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Username string `json:"username"`
			Password string `json:"password"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		resp, err := app.AdminSetup(req.Username, req.Password)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleAdminStatus(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		cfg := app.configManager.Get()
		writeJSON(w, http.StatusOK, map[string]interface{}{
			"configured":  app.IsAdminConfigured(),
			"login_route": cfg.Admin.LoginRoute,
		})
	}
}

// --- User registration & login handlers ---

func handleRegister(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req RegisterRequest
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		baseURL := "http://" + r.Host
		if r.TLS != nil {
			baseURL = "https://" + r.Host
		}
		if fwd := r.Header.Get("X-Forwarded-Proto"); fwd != "" {
			baseURL = fwd + "://" + r.Host
		}
		if err := app.Register(req, baseURL); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "注册成功，请查收验证邮件"})
	}
}

func handleUserLogin(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Email    string `json:"email"`
			Password string `json:"password"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		resp, err := app.UserLogin(req.Email, req.Password)
		if err != nil {
			writeError(w, http.StatusUnauthorized, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

func handleVerifyEmail(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		token := r.URL.Query().Get("token")
		if err := app.VerifyEmail(token); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "邮箱验证成功，请登录"})
	}
}

// --- Query handler ---

func handleQuery(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req query.QueryRequest
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		resp, err := app.queryEngine.Query(req)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// --- Document handlers ---

func handleDocuments(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		docs, err := app.ListDocuments()
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if docs == nil {
			docs = []document.DocumentInfo{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"documents": docs})
	}
}

func handleDocumentUpload(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		// Parse multipart form (max 50MB)
		if err := r.ParseMultipartForm(50 << 20); err != nil {
			writeError(w, http.StatusBadRequest, "failed to parse multipart form")
			return
		}

		file, header, err := r.FormFile("file")
		if err != nil {
			writeError(w, http.StatusBadRequest, "missing file in upload")
			return
		}
		defer file.Close()

		fileData, err := io.ReadAll(file)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "failed to read file")
			return
		}

		// Determine file type from extension
		fileType := detectFileType(header.Filename)

		req := document.UploadFileRequest{
			FileName: header.Filename,
			FileData: fileData,
			FileType: fileType,
		}
		doc, err := app.UploadFile(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, doc)
	}
}

func handleDocumentURL(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req document.UploadURLRequest
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		doc, err := app.UploadURL(req)
		if err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, doc)
	}
}

func handleDocumentByID(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// Extract document ID from path: /api/documents/{id}
		path := strings.TrimPrefix(r.URL.Path, "/api/documents/")
		if path == "" || path == r.URL.Path {
			writeError(w, http.StatusBadRequest, "missing document ID")
			return
		}
		docID := path

		if r.Method != http.MethodDelete {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}

		if err := app.DeleteDocument(docID); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// --- Pending question handlers ---

func handlePending(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		status := r.URL.Query().Get("status")
		questions, err := app.ListPendingQuestions(status)
		if err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		if questions == nil {
			questions = []pending.PendingQuestion{}
		}
		writeJSON(w, http.StatusOK, map[string]interface{}{"questions": questions})
	}
}

func handlePendingAnswer(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req pending.AdminAnswerRequest
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := app.AnswerQuestion(req); err != nil {
			writeError(w, http.StatusInternalServerError, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
	}
}

// --- Config handlers ---

func handleConfig(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			cfg := app.GetConfig()
			if cfg == nil {
				writeError(w, http.StatusInternalServerError, "config not loaded")
				return
			}
			writeJSON(w, http.StatusOK, cfg)
		case http.MethodPut:
			var updates map[string]interface{}
			if err := readJSONBody(r, &updates); err != nil {
				writeError(w, http.StatusBadRequest, "invalid request body")
				return
			}
			if err := app.UpdateConfig(updates); err != nil {
				writeError(w, http.StatusInternalServerError, err.Error())
				return
			}
			writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
		default:
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		}
	}
}

// --- Email test handler ---

func handleEmailTest(app *App) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost {
			writeError(w, http.StatusMethodNotAllowed, "method not allowed")
			return
		}
		var req struct {
			Email string `json:"email"`
		}
		if err := readJSONBody(r, &req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid request body")
			return
		}
		if err := app.TestEmail(req.Email); err != nil {
			writeError(w, http.StatusBadRequest, err.Error())
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "message": "测试邮件已发送"})
	}
}

// --- Helpers ---

// detectFileType maps file extensions to the internal file type names.
func detectFileType(filename string) string {
	lower := strings.ToLower(filename)
	switch {
	case strings.HasSuffix(lower, ".pdf"):
		return "pdf"
	case strings.HasSuffix(lower, ".docx"), strings.HasSuffix(lower, ".doc"):
		return "word"
	case strings.HasSuffix(lower, ".xlsx"), strings.HasSuffix(lower, ".xls"):
		return "excel"
	case strings.HasSuffix(lower, ".pptx"), strings.HasSuffix(lower, ".ppt"):
		return "ppt"
	default:
		return "unknown"
	}
}

// spaHandler serves static files from dir, falling back to index.html for SPA routes.
func spaHandler(dir string) http.Handler {
	fileServer := http.FileServer(http.Dir(dir))
	indexPath := filepath.Join(dir, "index.html")
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Clean the path and build the file path
		p := filepath.Join(dir, filepath.Clean(r.URL.Path))
		info, err := os.Stat(p)
		if err == nil && !info.IsDir() {
			// Static file exists, serve it
			fileServer.ServeHTTP(w, r)
			return
		}
		// Fallback: serve index.html for SPA routing
		http.ServeFile(w, r, indexPath)
	})
}
