// Package dispatcharr is a minimal client for the Dispatcharr REST API: just
// the channel, stream and M3U account endpoints rematch needs.
package dispatcharr

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"
)

const pageSize = 500

type Channel struct {
	ID             int    `json:"id"`
	Name           string `json:"name"`
	ChannelGroupID *int   `json:"channel_group_id"`
	Streams        []int  `json:"streams"`
}

type Stream struct {
	ID           int          `json:"id"`
	Name         string       `json:"name"`
	ChannelGroup *int         `json:"channel_group"`
	IsStale      bool         `json:"is_stale"`
	StreamStats  *StreamStats `json:"stream_stats"`
}

// StreamStats is what Dispatcharr records after it has actually played a
// stream; null until then.
type StreamStats struct {
	Resolution string `json:"resolution"` // e.g. "1920x1080"
}

type M3UAccount struct {
	ID          int     `json:"id"`
	Name        string  `json:"name"`
	IsActive    bool    `json:"is_active"`
	Status      string  `json:"status"`
	UpdatedAt   *string `json:"updated_at"`
	LastMessage *string `json:"last_message"`
}

type Client struct {
	baseURL  string
	username string
	password string
	http     *http.Client
	token    string
}

// New logs in and returns a client carrying the resulting access token.
func New(ctx context.Context, baseURL, username, password string) (*Client, error) {
	c := &Client{
		baseURL:  strings.TrimRight(baseURL, "/"),
		username: username,
		password: password,
		http:     &http.Client{Timeout: 30 * time.Second},
	}
	if err := c.login(ctx); err != nil {
		return nil, err
	}
	return c, nil
}

func (c *Client) login(ctx context.Context) error {
	body, _ := json.Marshal(map[string]string{"username": c.username, "password": c.password})
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/accounts/auth/login/", bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("login: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("login: %s", resp.Status)
	}
	var out struct {
		Access string `json:"access"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return fmt.Errorf("login: decode response: %w", err)
	}
	c.token = out.Access
	return nil
}

// do sends an authenticated request, logging in again once on a 401 (the
// access token expired mid-run), and decodes a JSON response into out.
func (c *Client) do(ctx context.Context, method, path string, in, out any) error {
	var payload []byte
	if in != nil {
		var err error
		if payload, err = json.Marshal(in); err != nil {
			return err
		}
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, bytes.NewReader(payload))
		if err != nil {
			return err
		}
		req.Header.Set("Authorization", "Bearer "+c.token)
		if in != nil {
			req.Header.Set("Content-Type", "application/json")
		}
		resp, err := c.http.Do(req)
		if err != nil {
			return fmt.Errorf("%s %s: %w", method, path, err)
		}

		if resp.StatusCode == http.StatusUnauthorized && attempt == 0 {
			resp.Body.Close()
			if err := c.login(ctx); err != nil {
				return err
			}
			continue
		}

		defer resp.Body.Close()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
			return fmt.Errorf("%s %s: %s: %s", method, path, resp.Status, strings.TrimSpace(string(msg)))
		}
		if out == nil {
			return nil
		}
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
			return fmt.Errorf("%s %s: decode response: %w", method, path, err)
		}
		return nil
	}
}

type page[T any] struct {
	Next    *string `json:"next"`
	Results []T     `json:"results"`
}

func paginated[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	var all []T
	next := fmt.Sprintf("%s?page_size=%d", path, pageSize)
	for next != "" {
		var p page[T]
		if err := c.do(ctx, http.MethodGet, next, nil, &p); err != nil {
			return nil, err
		}
		all = append(all, p.Results...)
		next = ""
		if p.Next != nil && *p.Next != "" {
			// `next` is absolute and built from Dispatcharr's own idea of its
			// host, which may differ from baseURL (e.g. behind Traefik); keep
			// only the path and query.
			u, err := url.Parse(*p.Next)
			if err != nil {
				return nil, fmt.Errorf("parse next page URL %q: %w", *p.Next, err)
			}
			next = u.RequestURI()
		}
	}
	return all, nil
}

func (c *Client) ListChannels(ctx context.Context) ([]Channel, error) {
	return paginated[Channel](ctx, c, "/api/channels/channels/")
}

func (c *Client) ListStreams(ctx context.Context) ([]Stream, error) {
	return paginated[Stream](ctx, c, "/api/channels/streams/")
}

func (c *Client) PatchChannelStreams(ctx context.Context, channelID int, streamIDs []int) error {
	return c.do(ctx, http.MethodPatch, fmt.Sprintf("/api/channels/channels/%d/", channelID), map[string][]int{"streams": streamIDs}, nil)
}

// RefreshAllM3UAccounts queues a refresh of every active M3U account. It
// returns immediately; the refresh runs asynchronously in Dispatcharr.
func (c *Client) RefreshAllM3UAccounts(ctx context.Context) error {
	return c.do(ctx, http.MethodPost, "/api/m3u/refresh/", nil, nil)
}

func (c *Client) ListActiveM3UAccounts(ctx context.Context) ([]M3UAccount, error) {
	// Unpaginated in current Dispatcharr, but tolerate a paginated response.
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "/api/m3u/accounts/", nil, &raw); err != nil {
		return nil, err
	}
	var accounts []M3UAccount
	if err := json.Unmarshal(raw, &accounts); err != nil {
		var p page[M3UAccount]
		if err2 := json.Unmarshal(raw, &p); err2 != nil {
			return nil, fmt.Errorf("decode M3U accounts: %w", err)
		}
		accounts = p.Results
	}
	return slices.DeleteFunc(accounts, func(a M3UAccount) bool { return !a.IsActive }), nil
}
