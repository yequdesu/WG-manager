package api

import (
	"net"
	"net/http"
	"strings"

	"wire-guard-dev/internal/store"
	"wire-guard-dev/internal/wg"
)

func NewServer(cfg *Config, s *store.State, m *wg.Manager) *http.Server {
	h := NewHandler(s, m, cfg)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/health", h.Health)
	mux.HandleFunc("/api/v1/client-script", h.ClientScript)

	registerHandler := authOrLocalMiddleware(cfg.APIKey)(h.Register)
	mux.HandleFunc("/api/v1/register", registerHandler)

	listPeersHandler := AuthMiddleware(cfg.APIKey)(AdminOnlyMiddleware(h.ListPeers))
	mux.Handle("/api/v1/peers", methodGuard(http.MethodGet, listPeersHandler))

	deletePeerHandler := AuthMiddleware(cfg.APIKey)(AdminOnlyMiddleware(h.DeletePeer))
	mux.Handle("/api/v1/peers/", methodGuard(http.MethodDelete, deletePeerHandler))

	statusHandler := AuthMiddleware(cfg.APIKey)(AdminOnlyMiddleware(h.Status))
	mux.Handle("/api/v1/status", methodGuard(http.MethodGet, statusHandler))

	return &http.Server{
		Addr:    cfg.MgmtListen,
		Handler: mux,
	}
}

func methodGuard(method string, handler http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == method {
			handler.ServeHTTP(w, r)
			return
		}
		writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
	})
}

func authOrLocalMiddleware(apiKey string) func(http.HandlerFunc) http.HandlerFunc {
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

			auth := r.Header.Get("Authorization")
			if auth == "" {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "missing Authorization header",
				})
				return
			}

			parts := strings.SplitN(auth, " ", 2)
			if len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") || parts[1] != apiKey {
				writeJSON(w, http.StatusUnauthorized, map[string]string{
					"error": "invalid API key",
				})
				return
			}

			next(w, r)
		}
	}
}
