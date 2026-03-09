package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type apiClient struct {
	baseURL string
	http    *http.Client
}

func newAPIClient(baseURL string) (*apiClient, error) {
	baseURL = strings.TrimSpace(baseURL)
	if baseURL == "" {
		return nil, usageErrorf("--api-url is required for this command")
	}
	if !strings.HasPrefix(baseURL, "http://") && !strings.HasPrefix(baseURL, "https://") {
		baseURL = "http://" + baseURL
	}
	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, runtimeErrorf("invalid api url %q", baseURL)
	}
	parsed.Path = strings.TrimSuffix(parsed.Path, "/")
	return &apiClient{
		baseURL: parsed.String(),
		http:    &http.Client{Timeout: 5 * time.Second},
	}, nil
}

func (c *apiClient) get(path string, out interface{}) error {
	return c.do(http.MethodGet, path, nil, out)
}

func (c *apiClient) post(path string, body interface{}, out interface{}) error {
	return c.do(http.MethodPost, path, body, out)
}

func (c *apiClient) do(method, path string, body interface{}, out interface{}) error {
	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			return err
		}
		reader = bytes.NewReader(payload)
	}
	req, err := http.NewRequest(method, c.baseURL+path, reader)
	if err != nil {
		return err
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return runtimeErrorf("failed to reach %s: %v", c.baseURL, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return err
	}
	if resp.StatusCode >= 400 {
		if len(raw) == 0 {
			return runtimeErrorf("%s %s returned %s", method, path, resp.Status)
		}
		return runtimeErrorf("%s %s returned %s: %s", method, path, resp.Status, strings.TrimSpace(string(raw)))
	}
	if out == nil || len(raw) == 0 {
		return nil
	}
	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("decode %s %s response: %w", method, path, err)
	}
	return nil
}
