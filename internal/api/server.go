package api

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"time"

	"wire-guard-dev/internal/audit"
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

	mux.HandleFunc("/api/v1/login", rateLimit(h.Login))
	mux.HandleFunc("/api/v1/logout", h.Logout)

	mux.HandleFunc("/api/v1/redeem", rateLimit(h.RedeemInvite))

	loggedMux := requestLogger(mux)

	return &http.Server{
		Addr:    cfg.MgmtListen,
		Handler: loggedMux,
	}, h
}

func requestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		lw := &loggingResponseWriter{ResponseWriter: w, statusCode: 200}
		next.ServeHTTP(lw, r)
		duration := time.Since(start)

		if r.Method == http.MethodGet && r.URL.Path == "/api/v1/health" {
			return
		}
		if r.Method == http.MethodGet && isLocal(r.RemoteAddr) {
			return
		}
		if r.URL.Path == "/connect" {
			return
		}

		src := r.RemoteAddr
		if host, _, err := net.SplitHostPort(src); err == nil {
			src = host
		}

		audit.Write("HTTP", "request",
			map[string]string{
				"status":   itoa(lw.statusCode),
				"method":   r.Method,
				"path":     r.URL.Path,
				"source":   src,
				"duration": duration.String(),
			},
		)
	})
}

func isLocal(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	return host == "127.0.0.1" || host == "::1"
}

type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lw *loggingResponseWriter) WriteHeader(code int) {
	lw.statusCode = code
	lw.ResponseWriter.WriteHeader(code)
}

func itoa(n int) string {
	return fmt.Sprintf("%d", n)
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
