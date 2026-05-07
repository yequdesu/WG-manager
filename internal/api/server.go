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

	// ── Public routes (no auth) ──────────────────────────────────────────

	mux.HandleFunc("/api/v1/health", h.Health)
	mux.HandleFunc("/connect", h.Connect)

	rateLimit := RateLimitMiddleware(ctx, 3)
	mux.HandleFunc("/api/v1/redeem", rateLimit(h.RedeemInvite))
	mux.HandleFunc("/api/v1/login", rateLimit(h.Login))
	mux.HandleFunc("/api/v1/logout", h.Logout)
	mux.HandleFunc("/bootstrap", h.Bootstrap)

	// ── Deprecated routes (return 410 Gone) ──────────────────────────────

	mux.HandleFunc("/api/v1/register", h.Register)
	mux.HandleFunc("/api/v1/request", h.SubmitRequest)
	mux.HandleFunc("/api/v1/request/", h.RequestStatus)
	mux.HandleFunc("/api/v1/requests", h.ListRequests)
	mux.HandleFunc("/api/v1/requests/", h.ManageRequest)

	// ── Admin routes (local-only, role-based auth) ───────────────────────

	adminMW := RequireRole(s, cfg.APIKey, store.RoleAdmin, store.RoleOwner)
	mux.Handle("/api/v1/peers", methodGuard(http.MethodGet, LocalOnly(adminMW(h.ListPeers))))
	mux.Handle("/api/v1/peers/alias", methodGuard(http.MethodPut, LocalOnly(adminMW(h.PeerAliasUpdate))))
	mux.Handle("/api/v1/peers/by-pubkey/", methodGuard(http.MethodDelete, LocalOnly(adminMW(h.PeerDeleteByPubkey))))
	mux.Handle("/api/v1/peers/", methodGuard(http.MethodDelete, LocalOnly(adminMW(h.DeletePeer))))
	mux.Handle("/api/v1/status", methodGuard(http.MethodGet, LocalOnly(adminMW(h.Status))))

	mux.HandleFunc("/api/v1/invites", LocalOnly(adminMW(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.ListInvites(w, r)
		case http.MethodPost:
			h.CreateInvite(w, r)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})))
	mux.Handle("/api/v1/invites/qrcode", methodGuard(http.MethodGet, LocalOnly(adminMW(h.ServeInviteQR))))
	mux.Handle("/api/v1/invites/", methodGuard(http.MethodDelete, LocalOnly(adminMW(h.RevokeInvite))))

	ownerMW := RequireRole(s, cfg.APIKey, store.RoleOwner)
	mux.HandleFunc("/api/v1/users", LocalOnly(ownerMW(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			h.ListUsers(w, r)
		case http.MethodPost:
			h.CreateUser(w, r)
		default:
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "method not allowed"})
		}
	})))
	mux.Handle("/api/v1/users/", methodGuard(http.MethodDelete, LocalOnly(ownerMW(h.DeleteUser))))

	// ── Self-status route (session-based, remote-accessible) ──────────────
	meMW := RequireRole(s, cfg.APIKey, store.RoleUser, store.RoleAdmin, store.RoleOwner)
	mux.Handle("/api/v1/me", methodGuard(http.MethodGet, meMW(h.Me)))

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
		// Token-bearing endpoints: skip logging to avoid potential leaks.
		if r.URL.Path == "/api/v1/redeem" {
			return
		}
		if r.Method == http.MethodPost && r.URL.Path == "/api/v1/invites" {
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
