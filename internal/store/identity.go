package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"

	"golang.org/x/crypto/bcrypt"
)

// Role represents a fixed user role in the human identity model.
type Role string

const (
	RoleOwner Role = "owner"
	RoleAdmin Role = "admin"
	RoleUser  Role = "user"
)

// User represents a human operator in the management plane.
// It is structurally separate from Peer (WireGuard device identity).
type User struct {
	Name         string `json:"name"`
	Role         Role   `json:"role"`
	PasswordHash string `json:"password_hash"`
	CreatedAt    string `json:"created_at"`
}

// Session represents an authenticated session for a user.
// TokenHash is the SHA-256 hash of the bearer token; the raw token is
// never persisted.
type Session struct {
	TokenHash string `json:"token_hash"`
	UserName  string `json:"user_name"`
	Role      Role   `json:"role"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

// HasRole reports whether the user has the given role.
func (u *User) HasRole(target Role) bool {
	return u.Role == target
}

// IsOwner is a shorthand for HasRole(RoleOwner).
func (u *User) IsOwner() bool {
	return u.Role == RoleOwner
}

// IsAdmin is a shorthand for HasRole(RoleAdmin).
func (u *User) IsAdmin() bool {
	return u.Role == RoleAdmin
}

// ── Password helpers ───────────────────────────────────────────────────

// HashPassword returns a bcrypt hash of the given password.
func HashPassword(password string) (string, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return "", fmt.Errorf("bcrypt hash: %w", err)
	}
	return string(hash), nil
}

// VerifyPassword reports whether the password matches the bcrypt hash.
func VerifyPassword(hash, password string) bool {
	err := bcrypt.CompareHashAndPassword([]byte(hash), []byte(password))
	return err == nil
}

// ── Token helpers ───────────────────────────────────────────────────────

// GenerateSessionToken returns a cryptographically random session token and
// its SHA-256 hash. The raw token is 32 bytes (64 hex chars); only the hash
// is persisted.
func GenerateSessionToken() (rawToken string, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate token: %w", err)
	}
	rawToken = hex.EncodeToString(raw)

	sum := sha256.Sum256([]byte(rawToken))
	tokenHash = hex.EncodeToString(sum[:])
	return rawToken, tokenHash, nil
}

// ── Store methods ──────────────────────────────────────────────────────

// HasUsers returns true if at least one user exists.
func (s *State) HasUsers() bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.Users) > 0
}

// AddUser creates a new user. Returns an error if the name already exists.
func (s *State) AddUser(name, passwordHash string, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Users[name]; ok {
		return fmt.Errorf("user %q already exists", name)
	}

	if s.Users == nil {
		s.Users = make(map[string]User)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	s.Users[name] = User{
		Name:         name,
		Role:         role,
		PasswordHash: passwordHash,
		CreatedAt:    now,
	}
	return nil
}

// GetUser returns a user by name and a boolean indicating existence.
func (s *State) GetUser(name string) (User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.Users[name]
	return u, ok
}

// ListUsers returns a slice of all users.
func (s *State) ListUsers() []User {
	s.mu.RLock()
	defer s.mu.RUnlock()

	users := make([]User, 0, len(s.Users))
	for _, u := range s.Users {
		users = append(users, u)
	}
	return users
}

// UpdateUser updates an existing user's role. Returns an error if the user
// does not exist.
func (s *State) UpdateUser(name string, role Role) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.Users[name]
	if !ok {
		return fmt.Errorf("user %q not found", name)
	}

	u.Role = role
	s.Users[name] = u
	return nil
}

// UpdateUserPassword updates an existing user's password hash.
// Returns an error if the user does not exist.
func (s *State) UpdateUserPassword(name, newPasswordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	u, ok := s.Users[name]
	if !ok {
		return fmt.Errorf("user %q not found", name)
	}

	u.PasswordHash = newPasswordHash
	s.Users[name] = u
	return nil
}

// DeleteUser removes a user by name. Returns an error if the user does
// not exist.
func (s *State) DeleteUser(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Users[name]; !ok {
		return fmt.Errorf("user %q not found", name)
	}

	delete(s.Users, name)
	return nil
}

// BootstrapOwner creates the first owner user (name "admin") iff no users
// exist. The passwordHash must already be a bcrypt hash.
// Returns an error if any user already exists.
func (s *State) BootstrapOwner(passwordHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.Users) > 0 {
		return fmt.Errorf("bootstrap refused: users already exist")
	}

	if s.Users == nil {
		s.Users = make(map[string]User)
	}

	now := time.Now().UTC().Format(time.RFC3339)
	s.Users["admin"] = User{
		Name:         "admin",
		Role:         RoleOwner,
		PasswordHash: passwordHash,
		CreatedAt:    now,
	}
	return nil
}

// ── Session methods ────────────────────────────────────────────────────

// CreateSession generates a session token for the given user and stores it
// with the specified duration. The raw token is returned to the caller.
func (s *State) CreateSession(userName string, role Role, duration time.Duration) (string, error) {
	rawToken, tokenHash, err := GenerateSessionToken()
	if err != nil {
		return "", fmt.Errorf("create session: %w", err)
	}

	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Sessions == nil {
		s.Sessions = make(map[string]Session)
	}

	s.Sessions[tokenHash] = Session{
		TokenHash: tokenHash,
		UserName:  userName,
		Role:      role,
		CreatedAt: now.Format(time.RFC3339),
		ExpiresAt: now.Add(duration).Format(time.RFC3339),
	}
	return rawToken, nil
}

// GetSessionByTokenHash looks up a session by its token hash.
func (s *State) GetSessionByTokenHash(tokenHash string) (Session, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	sess, ok := s.Sessions[tokenHash]
	return sess, ok
}

// DeleteSession removes a session by its token hash.
func (s *State) DeleteSession(tokenHash string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Sessions[tokenHash]; !ok {
		return fmt.Errorf("session not found")
	}
	delete(s.Sessions, tokenHash)
	return nil
}

// ExpireSessions removes all sessions whose ExpiresAt is before now and
// returns the list of removed sessions.
func (s *State) ExpireSessions() []Session {
	now := time.Now().UTC()
	s.mu.Lock()
	defer s.mu.Unlock()

	var expired []Session
	for hash, sess := range s.Sessions {
		expiresAt, err := time.Parse(time.RFC3339, sess.ExpiresAt)
		if err != nil || expiresAt.Before(now) {
			expired = append(expired, sess)
			delete(s.Sessions, hash)
		}
	}
	return expired
}

// ── Authentication ─────────────────────────────────────────────────────

// AuthenticateUser checks the given name and password against stored users.
// Returns a pointer to the User and true on success.
func (s *State) AuthenticateUser(name, password string) (*User, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	u, ok := s.Users[name]
	if !ok {
		return nil, false
	}

	if !VerifyPassword(u.PasswordHash, password) {
		return nil, false
	}

	cp := u
	return &cp, true
}
