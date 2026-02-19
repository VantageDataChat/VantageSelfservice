package handler

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
)

// WriteJSON encodes data as JSON and writes it to the response with the given status code.
func WriteJSON(w http.ResponseWriter, status int, data interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

// WriteError writes a JSON error response with the given status code and message.
func WriteError(w http.ResponseWriter, status int, message string) {
	WriteJSON(w, status, map[string]string{"error": message})
}

// ReadJSONBody decodes the request body as JSON into v.
// It validates Content-Type, limits body size to 1MB, and rejects trailing data.
func ReadJSONBody(r *http.Request, v interface{}) error {
	// Validate content type
	ct := r.Header.Get("Content-Type")
	if ct != "" && !strings.HasPrefix(ct, "application/json") {
		return fmt.Errorf("expected Content-Type application/json")
	}
	defer r.Body.Close()
	// Limit request body to 1MB to prevent large payload attacks
	limited := io.LimitReader(r.Body, 1<<20)
	decoder := json.NewDecoder(limited)
	if err := decoder.Decode(v); err != nil {
		return err
	}
	// Ensure no trailing data (prevents request smuggling)
	if decoder.More() {
		return fmt.Errorf("unexpected trailing data in request body")
	}
	return nil
}

// GetUserSession validates the Authorization bearer token and returns the user ID.
func GetUserSession(app *App, r *http.Request) (string, error) {
	authHeader := r.Header.Get("Authorization")
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" || token == authHeader {
		// token is empty, or Authorization header didn't have "Bearer " prefix
		return "", fmt.Errorf("未登录")
	}
	session, err := app.sessionManager.ValidateSession(token)
	if err != nil {
		return "", fmt.Errorf("会话已过期")
	}
	return session.UserID, nil
}

// GetAdminSession validates the session and checks if it's an admin session.
// Returns (userID, role, error). role is "super_admin" or "editor".
func GetAdminSession(app *App, r *http.Request) (string, string, error) {
	authHeader := r.Header.Get("Authorization")
	token := strings.TrimPrefix(authHeader, "Bearer ")
	if token == "" || token == authHeader {
		return "", "", fmt.Errorf("未登录")
	}
	session, err := app.sessionManager.ValidateSession(token)
	if err != nil {
		return "", "", fmt.Errorf("会话无效")
	}
	if !app.IsAdminSession(session.UserID) {
		return "", "", fmt.Errorf("无权限")
	}
	role := app.GetAdminRole(session.UserID)
	if role == "" {
		return "", "", fmt.Errorf("无权限")
	}
	return session.UserID, role, nil
}

// IsValidHexID checks if the given string is a valid 32-character lowercase hex ID.
func IsValidHexID(id string) bool {
	if len(id) != 32 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// IsValidVideoMagicBytes checks if the file data starts with known video format magic bytes.
func IsValidVideoMagicBytes(data []byte) bool {
	if len(data) < 12 {
		return false
	}
	// MP4/MOV: starts with ftyp box (offset 4)
	if string(data[4:8]) == "ftyp" {
		return true
	}
	// AVI: starts with RIFF....AVI
	if string(data[0:4]) == "RIFF" && len(data) >= 12 && string(data[8:12]) == "AVI " {
		return true
	}
	// MKV/WebM: starts with EBML header (0x1A 0x45 0xDF 0xA3)
	if data[0] == 0x1A && data[1] == 0x45 && data[2] == 0xDF && data[3] == 0xA3 {
		return true
	}
	return false
}

// IsValidOptionalID validates an optional ID parameter (empty is allowed, non-empty must be hex).
func IsValidOptionalID(id string) bool {
	if id == "" {
		return true
	}
	if len(id) > 64 {
		return false
	}
	for _, c := range id {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			return false
		}
	}
	return true
}

// DetectFileType maps file extensions to the internal file type names.
func DetectFileType(filename string) string {
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
	case strings.HasSuffix(lower, ".md"), strings.HasSuffix(lower, ".markdown"):
		return "markdown"
	case strings.HasSuffix(lower, ".html"), strings.HasSuffix(lower, ".htm"):
		return "html"
	case strings.HasSuffix(lower, ".mp4"):
		return "mp4"
	case strings.HasSuffix(lower, ".avi"):
		return "avi"
	case strings.HasSuffix(lower, ".mkv"):
		return "mkv"
	case strings.HasSuffix(lower, ".mov"):
		return "mov"
	case strings.HasSuffix(lower, ".webm"):
		return "webm"
	default:
		return "unknown"
	}
}
