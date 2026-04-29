package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateEntry struct {
	count    int
	window   time.Time
}

var (
	rateMap   = make(map[string]*rateEntry)
	rateMutex sync.Mutex
)

func RateLimitMiddleware(maxPerMinute int) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}

			if host == "127.0.0.1" || host == "::1" {
				next(w, r)
				return
			}

			rateMutex.Lock()
			entry, ok := rateMap[host]
			now := time.Now()
			if !ok || now.Sub(entry.window) > time.Minute {
				rateMap[host] = &rateEntry{count: 1, window: now}
				rateMutex.Unlock()
				next(w, r)
				return
			}
			entry.count++
			current := entry.count
			rateMutex.Unlock()

			if current > maxPerMinute {
				writeJSON(w, http.StatusTooManyRequests, map[string]string{
					"error": "rate limit exceeded, try again later",
				})
				return
			}
			next(w, r)
		}
	}
}

func AuthMiddleware(apiKey string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			host, _, err := net.SplitHostPort(r.RemoteAddr)
			if err != nil {
				host = r.RemoteAddr
			}
			if host == "127.0.0.1" || host == "::1" {
				next.ServeHTTP(w, r)
				return
			}

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
