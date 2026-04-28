package api

import (
	"net"
	"net/http"
	"strings"
)

func AuthMiddleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if apiKey == "" {
				next.ServeHTTP(w, r)
				return
			}

			auth := r.Header.Get("Authorization")
			if auth == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "missing Authorization header",
				})
				return
			}

			parts := strings.SplitN(auth, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "invalid Authorization format, expected: Bearer <token>",
				})
				return
			}

			if parts[1] != apiKey {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "invalid API key",
				})
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func AdminOnlyMiddleware(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		host, _, err := net.SplitHostPort(r.RemoteAddr)
		if err != nil {
			host = r.RemoteAddr
		}

		if host != "127.0.0.1" && host != "::1" {
			writeJSON(w, http.StatusForbidden, map[string]string{
				"error": "access denied: admin endpoints require localhost access",
			})
			return
		}

		next(w, r)
	}
}
