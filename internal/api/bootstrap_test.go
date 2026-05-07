package api

import (
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"testing"

	"wire-guard-dev/internal/store"
)

// newTestHandler creates a minimal Handler with a test store and cfg for
// testing the Bootstrap endpoint output (the embedded bash script).
func newTestHandler(cfg *Config) *Handler {
	s := store.NewState("/tmp/test_bootstrap.json", nil)
	return NewHandler(s, nil, cfg)
}

// getBootstrapScript calls the Bootstrap handler and returns the response body
// (the embedded bash script) as a string.
func getBootstrapScript(t *testing.T, h *Handler) string {
	t.Helper()

	req := httptest.NewRequest(http.MethodGet, "/bootstrap?token=testtoken&name=testpeer", nil)
	rec := httptest.NewRecorder()
	h.Bootstrap(rec, req)

	resp := rec.Result()
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("Bootstrap returned status %d, want 200", resp.StatusCode)
	}

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read response body: %v", err)
	}
	return string(raw)
}

// TestBootstrapFallbackParser asserts the bootstrap script includes a
// grep/sed fallback JSON parser for systems without jq or python3.
// The fallback must handle the known redeem JSON shape:
//
//	{"peer":{...},"success":true}
//
// Required extractable fields: success, error, and the nested peer fields
// (private_key, address, server_public_key, server_endpoint, dns, keepalive).
func TestBootstrapFallbackParser(t *testing.T) {
	cfg := &Config{
		ServerPublicIP: "vpn.example.com",
		DefaultDNS:     "1.1.1.1",
		WGServerIP:     "10.0.0.1",
	}
	h := newTestHandler(cfg)
	script := getBootstrapScript(t, h)

	// Fallback parser must handle the "success" key via grep.
	if !strings.Contains(script, `"success"`) && !strings.Contains(script, `success`) {
		t.Error("Bootstrap script does not handle success field in fallback parser")
	}

	// The script must contain grep/sed fallback patterns.
	fallbackIndicators := []string{
		"PARSER_MODE",
		"fallback",
		"grep",
		"sed",
	}
	for _, indicator := range fallbackIndicators {
		if !strings.Contains(script, indicator) {
			t.Errorf("Bootstrap script missing fallback parser indicator: %q", indicator)
		}
	}

	// The fallback json_get function must handle "success", "error", and
	// generic keys with string/numeric/boolean values.
	if !strings.Contains(script, `case "$key"`) {
		t.Error("Fallback json_get should use case-based key dispatch for known keys")
	}

	// json_get_nested must extract string values (cut -d'\"' -f4) and
	// numeric values (grep -oE '[0-9]+')
	if !strings.Contains(script, `cut -d'"' -f4`) && !strings.Contains(script, `cut -d'"'`) {
		t.Error("Fallback json_get_nested missing string value extraction (cut)")
	}
	if !strings.Contains(script, `[0-9]+`) {
		t.Error("Fallback json_get_nested missing numeric value extraction")
	}

	// The preflight must NOT exit immediately when jq/python3 missing;
	// it should fall through to check for grep/sed.
	// Verify by finding the parser strategy setting code.
	if !strings.Contains(script, `PARSER_OK=1`) {
		t.Error("Missing PARSER_OK fallback path when jq/python3 absent")
	}

	// Package manager detection must exist for interactive install prompts.
	pkgMgrPatterns := []string{
		"apt-get",
		"yum",
		"dnf",
		"apk",
		"brew",
	}
	foundPkgMgrs := 0
	for _, p := range pkgMgrPatterns {
		if strings.Contains(script, p) {
			foundPkgMgrs++
		}
	}
	if foundPkgMgrs < 3 {
		t.Errorf("Bootstrap script should detect at least 3 package managers, found %d", foundPkgMgrs)
	}

	// The parsers_suggest_install function must be present.
	if !strings.Contains(script, "parsers_suggest_install") {
		t.Error("Missing parsers_suggest_install function for interactive jq install prompt")
	}
}

// ── PREFLIGHT: Dependency check before redeem ───────────────────────────

// TestBootstrapPreflightDependencyCheck asserts the bootstrap script
// validates JSON-parser availability BEFORE calling redeem, with a
// three-tier strategy (jq → python3 → grep/sed fallback). The old
// behavior hard-exited when jq/python3 were missing; the new behavior
// falls through to grep/sed when available.
func TestBootstrapPreflightDependencyCheck(t *testing.T) {
	cfg := &Config{
		ServerPublicIP: "vpn.example.com",
		DefaultDNS:     "1.1.1.1",
		WGServerIP:     "10.0.0.1",
	}
	h := newTestHandler(cfg)
	script := getBootstrapScript(t, h)

	redeemIdx := strings.Index(script, `$SERVER_URL/api/v1/redeem`)
	if redeemIdx < 0 {
		t.Fatal("could not find redeem curl call in bootstrap script")
	}
	prefix := script[:redeemIdx]

	// Preflight must check jq, python3, and grep availability.
	if !regexp.MustCompile(`command -v jq`).MatchString(prefix) {
		t.Error("Preflight must check for jq")
	}
	if !regexp.MustCompile(`command -v python3`).MatchString(prefix) {
		t.Error("Preflight must check for python3")
	}
	if !regexp.MustCompile(`command -v grep`).MatchString(prefix) {
		t.Error("Preflight must check for grep (fallback)")
	}
	if !regexp.MustCompile(`command -v sed`).MatchString(prefix) {
		t.Error("Preflight must check for sed (fallback)")
	}

	// Must set PARSER_MODE and PARSER_OK to track strategy.
	if !strings.Contains(prefix, "PARSER_MODE=") {
		t.Error("Preflight must set PARSER_MODE before redeem")
	}
	if !strings.Contains(prefix, "PARSER_OK=1") {
		t.Error("Preflight must set PARSER_OK=1 when a parser is available")
	}

	// The fallback warning must be present.
	if !strings.Contains(script, "fallback") {
		t.Error("Script must reference fallback parser mode")
	}
}

// ── WSL detection ─────────────────────────────────────────────────────

// TestBootstrapWSLDetection asserts the script explicitly detects WSL
// (Windows Subsystem for Linux) and handles it differently from native
// Linux.  WSL has constraints around kernel modules (WireGuard must be
// installed on the Windows host, not inside WSL).
func TestBootstrapWSLDetection(t *testing.T) {
	cfg := &Config{
		ServerPublicIP: "vpn.example.com",
		DefaultDNS:     "1.1.1.1",
		WGServerIP:     "10.0.0.1",
	}
	h := newTestHandler(cfg)
	script := getBootstrapScript(t, h)

	// The detect_os() function (or an equivalent section) must contain
	// WSL detection logic.  Typical indicators:
	//   - /proc/version containing "microsoft" or "WSL"
	//   - /proc/sys/fs/binfmt_misc/WSLInterop
	//   - uname -r containing "microsoft" or "WSL"
	wslPatterns := []string{
		`/proc/version`,
		`microsoft`,
		`WSL`,
		`wsl`,
		`WSLInterop`,
	}

	scriptLower := strings.ToLower(script)

	// Collect which patterns matched.
	var matched []string
	for _, p := range wslPatterns {
		if strings.Contains(scriptLower, strings.ToLower(p)) {
			matched = append(matched, p)
		}
	}

	if len(matched) == 0 {
		t.Error(`Bootstrap script does NOT detect WSL.
The detect_os() function only handles Linux|Darwin|unknown.  WSL must be
detected explicitly because:
  1. WireGuard kernel module must be installed on the Windows host, not
     inside WSL (requires different instructions).
  2. Network stack differences between native Linux and WSL may affect
     routing and DNS resolution.
  3. README.md claims "Linux / macOS / WSL / Windows / Mobile (QR)" support.
`)
	} else {
		t.Logf("WSL-related patterns found: %v (check if detection is explicit enough)", matched)
	}

	// Additionally, the OS detection region (near detect_os or equivalent)
	// should mention "wsl" as a distinct case.
	if !strings.Contains(scriptLower, "wsl") {
		t.Error(`Bootstrap script OS detection does not reference "WSL" as a distinct case.`)
	}
}

// ── Consumed-token recovery messaging ─────────────────────────────────

// TestBootstrapConsumedTokenRecoveryMessage asserts that when the HTTP
// redeem succeeds (server returns 200 with success:true) but JSON parsing
// fails (e.g. no jq/python3 available), the script produces explicit
// recovery guidance instead of a generic "unknown error".
//
// The current behaviour is:
//
//	SUCCESS=$(json_get "$RESP" "success" "")
//	if [ "$SUCCESS" != "true" ]; then
//	    err "Failed to redeem invite: $(json_get "$RESP" "error" "unknown error")"
//	    exit 1
//	fi
//
// On a system without jq or python3, SUCCESS is always "" so the "error"
// branch fires with "unknown error" even though the server returned a 200.
// The invite is already consumed.
func TestBootstrapConsumedTokenRecoveryMessage(t *testing.T) {
	cfg := &Config{
		ServerPublicIP: "vpn.example.com",
		DefaultDNS:     "1.1.1.1",
		WGServerIP:     "10.0.0.1",
	}
	h := newTestHandler(cfg)
	script := getBootstrapScript(t, h)

	// Locate the section where the script parses the redeem response and
	// validates SUCCESS.  We are looking for any recovery/guidance logic
	// that handles the "redeem-OK but config-parse-fails" scenario.
	redeemIdx := strings.Index(script, `$SERVER_URL/api/v1/redeem`)
	if redeemIdx < 0 {
		t.Fatal("could not find redeem curl call in bootstrap script")
	}

	// The portion of the script AFTER the redeem call (to end of script).
	suffix := script[redeemIdx:]

	// Recovery guidance keywords — the script should mention at least one of
	// these when redeem succeeded but config delivery failed.
	recoveryPatterns := []string{
		"contact",   // "contact your administrator"
		"admin",     // "ask your admin"
		"recovery",  // explicit recovery guidance
		"consumed",  // "invite was consumed"
		"used",      // "token was used"
		"one-time",  // "one-time token"
		"token was", // warning about token consumption
		"already",   // "already redeemed"
		"curl",      // raw curl fallback for manual debugging
		"manual",    // manual recovery instructions
		"save",      // "save this output"
		"retry",     // "do not retry with same token"
	}

	afterSuccess := suffix
	successIdx := strings.Index(suffix, `SUCCESS=`)
	if successIdx > 0 {
		// Search from SUCCESS extraction to end of script for recovery messaging.
		afterSuccess = suffix[successIdx:]
	}

	var found []string
	for _, kw := range recoveryPatterns {
		if strings.Contains(strings.ToLower(afterSuccess), kw) {
			found = append(found, kw)
		}
	}

	if len(found) == 0 {
		t.Error(`Bootstrap script has NO consumed-token recovery messaging.
When the HTTP redeem succeeds but the script cannot parse the JSON response
(no jq, no python3), the user sees:
  [x] Failed to redeem invite: unknown error

What SHOULD happen:
  - The script recognizes the HTTP status was 200 (redeem succeeded)
  - It tells the user the token WAS consumed and cannot be reused
  - It provides actionable guidance: "contact your admin to re-issue an invite"
  - Or: shows the raw curl response so the user can manually extract config

Without this, the user loses their one-time token with no recourse.`)
	} else {
		t.Logf("Found recovery-related keywords: %v", found)
	}

	// CRITICAL: The current code does NOT distinguish between "redeem
	// failed server-side" and "redeem succeeded but we can't parse the
	// response".  We need to assert that the script checks HTTP status
	// code OR curl exit code before calling json_get, to differentiate
	// these two failure modes.
	hasHTTPStatusCheck := strings.Contains(suffix, "200") ||
		strings.Contains(suffix, "--write-out") ||
		strings.Contains(suffix, "HTTP_STATUS") ||
		strings.Contains(suffix, "status_code") ||
		strings.Contains(suffix, "http_code") ||
		strings.Contains(suffix, "-w") ||
		strings.Contains(suffix, "-o")

	if !hasHTTPStatusCheck {
		t.Error(`Bootstrap script does NOT check the HTTP response status code.
It relies solely on json_get() to parse the JSON body.  Without an HTTP status
check, the script cannot differentiate between:
  a) Server rejected the redeem (HTTP 409, invite already consumed)
  b) Server accepted the redeem (HTTP 200) but local JSON parsing failed
In case (b), the invite is consumed but the user gets no config and no
recovery message.`)
	}
}
