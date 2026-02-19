package middleware

import "net/http"

// CORS 返回处理跨域请求的中间件。
// 仅允许同源请求：验证 Origin 头与请求 Host 是否匹配。
// 对 OPTIONS 预检请求返回 204 No Content。
func CORS() Middleware {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			// Only allow same-origin requests — reflect the Host as allowed origin
			origin := r.Header.Get("Origin")
			if origin != "" {
				// Validate that the origin matches the request host
				// This prevents cross-origin requests from arbitrary domains
				requestHost := r.Host
				if requestHost != "" && (origin == "http://"+requestHost || origin == "https://"+requestHost) {
					w.Header().Set("Access-Control-Allow-Origin", origin)
					w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
					w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
					w.Header().Set("Access-Control-Allow-Credentials", "true")
					w.Header().Set("Access-Control-Max-Age", "3600")
					w.Header().Set("Vary", "Origin")
				}
			}
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next(w, r)
		}
	}
}
