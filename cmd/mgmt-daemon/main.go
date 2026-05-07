package main

import (
	"context"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
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
	WGInterface            string
	WGPort                 int
	WGSubnet               string
	WGServerIP             string
	MgmtListen             string
	APIKey                 string
	ServerPublicIP         string
	ServerHost             string
	DefaultDNS             string
	PeerKeepalive          int
	PeersDBPath            string
	WGConfPath             string
	AuditLogPath           string
	CleanPeersOnExit       bool
	BootstrapOwnerPassword string
	RawPools               map[string]string
}

func loadConfig(path string) (*AppConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config file %s: %w", path, err)
	}

	cfg := &AppConfig{
		WGInterface:   "wg0",
		WGPort:        51820,
		WGSubnet:      "10.0.0.0/24",
		WGServerIP:    "10.0.0.1/24",
		MgmtListen:    "127.0.0.1:58880",
		DefaultDNS:    "1.1.1.1,8.8.8.8",
		PeerKeepalive: 25,
		PeersDBPath:   "./server/peers.json",
		WGConfPath:    "/etc/wireguard/wg0.conf",
		AuditLogPath:  "/var/log/wg-mgmt/wg-mgmt.log",
		RawPools:      make(map[string]string),
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
			} else {
				log.Printf("Warning: invalid WG_PORT value %q: %v", val, err)
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
		case "SERVER_HOST":
			cfg.ServerHost = val
		case "DEFAULT_DNS":
			cfg.DefaultDNS = val
		case "PEER_KEEPALIVE":
			if k, err := strconv.Atoi(val); err == nil {
				cfg.PeerKeepalive = k
			} else {
				log.Printf("Warning: invalid PEER_KEEPALIVE value %q: %v", val, err)
			}
		case "PEERS_DB_PATH":
			cfg.PeersDBPath = val
		case "WG_CONF_PATH":
			cfg.WGConfPath = val
		case "AUDIT_LOG_PATH":
			cfg.AuditLogPath = val
		case "CLEAN_PEERS_ON_EXIT":
			cfg.CleanPeersOnExit = strings.EqualFold(val, "true") || val == "1"
		case "BOOTSTRAP_OWNER_PASSWORD":
			cfg.BootstrapOwnerPassword = val
		default:
			if strings.HasPrefix(key, "POOL_") {
				poolName := strings.TrimPrefix(key, "POOL_")
				if poolName != "" {
					cfg.RawPools[poolName] = val
				}
			}
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
		buf := make([]byte, 64)
		n, err := resp.Body.Read(buf)
		resp.Body.Close()
		if err != nil && err.Error() != "EOF" {
			continue
		}
		ip := strings.TrimSpace(string(buf[:n]))
		if net.ParseIP(ip) != nil {
			return ip
		}
	}
	return ""
}

func reloadConfig(path string, appCfg *AppConfig, handler *api.Handler, state *store.State, wgMgr *wg.Manager) error {
	newCfg, err := loadConfig(path)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	if newCfg.WGInterface != appCfg.WGInterface {
		log.Printf("Warning: WG_INTERFACE change (%s → %s) requires restart, ignoring", appCfg.WGInterface, newCfg.WGInterface)
		newCfg.WGInterface = appCfg.WGInterface
	}
	if newCfg.WGPort != appCfg.WGPort {
		log.Printf("Warning: WG_PORT change (%d → %d) requires restart, ignoring", appCfg.WGPort, newCfg.WGPort)
		newCfg.WGPort = appCfg.WGPort
	}
	if newCfg.WGSubnet != appCfg.WGSubnet {
		log.Printf("Warning: WG_SUBNET change (%s → %s) requires restart, ignoring", appCfg.WGSubnet, newCfg.WGSubnet)
		newCfg.WGSubnet = appCfg.WGSubnet
	}
	if newCfg.WGServerIP != appCfg.WGServerIP {
		log.Printf("Warning: WG_SERVER_IP change (%s → %s) requires restart, ignoring", appCfg.WGServerIP, newCfg.WGServerIP)
		newCfg.WGServerIP = appCfg.WGServerIP
	}
	if newCfg.MgmtListen != appCfg.MgmtListen {
		log.Printf("Warning: MGMT_LISTEN change (%s → %s) requires restart, ignoring", appCfg.MgmtListen, newCfg.MgmtListen)
		newCfg.MgmtListen = appCfg.MgmtListen
	}

	*appCfg = *newCfg

	newApiCfg := &api.Config{
		WGInterface:    appCfg.WGInterface,
		WGPort:         appCfg.WGPort,
		WGSubnet:       appCfg.WGSubnet,
		WGServerIP:     appCfg.WGServerIP,
		MgmtListen:     appCfg.MgmtListen,
		APIKey:         appCfg.APIKey,
		ServerPublicIP: appCfg.ServerPublicIP,
		ServerHost:     appCfg.ServerHost,
		DefaultDNS:     appCfg.DefaultDNS,
		PeerKeepalive:  appCfg.PeerKeepalive,
		PeersDBPath:    appCfg.PeersDBPath,
		WGConfPath:     appCfg.WGConfPath,
	}
	handler.ReloadConfig(newApiCfg)

	var crypto *store.Crypto
	if appCfg.APIKey != "" {
		crypto = store.NewCrypto(appCfg.APIKey)
	}

	newState, _, err := store.Load(appCfg.PeersDBPath, crypto)
	if err != nil {
		log.Printf("Warning: failed to reload peers db (%s): %v — keeping current state", appCfg.PeersDBPath, err)
	} else {
		state.Replace(newState)
		log.Printf("Reloaded %d peer(s) from %s", len(newState.AllPeers()), appCfg.PeersDBPath)
	}

	if appCfg.AuditLogPath != "" {
		audit.Close()
		if err := audit.Init(appCfg.AuditLogPath); err != nil {
			log.Printf("Warning: audit re-init to %s: %v", appCfg.AuditLogPath, err)
		} else {
			log.Printf("Audit log reopened at %s", appCfg.AuditLogPath)
		}
	}

	if len(appCfg.RawPools) > 0 {
		pools, err := store.ParsePools(appCfg.WGSubnet, appCfg.RawPools)
		if err != nil {
			log.Printf("Warning: Pool config error on reload: %v — keeping current pools", err)
		} else {
			state.SetPools(pools)
			log.Printf("Reloaded %d address pool(s)", len(pools))
		}
	}

	peerMap := make(map[string]wg.PeerInfo)
	for _, p := range state.AllPeers() {
		peerMap[p.Name] = wg.PeerInfo{Alias: p.Alias, PubKey: p.PublicKey, Address: p.Address, Keepalive: p.Keepalive}
	}
	if err := wg.WriteFullConfig(appCfg.WGConfPath, appCfg.WGInterface, appCfg.WGPort, appCfg.PeerKeepalive, appCfg.WGServerIP, state.Server().PrivateKey, peerMap); err != nil {
		log.Printf("Warning: failed to write config on reload: %v", err)
	}

	return nil
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

	// Convert relative paths to absolute so daemon works from any working directory
	if !filepath.IsAbs(appCfg.PeersDBPath) {
		if abs, err := filepath.Abs(appCfg.PeersDBPath); err == nil {
			appCfg.PeersDBPath = abs
		}
	}
	if !filepath.IsAbs(appCfg.AuditLogPath) {
		if abs, err := filepath.Abs(appCfg.AuditLogPath); err == nil {
			appCfg.AuditLogPath = abs
		}
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if _, err := exec.LookPath("wg"); err != nil {
		log.Printf("Warning: 'wg' binary not found in PATH — WireGuard operations will fail")
	}
	if appCfg.ServerPublicIP == "" {
		log.Fatal("SERVER_PUBLIC_IP is empty — set it in config.env")
	}
	if appCfg.APIKey == "" {
		log.Printf("WARNING: MGMT_API_KEY is empty — set it in config.env (session-auth will be the primary auth)")
	}

	var crypto *store.Crypto
	if appCfg.APIKey != "" {
		crypto = store.NewCrypto(appCfg.APIKey)
	}

	state, mi, err := store.Load(appCfg.PeersDBPath, crypto)
	if err != nil {
		log.Printf("WARNING: Failed to load peer state: %v — starting with empty state", err)
		state = store.NewState(appCfg.PeersDBPath, crypto)
	}

	if mi.PeerAliases > 0 || mi.Invites > 0 {
		log.Printf("State migration complete: %d peer alias(es), %d invite(s) backfilled", mi.PeerAliases, mi.Invites)
	}

	// Parse and store address pools from config.
	if len(appCfg.RawPools) > 0 {
		pools, err := store.ParsePools(appCfg.WGSubnet, appCfg.RawPools)
		if err != nil {
			log.Printf("WARNING: Pool config error: %v — pools disabled", err)
		} else {
			state.SetPools(pools)
			log.Printf("Loaded %d address pool(s)", len(pools))
		}
	}

	if err := audit.Init(appCfg.AuditLogPath); err != nil {
		log.Printf("Warning: audit log init failed: %v", err)
	} else {
		audit.Log("daemon_started", map[string]string{"version": "1.0.0"})
	}
	defer audit.Close()

	// Bootstrap owner on first run if password is set and no users exist.
	if !state.HasUsers() && appCfg.BootstrapOwnerPassword != "" {
		ownerHash, err := store.HashPassword(appCfg.BootstrapOwnerPassword)
		if err != nil {
			log.Printf("WARNING: failed to hash bootstrap password: %v", err)
		} else if err := state.BootstrapOwner(ownerHash); err != nil {
			log.Printf("WARNING: bootstrap owner creation: %v", err)
		} else {
			log.Printf("Bootstrap owner 'admin' created")
			if err := state.Save(); err != nil {
				log.Printf("WARNING: failed to save bootstrap owner: %v", err)
			}
		}
	}

	wgMgr := wg.NewManager()

	if wgStatus, err := wgMgr.Show(appCfg.WGInterface); err == nil {
		wgPeers := make(map[string]store.Peer)
		wgPubKeySet := make(map[string]bool)
		for _, p := range wgStatus.Peers {
			wgPubKeySet[p.PublicKey] = true
			ip := "0.0.0.0"
			if parts := strings.SplitN(p.AllowedIPs, "/", 2); parts[0] != "" {
				ip = parts[0]
			}
			wgPeers[p.PublicKey] = store.Peer{
				Name:      "recovered-" + p.PublicKey[:12],
				PublicKey: p.PublicKey,
				Address:   ip,
				Keepalive: appCfg.PeerKeepalive,
			}
		}
		recovered := state.ReconcileFromWG(wgPeers)
		if recovered > 0 {
			log.Printf("Recovered %d peer(s) from live WireGuard interface", recovered)
		}

		addedToWG := 0
		for _, p := range state.AllPeers() {
			if !wgPubKeySet[p.PublicKey] {
				allowedIP := fmt.Sprintf("%s/32", p.Address)
				if err := wgMgr.AddPeerLive(appCfg.WGInterface, p.PublicKey, allowedIP, p.Keepalive); err != nil {
					log.Printf("WARNING: Failed to add peer %q to WireGuard: %v", p.Name, err)
				} else {
					log.Printf("Restored peer %q to WireGuard interface", p.Name)
					addedToWG++
				}
			}
		}

		if recovered > 0 || addedToWG > 0 {
			peerMap := make(map[string]wg.PeerInfo)
			for _, p := range state.AllPeers() {
				peerMap[p.Name] = wg.PeerInfo{Alias: p.Alias, PubKey: p.PublicKey, Address: p.Address, Keepalive: p.Keepalive}
			}
			if err := wg.WriteFullConfig(appCfg.WGConfPath, appCfg.WGInterface, appCfg.WGPort, appCfg.PeerKeepalive, appCfg.WGServerIP, state.Server().PrivateKey, peerMap); err != nil {
				log.Printf("WARNING: Failed to write config after recovery: %v", err)
			}
			if err := state.Save(); err != nil {
				log.Printf("WARNING: Failed to save state after recovery: %v", err)
			}
		}
	} else {
		log.Printf("WARNING: Cannot access WireGuard interface %q: %v", appCfg.WGInterface, err)
	}

	apiCfg := &api.Config{
		WGInterface:    appCfg.WGInterface,
		WGPort:         appCfg.WGPort,
		WGSubnet:       appCfg.WGSubnet,
		WGServerIP:     appCfg.WGServerIP,
		MgmtListen:     appCfg.MgmtListen,
		APIKey:         appCfg.APIKey,
		ServerPublicIP: appCfg.ServerPublicIP,
		ServerHost:     appCfg.ServerHost,
		DefaultDNS:     appCfg.DefaultDNS,
		PeerKeepalive:  appCfg.PeerKeepalive,
		PeersDBPath:    appCfg.PeersDBPath,
		WGConfPath:     appCfg.WGConfPath,
	}

	srv, handler := api.NewServer(ctx, apiCfg, state, wgMgr)

	// ── WireGuard state poll goroutine ──
	go func() {
		ticker := time.NewTicker(5 * time.Second)
		defer ticker.Stop()
		var prevStatus *wg.InterfaceStatus
		// Prime the initial state after a short delay
		time.Sleep(2 * time.Second)
		if s, err := wgMgr.Show(appCfg.WGInterface); err == nil {
			prevStatus = s
		}
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				cur, err := wgMgr.Show(appCfg.WGInterface)
				if err != nil || prevStatus == nil {
					prevStatus = cur
					continue
				}
				// Map previous peers by public key
				prevMap := make(map[string]wg.PeerStatus)
				for _, p := range prevStatus.Peers {
					prevMap[p.PublicKey] = p
				}
				curMap := make(map[string]wg.PeerStatus)
				for _, p := range cur.Peers {
					curMap[p.PublicKey] = p
				}
				ep := appCfg.ServerPublicIP + ":" + strconv.Itoa(appCfg.WGPort)
				for pk, cp := range curMap {
					if pp, ok := prevMap[pk]; ok {
						// Handshake occurred
						if pp.LatestHandshake == "0" && cp.LatestHandshake != "0" {
							audit.Write("WG", "peer_connected",
								map[string]string{"peer": pk[:12], "endpoint": cp.Endpoint})
						} else if pp.LatestHandshake != cp.LatestHandshake && cp.LatestHandshake != "0" {
							audit.Write("WG", "handshake_complete",
								map[string]string{"peer": pk[:12], "endpoint": cp.Endpoint})
						}
						// Endpoint changed
						if pp.Endpoint != cp.Endpoint && cp.Endpoint != "(none)" && pp.Endpoint != "(none)" {
							audit.Write("WG", "endpoint_changed",
								map[string]string{"peer": pk[:12], "old": pp.Endpoint, "new": cp.Endpoint})
						}
						// Transfer threshold (log every ~1MB change)
						if rxDiff(cp.TransferRx, pp.TransferRx) > 1_000_000 || txDiff(cp.TransferTx, pp.TransferTx) > 1_000_000 {
							audit.Write("WG", "transfer_update",
								map[string]string{"peer": pk[:12], "endpoint": ep})
						}
					} else {
						audit.Write("WG", "peer_added",
							map[string]string{"peer": pk[:12], "endpoint": cp.Endpoint})
					}
				}
				for pk := range prevMap {
					if _, ok := curMap[pk]; !ok {
						audit.Write("WG", "peer_removed",
							map[string]string{"peer": pk[:12]})
					}
				}
				prevStatus = cur
			}
		}
	}()

	idleConnsClosed := make(chan struct{})
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM, syscall.SIGHUP)

		for {
			sig := <-sigCh
			switch sig {
			case syscall.SIGHUP:
				log.Println("Received SIGHUP, reloading configuration...")
				if err := reloadConfig(configPath, appCfg, handler, state, wgMgr); err != nil {
					log.Printf("Config reload failed: %v", err)
				} else {
					audit.Log("config_reloaded", nil)
					log.Println("Config reloaded successfully")
				}

			case syscall.SIGINT, syscall.SIGTERM:
				log.Printf("Received signal %v, saving state and shutting down...", sig)

				if err := state.Save(); err != nil {
					log.Printf("Failed to save state on exit: %v", err)
				}

				if appCfg.CleanPeersOnExit {
					log.Printf("Removing all peers from %s...", appCfg.WGInterface)
					if err := wgMgr.RemoveAllPeers(appCfg.WGInterface); err != nil {
						log.Printf("Warning: peer cleanup: %v", err)
					}
				}

				cancel()

				shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
				defer shutdownCancel()

				if err := srv.Shutdown(shutdownCtx); err != nil {
					log.Printf("HTTP server shutdown error: %v", err)
				}
				close(idleConnsClosed)
				return
			}
		}
	}()

	log.Printf("WireGuard Management Daemon starting on %s", appCfg.MgmtListen)
	log.Printf("WireGuard interface: %s, port: %d", appCfg.WGInterface, appCfg.WGPort)

	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatalf("HTTP server error: %v", err)
	}

	<-idleConnsClosed
	log.Println("Daemon stopped")
}

func rxDiff(cur, prev string) int64 {
	return parseInt64(cur) - parseInt64(prev)
}

func txDiff(cur, prev string) int64 {
	return parseInt64(cur) - parseInt64(prev)
}

func parseInt64(s string) int64 {
	if s == "" {
		return 0
	}
	v, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0
	}
	return v
}
