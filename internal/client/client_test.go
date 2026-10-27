package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestGetRetriesAndAuth(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if r.Header.Get("x-api-key") != "k" || r.Header.Get("Authorization") != "" {
			t.Errorf("auth headers: %v", r.Header)
		}
		if r.URL.Query().Get("a") != "1" {
			t.Errorf("query = %s", r.URL.RawQuery)
		}
		if calls.Add(1) < 3 {
			w.WriteHeader(529)
			return
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c := &Client{Provider: "anthropic", BaseURL: srv.URL + "/v1/", Key: "k", AuthStyle: AuthXAPIKey, RetryWait: time.Millisecond}
	var v struct{ OK bool }
	if _, err := c.GetJSON(context.Background(), "/x", map[string][]string{"a": {"1"}}, &v); err != nil || !v.OK || calls.Load() != 3 {
		t.Fatalf("err=%v v=%v calls=%d", err, v, calls.Load())
	}
}

func TestGetErrorNoRetry(t *testing.T) {
	var calls atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Header.Get("Authorization") != "Bearer k" {
			t.Errorf("bearer missing")
		}
		w.WriteHeader(http.StatusForbidden)
		w.Write([]byte(`{"error":{"message":"needs admin","type":"invalid_request_error"}}`))
	}))
	defer srv.Close()
	c := &Client{Provider: "openai", BaseURL: srv.URL, Key: "k"}
	_, err := c.Get(context.Background(), "/y", nil)
	var e *APIError
	if !errors.As(err, &e) || e.Status != 403 || e.Message != "needs admin" || calls.Load() != 1 {
		t.Fatalf("err=%v calls=%d", err, calls.Load())
	}
	if !strings.Contains(err.Error(), "sk-admin-") {
		t.Fatalf("hint missing: %v", err)
	}
	if strings.Contains(err.Error(), "Bearer k") {
		t.Fatal("key leaked into error")
	}
}

func TestParseErrorPlainText(t *testing.T) {
	e := ParseError("openai", 502, []byte("<html>bad gateway</html>"))
	if e.Message != "<html>bad gateway</html>" {
		t.Fatalf("%+v", e)
	}
}
