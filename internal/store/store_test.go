package store

import (
	"encoding/json"
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

// ── Invite token hashing ────────────────────────────────────────────────

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

	// the JSON must NOT contain the raw token
	if strings.Contains(jsonStr, rawToken) {
		t.Error("marshaled JSON must NOT contain the raw token")
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
