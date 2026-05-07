package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL      string
	apiKey       string
	sessionToken string
	http         *http.Client
}

type LoginResponse struct {
	Token string `json:"token"`
	Role  string `json:"role"`
}

type MeResponse struct {
	Name      string `json:"name"`
	Role      string `json:"role"`
	CreatedAt string `json:"created_at"`
}

func NewClient(mgmtAddr, apiKey string) *Client {
	return &Client{
		baseURL: "http://" + strings.TrimSpace(mgmtAddr),
		apiKey:  strings.TrimSpace(apiKey),
		http:    &http.Client{Timeout: 10 * time.Second},
	}
}

func (c *Client) GetJSON(path string, out any) error {
	return c.doJSON(http.MethodGet, path, nil, out)
}

func (c *Client) PostJSON(path string, in any, out any) error {
	return c.doJSON(http.MethodPost, path, in, out)
}

func (c *Client) Delete(path string) error {
	return c.doJSON(http.MethodDelete, path, nil, nil)
}

func (c *Client) DeleteJSON(path string, out any) error {
	return c.doJSON(http.MethodDelete, path, nil, out)
}

func (c *Client) Login(name, password string) (*LoginResponse, error) {
	var response LoginResponse
	if err := c.doJSONWithAuth(http.MethodPost, "/api/v1/login", map[string]string{
		"name":     name,
		"password": password,
	}, &response, ""); err != nil {
		return nil, err
	}
	if response.Token == "" {
		return nil, fmt.Errorf("login succeeded without a session token")
	}
	c.sessionToken = response.Token
	return &response, nil
}

func (c *Client) Logout() error {
	if err := c.doJSON(http.MethodPost, "/api/v1/logout", nil, nil); err != nil {
		return err
	}
	c.sessionToken = ""
	return nil
}

func (c *Client) WithSessionToken(token string) *Client {
	clone := *c
	clone.sessionToken = strings.TrimSpace(token)
	return &clone
}

func (c *Client) CurrentAuthMethod() string {
	if c.sessionToken != "" {
		return "session token"
	}
	if c.apiKey != "" {
		return "API key"
	}
	return "none"
}

func (c *Client) HasSessionToken() bool {
	return c.sessionToken != ""
}

func (c *Client) Me() (*MeResponse, error) {
	var response MeResponse
	if err := c.GetJSON("/api/v1/me", &response); err != nil {
		return nil, err
	}
	return &response, nil
}

func (c *Client) doJSON(method, path string, in any, out any) error {
	return c.doJSONWithAuth(method, path, in, out, c.authHeader())
}

func (c *Client) doJSONWithAuth(method, path string, in any, out any, authHeader string) error {
	var body bytes.Buffer
	if in != nil {
		if err := json.NewEncoder(&body).Encode(in); err != nil {
			return fmt.Errorf("encode request body: %w", err)
		}
	}

	req, err := http.NewRequest(method, c.baseURL+path, &body)
	if err != nil {
		return fmt.Errorf("create request: %w", err)
	}
	if in == nil {
		req.Body = http.NoBody
	}
	if authHeader != "" {
		req.Header.Set("Authorization", authHeader)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connect to daemon at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		message := strings.TrimSpace(string(payload))
		if message != "" {
			return fmt.Errorf("%s %s failed: %s: %s", method, path, resp.Status, message)
		}
		return fmt.Errorf("%s %s failed: %s", method, path, resp.Status)
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}

func (c *Client) GetRaw(path string) ([]byte, error) {
	req, err := http.NewRequest(http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, fmt.Errorf("create request: %w", err)
	}
	if auth := c.authHeader(); auth != "" {
		req.Header.Set("Authorization", auth)
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("connect to daemon at %s: %w", c.baseURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		payload, _ := io.ReadAll(resp.Body)
		message := strings.TrimSpace(string(payload))
		if message != "" {
			return nil, fmt.Errorf("GET %s failed: %s: %s", path, resp.Status, message)
		}
		return nil, fmt.Errorf("GET %s failed: %s", path, resp.Status)
	}

	return io.ReadAll(resp.Body)
}

func (c *Client) authHeader() string {
	if c.sessionToken != "" {
		return "Bearer " + c.sessionToken
	}
	if c.apiKey != "" {
		return "Bearer " + c.apiKey
	}
	return ""
}
