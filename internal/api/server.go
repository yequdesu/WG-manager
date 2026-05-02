package api

import (
	"context"
	"net/http"

	"wire-guard-dev/internal/store"
	"wire-guard-dev/internal/wg"
)

func NewServer(ctx context.Context, cfg *Config, s *store.State, m *wg.Manager) (*http.Server, *Handler) {
	h := NewHandler(s, m, cfg)

	mux := http.NewServeMux()

	mux.HandleFunc("/api/v1/health", h.Health)
	mux.HandleFunc("/connect", h.Connect)

	registerHandler := KeyOrLocal(cfg.APIKey)(h.Register)
	mux.HandleFunc("/api/v1/register", registerHandler)

	rateLimit := RateLimitMiddleware(ctx, 3)
	mux.HandleFunc("/api/v1/request", rateLimit(h.SubmitRequest))
	mux.HandleFunc("/api/v1/request/", h.RequestStatus)

	mux.Handle("/api/v1/requests", methodGuard(http.MethodGet, LocalOnly(h.ListRequests)))
	mux.Handle("/api/v1/requests/", LocalOnly(h.ManageRequest))
	mux.Handle("/api/v1/peers", methodGuard(http.MethodGet, LocalOnly(h.ListPeers)))
	mux.Handle("/api/v1/peers/", methodGuard(http.MethodDelete, LocalOnly(h.DeletePeer)))
	mux.Handle("/api/v1/status", methodGuard(http.MethodGet, LocalOnly(h.Status)))

	return &http.Server{
		Addr:    cfg.MgmtListen,
		Handler: mux,
	}, h
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
