package store

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
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

type Request struct {
	ID         string `json:"id"`
	Hostname   string `json:"hostname"`
	DNS        string `json:"dns"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	Address    string `json:"address"`
	Keepalive  int    `json:"keepalive"`
	SourceIP   string `json:"source_ip"`
	Status     string `json:"status"`
	CreatedAt  string `json:"created_at"`
	ExpiresAt  string `json:"expires_at"`
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
	server   ServerConfig
	Peers    map[string]Peer    `json:"peers"`
	Requests map[string]Request `json:"requests,omitempty"`

	mu     sync.RWMutex `json:"-"`
	path   string       `json:"-"`
	crypto *Crypto      `json:"-"`
}

func NewState(path string, crypto *Crypto) *State {
	return &State{
		Peers:    make(map[string]Peer),
		Requests: make(map[string]Request),
		path:     path,
		crypto:   crypto,
	}
}

func (s *State) MarshalJSON() ([]byte, error) {
	type Alias struct {
		Server   ServerConfig      `json:"server"`
		Peers    map[string]Peer   `json:"peers"`
		Requests map[string]Request `json:"requests,omitempty"`
	}
	return json.Marshal(&Alias{
		Server:   s.server,
		Peers:    s.Peers,
		Requests: s.Requests,
	})
}

func (s *State) UnmarshalJSON(data []byte) error {
	type Alias struct {
		Server   ServerConfig      `json:"server"`
		Peers    map[string]Peer   `json:"peers"`
		Requests map[string]Request `json:"requests,omitempty"`
	}
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	s.server = alias.Server
	s.Peers = alias.Peers
	s.Requests = alias.Requests
	if s.Peers == nil {
		s.Peers = make(map[string]Peer)
	}
	if s.Requests == nil {
		s.Requests = make(map[string]Request)
	}
	return nil
}

func (s *State) Server() ServerConfig {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.server
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
	serverIP[len(serverIP)-1] = 1

	used := make(map[string]bool)
	used[serverIP.String()] = true
	for _, p := range s.Peers {
		used[p.Address] = true
	}
	for _, r := range s.Requests {
		used[r.Address] = true
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

	if s.crypto != nil {
		enc, err := s.crypto.Encrypt(data)
		if err != nil {
			return fmt.Errorf("encrypt state: %w", err)
		}
		result := make([]byte, len(encryptedPrefix)+len(enc))
		copy(result, encryptedPrefix)
		copy(result[len(encryptedPrefix):], enc)
		data = result
	}

	bakPath := s.path + ".bak"
	if existing, err := os.ReadFile(s.path); err == nil && len(existing) > 0 {
		os.WriteFile(bakPath, existing, 0600)
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

func Load(path string, crypto *Crypto) (*State, error) {
	s := &State{
		Peers:    make(map[string]Peer),
		Requests: make(map[string]Request),
		path:     path,
		crypto:   crypto,
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

	if bytes.HasPrefix(data, []byte(encryptedPrefix)) {
		if crypto == nil {
			return nil, fmt.Errorf("state is encrypted but no crypto key provided")
		}
		dec, err := crypto.Decrypt(data[len(encryptedPrefix):])
		if err != nil {
			bakPath := path + ".bak"
			bakData, bakErr := os.ReadFile(bakPath)
			if bakErr == nil && len(bakData) > 0 && bytes.HasPrefix(bakData, []byte(encryptedPrefix)) {
				if decData, decErr := crypto.Decrypt(bakData[len(encryptedPrefix):]); decErr == nil {
					fmt.Fprintf(os.Stderr, "WARNING: %s corrupted, recovered from %s\n", path, bakPath)
					data = decData
					goto unmarshal
				}
			}
			return nil, fmt.Errorf("decrypt state: %w", err)
		}
		data = dec
	} else if crypto != nil {
		fmt.Fprintf(os.Stderr, "WARNING: %s is not encrypted — will be encrypted on next save\n", path)
	}

unmarshal:
	if err := json.Unmarshal(data, s); err != nil {
		bakPath := path + ".bak"
		bakData, bakErr := os.ReadFile(bakPath)
		if bakErr != nil || len(bakData) == 0 {
			fmt.Fprintf(os.Stderr, "WARNING: %s corrupted and no valid backup, starting with empty state\n", path)
			return s, nil
		}
		if bytes.HasPrefix(bakData, []byte(encryptedPrefix)) {
			if crypto == nil {
				fmt.Fprintf(os.Stderr, "WARNING: %s corrupted, backup is encrypted but no key, starting empty\n", path)
				return s, nil
			}
			bakData, bakErr = crypto.Decrypt(bakData[len(encryptedPrefix):])
			if bakErr != nil {
				fmt.Fprintf(os.Stderr, "WARNING: %s and backup both corrupted, starting with empty state\n", path)
				return s, nil
			}
		}
		if bakErr := json.Unmarshal(bakData, s); bakErr != nil {
			fmt.Fprintf(os.Stderr, "WARNING: %s corrupted, backup also invalid, starting with empty state\n", path)
			return s, nil
		}
		fmt.Fprintf(os.Stderr, "WARNING: %s corrupted, recovered from %s\n", path, bakPath)
	}

	if s.Peers == nil {
		s.Peers = make(map[string]Peer)
	}
	if s.Requests == nil {
		s.Requests = make(map[string]Request)
	}

	return s, nil
}

// ── Request management ──────────────────────────

func GenerateRequestID() string {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		for i := range b {
			b[i] = byte((time.Now().UnixNano() >> (i * 4)) & 0xFF)
		}
	}
	return hex.EncodeToString(b)
}

func (s *State) AddRequest(r Request) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Requests[r.ID]; ok {
		return fmt.Errorf("request %q already exists", r.ID)
	}

	for _, existing := range s.Requests {
		if existing.Hostname == r.Hostname && (existing.Status == "" || existing.Status == "pending") {
			return fmt.Errorf("a pending request for %q already exists", r.Hostname)
		}
	}

	if r.CreatedAt == "" {
		r.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}
	if r.ExpiresAt == "" {
		r.ExpiresAt = time.Now().UTC().Add(24 * time.Hour).Format(time.RFC3339)
	}
	if r.Keepalive == 0 {
		r.Keepalive = 25
	}

	s.Requests[r.ID] = r
	return nil
}

func (s *State) GetRequest(id string) (Request, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.Requests[id]
	return r, ok
}

func (s *State) ApproveRequest(id string) (Peer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Requests[id]
	if !ok {
		return Peer{}, fmt.Errorf("request %q not found", id)
	}

	if _, ok := s.Peers[r.Hostname]; ok {
		return Peer{}, fmt.Errorf("peer %q already exists", r.Hostname)
	}

	peer := Peer{
		Name:       r.Hostname,
		PublicKey:  r.PublicKey,
		PrivateKey: r.PrivateKey,
		Address:    r.Address,
		DNS:        r.DNS,
		Keepalive:  r.Keepalive,
		CreatedAt:  time.Now().UTC().Format(time.RFC3339),
	}

	s.Peers[r.Hostname] = peer
	r.Status = "approved"
	r.ExpiresAt = time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)
	s.Requests[id] = r
	return peer, nil
}

func (s *State) RejectRequest(id string) (Request, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	r, ok := s.Requests[id]
	if !ok {
		return Request{}, fmt.Errorf("request %q not found", id)
	}

	r.Status = "rejected"
	r.ExpiresAt = time.Now().UTC().Add(5 * time.Minute).Format(time.RFC3339)
	s.Requests[id] = r
	return r, nil
}

func (s *State) PendingRequests() []Request {
	s.mu.RLock()
	defer s.mu.RUnlock()

	reqs := make([]Request, 0, len(s.Requests))
	for _, r := range s.Requests {
		if r.Status == "" || r.Status == "pending" {
			reqs = append(reqs, r)
		}
	}
	return reqs
}

func (s *State) ExpireRequests() []Request {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := time.Now().UTC()
	var expired []Request
	for id, r := range s.Requests {
		expAt, err := time.Parse(time.RFC3339, r.ExpiresAt)
		if err != nil || now.After(expAt) {
			expired = append(expired, r)
			delete(s.Requests, id)
		}
	}
	return expired
}
