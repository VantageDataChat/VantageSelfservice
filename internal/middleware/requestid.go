package middleware

import (
	"crypto/rand"
	"encoding/hex"
	"log"
	"net/http"
)

// RequestID 返回生成请求追踪 ID 的中间件。
// 为每个请求生成 8 字节随机 hex 字符串（16 个十六进制字符），
// 并设置为 X-Request-Id 响应头。
func RequestID() Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			reqID := make([]byte, 8)
			if _, err := rand.Read(reqID); err != nil {
				log.Printf("[RequestID] crypto/rand failed: %v", err)
			}
			w.Header().Set("X-Request-Id", hex.EncodeToString(reqID))
			next(w, r)
		}
	}
}
