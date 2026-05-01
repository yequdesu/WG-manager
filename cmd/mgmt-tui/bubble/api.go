package bubble

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"
)

type Config struct {
	APIURL   string
	APIKey   string
	AuditLog string
}

type Peer struct {
	Name            string `json:"name"`
	Address         string `json:"address"`
	DNS             string `json:"dns"`
	PublicKey       string `json:"public_key"`
	Keepalive       int    `json:"keepalive"`
	CreatedAt       string `json:"created_at"`
	Endpoint        string `json:"endpoint,omitempty"`
	LatestHandshake string `json:"latest_handshake,omitempty"`
	TransferRx      string `json:"transfer_rx,omitempty"`
	TransferTx      string `json:"transfer_tx,omitempty"`
	Online          bool   `json:"online"`
	Orphaned        bool   `json:"orphaned,omitempty"`
}

type Request struct {
	ID        string `json:"id"`
	Hostname  string `json:"hostname"`
	Address   string `json:"address"`
	DNS       string `json:"dns"`
	SourceIP  string `json:"source_ip"`
	CreatedAt string `json:"created_at"`
	ExpiresAt string `json:"expires_at"`
}

type StatusData struct {
	Daemon    string `json:"daemon"`
	Wireguard string `json:"wireguard"`
	Interface string `json:"interface"`
	Port      string `json:"listen_port"`
	Online    int    `json:"peer_online"`
	Total     int    `json:"peer_total"`
	Endpoint  string `json:"server_endpoint"`
}

type PeerListResp struct {
	Peers    []Peer `json:"peers"`
	Count    int    `json:"peer_count"`
	Endpoint string `json:"server_endpoint"`
}

type RequestListResp struct {
	Requests []Request `json:"requests"`
	Count    int       `json:"pending_count"`
}

type StatusResp struct {
	Daemon    string `json:"daemon"`
	Wireguard string `json:"wireguard"`
	Interface string `json:"interface"`
	Port      string `json:"listen_port"`
	Online    int    `json:"peer_online"`
	Total     int    `json:"peer_total"`
}

func fetchPeers(cfg Config) ([]Peer, string, error) {
	resp, err := doGet(cfg, "/api/v1/peers")
	if err != nil {
		return nil, "", err
	}
	var r PeerListResp
	if err := json.Unmarshal(resp, &r); err != nil {
		return nil, "", err
	}
	return r.Peers, r.Endpoint, nil
}

func fetchRequests(cfg Config) ([]Request, error) {
	resp, err := doGet(cfg, "/api/v1/requests")
	if err != nil {
		return nil, err
	}
	var r RequestListResp
	if err := json.Unmarshal(resp, &r); err != nil {
		return nil, err
	}
	return r.Requests, nil
}

func fetchStatus(cfg Config) (StatusData, error) {
	resp, err := doGet(cfg, "/api/v1/status")
	if err != nil {
		return StatusData{Daemon: "error", Wireguard: "error"}, nil
	}
	var r StatusResp
	if err := json.Unmarshal(resp, &r); err != nil {
		return StatusData{Daemon: "error", Wireguard: "error"}, nil
	}
	return StatusData{
		Daemon:    r.Daemon,
		Wireguard: r.Wireguard,
		Interface: r.Interface,
		Port:      r.Port,
		Online:    r.Online,
		Total:     r.Total,
	}, nil
}

func fetchLog(cfg Config) ([]string, error) {
	if cfg.AuditLog == "" {
		return nil, nil
	}
	data, err := os.ReadFile(cfg.AuditLog)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	var result []string
	for _, l := range lines {
		l = strings.TrimSpace(l)
		if l == "" {
			continue
		}
		result = append(result, l)
	}
	return result, nil
}

func doDeletePeer(cfg Config, name string) error {
	req, err := http.NewRequest("DELETE", cfg.APIURL+"/api/v1/peers/"+name, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, string(body))
	}
	return nil
}

func doApprove(cfg Config, id string) error {
	req, err := http.NewRequest("POST", cfg.APIURL+"/api/v1/requests/"+id+"/approve", nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, string(body))
	}
	return nil
}

func doDeny(cfg Config, id string) error {
	req, err := http.NewRequest("DELETE", cfg.APIURL+"/api/v1/requests/"+id, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("%s: %s", resp.Status, string(body))
	}
	return nil
}

func doGet(cfg Config, path string) ([]byte, error) {
	req, err := http.NewRequest("GET", cfg.APIURL+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.APIKey)
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	return io.ReadAll(resp.Body)
}
