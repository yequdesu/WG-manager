package cli

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
)

func LoadConfig(path string) (mgmtAddr, apiKey string, err error) {
	if path == "" {
		path = "config.env"
	}

	mgmtAddr = "127.0.0.1:58880"
	apiKey = ""

	data, err := os.ReadFile(path)
	if err != nil {
		origErr := err
		if home, homeErr := os.UserHomeDir(); homeErr == nil && home != "" {
			fallback := filepath.Join(home, "WG-manager", "config.env")
			data, err = os.ReadFile(fallback)
		}
		if err != nil {
			data, err = os.ReadFile("/opt/WG-manager/config.env")
		}
		if err != nil {
			return "", "", fmt.Errorf("read config file %s: %w", path, origErr)
		}
	}

	for line := range strings.SplitSeq(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		key, value, found := strings.Cut(line, "=")
		if !found {
			continue
		}

		switch strings.TrimSpace(key) {
		case "MGMT_LISTEN":
			addr := strings.TrimSpace(value)
			if addr == "" {
				continue
			}

			host, port, splitErr := net.SplitHostPort(addr)
			if splitErr != nil {
				if strings.Contains(addr, ":") {
					if strings.HasPrefix(addr, "0.0.0.0:") {
						mgmtAddr = "127.0.0.1" + addr[len("0.0.0.0"):]
					} else {
						mgmtAddr = addr
					}
				}
				continue
			}

			if host == "0.0.0.0" || host == "" {
				host = "127.0.0.1"
			}
			mgmtAddr = net.JoinHostPort(host, port)
		case "MGMT_API_KEY":
			apiKey = strings.TrimSpace(value)
		}
	}

	return mgmtAddr, apiKey, nil
}
