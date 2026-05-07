package api

// ── Capability Matrix ─────────────────────────────────────────────────────
//
// Three fixed roles: owner, admin, user. No custom/configurable roles.
//
// Machine-readable matrix — can be extracted into documentation.
// Format: ACTION | OWNER | ADMIN | USER | ENFORCEMENT
//
// ACTION                           | OWNER  | ADMIN  | USER   | ENFORCEMENT
// ──────────────────────────────── | ────── | ────── | ────── | ───────────
// Own status (GET /api/v1/me)      | ✅     | ✅     | ✅     | RequireRole(user+)
// Health, login, logout, redeem    | ✅     | ✅     | ✅     | Public
// Bootstrap, connect               | ✅     | ✅     | ✅     | Public
// List peers (GET /api/v1/peers)   | ✅     | ✅     | ❌     | LocalOnly + RequireRole(admin,owner)
// Delete peer (DELETE /api/v1/peers)| ✅    | ✅     | ❌     | LocalOnly + RequireRole(admin,owner)
// View status (GET /api/v1/status)  | ✅    | ✅     | ❌     | LocalOnly + RequireRole(admin,owner)
// List invites (GET /api/v1/invites)| ✅    | ✅     | ❌     | LocalOnly + RequireRole(admin,owner)
// Create invite (POST /api/v1/invites)| ✅  | ✅†    | ❌     | LocalOnly + RequireRole(admin,owner) + target_role check
// Invite QR (GET /api/v1/invites/qrcode)| ✅| ✅   | ❌     | LocalOnly + RequireRole(admin,owner)
// Revoke invite (DELETE /api/v1/invites/)| ✅| ✅   | ❌     | LocalOnly + RequireRole(admin,owner)
// List users (GET /api/v1/users)    | ✅    | ❌     | ❌     | LocalOnly + RequireRole(owner)
// Create user (POST /api/v1/users)  | ✅‡   | ❌     | ❌     | LocalOnly + RequireRole(owner)
// Delete user (DELETE /api/v1/users/)| ✅   | ❌     | ❌     | LocalOnly + RequireRole(owner)
// Bootstrap owner                  | ✅     | ❌     | ❌     | No users exist yet (one-shot)
//
// † Admin can create invites only with target_role="user".
//   Admin cannot create owner-level invites or direct user accounts.
// ‡ Owner may create users of any role (owner, admin, user).
//
// API key (MGMT_API_KEY) bypasses role checks entirely — this is the
// emergency/local fallback. All admin/owner routes are additionally
// protected by LocalOnly, preventing remote API key abuse.
//
// User routes work on self-status only (/api/v1/me).
// Admin routes work for admin+owner on peers, invites, status.
// Owner routes work for owner only on user CRUD, system config.

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"wire-guard-dev/internal/store"
)

type rateEntry struct {
	count  int
	window time.Time
}

var (
	rateMap   = make(map[string]*rateEntry)
	rateMutex sync.Mutex
)

func RateLimitMiddleware(ctx context.Context, maxPerMinute int) func(http.HandlerFunc) http.HandlerFunc {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				rateMutex.Lock()
				now := time.Now()
				for ip, e := range rateMap {
					if now.Sub(e.window) > 2*time.Minute {
						delete(rateMap, ip)
					}
				}
				rateMutex.Unlock()
			}
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

// contextKey is an unexported type for context keys to avoid collisions.
type contextKey string

const (
	ContextKeyUser contextKey = "auth_user"
	ContextKeyRole contextKey = "auth_role"
)

// RequireRole returns middleware that checks for a valid session token or
// falls back to the MGMT_API_KEY. The caller specifies which roles are allowed.
func RequireRole(st *store.State, apiKey string, roles ...store.Role) func(http.HandlerFunc) http.HandlerFunc {
	return func(next http.HandlerFunc) http.HandlerFunc {
		return func(w http.ResponseWriter, r *http.Request) {
			token := getTokenFromRequest(r)

			// Try session token first.
			if token != "" {
				sum := sha256.Sum256([]byte(token))
				tokenHash := hex.EncodeToString(sum[:])

				if sess, ok := st.GetSessionByTokenHash(tokenHash); ok {
					expiresAt, err := time.Parse(time.RFC3339, sess.ExpiresAt)
					if err == nil && time.Now().UTC().Before(expiresAt) {
						// Check against required roles.
						allowed := false
						for _, role := range roles {
							if sess.Role == role {
								allowed = true
								break
							}
						}
						if allowed {
							ctx := context.WithValue(r.Context(), ContextKeyUser, sess.UserName)
							ctx = context.WithValue(ctx, ContextKeyRole, sess.Role)
							next(w, r.WithContext(ctx))
							return
						}
					}
				}
			}

			// Fall back to API key check.
			if apiKey != "" {
				auth := r.Header.Get("Authorization")
				if auth != "" {
					parts := strings.SplitN(auth, " ", 2)
					if len(parts) == 2 && strings.EqualFold(parts[0], "Bearer") && parts[1] == apiKey {
						ctx := context.WithValue(r.Context(), ContextKeyUser, "apikey")
						ctx = context.WithValue(ctx, ContextKeyRole, "apikey")
						next(w, r.WithContext(ctx))
						return
					}
				}
			}

			writeJSON(w, http.StatusUnauthorized, map[string]string{
				"error": "unauthorized: valid session or API key required",
			})
		}
	}
}
