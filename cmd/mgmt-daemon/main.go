package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"wire-guard-dev/internal/api"
	"wire-guard-dev/internal/audit"
	"wire-guard-dev/internal/store"
	"wire-guard-dev/internal/wg"
)

type AppConfig struct {
	WGInterface          string
	WGPort               int
	WGSubnet             string
	WGServerIP           string
	MgmtListen           string
	APIKey               string
	ServerPublicIP       string
	DefaultDNS           string
	PeerKeepalive        int
	PeersDBPath          string
	WGConfPath           string
	AuditLogPath         string
}

func loadConfig(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	cfg := &AppConfig{
		WGInterface:          "wg0",
		WGPort:               51820,
		WGSubnet:             "10.0.0.0/24",
		WGServerIP:           "10.0.0.1/24",
		MgmtListen:           "0.0.0.0:58880",
		DefaultDNS:           "1.1.1.1,8.8.8.8",
		PeerKeepalive:        25,
		PeersDBPath:          "./server/peers.json",
		WGConfPath:           "/etc/wireguard/wg0.conf",
		AuditLogPath:         "/var/log/wg-mgmt/audit.log",
	}

	lines := strings.Split(string(data), "\n")
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "=", 2)
		if len(parts) != 2 {
			continue
		}
		key := strings.TrimSpace(parts[0])
		val := strings.TrimSpace(parts[1])

		switch key {
		case "WG_INTERFACE":
			cfg.WGInterface = val
		case "WG_PORT":
			if p, err := strconv.Atoi(val); err == nil {
				cfg.WGPort = p
			}
		case "WG_SUBNET":
			cfg.WGSubnet = val
		case "WG_SERVER_IP":
			cfg.WGServerIP = val
		case "MGMT_LISTEN":
			cfg.MgmtListen = val
		case "MGMT_API_KEY":
			cfg.APIKey = val
		case "SERVER_PUBLIC_IP":
			cfg.ServerPublicIP = val
		case "DEFAULT_DNS":
			cfg.DefaultDNS = val
		case "PEER_KEEPALIVE":
			if k, err := strconv.Atoi(val); err == nil {
				cfg.PeerKeepalive = k
			}
		case "PEERS_DB_PATH":
			cfg.PeersDBPath = val
		case "WG_CONF_PATH":
			cfg.WGConfPath = val
		case "AUDIT_LOG_PATH":
			cfg.AuditLogPath = val
		}
	}

	if cfg.ServerPublicIP == "" {
		cfg.ServerPublicIP = detectPublicIP()
	}

	return cfg, nil
}

func detectPublicIP() string {
	addrs := []string{
		"https://api.ipify.org",
		"https://ifconfig.me",
		"https://icanhazip.com",
	}

	client := &http.Client{Timeout: 5 * time.Second}
	for _, addr := range addrs {
		resp, err := client.Get(addr)
		if err != nil {
			continue
		}
		defer resp.Body.Close()
		buf := make([]byte, 64)
		n, _ := resp.Body.Read(buf)
		ip := strings.TrimSpace(string(buf[:n]))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return ""
}

func main() {
	configPath := "config.env"
	if len(os.Args) > 2 && os.Args[1] == "--config" {
		configPath = os.Args[2]
	} else {
		for _, arg := range os.Args[1:] {
			if strings.HasPrefix(arg, "--config=") {
				configPath = strings.TrimPrefix(arg, "--config=")
				break
			}
		}
	}

	appCfg, err := loadConfig(configPath)
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	state, err := store.Load(appCfg.PeersDBPath)
	if err != nil {
		log.Fatalf("Failed to load peer state: %v", err)
	}

	if err := audit.Init(appCfg.AuditLogPath); err != nil {
		log.Printf("Warning: audit log init failed: %v", err)
	} else {
		audit.Log("daemon_started", map[string]string{"version": "1.0.0"})
	}
	defer audit.Close()

	wgMgr := wg.NewManager()

	apiCfg := &api.Config{
		WGInterface:          appCfg.WGInterface,
		WGPort:               appCfg.WGPort,
		WGSubnet:             appCfg.WGSubnet,
		WGServerIP:           appCfg.WGServerIP,
		MgmtListen:           appCfg.MgmtListen,
		APIKey:               appCfg.APIKey,
		ServerPublicIP:       appCfg.ServerPublicIP,
		DefaultDNS:           appCfg.DefaultDNS,
		PeerKeepalive:        appCfg.PeerKeepalive,
		PeersDBPath:          appCfg.PeersDBPath,
		WGConfPath:           appCfg.WGConfPath,
	}

	srv := api.NewServer(apiCfg, state, wgMgr)

	idleConnsClosed := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		sig := <-sigCh
		log.Printf("Received signal %v, shutting down...", sig)

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()

		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("HTTP server shutdown error: %v", err)
		}
		close(idleConnsClosed)
	}()

	log.Printf("WireGuard Management Daemon starting on %s", appCfg.MgmtListen)
	log.Printf("WireGuard interface: %s, port: %d", appCfg.WGInterface, appCfg.WGPort)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}

	<-idleConnsClosed
	log.Println("Daemon stopped")
}
