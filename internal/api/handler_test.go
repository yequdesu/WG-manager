package api

import (
	"testing"
)

// TestPublicHost verifies that PublicHost() returns the domain when
// ServerHost is set, and falls back to ServerPublicIP when it is not.
func TestPublicHost(t *testing.T) {
	// Domain-first: when ServerHost is set, it should be returned.
	cfg := &Config{
		ServerHost:     "vpn.example.com",
		ServerPublicIP: "203.0.113.10",
	}
	if got := cfg.PublicHost(); got != "vpn.example.com" {
		t.Errorf("PublicHost() with domain = %q, want %q", got, "vpn.example.com")
	}

	// IP fallback: when ServerHost is empty, ServerPublicIP should be used.
	cfg2 := &Config{
		ServerHost:     "",
		ServerPublicIP: "203.0.113.10",
	}
	if got := cfg2.PublicHost(); got != "203.0.113.10" {
		t.Errorf("PublicHost() without domain = %q, want %q", got, "203.0.113.10")
	}

	// Empty both: edge case, should return empty string.
	cfg3 := &Config{
		ServerHost:     "",
		ServerPublicIP: "",
	}
	if got := cfg3.PublicHost(); got != "" {
		t.Errorf("PublicHost() with both empty = %q, want empty", got)
	}
}

// TestPublicURL verifies that PublicURL() returns https://domain
// when ServerHost is a domain, and http://ip when only IP is available.
func TestPublicURL(t *testing.T) {
	// Domain mode: should always produce https:// URLs.
	cfg := &Config{
		ServerHost:     "vpn.example.com",
		ServerPublicIP: "203.0.113.10",
	}
	if got := cfg.PublicURL(); got != "https://vpn.example.com" {
		t.Errorf("PublicURL() with domain = %q, want %q", got, "https://vpn.example.com")
	}

	// IP fallback: should produce http:// URLs.
	cfg2 := &Config{
		ServerHost:     "",
		ServerPublicIP: "203.0.113.10",
	}
	if got := cfg2.PublicURL(); got != "http://203.0.113.10" {
		t.Errorf("PublicURL() without domain = %q, want %q", got, "http://203.0.113.10")
	}

	// Empty both: edge case, should return empty string.
	cfg3 := &Config{
		ServerHost:     "",
		ServerPublicIP: "",
	}
	if got := cfg3.PublicURL(); got != "" {
		t.Errorf("PublicURL() with both empty = %q, want empty", got)
	}
}

// TestServerEndpointUnchanged verifies that ServerEndpoint() remains
// IP:PORT based and is NOT affected by the ServerHost field.
func TestServerEndpointUnchanged(t *testing.T) {
	cfg := &Config{
		ServerHost:     "vpn.example.com",
		ServerPublicIP: "203.0.113.10",
		WGPort:         51820,
	}
	if got := cfg.ServerEndpoint(); got != "203.0.113.10:51820" {
		t.Errorf("ServerEndpoint() with domain = %q, want %q", got, "203.0.113.10:51820")
	}
}
