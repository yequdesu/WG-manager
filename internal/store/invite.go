package store

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"time"
)

// InviteStatus represents the lifecycle state of an invite.
type InviteStatus string

const (
	InviteCreated   InviteStatus = "created"
	InviteRedeemed  InviteStatus = "redeemed"
	InviteRevoked   InviteStatus = "revoked"
	InviteExpired   InviteStatus = "expired"
)

// Invite is a first-class invitation model for zero-touch client onboarding.
// The raw token is never stored — only its SHA‑256 hash (TokenHash).
// No private keys or IP addresses are pre‑allocated until the invite is redeemed.
type Invite struct {
	ID              string       `json:"id"`
	TokenHash       string       `json:"token_hash"`
	IssuedBy        string       `json:"issued_by"`
	Status          InviteStatus `json:"status"`
	CreatedAt       string       `json:"created_at"`
	ExpiresAt       string       `json:"expires_at,omitempty"`
	RevokedAt       string       `json:"revoked_at,omitempty"`
	RedeemedAt      string       `json:"redeemed_at,omitempty"`
	RedeemedBy      string       `json:"redeemed_by,omitempty"`
	DisplayNameHint string       `json:"display_name_hint,omitempty"`
	DNSOverride     string       `json:"dns_override,omitempty"`
	ArtifactKind    string       `json:"artifact_kind,omitempty"`
	ClientCaps      string       `json:"client_capabilities,omitempty"`
	DeviceID        string       `json:"device_id,omitempty"`
}

// InviteOption is a functional option for CreateInvite.
type InviteOption func(*Invite)

// WithDisplayNameHint sets a display-name hint on the invite.
func WithDisplayNameHint(hint string) InviteOption {
	return func(i *Invite) {
		i.DisplayNameHint = hint
	}
}

// WithDNSOverride sets a DNS-override on the invite.
func WithDNSOverride(dns string) InviteOption {
	return func(i *Invite) {
		i.DNSOverride = dns
	}
}

// WithArtifactKind sets the artifact kind on the invite.
func WithArtifactKind(kind string) InviteOption {
	return func(i *Invite) {
		i.ArtifactKind = kind
	}
}

// WithClientCapabilities sets client-capability flags on the invite.
func WithClientCapabilities(caps string) InviteOption {
	return func(i *Invite) {
		i.ClientCaps = caps
	}
}

// WithDeviceID sets the device ID on the invite.
func WithDeviceID(id string) InviteOption {
	return func(i *Invite) {
		i.DeviceID = id
	}
}

// ── ID and token generation ────────────────────────────────────────────

// GenerateInviteID returns a cryptographically random invite ID.
// Uses the same 12‑byte → hex pattern as GenerateRequestID.
func GenerateInviteID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte((time.Now().UnixNano() >> (i * 4)) & 0xFF)
		}
	}
	return hex.EncodeToString(b)
}

// GenerateInviteToken returns a raw invite token and its SHA‑256 hash.
// The raw token is 32 bytes (64 hex chars); only the hash is persisted.
// Uses the same pattern as GenerateSessionToken.
func GenerateInviteToken() (rawToken string, tokenHash string, err error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", "", fmt.Errorf("generate invite token: %w", err)
	}
	rawToken = hex.EncodeToString(raw)

	sum := sha256.Sum256([]byte(rawToken))
	tokenHash = hex.EncodeToString(sum[:])
	return rawToken, tokenHash, nil
}

// ── Store methods ──────────────────────────────────────────────────────

// CreateInvite creates a new invite with the given issuer, optional expiry
// duration, and functional options. It returns the raw token (never persisted),
// the stored Invite, and any error.
func (s *State) CreateInvite(issuedBy string, expiry time.Duration, opts ...InviteOption) (string, Invite, error) {
	rawToken, tokenHash, err := GenerateInviteToken()
	if err != nil {
		return "", Invite{}, fmt.Errorf("create invite: %w", err)
	}

	id := GenerateInviteID()
	now := time.Now().UTC()

	inv := Invite{
		ID:        id,
		TokenHash: tokenHash,
		IssuedBy:  issuedBy,
		Status:    InviteCreated,
		CreatedAt: now.Format(time.RFC3339),
	}

	if expiry > 0 {
		inv.ExpiresAt = now.Add(expiry).Format(time.RFC3339)
	}

	for _, opt := range opts {
		opt(&inv)
	}

	s.mu.Lock()
	defer s.mu.Unlock()

	if s.Invites == nil {
		s.Invites = make(map[string]Invite)
	}

	s.Invites[id] = inv
	return rawToken, inv, nil
}

// GetInviteByID returns the invite with the given ID and whether it was found.
func (s *State) GetInviteByID(id string) (Invite, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	inv, ok := s.Invites[id]
	return inv, ok
}

// GetInviteByTokenHash returns the invite with the given token hash and
// whether it was found.
func (s *State) GetInviteByTokenHash(tokenHash string) (Invite, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, inv := range s.Invites {
		if inv.TokenHash == tokenHash {
			return inv, true
		}
	}
	return Invite{}, false
}

// ListInvites returns all invites.
func (s *State) ListInvites() []Invite {
	s.mu.RLock()
	defer s.mu.RUnlock()

	invites := make([]Invite, 0, len(s.Invites))
	for _, inv := range s.Invites {
		invites = append(invites, inv)
	}
	return invites
}

// RevokeInvite marks the invite with the given ID as revoked.
// Only invites in the "created" state can be revoked.
func (s *State) RevokeInvite(id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	inv, ok := s.Invites[id]
	if !ok {
		return fmt.Errorf("invite %q not found", id)
	}

	if inv.Status != InviteCreated {
		return fmt.Errorf("invite %q is already %s", id, inv.Status)
	}

	inv.Status = InviteRevoked
	inv.RevokedAt = time.Now().UTC().Format(time.RFC3339)
	s.Invites[id] = inv
	return nil
}

// RedeemInvite marks the invite with the given ID as redeemed by the given
// peer/device name. Only invites in the "created" state can be redeemed.
func (s *State) RedeemInvite(id string, redeemedBy string) (Invite, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	inv, ok := s.Invites[id]
	if !ok {
		return Invite{}, fmt.Errorf("invite %q not found", id)
	}

	if inv.Status != InviteCreated {
		return Invite{}, fmt.Errorf("invite %q is already %s", id, inv.Status)
	}

	now := time.Now().UTC()
	inv.Status = InviteRedeemed
	inv.RedeemedAt = now.Format(time.RFC3339)
	inv.RedeemedBy = redeemedBy
	s.Invites[id] = inv
	return inv, nil
}

// ExpireInvites removes invites whose ExpiresAt is before now and returns
// the list of expired invites.
func (s *State) ExpireInvites() []Invite {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	var expired []Invite
	for id, inv := range s.Invites {
		if inv.ExpiresAt == "" {
			continue
		}
		expAt, err := time.Parse(time.RFC3339, inv.ExpiresAt)
		if err != nil || now.After(expAt) {
			expired = append(expired, inv)
			delete(s.Invites, id)
		}
	}
	return expired
}
