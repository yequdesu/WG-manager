package store

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const (
	canonicalAuditLogPath = "/var/log/wg-mgmt/wg-mgmt.log"
	legacyAuditLogPath    = "/var/log/wg-mgmt/audit.log"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

func readRepoFile(t *testing.T, relPath string) string {
	t.Helper()
	content, err := os.ReadFile(filepath.Join(repoRoot(t), relPath))
	if err != nil {
		t.Fatalf("read %s: %v", relPath, err)
	}
	return string(content)
}

func TestAuditLogPathIsCanonicalAcrossSetupDaemonAndTUI(t *testing.T) {
	checks := []struct {
		name string
		path string
	}{
		{name: "server/setup-server.sh", path: "server/setup-server.sh"},
		{name: "cmd/mgmt-daemon/main.go", path: "cmd/mgmt-daemon/main.go"},
		{name: "wg-tui/src/app.rs", path: "wg-tui/src/app.rs"},
	}

	for _, check := range checks {
		content := readRepoFile(t, check.path)
		if !strings.Contains(content, canonicalAuditLogPath) {
			t.Fatalf("%s does not reference canonical audit log path %q", check.name, canonicalAuditLogPath)
		}
		if strings.Contains(content, legacyAuditLogPath) {
			t.Fatalf("%s still references legacy audit log path %q", check.name, legacyAuditLogPath)
		}
	}
}

func TestAuditLogPermissionsAllowNonRootTUIRead(t *testing.T) {
	script := readRepoFile(t, "server/setup-server.sh")

	if !strings.Contains(script, `chmod 755 "$log_dir"`) {
		t.Fatalf("setup-server.sh should make the log directory world-executable so a non-root TUI can traverse it")
	}
	if !strings.Contains(script, "create 0644 root root") {
		t.Fatalf("setup-server.sh should create the log file world-readable so a non-root TUI can read it")
	}
}
