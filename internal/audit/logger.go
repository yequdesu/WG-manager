package audit

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

var (
	mu          sync.Mutex
	path        string
	fd          *os.File
	ready       bool
	warnedNoLog bool
)

func Init(logPath string) error {
	mu.Lock()
	defer mu.Unlock()
	dir := filepath.Dir(logPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return fmt.Errorf("create audit dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("open audit log %s: %w", logPath, err)
	}
	fd = f
	path = logPath
	ready = true
	warnedNoLog = false
	return nil
}

func CurrentPath() string {
	mu.Lock()
	defer mu.Unlock()
	return path
}

func Close() {
	mu.Lock()
	defer mu.Unlock()
	if fd != nil {
		fd.Close()
		fd = nil
	}
	ready = false
}

func Log(event string, fields map[string]string) {
	mu.Lock()
	if !ready || fd == nil {
		if !warnedNoLog {
			fmt.Fprintf(os.Stderr, "[wg-mgmt] audit log not initialized — events are not being recorded\n")
			warnedNoLog = true
		}
		mu.Unlock()
		return
	}

	var b strings.Builder
	b.WriteString(time.Now().UTC().Format(time.RFC3339))
	b.WriteString(" ")
	b.WriteString(event)

	for _, key := range []string{"name", "ip", "source", "admin", "reason", "request_id", "version"} {
		if v, ok := fields[key]; ok && v != "" {
			b.WriteString(fmt.Sprintf(" %s=%s", key, v))
		}
	}
	b.WriteString("\n")
	line := b.String()
	mu.Unlock()

	if _, err := fd.WriteString(line); err != nil {
		mu.Lock()
		fmt.Fprintf(os.Stderr, "[wg-mgmt] audit write failed: %v — disabling audit logging\n", err)
		ready = false
		warnedNoLog = false
		mu.Unlock()
		return
	}
	if err := fd.Sync(); err != nil {
		mu.Lock()
		fmt.Fprintf(os.Stderr, "[wg-mgmt] audit sync failed: %v — disabling audit logging\n", err)
		ready = false
		warnedNoLog = false
		mu.Unlock()
	}
}
