// Package client is a GET-only HTTP client for the providers' admin APIs.
// llmspend never changes anything on the provider side, so the client has no
// way to send a body or another method: read-only is a property of the type,
// not a promise in the docs.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

// UserAgent is sent on every request; the root command stamps the version in.
var UserAgent = "llmspend/dev"

// Auth styles.
const (
	AuthBearer  = "bearer"    // Authorization: Bearer <key> (OpenAI)
	AuthXAPIKey = "x-api-key" // x-api-key: <key> (Anthropic)
)

// Client talks to one provider org with one admin key.
type Client struct {
	Provider   string // "openai" or "anthropic", used in error messages
	BaseURL    string
	Key        string
	AuthStyle  string
	Headers    map[string]string
	Verbose    bool
	MaxRetries int
	RetryWait  time.Duration
	HTTP       *http.Client
}

// APIError is a non-2xx answer. It prints as one line: what went wrong,
// the provider's own words, and the fix.
type APIError struct {
	Provider string
	Status   int
	Message  string
	Type     string
	// Reason and Fix override the generic text; provider packages set them
	// for cases they can name (an Anthropic individual account, say).
	Reason string
	Fix    string
}

func (e *APIError) Error() string {
	msg := e.Message
	if msg == "" {
		msg = http.StatusText(e.Status)
	}
	detail := fmt.Sprintf("HTTP %d: %s", e.Status, msg)
	if e.Type != "" {
		detail += " [" + e.Type + "]"
	}
	reason, fix := e.Reason, e.Fix
	if reason == "" && fix == "" {
		reason, fix = e.generic()
	}
	s := e.Provider + ": "
	if reason != "" {
		s += reason + " (" + detail + ")"
	} else {
		s += detail
	}
	if fix != "" {
		s += " — fix: " + fix
	}
	return s
}

func (e *APIError) generic() (reason, fix string) {
	switch e.Status {
	case http.StatusUnauthorized:
		return "the admin key was rejected", "check it with 'llmspend config show' and 'llmspend orgs'; replace it with 'llmspend config add'"
	case http.StatusForbidden:
		if e.Provider == "openai" {
			return "this key may not read the org", "use an OpenAI admin key (sk-admin-…) from platform.openai.com/settings/organization/admin-keys"
		}
		return "this key may not read the org", "use an Admin API key (sk-ant-admin01-…)"
	case http.StatusNotFound:
		return "not available for this org", ""
	case http.StatusTooManyRequests:
		return "rate limited", "retry in a minute"
	}
	return "", ""
}

// ParseError builds an APIError from an error body. Both providers answer
// with {"error":{"message":"...","type":"..."}}; proxies may send plain text.
func ParseError(provider string, status int, body []byte) *APIError {
	e := &APIError{Provider: provider, Status: status}
	var head struct {
		Error struct {
			Message string `json:"message"`
			Type    string `json:"type"`
		} `json:"error"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &head) != nil {
		e.Message = strings.TrimSpace(string(body))
		if len(e.Message) > 300 {
			e.Message = e.Message[:300] + "…"
		}
		return e
	}
	e.Message, e.Type = head.Error.Message, head.Error.Type
	if e.Message == "" {
		e.Message = head.Message
	}
	return e
}

func retryable(status int) bool {
	switch status {
	case http.StatusTooManyRequests, http.StatusBadGateway, http.StatusServiceUnavailable, http.StatusGatewayTimeout, 529:
		return true
	}
	return false
}

// Response is a decoded 2xx answer plus the headers worth keeping.
type Response struct {
	Body   []byte
	Header http.Header
}

// Get fetches path (relative to BaseURL) with query q, retrying 429/5xx with
// backoff and honouring Retry-After.
func (c *Client) Get(ctx context.Context, path string, q url.Values) (*Response, error) {
	u := strings.TrimRight(c.BaseURL, "/") + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	wait := c.RetryWait
	if wait <= 0 {
		wait = time.Second
	}
	retries := c.MaxRetries
	if retries == 0 {
		retries = 3
	}
	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
		if err != nil {
			return nil, err
		}
		switch c.AuthStyle {
		case AuthXAPIKey:
			req.Header.Set("x-api-key", c.Key)
		default:
			req.Header.Set("Authorization", "Bearer "+c.Key)
		}
		for k, v := range c.Headers {
			req.Header.Set(k, v)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("User-Agent", UserAgent)
		if c.Verbose {
			fmt.Fprintf(os.Stderr, "→ GET %s\n", u)
		}
		resp, err := hc.Do(req)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Provider, err)
		}
		b, err := io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("%s: read response: %w", c.Provider, err)
		}
		if c.Verbose {
			fmt.Fprintf(os.Stderr, "← HTTP %d (%d bytes)\n", resp.StatusCode, len(b))
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			return &Response{Body: b, Header: resp.Header}, nil
		}
		if !retryable(resp.StatusCode) || attempt >= retries {
			return nil, ParseError(c.Provider, resp.StatusCode, b)
		}
		d := wait
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, e := strconv.Atoi(ra); e == nil && secs > 0 {
				d = time.Duration(secs) * time.Second
			}
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(d):
		}
		wait *= 2
	}
}

// GetJSON is Get decoded into v.
func (c *Client) GetJSON(ctx context.Context, path string, q url.Values, v any) (http.Header, error) {
	r, err := c.Get(ctx, path, q)
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(r.Body, v); err != nil {
		return nil, fmt.Errorf("%s: decode %s: %w", c.Provider, path, err)
	}
	return r.Header, nil
}
