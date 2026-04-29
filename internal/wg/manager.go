package wg

import (
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"strings"
)

type Manager struct{}

func NewManager() *Manager {
	return &Manager{}
}

type PeerStatus struct {
	PublicKey           string `json:"public_key"`
	PresharedKey        string `json:"preshared_key"`
	Endpoint            string `json:"endpoint"`
	AllowedIPs          string `json:"allowed_ips"`
	LatestHandshake     string `json:"latest_handshake"`
	TransferRx          string `json:"transfer_rx"`
	TransferTx          string `json:"transfer_tx"`
	PersistentKeepalive string `json:"persistent_keepalive"`
}

type InterfaceStatus struct {
	PrivateKey string       `json:"private_key"`
	PublicKey  string       `json:"public_key"`
	ListenPort string       `json:"listen_port"`
	Peers      []PeerStatus `json:"peers"`
}

func (m *Manager) GenKeyPair() (private string, public string, err error) {
	privCmd := exec.Command("wg", "genkey")
	var privOut bytes.Buffer
	privCmd.Stdout = &privOut
	privCmd.Stderr = os.Stderr
	if err := privCmd.Run(); err != nil {
		return "", "", fmt.Errorf("wg genkey: %w", err)
	}
	private = strings.TrimSpace(privOut.String())

	pubCmd := exec.Command("wg", "pubkey")
	pubCmd.Stdin = strings.NewReader(private)
	var pubOut bytes.Buffer
	pubCmd.Stdout = &pubOut
	pubCmd.Stderr = os.Stderr
	if err := pubCmd.Run(); err != nil {
		return "", "", fmt.Errorf("wg pubkey: %w", err)
	}
	public = strings.TrimSpace(pubOut.String())

	return private, public, nil
}

func (m *Manager) Show(iface string) (*InterfaceStatus, error) {
	cmd := exec.Command("wg", "show", iface, "dump")
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return nil, fmt.Errorf("wg show %s dump: %w", iface, err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) == 0 || lines[0] == "" {
		return &InterfaceStatus{}, nil
	}

	ifaceLine := strings.Split(lines[0], "\t")
	if len(ifaceLine) < 4 {
		return nil, fmt.Errorf("unexpected wg show dump format for interface line")
	}

	status := &InterfaceStatus{
		PrivateKey: ifaceLine[0],
		PublicKey:  ifaceLine[1],
		ListenPort: ifaceLine[2],
	}

	for _, line := range lines[1:] {
		fields := strings.Split(line, "\t")
		if len(fields) < 8 {
			continue
		}
		status.Peers = append(status.Peers, PeerStatus{
			PublicKey:           fields[0],
			PresharedKey:        fields[1],
			Endpoint:            fields[2],
			AllowedIPs:          fields[3],
			LatestHandshake:     fields[4],
			TransferRx:          fields[5],
			TransferTx:          fields[6],
			PersistentKeepalive: fields[7],
		})
	}

	return status, nil
}

func (m *Manager) AddPeerLive(iface, pubKey, allowedIP string, keepalive int) error {
	args := []string{"set", iface, "peer", pubKey, "allowed-ips", allowedIP}
	if keepalive > 0 {
		args = append(args, "persistent-keepalive", fmt.Sprintf("%d", keepalive))
	}

	cmd := exec.Command("wg", args...)
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wg set peer: %w", err)
	}
	return nil
}

func (m *Manager) RemovePeerByKey(iface string, pubKey string) error {
	cmd := exec.Command("wg", "set", iface, "peer", pubKey, "remove")
	cmd.Stderr = os.Stderr
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("wg set peer remove: %w", err)
	}
	return nil
}

type PeerInfo struct {
	PubKey    string
	Address   string
	Keepalive int
}

func WriteFullConfig(cfgPath, iface string, listenPort int, keepalive int, serverAddress, serverPrivateKey string, peers map[string]PeerInfo) error {
	var b strings.Builder

	b.WriteString("[Interface]\n")
	b.WriteString(fmt.Sprintf("Address = %s\n", serverAddress))
	if listenPort > 0 {
		b.WriteString(fmt.Sprintf("ListenPort = %d\n", listenPort))
	}
	b.WriteString(fmt.Sprintf("PrivateKey = %s\n", serverPrivateKey))
	b.WriteString(fmt.Sprintf("PostUp = iptables -A FORWARD -i %s -j ACCEPT; iptables -A FORWARD -o %s -j ACCEPT\n", iface, iface))
	b.WriteString(fmt.Sprintf("PostDown = iptables -D FORWARD -i %s -j ACCEPT; iptables -D FORWARD -o %s -j ACCEPT\n", iface, iface))

	for name, p := range peers {
		b.WriteString("\n")
		b.WriteString(fmt.Sprintf("# %s\n", name))
		b.WriteString("[Peer]\n")
		b.WriteString(fmt.Sprintf("PublicKey = %s\n", p.PubKey))
		b.WriteString(fmt.Sprintf("AllowedIPs = %s/32\n", p.Address))
		k := p.Keepalive
		if k == 0 {
			k = keepalive
		}
		if k > 0 {
			b.WriteString(fmt.Sprintf("PersistentKeepalive = %d\n", k))
		}
	}
	b.WriteString("\n")

	content := b.String()

	if err := os.WriteFile(cfgPath, []byte(content), 0600); err != nil {
		return fmt.Errorf("write config file: %w", err)
	}

	return nil
}
