package main

import (
	"errors"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"strings"
	"text/tabwriter"

	"wire-guard-dev/internal/cli"
)

type command struct {
	name        string
	description string
	run         func(*cli.Client, []string) error
}

func main() {
	configPath := flag.String("config", "config.env", "path to config.env")
	flag.Usage = func() {
		fmt.Fprintf(flag.CommandLine.Output(), "Usage: %s [--config FILE] <command>\n\n", os.Args[0])
		fmt.Fprintln(flag.CommandLine.Output(), "Commands:")
		for _, cmd := range commands() {
			fmt.Fprintf(flag.CommandLine.Output(), "  %-8s %s\n", cmd.name, cmd.description)
		}
		fmt.Fprintln(flag.CommandLine.Output(), "\nFlags:")
		fmt.Fprintln(flag.CommandLine.Output(), "  --config FILE   config file path (default: config.env)")
	}

	args := os.Args[1:]
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			flag.Usage()
			return
		}
	}

	if err := flag.CommandLine.Parse(args); err != nil {
		os.Exit(2)
	}

	remaining := flag.Args()
	if len(remaining) == 0 {
		flag.Usage()
		return
	}

	commandName := remaining[0]
	clientConfig, apiKey, err := cli.LoadConfig(*configPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}

	client := cli.NewClient(clientConfig, apiKey)
	for _, cmd := range commands() {
		if cmd.name == commandName {
			if err := cmd.run(client, remaining[1:]); err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(1)
			}
			return
		}
	}

	fmt.Fprintf(os.Stderr, "unknown command %q\n\n", commandName)
	flag.Usage()
	os.Exit(1)
}

func commands() []command {
	return []command{
		{name: "peer", description: "peer operations", run: runPeer},
		{name: "invite", description: "invite operations", run: runInvite},
		{name: "user", description: "user operations", run: runUser},
		{name: "status", description: "status operations", run: runStatus},
		{name: "auth", description: "auth operations", run: runAuth},
		{name: "me", description: "current user operations", run: runMe},
	}
}

func runPeer(c *cli.Client, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: wg-mgmt peer <subcommand>")
		fmt.Fprintln(os.Stderr, "Subcommands: list, alias, delete")
		fmt.Fprintln(os.Stderr, "Try: wg-mgmt peer list --help")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "list":
		return cmdPeerList(c, subArgs)
	case "alias":
		return cmdPeerAlias(c, subArgs)
	case "delete":
		return cmdPeerDelete(c, subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown peer subcommand %q\n", sub)
		return nil
	}
}

type peerListItem struct {
	Name            string `json:"name"`
	Alias           string `json:"alias,omitempty"`
	PublicKey       string `json:"public_key"`
	Address         string `json:"address"`
	DNS             string `json:"dns,omitempty"`
	Keepalive       int    `json:"keepalive"`
	CreatedAt       string `json:"created_at,omitempty"`
	Endpoint        string `json:"endpoint,omitempty"`
	LatestHandshake string `json:"latest_handshake,omitempty"`
	TransferRx      string `json:"transfer_rx,omitempty"`
	TransferTx      string `json:"transfer_tx,omitempty"`
	Online          bool   `json:"online"`
	Orphaned        bool   `json:"orphaned,omitempty"`
}

type peerListResponse struct {
	PeerCount int            `json:"peer_count"`
	Peers     []peerListItem `json:"peers"`
}

func cmdPeerList(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	formatJSON := fs.Bool("format", false, "output as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	var resp peerListResponse
	if err := c.GetJSON("/api/v1/peers", &resp); err != nil {
		return fmt.Errorf("list peers: %w", err)
	}

	if *formatJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "PUBLIC KEY\tALIAS\tNAME\tIP\tONLINE\tENDPOINT")
	for _, p := range resp.Peers {
		pubKey := p.PublicKey
		if len(pubKey) > 16 {
			pubKey = pubKey[:16]
		}
		alias := p.Alias
		if alias == "" {
			alias = p.Name
		}
		status := "offline"
		if p.Online {
			status = "online"
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			pubKey,
			alias,
			p.Name,
			p.Address,
			status,
			p.Endpoint,
		)
	}
	return w.Flush()
}

func cmdPeerAlias(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("alias", flag.ExitOnError)
	id := fs.String("id", "", "peer public key (immutable ID)")
	alias := fs.String("alias", "", "new alias for the peer")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *id == "" {
		return fmt.Errorf("--id (peer public key) is required")
	}
	if *alias == "" {
		return fmt.Errorf("--alias is required")
	}

	body := map[string]string{
		"pubkey": *id,
		"alias":  *alias,
	}
	var resp struct {
		Success  bool   `json:"success"`
		Name     string `json:"name"`
		Pubkey   string `json:"pubkey"`
		OldAlias string `json:"old_alias"`
		NewAlias string `json:"new_alias"`
	}
	if err := c.PostJSON("/api/v1/peers/alias", body, &resp); err != nil {
		return fmt.Errorf("update alias: %w", err)
	}

	fmt.Fprintf(os.Stdout, "Alias updated: %q -> %q (peer: %s)\n", resp.OldAlias, resp.NewAlias, resp.Name)
	return nil
}

func cmdPeerDelete(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	id := fs.String("id", "", "peer public key (immutable ID)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *id == "" {
		return fmt.Errorf("--id (peer public key) is required for deletion\nUse immutable public key ID — ambiguous alias-only delete is rejected")
	}

	var resp struct {
		Success bool   `json:"success"`
		Message string `json:"message"`
	}
	path := fmt.Sprintf("/api/v1/peers/by-pubkey/%s", *id)
	if err := c.DeleteJSON(path, &resp); err != nil {
		return fmt.Errorf("delete peer: %w", err)
	}
	fmt.Fprintln(os.Stdout, resp.Message)
	return nil
}

func runInvite(_ *cli.Client, _ []string) error {
	return printStub()
}

func runUser(_ *cli.Client, _ []string) error {
	return printStub()
}

type outputFormat string

const (
	outputHuman outputFormat = "human"
	outputJSON  outputFormat = "json"
)

type statusResponse struct {
	Interface  string `json:"interface"`
	ListenPort int    `json:"listen_port,omitempty"`
	Daemon     string `json:"daemon"`
	WireGuard  string `json:"wireguard"`
	WGError    string `json:"wg_error,omitempty"`
	PeerOnline int    `json:"peer_online,omitempty"`
	PeerTotal  int    `json:"peer_total,omitempty"`
	PeerCount  int    `json:"peer_count,omitempty"`
}

func runStatus(client *cli.Client, args []string) error {
	format, sessionToken, remaining, err := parseCLIFlags(args)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return fmt.Errorf("status takes no positional arguments")
	}

	authClient := client
	if authClient.CurrentAuthMethod() == "none" {
		sessionToken = resolveSessionToken(sessionToken)
		if sessionToken == "" {
			return fmt.Errorf("status requires an API key in config.env or a session token via MGMT_SESSION_TOKEN")
		}
		authClient = client.WithSessionToken(sessionToken)
	}

	var response statusResponse
	if err := authClient.GetJSON("/api/v1/status", &response); err != nil {
		return formatCLIError(err)
	}

	return renderCLIOutput(format, response, renderStatusHuman, renderStatusJSON)
}

func runAuth(_ *cli.Client, _ []string) error {
	return printStub()
}

func runMe(client *cli.Client, args []string) error {
	format, sessionToken, remaining, err := parseCLIFlags(args)
	if err != nil {
		return err
	}
	if len(remaining) != 0 {
		return fmt.Errorf("me takes no positional arguments")
	}

	sessionToken = resolveSessionToken(sessionToken)
	if sessionToken == "" {
		return fmt.Errorf("me requires a session token via MGMT_SESSION_TOKEN or --session-token")
	}

	response, err := client.WithSessionToken(sessionToken).Me()
	if err != nil {
		return formatCLIError(err)
	}

	return renderCLIOutput(format, *response, renderMeHuman, renderMeJSON)
}

func parseCLIFlags(args []string) (outputFormat, string, []string, error) {
	fs := flag.NewFlagSet("wg-mgmt", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	format := fs.String("format", string(outputHuman), "output format (human|json)")
	sessionToken := fs.String("session-token", "", "session token to use")
	if err := fs.Parse(args); err != nil {
		return "", "", nil, err
	}

	switch strings.ToLower(strings.TrimSpace(*format)) {
	case "", string(outputHuman), "text":
		return outputHuman, strings.TrimSpace(*sessionToken), fs.Args(), nil
	case string(outputJSON):
		return outputJSON, strings.TrimSpace(*sessionToken), fs.Args(), nil
	default:
		return "", "", nil, fmt.Errorf("unsupported format %q", *format)
	}
}

func resolveSessionToken(flagToken string) string {
	if token := strings.TrimSpace(flagToken); token != "" {
		return token
	}
	for _, envName := range []string{"MGMT_SESSION_TOKEN", "WG_SESSION_TOKEN"} {
		if token := strings.TrimSpace(os.Getenv(envName)); token != "" {
			return token
		}
	}
	return ""
}

func formatCLIError(err error) error {
	if isDaemonUnreachable(err) {
		return fmt.Errorf("daemon unreachable: check that wg-mgmt-daemon is running on localhost")
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "failed to resolve session") {
		return fmt.Errorf("me requires a session token; log in first or pass --session-token: %w", err)
	}
	return err
}

func isDaemonUnreachable(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "connect to daemon") || strings.Contains(msg, "connection refused") || strings.Contains(msg, "no such host") || strings.Contains(msg, "no route to host") || strings.Contains(msg, "network is unreachable") || strings.Contains(msg, "i/o timeout") {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr)
}

func renderCLIOutput[T any](format outputFormat, value T, human func(T), asJSON func(T) error) error {
	switch format {
	case outputJSON:
		return asJSON(value)
	default:
		human(value)
		return nil
	}
}

func renderMeHuman(me cli.MeResponse) {
	fmt.Printf("name: %s\nrole: %s\ncreated_at: %s\n", me.Name, me.Role, me.CreatedAt)
}

func renderMeJSON(me cli.MeResponse) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(me)
}

func renderStatusHuman(status statusResponse) {
	fmt.Printf("daemon: %s\nwireguard: %s\ninterface: %s\n", status.Daemon, status.WireGuard, status.Interface)
	if status.ListenPort != 0 {
		fmt.Printf("listen_port: %d\n", status.ListenPort)
	}
	if status.WGError != "" {
		fmt.Printf("wg_error: %s\n", status.WGError)
	}
	if status.PeerOnline != 0 || status.PeerTotal != 0 {
		fmt.Printf("peers: %d online / %d total\n", status.PeerOnline, status.PeerTotal)
		return
	}
	if status.PeerCount != 0 {
		fmt.Printf("peers: %d\n", status.PeerCount)
	}
}

func renderStatusJSON(status statusResponse) error {
	encoder := json.NewEncoder(os.Stdout)
	encoder.SetIndent("", "  ")
	return encoder.Encode(status)
}

func printStub() error {
	fmt.Println("Subcommand not yet implemented")
	return nil
}
