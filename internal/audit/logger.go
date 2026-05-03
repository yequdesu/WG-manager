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
		return fmt.Errorf("create log dir %s: %w", dir, err)
	}
	f, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0640)
	if err != nil {
		return fmt.Errorf("open log file %s: %w", logPath, err)
	}
	if fd != nil {
		fd.Close()
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

func Write(module, event string, fields map[string]string) {
	mu.Lock()
	defer mu.Unlock()

	if !ready || fd == nil {
		if !warnedNoLog {
			fmt.Fprintf(os.Stderr, "[wg-mgmt] audit log not initialized — events are not being recorded\n")
			warnedNoLog = true
		}
		return
	}

	var b strings.Builder
	b.WriteString(time.Now().UTC().Format("2006-01-02T15:04:05.000000Z"))
	b.WriteString(" [")
	b.WriteString(module)
	b.WriteString("] ")
	b.WriteString(event)

	for _, key := range []string{"name", "ip", "source", "admin", "peer", "endpoint", "old", "new", "status", "method", "path", "duration", "reason", "request_id", "retry", "version", "interface", "port", "dns", "online", "total"} {
		if v, ok := fields[key]; ok && v != "" {
			b.WriteString(" ")
			b.WriteString(key)
			b.WriteString("=")
			b.WriteString(v)
		}
	}
	b.WriteString("\n")

	if _, err := fd.WriteString(b.String()); err != nil {
		fmt.Fprintf(os.Stderr, "[wg-mgmt] log write failed: %v — disabling\n", err)
		ready = false
		warnedNoLog = false
		return
	}
	if err := fd.Sync(); err != nil {
		fmt.Fprintf(os.Stderr, "[wg-mgmt] log sync failed: %v — disabling\n", err)
		ready = false
		warnedNoLog = false
	}
}

// Log is kept for backward compatibility — writes with [DAEMON] module.
func Log(event string, fields map[string]string) {
	Write("DAEMON", event, fields)
}
