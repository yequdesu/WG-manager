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

// ── PREFLIGHT: Dependency check before redeem ───────────────────────────

// TestBootstrapPreflightDependencyCheck asserts that the bootstrap script
// validates JSON-parser availability (jq or python3) BEFORE calling
// POST /api/v1/redeem.  Without this check, the script silently loses the
// invite token when neither parser exists.
func TestBootstrapPreflightDependencyCheck(t *testing.T) {
	cfg := &Config{
		ServerPublicIP: "vpn.example.com",
		DefaultDNS:     "1.1.1.1",
		WGServerIP:     "10.0.0.1",
	}
	h := newTestHandler(cfg)
	script := getBootstrapScript(t, h)

	// Locate the redeem curl call.
	redeemIdx := strings.Index(script, `$SERVER_URL/api/v1/redeem`)
	if redeemIdx < 0 {
		t.Fatal("could not find redeem curl call in bootstrap script")
	}

	// Everything before the redeem call.
	prefix := script[:redeemIdx]

	// Look for a deliberate preflight gate that checks BOTH jq AND python3
	// together and exits before reaching the redeem call.  This is distinct
	// from the json_get() function definition, which also references those
	// commands but does not gate the redeem call.
	//
	// Expected pattern (pseudocode):
	//   if ! command -v jq && ! command -v python3; then
	//       err "need jq or python3 to parse config"
	//       exit 1
	//   fi
	hasExplicitPreflight := regexp.MustCompile(
		`command -v jq.*command -v python3|command -v python3.*command -v jq`,
	).MatchString(prefix)

	if !hasExplicitPreflight {
		t.Error(`Bootstrap script has NO preflight dependency check before calling redeem.
The script directly calls POST /api/v1/redeem without first verifying that
jq or python3 is available for parsing the JSON response.

On a system without jq or python3, the script will:
  1. POST to /api/v1/redeem (consuming the invite server-side)
  2. Fail to parse the response via json_get → SUCCESS=""
  3. Exit with "unknown error" leaving the token irretrievably consumed

The json_get() function definition (which contains command -v jq/python3) is
not a preflight gate — it silently returns the default on parse failure.
A preflight check must abort the script BEFORE the HTTP POST.`)
		return
	}

	// The preflight check must abort when the check fails.
	// Look for exit within ~100 chars after the dependency check.
	_, after, _ := strings.Cut(prefix, "command -v python3")
	if !strings.Contains(after[:min(len(after), 100)], "exit") {
		t.Error("preflight dependency check found but missing exit on failure")
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
//   SUCCESS=$(json_get "$RESP" "success" "")
//   if [ "$SUCCESS" != "true" ]; then
//       err "Failed to redeem invite: $(json_get "$RESP" "error" "unknown error")"
//       exit 1
//   fi
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
	if successIdx > 0 && successIdx+200 < len(suffix) {
		// Look at the ~300 chars after SUCCESS extraction for recovery messaging.
		end := successIdx + 300
		if end > len(suffix) {
			end = len(suffix)
		}
		afterSuccess = suffix[successIdx:end]
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
