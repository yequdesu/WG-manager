package store

import (
	"fmt"
	"time"
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
