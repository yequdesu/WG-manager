package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	baseURL string
	apiKey  string
	http    *http.Client
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

func (c *Client) doJSON(method, path string, in any, out any) error {
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
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}
	if in != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("perform request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("unexpected status: %s", resp.Status)
	}

	if out == nil {
		return nil
	}

	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response body: %w", err)
	}
	return nil
}
