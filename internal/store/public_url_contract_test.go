package store_test

import (
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"wire-guard-dev/internal/api"
	"wire-guard-dev/internal/store"
)

func newContractHandler(t *testing.T, publicHost string) (*api.Handler, *store.State) {
	t.Helper()

	statePath := filepath.Join(t.TempDir(), "state.json")
	state := store.NewState(statePath, nil)
	config := &api.Config{
		WGPort:         51820,
		WGSubnet:       "10.0.0.0/24",
		WGServerIP:     "10.0.0.1",
		ServerPublicIP: publicHost,
	}

	return api.NewHandler(state, nil, config), state
}

func bootstrapScheme(publicHost string) string {
	if net.ParseIP(publicHost) != nil {
		return "http"
	}
	return "https"
}

func inviteBootstrapURL(publicHost, token string) string {
	return fmt.Sprintf("%s://%s/bootstrap?token=%s", bootstrapScheme(publicHost), publicHost, token)
}

func connectBootstrapURL(publicHost, token, name string) string {
	baseURL := inviteBootstrapURL(publicHost, token)
	if name != "" {
		baseURL += "&name=" + name
	}
	return baseURL
}

func TestCreateInviteIncludesBootstrapURL(t *testing.T) {
	for _, tc := range []struct {
		name       string
		publicHost string
	}{
		{name: "domain", publicHost: "vpn.example.test"},
		{name: "ip", publicHost: "203.0.113.10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Run("without_device_name", func(t *testing.T) {
				handler, _ := newContractHandler(t, tc.publicHost)

				request := httptest.NewRequest(http.MethodPost, "/api/v1/invites", strings.NewReader("{}"))
				recorder := httptest.NewRecorder()

				handler.CreateInvite(recorder, request)

				if recorder.Code != http.StatusCreated {
					t.Fatalf("CreateInvite status = %d, want %d; body=%s", recorder.Code, http.StatusCreated, recorder.Body.String())
				}

				var payload map[string]any
				if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
					t.Fatalf("unmarshal CreateInvite response: %v", err)
				}

				token, ok := payload["token"].(string)
				if !ok || token == "" {
					t.Fatalf("CreateInvite response token missing: %v", payload)
				}

				bootstrapURL, ok := payload["bootstrap_url"].(string)
				if !ok {
					t.Fatalf("CreateInvite response missing bootstrap_url: %v", payload)
				}

				want := inviteBootstrapURL(tc.publicHost, token)
				if bootstrapURL != want {
					t.Fatalf("bootstrap_url = %q, want %q", bootstrapURL, want)
				}

				command, ok := payload["command"].(string)
				if !ok || command == "" {
					t.Fatalf("CreateInvite response missing command: %v", payload)
				}
				if strings.Contains(command, "&name=") {
					t.Fatalf("command = %q, want no name parameter without device_name", command)
				}
			})

			t.Run("with_device_name", func(t *testing.T) {
				handler, _ := newContractHandler(t, tc.publicHost)

				request := httptest.NewRequest(http.MethodPost, "/api/v1/invites", strings.NewReader(`{"device_name":"laptop"}`))
				recorder := httptest.NewRecorder()

				handler.CreateInvite(recorder, request)

				if recorder.Code != http.StatusCreated {
					t.Fatalf("CreateInvite status = %d, want %d; body=%s", recorder.Code, http.StatusCreated, recorder.Body.String())
				}

				var payload map[string]any
				if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
					t.Fatalf("unmarshal CreateInvite response: %v", err)
				}

				token := payload["token"].(string)
				want := connectBootstrapURL(tc.publicHost, token, "laptop")
				if got := payload["bootstrap_url"]; got != want {
					t.Fatalf("bootstrap_url = %q, want %q", got, want)
				}
				if command := payload["command"].(string); !strings.Contains(command, want) {
					t.Fatalf("command = %q, want it to include %q", command, want)
				}
			})
		})
	}
}

func TestInviteLinkByIDUsesStoredRawToken(t *testing.T) {
	handler, state := newContractHandler(t, "vpn.example.test")
	rawToken, inv, err := state.CreateInvite("admin", time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite seed failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/invites/"+inv.ID+"/link?name=my-device", nil)
	recorder := httptest.NewRecorder()

	handler.InviteLink(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("InviteLink status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal InviteLink response: %v", err)
	}

	wantURL := connectBootstrapURL("vpn.example.test", rawToken, "my-device")
	if got := payload["bootstrap_url"]; got != wantURL {
		t.Fatalf("bootstrap_url = %q, want %q", got, wantURL)
	}
	command, ok := payload["command"].(string)
	if !ok || !strings.Contains(command, wantURL) {
		t.Fatalf("command = %v, want it to include %q", payload["command"], wantURL)
	}
}

func TestInviteLinkOmitsNameWhenUnset(t *testing.T) {
	handler, state := newContractHandler(t, "vpn.example.test")
	rawToken, inv, err := state.CreateInvite("admin", time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite seed failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/invites/"+inv.ID+"/link", nil)
	recorder := httptest.NewRecorder()

	handler.InviteLink(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("InviteLink status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal InviteLink response: %v", err)
	}

	wantURL := inviteBootstrapURL("vpn.example.test", rawToken)
	if got := payload["bootstrap_url"]; got != wantURL {
		t.Fatalf("bootstrap_url = %q, want %q", got, wantURL)
	}
}

func TestInviteLinkUsesStoredDeviceName(t *testing.T) {
	handler, state := newContractHandler(t, "vpn.example.test")
	rawToken, inv, err := state.CreateInvite("admin", time.Hour, store.WithDeviceName("laptop"))
	if err != nil {
		t.Fatalf("CreateInvite seed failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/invites/"+inv.ID+"/link", nil)
	recorder := httptest.NewRecorder()

	handler.InviteLink(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("InviteLink status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal InviteLink response: %v", err)
	}

	wantURL := connectBootstrapURL("vpn.example.test", rawToken, "laptop")
	if got := payload["bootstrap_url"]; got != wantURL {
		t.Fatalf("bootstrap_url = %q, want %q", got, wantURL)
	}
}

func TestInviteLinkExplicitNameOverridesStoredDeviceName(t *testing.T) {
	handler, state := newContractHandler(t, "vpn.example.test")
	rawToken, inv, err := state.CreateInvite("admin", time.Hour, store.WithDeviceName("laptop"))
	if err != nil {
		t.Fatalf("CreateInvite seed failed: %v", err)
	}

	request := httptest.NewRequest(http.MethodGet, "/api/v1/invites/"+inv.ID+"/link?name=desktop", nil)
	recorder := httptest.NewRecorder()

	handler.InviteLink(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("InviteLink status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal InviteLink response: %v", err)
	}

	wantURL := connectBootstrapURL("vpn.example.test", rawToken, "desktop")
	if got := payload["bootstrap_url"]; got != wantURL {
		t.Fatalf("bootstrap_url = %q, want %q", got, wantURL)
	}
}

func TestInviteLinkByIDHandlesLegacyMissingRawToken(t *testing.T) {
	handler, state := newContractHandler(t, "vpn.example.test")
	_, inv, err := state.CreateInvite("admin", time.Hour)
	if err != nil {
		t.Fatalf("CreateInvite seed failed: %v", err)
	}
	inv.RawToken = ""
	state.Invites[inv.ID] = inv

	request := httptest.NewRequest(http.MethodGet, "/api/v1/invites/"+inv.ID+"/link?name=my-device", nil)
	recorder := httptest.NewRecorder()

	handler.InviteLink(recorder, request)

	if recorder.Code != http.StatusOK {
		t.Fatalf("InviteLink status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
	}

	var payload map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &payload); err != nil {
		t.Fatalf("unmarshal InviteLink response: %v", err)
	}
	if _, ok := payload["bootstrap_url"]; ok {
		t.Fatalf("legacy invite without raw token should not return bootstrap_url: %v", payload)
	}
	if note, ok := payload["note"].(string); !ok || !strings.Contains(note, "cannot be reconstructed") {
		t.Fatalf("legacy invite response note = %v", payload["note"])
	}
}

func TestConnectPageUsesResolvedPublicHost(t *testing.T) {
	for _, tc := range []struct {
		name       string
		publicHost string
	}{
		{name: "domain", publicHost: "vpn.example.test"},
		{name: "ip", publicHost: "203.0.113.10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, _ := newContractHandler(t, tc.publicHost)

			request := httptest.NewRequest(http.MethodGet, "/connect", nil)
			request.Header.Set("User-Agent", "Mozilla/5.0")
			recorder := httptest.NewRecorder()

			handler.Connect(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("Connect status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}

			body := recorder.Body.String()
			wantInspect := fmt.Sprintf("curl -sSf %s://%s/bootstrap", bootstrapScheme(tc.publicHost), tc.publicHost)
			if !strings.Contains(body, wantInspect) {
				t.Fatalf("connect page missing inspect URL %q\nbody=%s", wantInspect, body)
			}

			wantRun := inviteBootstrapURL(tc.publicHost, "TOKEN")
			if !strings.Contains(body, wantRun) {
				t.Fatalf("connect page missing run URL %q\nbody=%s", wantRun, body)
			}
		})
	}
}

func TestInviteQRUsesSamePublicHost(t *testing.T) {
	for _, tc := range []struct {
		name       string
		publicHost string
	}{
		{name: "domain", publicHost: "vpn.example.test"},
		{name: "ip", publicHost: "203.0.113.10"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			handler, state := newContractHandler(t, tc.publicHost)

			rawToken, _, err := state.CreateInvite("admin", time.Hour)
			if err != nil {
				t.Fatalf("CreateInvite seed failed: %v", err)
			}

			qrencodeDir := t.TempDir()
			t.Setenv("PATH", qrencodeDir)

			request := httptest.NewRequest(http.MethodGet, "/api/v1/invites/qrcode?token="+rawToken+"&name=phone1", nil)
			recorder := httptest.NewRecorder()

			handler.ServeInviteQR(recorder, request)

			if recorder.Code != http.StatusOK {
				t.Fatalf("ServeInviteQR status = %d, want %d; body=%s", recorder.Code, http.StatusOK, recorder.Body.String())
			}

			wantPrefix := fmt.Sprintf("%s://%s/bootstrap?token=", bootstrapScheme(tc.publicHost), tc.publicHost)
			if !strings.Contains(recorder.Body.String(), wantPrefix) {
				t.Fatalf("QR output missing encoded host prefix %q\nbody=%s", wantPrefix, recorder.Body.String())
			}
		})
	}
}
