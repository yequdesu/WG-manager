package store

import (
	"encoding/json"
	"fmt"
	"net"
	"os"
	"sync"
	"time"
)

type Peer struct {
	Name       string `json:"name"`
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	Address    string `json:"address"`
	DNS        string `json:"dns"`
	Keepalive  int    `json:"keepalive"`
	CreatedAt  string `json:"created_at"`
}

type ServerConfig struct {
	PublicKey  string `json:"public_key"`
	PrivateKey string `json:"private_key"`
	Endpoint   string `json:"endpoint"`
	ListenPort int    `json:"listen_port"`
	Address    string `json:"address"`
	Subnet     string `json:"subnet"`
}

type State struct {
	server ServerConfig
	Peers  map[string]Peer `json:"peers"`

	mu   sync.RWMutex `json:"-"`
	path string       `json:"-"`
}

func (s *State) MarshalJSON() ([]byte, error) {
	type Alias struct {
		Server ServerConfig   `json:"server"`
		Peers  map[string]Peer `json:"peers"`
	}
	return json.Marshal(&Alias{
		Server: s.server,
		Peers:  s.Peers,
	})
}

func (s *State) UnmarshalJSON(data []byte) error {
	type Alias struct {
		Server ServerConfig  `json:"server"`
		Peers  map[string]Peer `json:"peers"`
	}
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	s.server = alias.Server
	s.Peers = alias.Peers
	if s.Peers == nil {
		s.Peers = make(map[string]Peer)
	}
	return nil
}

func (s *State) Server() ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.server
}

func (s *State) SetServer(sc ServerConfig) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.server = sc
}

func (s *State) AddPeer(p Peer) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Peers[p.Name]; ok {
		return fmt.Errorf("peer %q already exists", p.Name)
	}

	if p.Keepalive == 0 {
		p.Keepalive = 25
	}
	if p.CreatedAt == "" {
		p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	s.Peers[p.Name] = p
	return nil
}

func (s *State) RemovePeer(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Peers[name]; !ok {
		return fmt.Errorf("peer %q not found", name)
	}

	delete(s.Peers, name)
	return nil
}

func (s *State) GetPeer(name string) (Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	p, ok := s.Peers[name]
	return p, ok
}

func (s *State) HasPeer(name string) bool {
	s.mu.RLock()
	defer s.mu.RUnlock()
	_, ok := s.Peers[name]
	return ok
}

func (s *State) AllPeers() []Peer {
	s.mu.RLock()
	defer s.mu.RUnlock()

	peers := make([]Peer, 0, len(s.Peers))
	for _, p := range s.Peers {
		peers = append(peers, p)
	}
	return peers
}

func (s *State) NextAvailableIP(subnet string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("invalid subnet %q: %w", subnet, err)
	}

	serverIP := ipNet.IP
	serverIP[len(serverIP)-1] = 1 // 10.0.0.1

	used := make(map[string]bool)
	used[serverIP.String()] = true
	for _, p := range s.Peers {
		used[p.Address] = true
	}

	ip := make(net.IP, len(ipNet.IP))
	copy(ip, ipNet.IP)

	for i := 2; i <= 254; i++ {
		ip[len(ip)-1] = byte(i)
		addr := ip.String()
		if !used[addr] {
			return addr, nil
		}
	}

	return "", fmt.Errorf("no available IP in subnet %s", subnet)
}

func (s *State) Save() error {
	s.mu.RLock()
	defer s.mu.RUnlock()

	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal state: %w", err)
	}

	tmpPath := s.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0600); err != nil {
		return fmt.Errorf("write temp file: %w", err)
	}
	if err := os.Rename(tmpPath, s.path); err != nil {
		os.Remove(tmpPath)
		return fmt.Errorf("rename temp file: %w", err)
	}
	return nil
}

func Load(path string) (*State, error) {
	s := &State{
		Peers: make(map[string]Peer),
		path:  path,
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return s, nil
		}
		return nil, fmt.Errorf("read state file: %w", err)
	}

	if len(data) == 0 {
		return s, nil
	}

	if err := json.Unmarshal(data, s); err != nil {
		return nil, fmt.Errorf("unmarshal state: %w", err)
	}

	if s.Peers == nil {
		s.Peers = make(map[string]Peer)
	}

	return s, nil
}
