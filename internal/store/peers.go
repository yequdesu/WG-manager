package store

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
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
	server   ServerConfig
	Peers    map[string]Peer    `json:"peers"`
	Users    map[string]User    `json:"users,omitempty"`
	Sessions map[string]Session `json:"sessions,omitempty"`
	Invites  map[string]Invite  `json:"invites,omitempty"`

	mu     sync.RWMutex `json:"-"`
	path   string       `json:"-"`
	crypto *Crypto      `json:"-"`
}

func NewState(path string, crypto *Crypto) *State {
	return &State{
		Peers:    make(map[string]Peer),
		Users:    make(map[string]User),
		Sessions: make(map[string]Session),
		Invites:  make(map[string]Invite),
		path:     path,
		crypto:   crypto,
	}
}

func (s *State) MarshalJSON() ([]byte, error) {
	type Alias struct {
		Server   ServerConfig       `json:"server"`
		Peers    map[string]Peer    `json:"peers"`
		Users    map[string]User    `json:"users,omitempty"`
		Sessions map[string]Session `json:"sessions,omitempty"`
		Invites  map[string]Invite  `json:"invites,omitempty"`
	}
	return json.Marshal(&Alias{
		Server:   s.server,
		Peers:    s.Peers,
		Users:    s.Users,
		Sessions: s.Sessions,
		Invites:  s.Invites,
	})
}

func (s *State) UnmarshalJSON(data []byte) error {
	type Alias struct {
		Server   ServerConfig       `json:"server"`
		Peers    map[string]Peer    `json:"peers"`
		Users    map[string]User    `json:"users,omitempty"`
		Sessions map[string]Session `json:"sessions,omitempty"`
		Invites  map[string]Invite  `json:"invites,omitempty"`
	}
	var alias Alias
	if err := json.Unmarshal(data, &alias); err != nil {
		return err
	}
	s.server = alias.Server
	s.Peers = alias.Peers
	s.Users = alias.Users
	s.Sessions = alias.Sessions
	s.Invites = alias.Invites
	if s.Peers == nil {
		s.Peers = make(map[string]Peer)
	}
	if s.Users == nil {
		s.Users = make(map[string]User)
	}
	if s.Sessions == nil {
		s.Sessions = make(map[string]Session)
	}
	if s.Invites == nil {
		s.Invites = make(map[string]Invite)
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

func (s *State) PeerByPublicKey(pubKey string) (Peer, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, p := range s.Peers {
		if p.PublicKey == pubKey {
			return p, true
		}
	}
	return Peer{}, false
}

func (s *State) ReconcileFromWG(wgPeers map[string]Peer) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	added := 0
	for pubKey, wp := range wgPeers {
		found := false
		for _, p := range s.Peers {
			if p.PublicKey == wp.PublicKey {
				found = true
				break
			}
		}
		if !found {
			name := "recovered-" + pubKey[:12]
			if len(name) > 32 {
				name = name[:32]
			}
			if wp.Keepalive == 0 {
				wp.Keepalive = 25
			}
			if wp.CreatedAt == "" {
				wp.CreatedAt = time.Now().UTC().Format(time.RFC3339)
			}
			s.Peers[name] = wp
			added++
		}
	}
	return added
}

func (s *State) NextAvailableIP(subnet string) (string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.nextIPInLock(subnet, nil)
}

func (s *State) nextIPInLock(subnet string, extraUsed map[string]bool) (string, error) {
	_, ipNet, err := net.ParseCIDR(subnet)
	if err != nil {
		return "", fmt.Errorf("invalid subnet %q: %w", subnet, err)
	}

	ipv4 := ipNet.IP.To4()
	if ipv4 == nil {
		return "", fmt.Errorf("only IPv4 subnets are supported")
	}

	used := make(map[string]bool)
	used[ipNet.IP.String()] = true

	serverIP := net.IPv4(ipv4[0], ipv4[1], ipv4[2], ipv4[3]+1)
	used[serverIP.String()] = true

	for _, p := range s.Peers {
		used[p.Address] = true
	}
	for k := range extraUsed {
		used[k] = true
	}

	ones, bits := ipNet.Mask.Size()
	hostBits := uint(bits - ones)
	if hostBits > 30 {
		return "", fmt.Errorf("subnet %s too small (need at least 2 host addresses)", subnet)
	}
	maxHost := uint32(1) << hostBits

	netUint := uint32(ipv4[0])<<24 | uint32(ipv4[1])<<16 | uint32(ipv4[2])<<8 | uint32(ipv4[3])

	for i := uint32(2); i < maxHost-1; i++ {
		addrUint := netUint + i
		ip := net.IPv4(
			byte(addrUint>>24),
			byte(addrUint>>16),
			byte(addrUint>>8),
			byte(addrUint),
		)
		addr := ip.String()
		if !used[addr] {
			return addr, nil
		}
	}

	return "", fmt.Errorf("no available IP in subnet %s", subnet)
}

func (s *State) AllocateIPAndAddPeer(p *Peer, subnet string, extraUsed map[string]bool) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, ok := s.Peers[p.Name]; ok {
		return "", fmt.Errorf("peer %q already exists", p.Name)
	}

	ip, err := s.nextIPInLock(subnet, extraUsed)
	if err != nil {
		return "", err
	}

	p.Address = ip
	if p.Keepalive == 0 {
		p.Keepalive = 25
	}
	if p.CreatedAt == "" {
		p.CreatedAt = time.Now().UTC().Format(time.RFC3339)
	}

	s.Peers[p.Name] = *p
	return ip, nil
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
		if err := os.WriteFile(bakPath, existing, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "WARNING: failed to write backup %s: %v\n", bakPath, err)
		}
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
		Users:    make(map[string]User),
		Sessions: make(map[string]Session),
		Invites:  make(map[string]Invite),
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
	if s.Users == nil {
		s.Users = make(map[string]User)
	}
	if s.Sessions == nil {
		s.Sessions = make(map[string]Session)
	}
	if s.Invites == nil {
		s.Invites = make(map[string]Invite)
	}

	return s, nil
}

func (s *State) Replace(other *State) {
	s.mu.Lock()
	defer s.mu.Unlock()
	other.mu.RLock()
	defer other.mu.RUnlock()
	s.server = other.server
	s.crypto = other.crypto
	s.Peers = make(map[string]Peer)
	maps.Copy(s.Peers, other.Peers)
	s.Users = make(map[string]User)
	maps.Copy(s.Users, other.Users)
	s.Sessions = make(map[string]Session)
	maps.Copy(s.Sessions, other.Sessions)
	s.Invites = make(map[string]Invite)
	maps.Copy(s.Invites, other.Invites)
}
