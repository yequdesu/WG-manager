package store

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// ── Role enforcement ────────────────────────────────────────────────────

func TestRoleConstants(t *testing.T) {
	if RoleOwner == "" {
		t.Error("RoleOwner should be non-empty")
	}
	if RoleAdmin == "" {
		t.Error("RoleAdmin should be non-empty")
	}
	if RoleUser == "" {
		t.Error("RoleUser should be non-empty")
	}
	if RoleOwner == RoleAdmin {
		t.Error("RoleOwner and RoleAdmin should be distinct")
	}
	if RoleOwner == RoleUser {
		t.Error("RoleOwner and RoleUser should be distinct")
	}
	if RoleAdmin == RoleUser {
		t.Error("RoleAdmin and RoleUser should be distinct")
	}
}

func TestUserHasRole(t *testing.T) {
	u := &User{Name: "test", Role: RoleAdmin}

	if !u.HasRole(RoleAdmin) {
		t.Error("HasRole(RoleAdmin) should be true for admin")
	}
	if u.HasRole(RoleOwner) {
		t.Error("HasRole(RoleOwner) should be false for admin")
	}
	if u.IsAdmin() != true {
		t.Error("IsAdmin() should be true for admin")
	}
	if u.IsOwner() {
		t.Error("IsOwner() should be false for admin")
	}

	owner := &User{Name: "owner", Role: RoleOwner}
	if !owner.IsOwner() {
		t.Error("IsOwner() should be true for owner")
	}
	if owner.IsAdmin() {
		t.Error("IsAdmin() should be false for owner")
	}

	user := &User{Name: "user", Role: RoleUser}
	if !user.HasRole(RoleUser) {
		t.Error("HasRole(RoleUser) should be true for user")
	}
	if user.IsOwner() || user.IsAdmin() {
		t.Error("IsOwner/IsAdmin should be false for plain user")
	}
}

func TestPermissionMatrix(t *testing.T) {
	s := NewState("/tmp/test_perm.json", nil)

	// bootstrap owner
	hash, err := HashPassword("boot-pass")
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if err := s.BootstrapOwner(hash); err != nil {
		t.Fatalf("BootstrapOwner failed: %v", err)
	}

	// verify the bootstrap owner is RoleOwner
	owner, ok := s.GetUser("admin")
	if !ok {
		t.Fatal("bootstrap owner should exist")
	}
	if owner.Role != RoleOwner {
		t.Errorf("bootstrap owner role = %q, want RoleOwner", owner.Role)
	}

	// cannot create a duplicate owner (bootstrap again should fail)
	if err := s.BootstrapOwner(hash); err == nil {
		t.Error("second BootstrapOwner should fail")
	}

	// owner can create another user with role admin
	if err := s.AddUser("admin2", hash, RoleAdmin); err != nil {
		t.Errorf("AddUser admin2 failed: %v", err)
	}
	admin2, ok := s.GetUser("admin2")
	if !ok || admin2.Role != RoleAdmin {
		t.Errorf("admin2 role = %q, want RoleAdmin", admin2.Role)
	}

	// owner can create a user with role user
	if err := s.AddUser("user1", hash, RoleUser); err != nil {
		t.Errorf("AddUser user1 failed: %v", err)
	}
	user1, ok := s.GetUser("user1")
	if !ok || user1.Role != RoleUser {
		t.Errorf("user1 role = %q, want RoleUser", user1.Role)
	}

	// cannot create duplicate names via AddUser
	if err := s.AddUser("admin", hash, RoleAdmin); err == nil {
		t.Error("AddUser with duplicate name should fail")
	}
}

// ── Session lifecycle ───────────────────────────────────────────────────

func TestCreateSession(t *testing.T) {
	s := NewState("/tmp/test_session.json", nil)

	rawToken, err := s.CreateSession("testuser", RoleUser, 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}
	if rawToken == "" {
		t.Error("CreateSession should return non-empty token")
	}
	if len(rawToken) != 64 {
		t.Errorf("raw token length = %d, want 64 hex chars", len(rawToken))
	}

	// verify the session exists in state
	tokenHash := ""
	s.mu.RLock()
	for h, sess := range s.Sessions {
		if sess.UserName == "testuser" && sess.Role == RoleUser {
			tokenHash = h
			break
		}
	}
	s.mu.RUnlock()

	if tokenHash == "" {
		t.Fatal("session not found in state")
	}

	sess, ok := s.GetSessionByTokenHash(tokenHash)
	if !ok {
		t.Fatal("GetSessionByTokenHash should find the session")
	}
	if sess.UserName != "testuser" {
		t.Errorf("session UserName = %q, want %q", sess.UserName, "testuser")
	}
	if sess.Role != RoleUser {
		t.Errorf("session Role = %q, want %q", sess.Role, RoleUser)
	}
	if sess.TokenHash != tokenHash {
		t.Error("session TokenHash mismatch")
	}
}

func TestGetSessionByTokenHash(t *testing.T) {
	s := NewState("/tmp/test_get_session.json", nil)

	rawToken, err := s.CreateSession("testuser", RoleAdmin, 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// compute the token hash the same way GenerateSessionToken does
	tokenHash := ""
	s.mu.RLock()
	for h := range s.Sessions {
		tokenHash = h
		break
	}
	s.mu.RUnlock()

	sess, ok := s.GetSessionByTokenHash(tokenHash)
	if !ok {
		t.Fatal("GetSessionByTokenHash should find the session")
	}
	if sess.UserName != "testuser" {
		t.Errorf("UserName = %q, want %q", sess.UserName, "testuser")
	}

	// verify rawToken is not the hash itself
	if rawToken == tokenHash {
		t.Error("raw token should not equal its hash")
	}

	// lookup with a bogus hash should return false
	_, ok = s.GetSessionByTokenHash("deadbeef")
	if ok {
		t.Error("GetSessionByTokenHash should return false for unknown hash")
	}
}

func TestSessionExpiry(t *testing.T) {
	s := NewState("/tmp/test_session_expiry.json", nil)

	_, err := s.CreateSession("testuser", RoleUser, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	// get the token hash
	tokenHash := ""
	s.mu.RLock()
	for h := range s.Sessions {
		tokenHash = h
		break
	}
	s.mu.RUnlock()

	// wait for the session to expire
	time.Sleep(5 * time.Millisecond)

	// ExpireSessions should clean it up
	expired := s.ExpireSessions()
	if len(expired) != 1 {
		t.Errorf("ExpireSessions returned %d sessions, want 1", len(expired))
	}

	// it should be gone now
	_, ok := s.GetSessionByTokenHash(tokenHash)
	if ok {
		t.Error("session should not be found after expiry")
	}
}

func TestDeleteSession(t *testing.T) {
	s := NewState("/tmp/test_del_session.json", nil)

	_, err := s.CreateSession("testuser", RoleUser, 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession failed: %v", err)
	}

	tokenHash := ""
	s.mu.RLock()
	for h := range s.Sessions {
		tokenHash = h
		break
	}
	s.mu.RUnlock()

	if err := s.DeleteSession(tokenHash); err != nil {
		t.Errorf("DeleteSession failed: %v", err)
	}

	_, ok := s.GetSessionByTokenHash(tokenHash)
	if ok {
		t.Error("session should not exist after deletion")
	}

	// deleting again should fail
	if err := s.DeleteSession(tokenHash); err == nil {
		t.Error("second DeleteSession should fail")
	}
}

func TestExpireSessions(t *testing.T) {
	s := NewState("/tmp/test_expire_sessions.json", nil)

	// create an expired session
	_, err := s.CreateSession("expired", RoleUser, 1*time.Millisecond)
	if err != nil {
		t.Fatalf("CreateSession expired failed: %v", err)
	}

	// create a valid session
	_, err = s.CreateSession("valid", RoleAdmin, 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateSession valid failed: %v", err)
	}

	time.Sleep(5 * time.Millisecond)

	expired := s.ExpireSessions()
	if len(expired) != 1 {
		t.Errorf("ExpireSessions returned %d sessions, want 1", len(expired))
	}

	// the valid session should still exist
	s.mu.RLock()
	count := len(s.Sessions)
	s.mu.RUnlock()
	if count != 1 {
		t.Errorf("remaining sessions count = %d, want 1", count)
	}
}

// ── Password hashing ────────────────────────────────────────────────────

func TestHashAndVerifyPassword(t *testing.T) {
	password := "correct-horse-battery-staple"

	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}
	if hash == "" {
		t.Error("hash should be non-empty")
	}
	if hash == password {
		t.Error("hash should not equal the password")
	}

	// correct password
	if !VerifyPassword(hash, password) {
		t.Error("VerifyPassword should succeed for correct password")
	}

	// wrong password
	if VerifyPassword(hash, "wrong-password") {
		t.Error("VerifyPassword should fail for wrong password")
	}

	// empty password
	if VerifyPassword(hash, "") {
		t.Error("VerifyPassword should fail for empty password")
	}
}

func TestAuthenticateUser(t *testing.T) {
	s := NewState("/tmp/test_auth.json", nil)

	password := "my-secret"
	hash, err := HashPassword(password)
	if err != nil {
		t.Fatalf("HashPassword failed: %v", err)
	}

	if err := s.AddUser("alice", hash, RoleAdmin); err != nil {
		t.Fatalf("AddUser failed: %v", err)
	}

	// successful auth
	user, ok := s.AuthenticateUser("alice", password)
	if !ok {
		t.Error("AuthenticateUser should succeed with correct password")
	}
	if user == nil {
		t.Fatal("AuthenticateUser returned nil user on success")
	}
	if user.Role != RoleAdmin {
		t.Errorf("user role = %q, want %q", user.Role, RoleAdmin)
	}

	// wrong password
	_, ok = s.AuthenticateUser("alice", "wrong-pass")
	if ok {
		t.Error("AuthenticateUser should fail with wrong password")
	}

	// non-existent user
	_, ok = s.AuthenticateUser("nobody", password)
	if ok {
		t.Error("AuthenticateUser should fail for non-existent user")
	}
}

// ── Invite lifecycle ────────────────────────────────────────────────────

func TestCreateInvite(t *testing.T) {
	s := NewState("/tmp/test_create_invite.json", nil)

	rawToken, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	if rawToken == "" {
		t.Error("CreateInvite should return non-empty raw token")
	}
	if len(rawToken) != 64 {
		t.Errorf("raw token length = %d, want 64", len(rawToken))
	}
	if inv.Status != InviteCreated {
		t.Errorf("invite status = %q, want %q", inv.Status, InviteCreated)
	}
	if inv.IssuedBy != "admin" {
		t.Errorf("invite IssuedBy = %q, want %q", inv.IssuedBy, "admin")
	}
	if inv.TokenHash == "" {
		t.Error("invite TokenHash should be non-empty")
	}
	if inv.RawToken != rawToken {
		t.Error("invite RawToken should persist the raw token for later link display")
	}
	if inv.ID == "" {
		t.Error("invite ID should be non-empty")
	}
	if inv.CreatedAt == "" {
		t.Error("invite CreatedAt should be non-empty")
	}
	if inv.ExpiresAt == "" {
		t.Error("invite ExpiresAt should be set when expiry > 0")
	}

	// retrieve by token hash
	found, ok := s.GetInviteByTokenHash(inv.TokenHash)
	if !ok {
		t.Fatal("GetInviteByTokenHash should find the invite")
	}
	if found.ID != inv.ID {
		t.Errorf("found invite ID mismatch")
	}

	// retrieve by ID
	found2, ok := s.GetInviteByID(inv.ID)
	if !ok {
		t.Fatal("GetInviteByID should find the invite")
	}
	if found2.TokenHash != inv.TokenHash {
		t.Errorf("found invite TokenHash mismatch")
	}
	if found2.RawToken != rawToken {
		t.Errorf("found invite RawToken mismatch")
	}
}

func TestPeerByPublicKeyPrefix(t *testing.T) {
	s := NewState("/tmp/test_peer_prefix.json", nil)
	s.Peers = map[string]Peer{
		"peer1": {Name: "peer1", PublicKey: "RoJ7SRMQC7ZuAbCDeFgHiJkLmNoPqRsT1234567890="},
		"peer2": {Name: "peer2", PublicKey: "RoJ7aBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789+/"},
		"peer3": {Name: "peer3", PublicKey: "AAAAbBcDeFgHiJkLmNoPqRsTuVwXyZ0123456789=="},
	}

	p, ok, err := s.PeerByPublicKeyPrefix("RoJ7SRMQC7ZuAbCDeFgHiJkLmNoPqRsT1234567890=")
	if err != nil {
		t.Fatalf("exact match returned error: %v", err)
	}
	if !ok {
		t.Fatal("exact match should return ok")
	}
	if p.Name != "peer1" {
		t.Fatalf("exact match name = %q, want %q", p.Name, "peer1")
	}

	p, ok, err = s.PeerByPublicKeyPrefix("AAAA")
	if err != nil {
		t.Fatalf("prefix match returned error: %v", err)
	}
	if !ok {
		t.Fatal("prefix match should return ok")
	}
	if p.Name != "peer3" {
		t.Fatalf("prefix match name = %q, want %q", p.Name, "peer3")
	}

	_, ok, err = s.PeerByPublicKeyPrefix("RoJ7")
	if err == nil {
		t.Fatal("ambiguous prefix should return error")
	}
	if ok {
		t.Fatal("ambiguous prefix should not return ok")
	}
	if !strings.Contains(err.Error(), "ambiguous") {
		t.Fatalf("ambiguous error = %q, want to contain %q", err.Error(), "ambiguous")
	}

	_, ok, err = s.PeerByPublicKeyPrefix("RoJ")
	if err == nil {
		t.Fatal("short prefix should return error")
	}
	if ok {
		t.Fatal("short prefix should not return ok")
	}
	if !strings.Contains(err.Error(), "at least 4 characters") {
		t.Fatalf("short prefix error = %q, want to contain %q", err.Error(), "at least 4 characters")
	}

	_, ok, err = s.PeerByPublicKeyPrefix("XXXX")
	if err != nil {
		t.Fatalf("no match should not return error: %v", err)
	}
	if ok {
		t.Fatal("no match should not return ok")
	}
}

func TestInviteStatusConstants(t *testing.T) {
	if InviteCreated == "" {
		t.Error("InviteCreated should be non-empty")
	}
	if InviteRedeemed == "" {
		t.Error("InviteRedeemed should be non-empty")
	}
	if InviteRevoked == "" {
		t.Error("InviteRevoked should be non-empty")
	}
	if InviteExpired == "" {
		t.Error("InviteExpired should be non-empty")
	}

	if InviteCreated != "created" {
		t.Errorf("InviteCreated = %q, want %q", InviteCreated, "created")
	}
	if InviteRedeemed != "redeemed" {
		t.Errorf("InviteRedeemed = %q, want %q", InviteRedeemed, "redeemed")
	}
	if InviteRevoked != "revoked" {
		t.Errorf("InviteRevoked = %q, want %q", InviteRevoked, "revoked")
	}
	if InviteExpired != "expired" {
		t.Errorf("InviteExpired = %q, want %q", InviteExpired, "expired")
	}
}

func TestInviteReplay(t *testing.T) {
	s := NewState("/tmp/test_invite_replay.json", nil)

	_, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// first redeem succeeds
	redeemed, err := s.RedeemInvite(inv.ID, "peer1")
	if err != nil {
		t.Fatalf("first RedeemInvite failed: %v", err)
	}
	if redeemed.Status != InviteRedeemed {
		t.Errorf("redeemed status = %q, want %q", redeemed.Status, InviteRedeemed)
	}
	if redeemed.RedeemedBy != "peer1" {
		t.Errorf("RedeemedBy = %q, want %q", redeemed.RedeemedBy, "peer1")
	}

	// second redeem fails (already redeemed)
	_, err = s.RedeemInvite(inv.ID, "peer2")
	if err == nil {
		t.Error("second RedeemInvite should fail (already redeemed)")
	}

	// also test via RedeemInviteByTokenHash
	_, err = s.RedeemInviteByTokenHash(inv.TokenHash, "peer2")
	if err == nil {
		t.Error("second RedeemInviteByTokenHash should fail (already redeemed)")
	}
}

func TestInviteExpiry(t *testing.T) {
	s := NewState("/tmp/test_invite_expiry.json", nil)

	// create invite with very short expiry
	_, inv, err := s.CreateInvite("admin", 1*time.Millisecond)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// wait for it to expire
	time.Sleep(5 * time.Millisecond)

	// GetInviteByTokenHash should still return it (it only looks up, doesn't check expiry)
	found, ok := s.GetInviteByTokenHash(inv.TokenHash)
	if !ok {
		t.Error("GetInviteByTokenHash should still return expired invite")
	}
	if found.ID != inv.ID {
		t.Error("found invite ID mismatch")
	}

	// RedeemInviteByTokenHash should reject it as expired
	_, err = s.RedeemInviteByTokenHash(inv.TokenHash, "peer1")
	if err == nil {
		t.Error("RedeemInviteByTokenHash should fail for expired invite")
	}
	if err.Error() != "invite has expired" {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestInviteRevoke(t *testing.T) {
	s := NewState("/tmp/test_invite_revoke.json", nil)

	_, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// revoke
	if err := s.RevokeInvite(inv.ID); err != nil {
		t.Fatalf("RevokeInvite failed: %v", err)
	}

	// verify status
	revoked, ok := s.GetInviteByID(inv.ID)
	if !ok {
		t.Fatal("revoked invite should still exist")
	}
	if revoked.Status != InviteRevoked {
		t.Errorf("revoked status = %q, want %q", revoked.Status, InviteRevoked)
	}
	if revoked.RevokedAt == "" {
		t.Error("RevokedAt should be set")
	}

	// cannot revoke twice
	if err := s.RevokeInvite(inv.ID); err == nil {
		t.Error("second RevokeInvite should fail")
	}

	// cannot redeem a revoked invite
	_, err = s.RedeemInvite(revoked.ID, "peer1")
	if err == nil {
		t.Error("RedeemInvite should fail for revoked invite")
	}
}

func TestDuplicateName(t *testing.T) {
	s := NewState("/tmp/test_dup_name.json", nil)

	// create invite, redeem it
	_, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}
	if _, err := s.RedeemInvite(inv.ID, "test-peer"); err != nil {
		t.Fatalf("first RedeemInvite failed: %v", err)
	}

	// add a peer with the same name (simulating what the handler does after redeem)
	if err := s.AddPeer(Peer{Name: "test-peer"}); err != nil {
		t.Fatalf("first AddPeer failed: %v", err)
	}

	// create another invite, redeem it with the same peer name
	_, inv2, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("second CreateInvite failed: %v", err)
	}
	if _, err := s.RedeemInvite(inv2.ID, "test-peer"); err != nil {
		t.Fatalf("second RedeemInvite succeeded (invite lifecycle is separate from peer name check): %v", err)
	}

	// but adding another peer with the same name should fail
	if err := s.AddPeer(Peer{Name: "test-peer"}); err == nil {
		t.Error("second AddPeer with duplicate name should fail")
	}
}

// ── Invite token storage ────────────────────────────────────────────────

func TestInviteTokenStorage(t *testing.T) {
	s := NewState("/tmp/test_token_storage.json", nil)

	rawToken, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// serialize the state to JSON
	data, err := json.Marshal(s)
	if err != nil {
		t.Fatalf("marshal failed: %v", err)
	}
	jsonStr := string(data)

	// the JSON should contain the token_hash
	if !strings.Contains(jsonStr, inv.TokenHash) {
		t.Error("marshaled JSON should contain the token_hash")
	}
	if !strings.Contains(jsonStr, `"token_hash"`) {
		t.Error("marshaled JSON should contain token_hash field")
	}

	// The raw token is intentionally retained so local admins can re-display
	// the full onboarding link for already-issued invites.
	if !strings.Contains(jsonStr, rawToken) {
		t.Error("marshaled JSON should contain the raw token for invite link redisplay")
	}
	if !strings.Contains(jsonStr, `"raw_token"`) {
		t.Error("marshaled JSON should contain raw_token field")
	}
}

func TestGenerateInviteToken(t *testing.T) {
	raw1, hash1, err := GenerateInviteToken()
	if err != nil {
		t.Fatalf("GenerateInviteToken failed: %v", err)
	}

	// raw token should be 64 hex chars
	if len(raw1) != 64 {
		t.Errorf("raw token length = %d, want 64", len(raw1))
	}

	// hash should be 64 hex chars (SHA-256)
	if len(hash1) != 64 {
		t.Errorf("token hash length = %d, want 64", len(hash1))
	}

	// raw != hash
	if raw1 == hash1 {
		t.Error("raw token should not equal its hash")
	}

	// different each call
	raw2, hash2, err := GenerateInviteToken()
	if err != nil {
		t.Fatalf("second GenerateInviteToken failed: %v", err)
	}
	if raw1 == raw2 {
		t.Error("two calls should produce different raw tokens")
	}
	if hash1 == hash2 {
		t.Error("two calls should produce different token hashes")
	}

	// all hex characters
	for _, c := range raw1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("raw token contains non-hex character: %c", c)
			break
		}
	}
	for _, c := range hash1 {
		if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f')) {
			t.Errorf("hash contains non-hex character: %c", c)
			break
		}
	}
}

// ── Peer alias migration ────────────────────────────────────────────────

func TestPeerAliasMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	// Write old-format state: peer with no "alias" field.
	oldJSON := `{
  "peers": {
    "old-peer": {
      "name": "old-peer",
      "public_key": "fake-pubkey",
      "private_key": "fake-privkey",
      "address": "10.0.0.2",
      "dns": "1.1.1.1",
      "keepalive": 25,
      "created_at": "2024-01-01T00:00:00Z"
    }
  }
}`
	if err := os.WriteFile(path, []byte(oldJSON), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	s, mi, err := Load(path, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	if mi.PeerAliases != 1 {
		t.Errorf("PeerAliases = %d, want 1", mi.PeerAliases)
	}

	p, ok := s.GetPeer("old-peer")
	if !ok {
		t.Fatal("peer not found after load")
	}
	if p.Alias != p.Name {
		t.Errorf("Alias = %q, want Name = %q", p.Alias, p.Name)
	}
	if p.Alias != "old-peer" {
		t.Errorf("Alias = %q, want %q", p.Alias, "old-peer")
	}

	// Peer that already has an alias should not be double-counted.
	oldJSON2 := `{
  "peers": {
    "alias-peer": {
      "name": "alias-peer",
      "alias": "my-alias",
      "public_key": "abc",
      "private_key": "def",
      "address": "10.0.0.3",
      "dns": "1.1.1.1",
      "keepalive": 25,
      "created_at": "2024-01-01T00:00:00Z"
    }
  }
}`
	path2 := filepath.Join(dir, "peers2.json")
	if err := os.WriteFile(path2, []byte(oldJSON2), 0644); err != nil {
		t.Fatalf("write temp file 2: %v", err)
	}

	s2, mi2, err := Load(path2, nil)
	if err != nil {
		t.Fatalf("Load 2 failed: %v", err)
	}
	if mi2.PeerAliases != 0 {
		t.Errorf("PeerAliases = %d, want 0 (already had alias)", mi2.PeerAliases)
	}
	p2, _ := s2.GetPeer("alias-peer")
	if p2.Alias != "my-alias" {
		t.Errorf("Alias = %q, want %q", p2.Alias, "my-alias")
	}
}

// ── Invite field backfill migration ──────────────────────────────────────

func TestInviteMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "peers.json")

	// Old-format invites: no max_uses, no target_role, no used_count.
	oldJSON := `{
  "invites": {
    "inv-created": {
      "id": "inv-created",
      "token_hash": "aaa",
      "issued_by": "admin",
      "status": "created",
      "created_at": "2024-01-01T00:00:00Z"
    },
    "inv-redeemed": {
      "id": "inv-redeemed",
      "token_hash": "bbb",
      "issued_by": "admin",
      "status": "redeemed",
      "created_at": "2024-01-01T00:00:00Z",
      "redeemed_at": "2024-01-02T00:00:00Z",
      "redeemed_by": "peer1"
    },
    "inv-revoked": {
      "id": "inv-revoked",
      "token_hash": "ccc",
      "issued_by": "admin",
      "status": "revoked",
      "created_at": "2024-01-01T00:00:00Z",
      "revoked_at": "2024-01-02T00:00:00Z"
    }
  }
}`
	if err := os.WriteFile(path, []byte(oldJSON), 0644); err != nil {
		t.Fatalf("write temp file: %v", err)
	}

	s, mi, err := Load(path, nil)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}

	// All 3 invites should need backfill.
	if mi.Invites != 3 {
		t.Errorf("Invites migrated = %d, want 3", mi.Invites)
	}

	// Created invite: MaxUses=1, TargetRole="user", UsedCount=0
	inv1, ok := s.GetInviteByID("inv-created")
	if !ok {
		t.Fatal("inv-created not found")
	}
	if inv1.MaxUses != 1 {
		t.Errorf("inv-created MaxUses = %d, want 1", inv1.MaxUses)
	}
	if inv1.TargetRole != "user" {
		t.Errorf("inv-created TargetRole = %q, want %q", inv1.TargetRole, "user")
	}
	if inv1.UsedCount != 0 {
		t.Errorf("inv-created UsedCount = %d, want 0", inv1.UsedCount)
	}

	// Redeemed invite: MaxUses=1, TargetRole="user", UsedCount=1
	inv2, ok := s.GetInviteByID("inv-redeemed")
	if !ok {
		t.Fatal("inv-redeemed not found")
	}
	if inv2.MaxUses != 1 {
		t.Errorf("inv-redeemed MaxUses = %d, want 1", inv2.MaxUses)
	}
	if inv2.TargetRole != "user" {
		t.Errorf("inv-redeemed TargetRole = %q, want %q", inv2.TargetRole, "user")
	}
	if inv2.UsedCount != 1 {
		t.Errorf("inv-redeemed UsedCount = %d, want 1", inv2.UsedCount)
	}

	// Revoked invite: MaxUses=1, TargetRole="user", UsedCount=1
	inv3, ok := s.GetInviteByID("inv-revoked")
	if !ok {
		t.Fatal("inv-revoked not found")
	}
	if inv3.MaxUses != 1 {
		t.Errorf("inv-revoked MaxUses = %d, want 1", inv3.MaxUses)
	}
	if inv3.TargetRole != "user" {
		t.Errorf("inv-revoked TargetRole = %q, want %q", inv3.TargetRole, "user")
	}
	if inv3.UsedCount != 1 {
		t.Errorf("inv-revoked UsedCount = %d, want 1", inv3.UsedCount)
	}

	// Invite that already has MaxUses and TargetRole should not be double-counted.
	alreadyOK := `{
  "invites": {
    "inv-ok": {
      "id": "inv-ok",
      "token_hash": "ddd",
      "issued_by": "admin",
      "status": "created",
      "created_at": "2024-01-01T00:00:00Z",
      "max_uses": 5,
      "target_role": "admin",
      "used_count": 0
    }
  }
}`
	path2 := filepath.Join(dir, "peers2.json")
	if err := os.WriteFile(path2, []byte(alreadyOK), 0644); err != nil {
		t.Fatalf("write temp file 2: %v", err)
	}

	s2, mi2, err := Load(path2, nil)
	if err != nil {
		t.Fatalf("Load 2 failed: %v", err)
	}
	if mi2.Invites != 0 {
		t.Errorf("Invites migrated = %d, want 0 (already has fields)", mi2.Invites)
	}
	invOK, _ := s2.GetInviteByID("inv-ok")
	if invOK.MaxUses != 5 {
		t.Errorf("MaxUses = %d, want 5", invOK.MaxUses)
	}
	if invOK.TargetRole != "admin" {
		t.Errorf("TargetRole = %q, want %q", invOK.TargetRole, "admin")
	}
}

// ── Pool IP allocation ───────────────────────────────────────────────────

func TestPoolAllocation(t *testing.T) {
	_, subnet, err := net.ParseCIDR("10.0.0.0/24")
	if err != nil {
		t.Fatalf("parse subnet: %v", err)
	}

	// Valid pool.
	pool, err := NewPool("clients", net.ParseIP("10.0.0.10"), net.ParseIP("10.0.0.20"), subnet)
	if err != nil {
		t.Fatalf("NewPool valid: %v", err)
	}
	if pool.Name != "clients" {
		t.Errorf("Name = %q, want %q", pool.Name, "clients")
	}
	if pool.StartIP.String() != "10.0.0.10" {
		t.Errorf("StartIP = %s, want 10.0.0.10", pool.StartIP)
	}
	if pool.EndIP.String() != "10.0.0.20" {
		t.Errorf("EndIP = %s, want 10.0.0.20", pool.EndIP)
	}

	// Pool must not include server IP (.1).
	_, err = NewPool("bad", net.ParseIP("10.0.0.1"), net.ParseIP("10.0.0.10"), subnet)
	if err == nil {
		t.Error("NewPool should reject pool containing server IP (.1)")
	}

	// Pool must be within subnet.
	_, err = NewPool("bad", net.ParseIP("10.0.1.1"), net.ParseIP("10.0.1.10"), subnet)
	if err == nil {
		t.Error("NewPool should reject pool outside subnet")
	}

	// Start must be <= end.
	_, err = NewPool("bad", net.ParseIP("10.0.0.20"), net.ParseIP("10.0.0.10"), subnet)
	if err == nil {
		t.Error("NewPool should reject start > end")
	}

	// Empty name.
	_, err = NewPool("", net.ParseIP("10.0.0.10"), net.ParseIP("10.0.0.20"), subnet)
	if err == nil {
		t.Error("NewPool should reject empty name")
	}

	// ── Allocation within bounds ────────────────────────────────────────

	s := NewState("/tmp/test_pool_alloc.json", nil)
	s.SetPools(map[string]*Pool{"clients": pool})

	// Allocate first IP.
	ip1, err := s.AllocateIPInPool(pool, "10.0.0.0/24", nil)
	if err != nil {
		t.Fatalf("first allocation: %v", err)
	}
	if ip1 != "10.0.0.10" {
		t.Errorf("first IP = %s, want 10.0.0.10", ip1)
	}

	// Allocate second IP with first one marked as used.
	used := map[string]bool{ip1: true}
	ip2, err := s.AllocateIPInPool(pool, "10.0.0.0/24", used)
	if err != nil {
		t.Fatalf("second allocation: %v", err)
	}
	if ip2 != "10.0.0.11" {
		t.Errorf("second IP = %s, want 10.0.0.11", ip2)
	}

	// Allocate third IP.
	used[ip2] = true
	ip3, err := s.AllocateIPInPool(pool, "10.0.0.0/24", used)
	if err != nil {
		t.Fatalf("third allocation: %v", err)
	}
	if ip3 != "10.0.0.12" {
		t.Errorf("third IP = %s, want 10.0.0.12", ip3)
	}

	// ── Pool exhaustion ─────────────────────────────────────────────────

	// Fill all remaining IPs in the pool range (10.0.0.13 through 10.0.0.20 = 8 IPs).
	exhausted := map[string]bool{
		"10.0.0.10": true,
		"10.0.0.11": true,
		"10.0.0.12": true,
		"10.0.0.13": true,
		"10.0.0.14": true,
		"10.0.0.15": true,
		"10.0.0.16": true,
		"10.0.0.17": true,
		"10.0.0.18": true,
		"10.0.0.19": true,
		"10.0.0.20": true,
	}
	_, err = s.AllocateIPInPool(pool, "10.0.0.0/24", exhausted)
	if err == nil {
		t.Error("allocation should fail when pool is exhausted")
	}

	// ── Pool contains ───────────────────────────────────────────────────

	if !pool.ContainsStr("10.0.0.10") {
		t.Error("pool should contain 10.0.0.10")
	}
	if !pool.ContainsStr("10.0.0.20") {
		t.Error("pool should contain 10.0.0.20 (inclusive end)")
	}
	if pool.ContainsStr("10.0.0.9") {
		t.Error("pool should not contain 10.0.0.9 (below range)")
	}
	if pool.ContainsStr("10.0.0.21") {
		t.Error("pool should not contain 10.0.0.21 (above range)")
	}

	// ── Allocations stay within pool bounds ──────────────────────────────

	// With some peers, allocation must stay within pool range.
	s2 := NewState("/tmp/test_pool_bounds.json", nil)
	s2.SetPools(map[string]*Pool{"clients": pool})

	// Add a peer occupying an IP outside the pool but within the subnet.
	if err := s2.AddPeer(Peer{Name: "peer1", Address: "10.0.0.30", PublicKey: "pk1", PrivateKey: "sk1"}); err != nil {
		t.Fatalf("AddPeer: %v", err)
	}

	ip, err := s2.AllocateIPInPool(pool, "10.0.0.0/24", nil)
	if err != nil {
		t.Fatalf("allocation with existing peers: %v", err)
	}
	if ip != "10.0.0.10" {
		t.Errorf("IP with existing peers = %s, want 10.0.0.10", ip)
	}

	// The allocation should not be affected by peer outside pool range.
	if ip == "10.0.0.30" {
		t.Error("should not allocate an IP outside the pool range")
	}
}

// ── ParsePools ───────────────────────────────────────────────────────────

func TestParsePools(t *testing.T) {
	pools, err := ParsePools("10.0.0.0/24", map[string]string{
		"clients": "10.0.0.10-10.0.0.100",
	})
	if err != nil {
		t.Fatalf("ParsePools: %v", err)
	}
	if len(pools) != 1 {
		t.Fatalf("len(pools) = %d, want 1", len(pools))
	}
	pool, ok := pools["clients"]
	if !ok {
		t.Fatal("clients pool not found")
	}
	if pool.StartIP.String() != "10.0.0.10" {
		t.Errorf("StartIP = %s, want 10.0.0.10", pool.StartIP)
	}
	if pool.EndIP.String() != "10.0.0.100" {
		t.Errorf("EndIP = %s, want 10.0.0.100", pool.EndIP)
	}

	// Invalid range format.
	_, err = ParsePools("10.0.0.0/24", map[string]string{
		"bad": "not-a-range",
	})
	if err == nil {
		t.Error("ParsePools should reject invalid range format")
	}

	// Overlapping pools.
	_, err = ParsePools("10.0.0.0/24", map[string]string{
		"a": "10.0.0.10-10.0.0.50",
		"b": "10.0.0.30-10.0.0.60",
	})
	if err == nil {
		t.Error("ParsePools should reject overlapping pools")
	}

	// Pool outside subnet.
	_, err = ParsePools("10.0.0.0/24", map[string]string{
		"bad": "10.0.1.10-10.0.1.100",
	})
	if err == nil {
		t.Error("ParsePools should reject pool outside subnet")
	}
}

// ── Invite MaxUses enforcement ───────────────────────────────────────────

func TestInviteMaxUses(t *testing.T) {
	s := NewState("/tmp/test_max_uses.json", nil)

	// Create invite with MaxUses=1.
	_, inv, err := s.CreateInvite("admin", 1*time.Hour, WithMaxUses(1))
	if err != nil {
		t.Fatalf("CreateInvite: %v", err)
	}
	if inv.MaxUses != 1 {
		t.Errorf("MaxUses = %d, want 1", inv.MaxUses)
	}
	if inv.UsedCount != 0 {
		t.Errorf("UsedCount = %d, want 0", inv.UsedCount)
	}

	// First redeem succeeds.
	_, err = s.RedeemInvite(inv.ID, "peer1")
	if err != nil {
		t.Fatalf("first RedeemInvite: %v", err)
	}

	// After first redeem, status is Redeemed → second redeem-by-ID fails.
	_, err = s.RedeemInvite(inv.ID, "peer2")
	if err == nil {
		t.Error("second RedeemInvite should fail (already redeemed)")
	}

	// Use UnredeemByTokenHash to roll back status to "created"
	// while keeping UsedCount at 1.
	if err := s.UnredeemByTokenHash(inv.TokenHash); err != nil {
		t.Fatalf("UnredeemByTokenHash: %v", err)
	}

	// Now status is back to "created" but UsedCount is still 1.
	loopedBack, _ := s.GetInviteByID(inv.ID)
	if loopedBack.UsedCount != 1 {
		t.Errorf("UsedCount after unredeem = %d, want 1", loopedBack.UsedCount)
	}
	if loopedBack.Status != InviteCreated {
		t.Errorf("Status after unredeem = %q, want %q", loopedBack.Status, InviteCreated)
	}

	// RedeemInvite should now fail because UsedCount(1) >= MaxUses(1).
	_, err = s.RedeemInvite(inv.ID, "peer2")
	if err == nil {
		t.Error("RedeemInvite after unredeem+max uses reached: should fail")
	}
	if err.Error() != `invite "`+inv.ID+`" has reached max uses (1)` {
		t.Errorf("unexpected error: %v", err)
	}

	// Same for RedeemInviteByTokenHash.
	_, err = s.RedeemInviteByTokenHash(inv.TokenHash, "peer2")
	if err == nil {
		t.Error("RedeemInviteByTokenHash after unredeem+max uses reached: should fail")
	}
	if err.Error() != "invite has reached max uses (1)" {
		t.Errorf("unexpected RedeemInviteByTokenHash error: %v", err)
	}

	// Create a multi-use invite with MaxUses=2.
	_, inv2, err := s.CreateInvite("admin", 1*time.Hour, WithMaxUses(2))
	if err != nil {
		t.Fatalf("CreateInvite multi-use: %v", err)
	}

	// First redeem succeeds.
	_, err = s.RedeemInvite(inv2.ID, "peer-a")
	if err != nil {
		t.Fatalf("first redeem of multi-use: %v", err)
	}
	// Verify UsedCount incremented.
	inv2after, _ := s.GetInviteByID(inv2.ID)
	if inv2after.UsedCount != 1 {
		t.Errorf("UsedCount after first redeem = %d, want 1", inv2after.UsedCount)
	}
	if inv2after.MaxUses != 2 {
		t.Errorf("MaxUses = %d, want 2", inv2after.MaxUses)
	}

	// Verify MaxUses defaults to 1 when not specified.
	_, invDefault, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite default: %v", err)
	}
	if invDefault.MaxUses != 1 {
		t.Errorf("default MaxUses = %d, want 1", invDefault.MaxUses)
	}
}

// ── CLI status contract regressions ──────────────────────────────────────

func TestStatusCommandDecodesStringListenPort(t *testing.T) {
	stdout, stderr, err := runStatusCLI(t, `{"interface":"wg0","listen_port":"51820","daemon":"running","wireguard":"ok","peer_online":3,"peer_total":5}`, true)
	if err != nil {
		t.Fatalf("status should accept daemon listen_port payload as-is; got err=%v\nstderr:\n%s\nstdout:\n%s", err, stderr, stdout)
	}
	if !strings.Contains(stdout, "listen_port: 51820") {
		t.Fatalf("status output missing listen_port line:\n%s", stdout)
	}
}

func TestStatusCommandRendersZeroPeersDeterministically(t *testing.T) {
	stdout, stderr, err := runStatusCLI(t, `{"interface":"wg0","listen_port":51820,"daemon":"running","wireguard":"ok","peer_online":0,"peer_total":0}`, true)
	if err != nil {
		t.Fatalf("status should render zero-peer state without error; got err=%v\nstderr:\n%s\nstdout:\n%s", err, stderr, stdout)
	}
	want := "peers: 0 online / 0 total"
	if !strings.Contains(stdout, want) {
		t.Fatalf("status output should include %q for zero peers; got:\n%s", want, stdout)
	}
}

func TestStatusCommandWithoutCredentialsShowsActionableAuthError(t *testing.T) {
	stdout, stderr, err := runStatusCLI(t, `{"interface":"wg0","listen_port":51820,"daemon":"running","wireguard":"ok","peer_online":0,"peer_total":0}`, false)
	if err == nil {
		t.Fatalf("expected status to fail without API key or session token; stdout=%q", stdout)
	}
	if stdout != "" {
		t.Fatalf("expected no stdout on auth failure, got %q", stdout)
	}
	want := "status requires an API key in config.env or a session token via MGMT_SESSION_TOKEN or --session-token"
	if !strings.Contains(stderr, want) {
		t.Fatalf("status auth error should be actionable; want %q in stderr, got:\n%s", want, stderr)
	}
}

func runStatusCLI(t *testing.T, responseBody string, withAPIKey bool) (stdout, stderr string, err error) {
	t.Helper()

	if withAPIKey {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/api/v1/status" {
				return
			}
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(responseBody))
		}))
		defer server.Close()

		configPath := writeCLIConfig(t, strings.TrimPrefix(server.URL, "http://"), "test-api-key")
		return invokeWGMgmtStatus(t, configPath)
	}

	configPath := writeCLIConfig(t, "127.0.0.1:58880", "")
	return invokeWGMgmtStatus(t, configPath)
}

func writeCLIConfig(t *testing.T, mgmtListen, apiKey string) string {
	t.Helper()

	dir := t.TempDir()
	configPath := filepath.Join(dir, "config.env")
	var content bytes.Buffer
	content.WriteString("MGMT_LISTEN=")
	content.WriteString(mgmtListen)
	content.WriteString("\n")
	if apiKey != "" {
		content.WriteString("MGMT_API_KEY=")
		content.WriteString(apiKey)
		content.WriteString("\n")
	}
	if err := os.WriteFile(configPath, content.Bytes(), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return configPath
}

func invokeWGMgmtStatus(t *testing.T, configPath string) (stdout, stderr string, err error) {
	t.Helper()

	repoRoot, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatalf("resolve repo root: %v", err)
	}

	cmd := exec.Command("go", "run", "./cmd/wg-mgmt", "--config", configPath, "status")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "MGMT_SESSION_TOKEN=")

	var stdoutBuf bytes.Buffer
	var stderrBuf bytes.Buffer
	cmd.Stdout = &stdoutBuf
	cmd.Stderr = &stderrBuf
	err = cmd.Run()
	return stdoutBuf.String(), stderrBuf.String(), err
}

// ── Force-delete invite ─────────────────────────────────────────────────

func TestForceDeleteInviteCreated(t *testing.T) {
	s := NewState("/tmp/test_force_del_created.json", nil)

	_, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// Force-delete a created invite.
	if err := s.ForceDeleteInvite(inv.ID); err != nil {
		t.Fatalf("ForceDeleteInvite failed: %v", err)
	}

	// It should be gone.
	_, ok := s.GetInviteByID(inv.ID)
	if ok {
		t.Error("Force-deleted invite should not exist")
	}

	// Force-delete again should fail.
	if err := s.ForceDeleteInvite(inv.ID); err == nil {
		t.Error("Second ForceDeleteInvite should fail")
	}
}

func TestForceDeleteInviteRedeemed(t *testing.T) {
	s := NewState("/tmp/test_force_del_redeemed.json", nil)

	_, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// Redeem it first.
	if _, err := s.RedeemInvite(inv.ID, "peer1"); err != nil {
		t.Fatalf("RedeemInvite failed: %v", err)
	}

	// Force-delete a redeemed invite (not allowed by soft-delete).
	if err := s.ForceDeleteInvite(inv.ID); err != nil {
		t.Fatalf("ForceDeleteInvite on redeemed invite failed: %v", err)
	}

	_, ok := s.GetInviteByID(inv.ID)
	if ok {
		t.Error("Force-deleted redeemed invite should not exist")
	}
}

func TestForceDeleteInviteDeleted(t *testing.T) {
	s := NewState("/tmp/test_force_del_deleted.json", nil)

	_, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// Soft-delete first.
	if err := s.DeleteInvite(inv.ID, "admin"); err != nil {
		t.Fatalf("DeleteInvite failed: %v", err)
	}

	// Verify it's soft-deleted.
	softDel, ok := s.GetInviteByID(inv.ID)
	if !ok || softDel.Status != InviteDeleted {
		t.Fatal("Invite should be soft-deleted")
	}

	// Force-delete should work on already-soft-deleted invite.
	if err := s.ForceDeleteInvite(inv.ID); err != nil {
		t.Fatalf("ForceDeleteInvite on soft-deleted invite failed: %v", err)
	}

	_, ok = s.GetInviteByID(inv.ID)
	if ok {
		t.Error("Force-deleted invite should not exist after force-delete")
	}
}

func TestForceDeleteInviteRevoked(t *testing.T) {
	s := NewState("/tmp/test_force_del_revoked.json", nil)

	_, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// Revoke it.
	if err := s.RevokeInvite(inv.ID); err != nil {
		t.Fatalf("RevokeInvite failed: %v", err)
	}

	// Force-delete should work on revoked invite.
	if err := s.ForceDeleteInvite(inv.ID); err != nil {
		t.Fatalf("ForceDeleteInvite on revoked invite failed: %v", err)
	}

	_, ok := s.GetInviteByID(inv.ID)
	if ok {
		t.Error("Force-deleted revoked invite should not exist")
	}
}

func TestForceDeleteInviteByTokenHash(t *testing.T) {
	s := NewState("/tmp/test_force_del_byhash.json", nil)

	_, inv, err := s.CreateInvite("admin", 1*time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite failed: %v", err)
	}

	// Redeem it.
	if _, err := s.RedeemInvite(inv.ID, "peer1"); err != nil {
		t.Fatalf("RedeemInvite failed: %v", err)
	}

	// Force-delete by token hash (which is the canonical way an external
	// caller with only the raw token would identify the invite).
	if err := s.ForceDeleteInviteByTokenHash(inv.TokenHash); err != nil {
		t.Fatalf("ForceDeleteInviteByTokenHash failed: %v", err)
	}

	_, ok := s.GetInviteByTokenHash(inv.TokenHash)
	if ok {
		t.Error("Force-deleted invite should not be found by token hash")
	}

	// Second attempt should fail.
	if err := s.ForceDeleteInviteByTokenHash(inv.TokenHash); err == nil {
		t.Error("Second ForceDeleteInviteByTokenHash should fail")
	}
}
