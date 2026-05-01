package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"
)

type rateEntry struct {
	count  int
	window time.Time
}

var (
	rateMap   = make(map[string]*rateEntry)
	rateMutex sync.Mutex
)

func RateLimitMiddleware(maxPerMinute int) func(http.HandlerFunc) http.HandlerFunc {
	go func() {
		for {
			time.Sleep(5 * time.Minute)
			rateMutex.Lock()
			now := time.Now()
			for ip, e := range rateMap {
				if now.Sub(e.window) > 2*time.Minute {
					delete(rateMap, ip)
				}
			}
			rateMutex.Unlock()
		}
	}()
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

// LocalOnly allows only localhost (127.0.0.1 / ::1).
func LocalOnly(next http.HandlerFunc) http.HandlerFunc {
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

// KeyOrLocal allows localhost, or remote with valid API Key via Authorization: Bearer header.
func KeyOrLocal(apiKey string) func(http.HandlerFunc) http.HandlerFunc {
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

			keyOK := false
			if auth := r.Header.Get("Authorization"); auth != "" {
				parts := strings.SplitN(auth, " ", 2)
				if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] == apiKey {
					keyOK = true
				}
			}
	if !keyOK {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "invalid or missing API key",
				})
				return
			}
			next(w, r)
		}
	}
}
