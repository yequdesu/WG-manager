package main

import (
	"bufio"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"strconv"
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

type userInfo struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

type userListResponse struct {
	UserCount int        `json:"user_count"`
	Users     []userInfo `json:"users"`
}

type userCreateResponse struct {
	Success bool `json:"success"`
	User    struct {
		Name string `json:"name"`
		Role string `json:"role"`
	} `json:"user"`
}

type userDeleteResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
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

func runInvite(c *cli.Client, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: wg-mgmt invite <subcommand>")
		fmt.Fprintln(os.Stderr, "Subcommands: create, list, revoke, delete, force-delete, link, qrcode")
		fmt.Fprintln(os.Stderr, "Try: wg-mgmt invite create --help")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "create":
		return cmdInviteCreate(c, subArgs)
	case "list":
		return cmdInviteList(c, subArgs)
	case "revoke":
		return cmdInviteRevoke(c, subArgs)
	case "delete":
		return cmdInviteDelete(c, subArgs)
	case "force-delete":
		return cmdInviteForceDelete(c, subArgs)
	case "link":
		return cmdInviteLink(c, subArgs)
	case "qrcode":
		return cmdInviteQRCode(c, subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown invite subcommand %q\n", sub)
		return nil
	}
}

// ── Invite response structs ────────────────────────────────────────────

type inviteCreateResponse struct {
	InviteID     string `json:"invite_id"`
	Token        string `json:"token"`
	ExpiresAt    string `json:"expires_at"`
	BootstrapURL string `json:"bootstrap_url"`
	Command      string `json:"command,omitempty"`
	Message      string `json:"message"`
}

type inviteInfo struct {
	ID              string            `json:"id"`
	Status          string            `json:"status"`
	CreatedAt       string            `json:"created_at"`
	ExpiresAt       string            `json:"expires_at,omitempty"`
	RedeemedAt      string            `json:"redeemed_at,omitempty"`
	RedeemedBy      string            `json:"redeemed_by,omitempty"`
	RevokedAt       string            `json:"revoked_at,omitempty"`
	DeletedAt       string            `json:"deleted_at,omitempty"`
	DeletedBy       string            `json:"deleted_by,omitempty"`
	IssuedBy        string            `json:"issued_by"`
	DisplayNameHint string            `json:"display_name_hint,omitempty"`
	DNSOverride     string            `json:"dns_override,omitempty"`
	PoolName        string            `json:"pool_name,omitempty"`
	TargetRole      string            `json:"target_role,omitempty"`
	DeviceName      string            `json:"device_name,omitempty"`
	MaxUses         int               `json:"max_uses,omitempty"`
	UsedCount       int               `json:"used_count,omitempty"`
	Labels          map[string]string `json:"labels,omitempty"`
}

type inviteListResponse struct {
	InviteCount int          `json:"invite_count"`
	Invites     []inviteInfo `json:"invites"`
}

type inviteActionResponse struct {
	Success bool   `json:"success"`
	Message string `json:"message"`
}

// ── invite create ──────────────────────────────────────────────────────

func cmdInviteCreate(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	ttl := fs.Int("ttl", 0, "TTL in hours (default 72)")
	pool := fs.String("pool", "", "address pool name")
	dns := fs.String("dns", "", "DNS server(s) for the peer")
	role := fs.String("role", "", "target role (user or admin)")
	maxUses := fs.Int("max-uses", 0, "maximum redemption count (default 1)")
	labelsRaw := fs.String("labels", "", `comma-separated key=value pairs (e.g. "env=prod,team=frontend")`)
	nameHint := fs.String("name-hint", "", "display name hint for the invite")
	deviceName := fs.String("device-name", "", "pre-bound device name")
	format := fs.String("format", "human", "output format (human|json)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *format != "human" && *format != "json" {
		return fmt.Errorf("unsupported format %q", *format)
	}

	body := map[string]any{}
	if *ttl > 0 {
		body["ttl_hours"] = *ttl
	}
	if *pool != "" {
		body["pool_name"] = *pool
	}
	if *dns != "" {
		body["dns"] = *dns
	}
	if *role != "" {
		body["target_role"] = *role
	}
	if *maxUses > 0 {
		body["max_uses"] = *maxUses
	}
	if *nameHint != "" {
		body["name_hint"] = *nameHint
	}
	if *deviceName != "" {
		body["device_name"] = *deviceName
	}
	if *labelsRaw != "" {
		labels, err := parseLabelsFlag(*labelsRaw)
		if err != nil {
			return fmt.Errorf("invalid --labels: %w", err)
		}
		body["labels"] = labels
	}

	var resp inviteCreateResponse
	if len(body) == 0 {
		if err := c.PostJSON("/api/v1/invites", struct{}{}, &resp); err != nil {
			return fmt.Errorf("create invite: %w", err)
		}
	} else {
		if err := c.PostJSON("/api/v1/invites", body, &resp); err != nil {
			return fmt.Errorf("create invite: %w", err)
		}
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	fmt.Printf("Invite created: %s\n", resp.InviteID)
	fmt.Printf("Token:       %s\n", resp.Token)
	fmt.Printf("Expires:     %s\n", resp.ExpiresAt)
	fmt.Println()
	fmt.Println("Bootstrap URL (share this with the client):")
	fmt.Printf("  %s\n", resp.BootstrapURL)
	fmt.Println()
	fmt.Println("Copy-and-paste command:")
	if resp.Command != "" {
		fmt.Printf("  %s\n", resp.Command)
	} else {
		fmt.Printf("  curl -sSf \"%s\" | sudo bash\n", resp.BootstrapURL)
	}
	fmt.Println()
	fmt.Printf("%s\n", resp.Message)
	return nil
}

// ── invite list ────────────────────────────────────────────────────────

func cmdInviteList(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	format := fs.String("format", "human", "output format (human|json)")
	showDeleted := fs.Bool("show-deleted", false, "include soft-deleted invites")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *format != "human" && *format != "json" {
		return fmt.Errorf("unsupported format %q", *format)
	}

	path := "/api/v1/invites"
	if *showDeleted {
		path += "?show_deleted=true"
	}

	var resp inviteListResponse
	if err := c.GetJSON(path, &resp); err != nil {
		return fmt.Errorf("list invites: %w", err)
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if len(resp.Invites) == 0 {
		fmt.Println("No invites found.")
		return nil
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "ID\tSTATUS\tNAME HINT\tISSUED BY\tEXPIRES\tREDEEMED")
	for _, inv := range resp.Invites {
		id := inv.ID
		if len(id) > 12 {
			id = id[:12]
		}
		nameHint := inv.DisplayNameHint
		if nameHint == "" {
			nameHint = "-"
		}
		expires := "-"
		if inv.ExpiresAt != "" {
			expires = inv.ExpiresAt
		}
		redeemed := "-"
		if inv.RedeemedAt != "" {
			redeemed = fmt.Sprintf("%s by %s", inv.RedeemedAt, inv.RedeemedBy)
		}
		fmt.Fprintf(w, "%s\t%s\t%s\t%s\t%s\t%s\n",
			id,
			inv.Status,
			nameHint,
			inv.IssuedBy,
			expires,
			redeemed,
		)
	}
	return w.Flush()
}

// ── invite revoke ──────────────────────────────────────────────────────

func cmdInviteRevoke(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("revoke", flag.ExitOnError)
	id := fs.String("id", "", "invite ID to revoke")

	if err := fs.Parse(args); err != nil {
		return err
	}

	inviteID := firstNonEmpty(*id, firstArg(fs.Args()))
	if inviteID == "" {
		return fmt.Errorf("invite ID is required: wg-mgmt invite revoke <id>")
	}
	resolvedID, err := resolveInviteRef(c, inviteID)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/invites/%s", url.PathEscape(resolvedID))
	var resp inviteActionResponse
	if err := c.DeleteJSON(path, &resp); err != nil {
		return fmt.Errorf("revoke invite: %w", err)
	}

	fmt.Fprintln(os.Stdout, resp.Message)
	return nil
}

// ── invite delete ──────────────────────────────────────────────────────

func cmdInviteDelete(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	id := fs.String("id", "", "invite ID to soft-delete")

	if err := fs.Parse(args); err != nil {
		return err
	}

	inviteID := firstNonEmpty(*id, firstArg(fs.Args()))
	if inviteID == "" {
		return fmt.Errorf("invite ID is required: wg-mgmt invite delete <id>")
	}
	resolvedID, err := resolveInviteRef(c, inviteID)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/invites/%s?action=delete", url.PathEscape(resolvedID))
	var resp inviteActionResponse
	if err := c.DeleteJSON(path, &resp); err != nil {
		return fmt.Errorf("delete invite: %w", err)
	}

	fmt.Fprintln(os.Stdout, resp.Message)
	return nil
}

type inviteLinkResponse struct {
	InviteID     string `json:"invite_id"`
	Status       string `json:"status"`
	BootstrapURL string `json:"bootstrap_url"`
	Command      string `json:"command,omitempty"`
	Inspect      string `json:"inspect,omitempty"`
	Note         string `json:"note,omitempty"`
}

// ── invite link ────────────────────────────────────────────────────────

func cmdInviteLink(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("link", flag.ExitOnError)
	id := fs.String("id", "", "invite ID or raw token")
	name := fs.String("name", "my-device", "device name for the bootstrap URL")
	format := fs.String("format", "human", "output format (human|json)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	inviteID := firstNonEmpty(*id, firstArg(fs.Args()))
	if inviteID == "" {
		return fmt.Errorf("invite ID, name hint, or raw token is required: wg-mgmt invite link <id|name|token>")
	}

	if *format != "human" && *format != "json" {
		return fmt.Errorf("unsupported format %q", *format)
	}

	resolvedID, err := resolveInviteRef(c, inviteID)
	if err != nil {
		return err
	}

	path := fmt.Sprintf("/api/v1/invites/%s/link?name=%s", url.PathEscape(resolvedID), url.QueryEscape(*name))
	var resp inviteLinkResponse
	if err := c.GetJSON(path, &resp); err != nil {
		return fmt.Errorf("get invite link: %w", err)
	}

	if *format == "json" {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	if resp.Note != "" {
		fmt.Println(resp.Note)
	}

	if resp.BootstrapURL != "" {
		fmt.Println("Bootstrap URL:")
		fmt.Printf("  %s\n", resp.BootstrapURL)
		fmt.Println()
		fmt.Println("Copy-and-paste command:")
		if resp.Command != "" {
			fmt.Printf("  %s\n", resp.Command)
		} else {
			fmt.Printf("  curl -sSf \"%s\" | sudo bash\n", resp.BootstrapURL)
		}
	}

	if resp.Inspect != "" {
		fmt.Println()
		fmt.Println("Inspect before running:")
		fmt.Printf("  %s\n", resp.Inspect)
	}

	return nil
}

// ── invite force-delete ────────────────────────────────────────────────

func cmdInviteForceDelete(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("force-delete", flag.ExitOnError)
	id := fs.String("id", "", "invite ID to permanently delete")
	confirm := fs.String("confirm", "", "confirmation: must match --id value exactly")

	if err := fs.Parse(args); err != nil {
		return err
	}

	inviteID := firstNonEmpty(*id, firstArg(fs.Args()))
	if inviteID == "" {
		return fmt.Errorf("--id is required for force-delete")
	}
	resolvedID, err := resolveInviteRef(c, inviteID)
	if err != nil {
		return err
	}

	if *confirm == "" {
		return fmt.Errorf("--confirm is required for force-delete: use --confirm %s to confirm permanent deletion", inviteID)
	}

	if *confirm != inviteID && *confirm != resolvedID {
		return fmt.Errorf("confirmation mismatch: --confirm value %q must match %q or resolved ID %q", *confirm, inviteID, resolvedID)
	}

	path := fmt.Sprintf("/api/v1/invites/%s?action=force-delete", url.PathEscape(resolvedID))
	var resp inviteActionResponse
	if err := c.DeleteJSON(path, &resp); err != nil {
		return fmt.Errorf("force-delete invite: %w", err)
	}

	fmt.Fprintln(os.Stdout, resp.Message)
	return nil
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return strings.TrimSpace(args[0])
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func resolveInviteRef(c *cli.Client, ref string) (string, error) {
	ref = strings.TrimSpace(ref)
	if ref == "" {
		return "", fmt.Errorf("invite reference is required")
	}

	var resp inviteListResponse
	if err := c.GetJSON("/api/v1/invites?show_deleted=true", &resp); err != nil {
		return ref, nil
	}

	for _, invite := range resp.Invites {
		if invite.ID == ref {
			return invite.ID, nil
		}
	}

	var idMatches []inviteInfo
	for _, invite := range resp.Invites {
		if strings.HasPrefix(invite.ID, ref) {
			idMatches = append(idMatches, invite)
		}
	}
	if len(idMatches) == 1 {
		return idMatches[0].ID, nil
	}
	if len(idMatches) > 1 {
		return "", fmt.Errorf("invite reference %q is ambiguous; use a longer ID prefix", ref)
	}

	var nameMatches []inviteInfo
	for _, invite := range resp.Invites {
		if invite.DisplayNameHint == ref || invite.DeviceName == ref {
			nameMatches = append(nameMatches, invite)
		}
	}
	if len(nameMatches) == 1 {
		return nameMatches[0].ID, nil
	}
	if len(nameMatches) > 1 {
		return "", fmt.Errorf("invite name %q is ambiguous; use the ID prefix instead", ref)
	}

	return ref, nil
}

// ── invite qrcode ──────────────────────────────────────────────────────

func cmdInviteQRCode(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("qrcode", flag.ExitOnError)
	id := fs.String("id", "", "invite token (raw token from invite create)")
	name := fs.String("name", "mobile", "device name for the QR bootstrap URL")
	output := fs.String("output", "", "output SVG file path (required)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *id == "" {
		return fmt.Errorf("--id (invite token) is required for QR code generation")
	}
	if *output == "" {
		return fmt.Errorf("--output is required for QR code generation")
	}

	path := fmt.Sprintf("/api/v1/invites/qrcode?token=%s&name=%s", *id, *name)
	svgBytes, err := c.GetRaw(path)
	if err != nil {
		return fmt.Errorf("fetch QR code: %w", err)
	}

	if err := os.WriteFile(*output, svgBytes, 0644); err != nil {
		return fmt.Errorf("write QR SVG: %w", err)
	}

	fmt.Printf("QR code written to %s\n", *output)
	return nil
}

// parseLabelsFlag parses a comma-separated list of key=value pairs into a map.
func parseLabelsFlag(raw string) (map[string]string, error) {
	labels := make(map[string]string)
	for pair := range strings.SplitSeq(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		kv := strings.SplitN(pair, "=", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("invalid label pair %q: expected key=value format", pair)
		}
		key := strings.TrimSpace(kv[0])
		val := strings.TrimSpace(kv[1])
		if key == "" {
			return nil, fmt.Errorf("invalid label pair %q: empty key", pair)
		}
		labels[key] = val
	}
	return labels, nil
}

func runUser(c *cli.Client, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: wg-mgmt user <subcommand>")
		fmt.Fprintln(os.Stderr, "Subcommands: list, create, delete")
		fmt.Fprintln(os.Stderr, "Try: wg-mgmt user list --help")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "list":
		return cmdUserList(c, subArgs)
	case "create":
		return cmdUserCreate(c, subArgs)
	case "delete":
		return cmdUserDelete(c, subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown user subcommand %q\n", sub)
		return nil
	}
}

func cmdUserList(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("list", flag.ExitOnError)
	formatJSON := fs.Bool("format", false, "output as JSON")

	if err := fs.Parse(args); err != nil {
		return err
	}

	var resp userListResponse
	if err := c.GetJSON("/api/v1/users", &resp); err != nil {
		return fmt.Errorf("list users: %w", err)
	}

	if *formatJSON {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		return enc.Encode(resp)
	}

	w := tabwriter.NewWriter(os.Stdout, 2, 2, 2, ' ', 0)
	fmt.Fprintln(w, "NAME\tROLE\tCREATED")
	for _, u := range resp.Users {
		fmt.Fprintf(w, "%s\t%s\t%s\n", u.Name, u.Role, u.CreatedAt)
	}
	return w.Flush()
}

func cmdUserCreate(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("create", flag.ExitOnError)
	name := fs.String("name", "", "username for the new account")
	password := fs.String("password", "", "password for the new account")
	role := fs.String("role", "user", "role (owner, admin, user)")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *name == "" || *password == "" {
		return fmt.Errorf("--name and --password are required")
	}

	body := map[string]string{
		"name":     *name,
		"password": *password,
		"role":     *role,
	}

	var resp userCreateResponse
	if err := c.PostJSON("/api/v1/users", body, &resp); err != nil {
		return fmt.Errorf("create user: %w", err)
	}

	fmt.Fprintf(os.Stdout, "User %q created with role %s\n", resp.User.Name, resp.User.Role)
	return nil
}

func cmdUserDelete(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("delete", flag.ExitOnError)
	name := fs.String("name", "", "username to delete")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if *name == "" {
		return fmt.Errorf("--name is required for user deletion")
	}

	var resp userDeleteResponse
	path := fmt.Sprintf("/api/v1/users/%s", *name)
	if err := c.DeleteJSON(path, &resp); err != nil {
		return fmt.Errorf("delete user: %w", err)
	}
	fmt.Fprintln(os.Stdout, resp.Message)
	return nil
}

type outputFormat string

const (
	outputHuman outputFormat = "human"
	outputJSON  outputFormat = "json"
)

// PortString unmarshals a WireGuard listen port from JSON (daemon returns string; tests may pass number).
type PortString string

func (p *PortString) UnmarshalJSON(b []byte) error {
	if len(b) > 0 && b[0] == '"' {
		return json.Unmarshal(b, (*string)(p))
	}
	var n int
	if err := json.Unmarshal(b, &n); err != nil {
		return err
	}
	*p = PortString(strconv.Itoa(n))
	return nil
}

type statusResponse struct {
	Interface  string     `json:"interface"`
	ListenPort PortString `json:"listen_port,omitempty"`
	Daemon     string     `json:"daemon"`
	WireGuard  string     `json:"wireguard"`
	WGError    string     `json:"wg_error,omitempty"`
	PeerOnline int        `json:"peer_online,omitempty"`
	PeerTotal  int        `json:"peer_total,omitempty"`
	PeerCount  int        `json:"peer_count,omitempty"`
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
			return fmt.Errorf("status requires an API key in config.env or a session token via MGMT_SESSION_TOKEN or --session-token")
		}
		authClient = client.WithSessionToken(sessionToken)
	}

	var response statusResponse
	if err := authClient.GetJSON("/api/v1/status", &response); err != nil {
		return formatCLIError(err)
	}

	return renderCLIOutput(format, response, renderStatusHuman, renderStatusJSON)
}

func runAuth(c *cli.Client, args []string) error {
	if len(args) == 0 {
		fmt.Fprintln(os.Stderr, "Usage: wg-mgmt auth <subcommand>")
		fmt.Fprintln(os.Stderr, "Subcommands: login, logout")
		return nil
	}

	sub := args[0]
	subArgs := args[1:]
	switch sub {
	case "login":
		return cmdAuthLogin(c, subArgs)
	case "logout":
		return cmdAuthLogout(c, subArgs)
	default:
		fmt.Fprintf(os.Stderr, "unknown auth subcommand %q\n", sub)
		return nil
	}
}

func cmdAuthLogin(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("login", flag.ExitOnError)
	name := fs.String("name", "", "username")
	password := fs.String("password", "", "password")

	if err := fs.Parse(args); err != nil {
		return err
	}

	if c.CurrentAuthMethod() == "API key" {
		fmt.Println("Already authenticated with API key from config.env")
		return nil
	}

	username := strings.TrimSpace(*name)
	pass := strings.TrimSpace(*password)

	if username == "" || pass == "" {
		reader := bufio.NewReader(os.Stdin)
		if username == "" {
			fmt.Fprint(os.Stderr, "Username: ")
			input, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read username: %w", err)
			}
			username = strings.TrimSpace(input)
		}
		if pass == "" {
			fmt.Fprint(os.Stderr, "Password: ")
			input, err := reader.ReadString('\n')
			if err != nil {
				return fmt.Errorf("read password: %w", err)
			}
			pass = strings.TrimSpace(input)
		}
	}

	if username == "" || pass == "" {
		return fmt.Errorf("username and password are required; use --name and --password flags or interactive prompts")
	}

	resp, err := c.Login(username, pass)
	if err != nil {
		return fmt.Errorf("login failed: %w", err)
	}

	fmt.Printf("Logged in as %s\n", username)
	fmt.Printf("Role: %s\n", resp.Role)
	fmt.Printf("Session token (store in MGMT_SESSION_TOKEN):\n%s\n", resp.Token)
	return nil
}

func cmdAuthLogout(c *cli.Client, args []string) error {
	fs := flag.NewFlagSet("logout", flag.ExitOnError)
	_ = fs.Parse(args)

	if !c.HasSessionToken() {
		fmt.Println("No active session to log out")
		return nil
	}

	if err := c.Logout(); err != nil {
		return fmt.Errorf("logout failed: %w", err)
	}

	fmt.Println("Logged out. Unset MGMT_SESSION_TOKEN if set.")
	return nil
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
	if status.ListenPort != "" {
		fmt.Printf("listen_port: %s\n", string(status.ListenPort))
	}
	if status.WGError != "" {
		fmt.Printf("wg_error: %s\n", status.WGError)
	}
	fmt.Printf("peers: %d online / %d total\n", status.PeerOnline, status.PeerTotal)
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
