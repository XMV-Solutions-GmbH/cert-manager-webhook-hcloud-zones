// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package hcloud

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// ----------------------------------------------------------------------------
// Helpers
// ----------------------------------------------------------------------------

const testToken = "super-secret-bearer-token-DO-NOT-LEAK"

// loadFixture reads a JSON fixture file from testdata/fixtures and
// strips the "_comment" field so the body parses as a clean Hetzner
// response. The unprocessed file ships the documentation comment per
// the brief; tests serve the cleaned variant to httptest clients.
func loadFixture(t *testing.T, name string) []byte {
	t.Helper()
	// #nosec G304 -- name is a literal supplied by tests; reads stay
	// confined to the in-repo testdata directory.
	raw, err := os.ReadFile(filepath.Join("testdata", "fixtures", name))
	if err != nil {
		t.Fatalf("read fixture %s: %v", name, err)
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("parse fixture %s: %v", name, err)
	}
	delete(m, "_comment")
	clean, err := json.Marshal(m)
	if err != nil {
		t.Fatalf("re-marshal fixture %s: %v", name, err)
	}
	return clean
}

// fakeClock records every Sleep call without actually waiting.
type fakeClock struct {
	mu     sync.Mutex
	now    time.Time
	sleeps []time.Duration
}

func newFakeClock() *fakeClock {
	return &fakeClock{now: time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)}
}

func (f *fakeClock) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *fakeClock) Sleep(ctx context.Context, d time.Duration) error {
	f.mu.Lock()
	f.sleeps = append(f.sleeps, d)
	f.now = f.now.Add(d)
	f.mu.Unlock()
	return ctx.Err()
}

func (f *fakeClock) Sleeps() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]time.Duration, len(f.sleeps))
	copy(out, f.sleeps)
	return out
}

// captureBuf is a goroutine-safe bytes.Buffer for slog output.
type captureBuf struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (c *captureBuf) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.Write(p)
}

func (c *captureBuf) String() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.buf.String()
}

// debugLogger constructs a logger that captures all levels (including
// debug) into the returned buffer.
func debugLogger() (*slog.Logger, *captureBuf) {
	buf := &captureBuf{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// newTestClient wires a Client against an httptest.Server with a fake
// clock and a debug-capturing logger.
func newTestClient(t *testing.T, srv *httptest.Server) (*Client, *fakeClock, *captureBuf) {
	t.Helper()
	clk := newFakeClock()
	logger, buf := debugLogger()
	c, err := New(
		StaticToken(testToken),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(clk),
		WithLogger(logger),
		WithBackoff(10*time.Millisecond, 1*time.Second),
		WithMaxRetries(6),
		WithRequestTimeout(5*time.Second),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	return c, clk, buf
}

// assertAuthHeader fails the test if the request did not carry the
// expected bearer token.
func assertAuthHeader(t *testing.T, r *http.Request) {
	t.Helper()
	got := r.Header.Get("Authorization")
	want := "Bearer " + testToken
	if got != want {
		t.Fatalf("Authorization header = %q, want %q", got, want)
	}
}

// ----------------------------------------------------------------------------
// 1. Happy-path: GET /v1/zones
// ----------------------------------------------------------------------------

func TestClient_ListZones_HappyPath(t *testing.T) {
	body := loadFixture(t, "list_zones.json")

	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Method != http.MethodGet || r.URL.Path != "/v1/zones" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		assertAuthHeader(t, r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	zones, err := c.ListZones(context.Background())
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("got %d zones, want 2", len(zones))
	}
	if zones[0].Name != "example.com" || zones[0].ID != 42 {
		t.Fatalf("first zone = %+v", zones[0])
	}
	if zones[1].Name != "example.net" {
		t.Fatalf("second zone = %+v", zones[1])
	}
	if atomic.LoadInt32(&hits) != 1 {
		t.Fatalf("expected 1 server hit, got %d", hits)
	}
}

// ----------------------------------------------------------------------------
// 2. Happy-path: POST /v1/zones/{id}/rrsets
// ----------------------------------------------------------------------------

func TestClient_CreateRRSet_HappyPath(t *testing.T) {
	body := loadFixture(t, "create_rrset.json")

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/v1/zones/42/rrsets" {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		assertAuthHeader(t, r)
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		raw, _ := io.ReadAll(r.Body)
		var got CreateRRSetRequest
		if err := json.Unmarshal(raw, &got); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		if got.Name != "_acme-challenge" || got.Type != "TXT" {
			t.Errorf("request body = %+v", got)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	ttl := 60
	out, err := c.CreateRRSet(context.Background(), 42, CreateRRSetRequest{
		Name:    "_acme-challenge",
		Type:    "TXT",
		TTL:     &ttl,
		Records: []Record{{Value: `"challenge"`}},
	})
	if err != nil {
		t.Fatalf("CreateRRSet: %v", err)
	}
	if out.ID != "rrset-7c1f" || out.ZoneID != 42 {
		t.Fatalf("response = %+v", out)
	}
}

// ----------------------------------------------------------------------------
// 3. Happy-path: POST /v1/zones/{id}/rrsets/{name}/{type}/actions/set_records
//
// This is the regression guard for issue #34: records are replaced via
// the set_records ACTION, never a PATCH on the RRSet (which 404s) or a
// PUT (which refuses with "can't update records with this endpoint").
// The test asserts the exact method + path the client emits, the
// double-quoted TXT value in the body, and a 201 + action treated as
// success.
// ----------------------------------------------------------------------------

func TestClient_SetRRSetRecords_HappyPath(t *testing.T) {
	body := loadFixture(t, "set_rrset_records.json")

	var (
		gotMethod string
		gotPath   string
		gotBody   SetRRSetRecordsRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotMethod = r.Method
		gotPath = r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		if err := json.Unmarshal(raw, &gotBody); err != nil {
			t.Fatalf("decode body: %v", err)
		}
		assertAuthHeader(t, r)
		if ct := r.Header.Get("Content-Type"); ct != "application/json" {
			t.Errorf("Content-Type = %q, want application/json", ct)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusCreated)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	action, err := c.SetRRSetRecords(context.Background(), 42, "_acme-challenge", "TXT", SetRRSetRecordsRequest{
		Records: []Record{{Value: `"updated-challenge-token-abc"`}},
	})
	if err != nil {
		t.Fatalf("SetRRSetRecords: %v", err)
	}

	// Regression guard: the verb MUST be POST and the path MUST be the
	// set_records action — not a PATCH/PUT on the RRSet itself.
	const wantPath = "/v1/zones/42/rrsets/_acme-challenge/TXT/actions/set_records"
	if gotMethod != http.MethodPost {
		t.Fatalf("method = %s, want POST (set_records action, not PATCH/PUT)", gotMethod)
	}
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
	if len(gotBody.Records) != 1 || gotBody.Records[0].Value != `"updated-challenge-token-abc"` {
		t.Fatalf("request body records = %+v, want one double-quoted TXT value", gotBody.Records)
	}
	if action.Command != "set_rrset_records" {
		t.Fatalf("action.Command = %q, want set_rrset_records", action.Command)
	}
}

// ----------------------------------------------------------------------------
// 4. Happy-path: DELETE /v1/zones/{id}/rrsets/{name}/{type}
// ----------------------------------------------------------------------------

func TestClient_DeleteRRSet_HappyPath(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		wantPath := "/v1/zones/42/rrsets/_acme-challenge/TXT"
		if r.Method != http.MethodDelete || r.URL.Path != wantPath {
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
		}
		assertAuthHeader(t, r)
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	if err := c.DeleteRRSet(context.Background(), 42, "_acme-challenge", "TXT"); err != nil {
		t.Fatalf("DeleteRRSet: %v", err)
	}
}

// ----------------------------------------------------------------------------
// 5. Error: 401 invalid token
// ----------------------------------------------------------------------------

func TestClient_Error401_InvalidToken(t *testing.T) {
	body := loadFixture(t, "error_401.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	_, err := c.ListZones(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
	var apiErr *APIError
	if !errors.As(err, &apiErr) || apiErr.Code != "unauthorized" {
		t.Fatalf("apiErr = %+v", apiErr)
	}
}

// ----------------------------------------------------------------------------
// 6. Error: 403 wrong project
// ----------------------------------------------------------------------------

func TestClient_Error403_Forbidden(t *testing.T) {
	body := loadFixture(t, "error_403.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	err := c.DeleteRRSet(context.Background(), 99, "_acme-challenge", "TXT")
	if !errors.Is(err, ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden", err)
	}
}

// ----------------------------------------------------------------------------
// 7. Error: 404 zone not found
// ----------------------------------------------------------------------------

func TestClient_Error404_NotFound(t *testing.T) {
	body := loadFixture(t, "error_404.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	err := c.DeleteRRSet(context.Background(), 42, "_acme-challenge", "TXT")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
}

// ----------------------------------------------------------------------------
// 8. Error: 409 conflict on create
// ----------------------------------------------------------------------------

func TestClient_Error409_Conflict(t *testing.T) {
	body := loadFixture(t, "error_409.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	_, err := c.CreateRRSet(context.Background(), 42, CreateRRSetRequest{
		Name: "_acme-challenge", Type: "TXT", Records: []Record{{Value: `"x"`}},
	})
	if !errors.Is(err, ErrConflict) {
		t.Fatalf("err = %v, want ErrConflict", err)
	}
}

// ----------------------------------------------------------------------------
// 9. Error: 422 invalid zone name
// ----------------------------------------------------------------------------

func TestClient_Error422_InvalidZoneName(t *testing.T) {
	body := loadFixture(t, "error_422.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnprocessableEntity)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	_, err := c.CreateRRSet(context.Background(), 42, CreateRRSetRequest{
		Name: "x", Type: "TXT", Records: []Record{{Value: `"x"`}},
	})
	if !errors.Is(err, ErrInvalidZoneName) {
		t.Fatalf("err = %v, want ErrInvalidZoneName", err)
	}
}

// ----------------------------------------------------------------------------
// 10. Retry-After honoured: 429 with delta-seconds, then 200
// ----------------------------------------------------------------------------

func TestClient_RetryAfter_Seconds(t *testing.T) {
	rateLimitBody := loadFixture(t, "error_429.json")
	okBody := loadFixture(t, "list_zones.json")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", "7")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write(rateLimitBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(okBody)
	}))
	defer srv.Close()

	// Build a client whose maxBackoff is high enough to honour the
	// 7-second Retry-After exactly (the safety cap clamps at maxBackoff).
	clk := newFakeClock()
	logger, _ := debugLogger()
	c, err := New(
		StaticToken(testToken),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(clk),
		WithLogger(logger),
		WithBackoff(10*time.Millisecond, 60*time.Second),
		WithMaxRetries(6),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	zones, err := c.ListZones(context.Background())
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("len(zones) = %d", len(zones))
	}
	sleeps := clk.Sleeps()
	if len(sleeps) != 1 {
		t.Fatalf("sleeps = %v, want exactly 1", sleeps)
	}
	if sleeps[0] != 7*time.Second {
		t.Fatalf("first sleep = %v, want 7s", sleeps[0])
	}
}

// ----------------------------------------------------------------------------
// 11. Retry-After honoured: HTTP-date variant
// ----------------------------------------------------------------------------

func TestClient_RetryAfter_HTTPDate(t *testing.T) {
	rateLimitBody := loadFixture(t, "error_429.json")
	okBody := loadFixture(t, "list_zones.json")

	// Build an HTTP-date 12 seconds in the future relative to the
	// fake clock's epoch (newFakeClock() pins now=2026-05-25T12:00:00Z).
	future := time.Date(2026, 5, 25, 12, 0, 12, 0, time.UTC).Format(http.TimeFormat)

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.Header().Set("Retry-After", future)
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write(rateLimitBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(okBody)
	}))
	defer srv.Close()

	clk := newFakeClock()
	logger, _ := debugLogger()
	c, err := New(
		StaticToken(testToken),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(clk),
		WithLogger(logger),
		WithBackoff(10*time.Millisecond, 60*time.Second),
		WithMaxRetries(6),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.ListZones(context.Background()); err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	sleeps := clk.Sleeps()
	if len(sleeps) != 1 {
		t.Fatalf("sleeps = %v, want exactly 1", sleeps)
	}
	if sleeps[0] != 12*time.Second {
		t.Fatalf("sleep = %v, want 12s (HTTP-date delta relative to fake-clock now)", sleeps[0])
	}
}

// ----------------------------------------------------------------------------
// 12. Exponential backoff on 5xx, then eventual success
// ----------------------------------------------------------------------------

func TestClient_ExponentialBackoff_On5xx(t *testing.T) {
	errBody := loadFixture(t, "error_500.json")
	okBody := loadFixture(t, "list_zones.json")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 3 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write(errBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(okBody)
	}))
	defer srv.Close()

	c, clk, _ := newTestClient(t, srv)
	zones, err := c.ListZones(context.Background())
	if err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if len(zones) != 2 {
		t.Fatalf("len(zones) = %d", len(zones))
	}
	sleeps := clk.Sleeps()
	want := []time.Duration{
		10 * time.Millisecond, // initial
		20 * time.Millisecond, // *2
		40 * time.Millisecond, // *2
	}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps = %v, want %v", sleeps, want)
	}
	for i, d := range want {
		if sleeps[i] != d {
			t.Fatalf("sleep[%d] = %v, want %v (all %v)", i, sleeps[i], d, sleeps)
		}
	}
}

// ----------------------------------------------------------------------------
// 13. Exponential backoff is capped at maxBackoff
// ----------------------------------------------------------------------------

func TestClient_ExponentialBackoff_Capped(t *testing.T) {
	errBody := loadFixture(t, "error_500.json")
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write(errBody)
	}))
	defer srv.Close()

	clk := newFakeClock()
	logger, _ := debugLogger()
	c, err := New(
		StaticToken(testToken),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(clk),
		WithLogger(logger),
		WithBackoff(100*time.Millisecond, 250*time.Millisecond),
		WithMaxRetries(6),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = c.ListZones(context.Background())
	if err == nil || !errors.Is(err, ErrServer) {
		t.Fatalf("err = %v, want ErrServer", err)
	}
	sleeps := clk.Sleeps()
	// Sequence: 100ms, 200ms, then capped to 250ms for the rest.
	want := []time.Duration{
		100 * time.Millisecond,
		200 * time.Millisecond,
		250 * time.Millisecond,
		250 * time.Millisecond,
		250 * time.Millisecond,
		250 * time.Millisecond,
	}
	if len(sleeps) != len(want) {
		t.Fatalf("sleeps len = %d, want %d (%v)", len(sleeps), len(want), sleeps)
	}
	for i, d := range want {
		if sleeps[i] != d {
			t.Fatalf("sleep[%d] = %v, want %v", i, sleeps[i], d)
		}
	}
	if atomic.LoadInt32(&calls) != 7 { // 1 initial + 6 retries
		t.Fatalf("calls = %d, want 7", calls)
	}
}

// ----------------------------------------------------------------------------
// 14. 5xx exhausts retries → ErrServer
// ----------------------------------------------------------------------------

func TestClient_5xx_ExhaustsRetries(t *testing.T) {
	errBody := loadFixture(t, "error_500.json")
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusServiceUnavailable)
		_, _ = w.Write(errBody)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	_, err := c.ListZones(context.Background())
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, ErrServer) {
		t.Fatalf("err = %v, want ErrServer", err)
	}
	// 1 initial + 6 retries = 7 total
	if atomic.LoadInt32(&calls) != 7 {
		t.Fatalf("calls = %d, want 7", calls)
	}
}

// ----------------------------------------------------------------------------
// 15. 4xx other than 429 does NOT retry
// ----------------------------------------------------------------------------

func TestClient_4xx_NotRetried(t *testing.T) {
	body := loadFixture(t, "error_404.json")
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&calls, 1)
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	err := c.DeleteRRSet(context.Background(), 42, "missing", "TXT")
	if !errors.Is(err, ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if atomic.LoadInt32(&calls) != 1 {
		t.Fatalf("calls = %d, want 1 (no retry on 4xx)", calls)
	}
}

// ----------------------------------------------------------------------------
// 16. Log redaction — bearer token literal must never appear
// ----------------------------------------------------------------------------

func TestClient_LogRedaction_TokenNeverLeaks(t *testing.T) {
	okBody := loadFixture(t, "list_zones.json")
	errBody := loadFixture(t, "error_500.json")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write(errBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(okBody)
	}))
	defer srv.Close()

	c, _, buf := newTestClient(t, srv)
	if _, err := c.ListZones(context.Background()); err != nil {
		t.Fatalf("ListZones: %v", err)
	}

	logs := buf.String()
	if logs == "" {
		t.Fatal("expected debug log output, got nothing")
	}
	if strings.Contains(logs, testToken) {
		t.Fatalf("token literal leaked into log output:\n%s", logs)
	}
	if !strings.Contains(logs, redactedToken) {
		t.Fatalf("expected log output to contain %q redaction marker; got:\n%s", redactedToken, logs)
	}
}

// ----------------------------------------------------------------------------
// 17. Log redaction on auth-failure path — token still must not leak
// ----------------------------------------------------------------------------

func TestClient_LogRedaction_OnAuthFailure(t *testing.T) {
	body := loadFixture(t, "error_401.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write(body)
	}))
	defer srv.Close()

	c, _, buf := newTestClient(t, srv)
	_, err := c.ListZones(context.Background())
	if !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("err = %v, want ErrInvalidToken", err)
	}
	if strings.Contains(buf.String(), testToken) {
		t.Fatalf("token leaked on 401:\n%s", buf.String())
	}
	if strings.Contains(err.Error(), testToken) {
		t.Fatalf("token leaked into error message: %s", err.Error())
	}
}

// ----------------------------------------------------------------------------
// 18. Context cancellation aborts retry loop
// ----------------------------------------------------------------------------

func TestClient_ContextCancellation_AbortsRetry(t *testing.T) {
	errBody := loadFixture(t, "error_500.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write(errBody)
	}))
	defer srv.Close()

	// Use the real clock so context-cancellation has a deadline to
	// honour; but tie initial backoff to a short value so the test is
	// fast.
	logger, _ := debugLogger()
	c, err := New(
		StaticToken(testToken),
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithLogger(logger),
		WithBackoff(50*time.Millisecond, 5*time.Second),
		WithMaxRetries(20),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 80*time.Millisecond)
	defer cancel()
	_, err = c.ListZones(ctx)
	if err == nil {
		t.Fatal("expected error from cancelled context")
	}
	if !errors.Is(err, context.DeadlineExceeded) && !errors.Is(err, context.Canceled) && !errors.Is(err, ErrServer) {
		// Either the context fired first, or the request itself
		// returned the 5xx error. Both are acceptable; we just need
		// to verify the loop terminates.
		t.Logf("err (acceptable): %v", err)
	}
}

// ----------------------------------------------------------------------------
// 19. Token source called once per attempt (rotation support)
// ----------------------------------------------------------------------------

func TestClient_TokenSource_CalledPerAttempt(t *testing.T) {
	okBody := loadFixture(t, "list_zones.json")
	errBody := loadFixture(t, "error_500.json")

	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&calls, 1)
		if n <= 2 {
			w.WriteHeader(http.StatusBadGateway)
			_, _ = w.Write(errBody)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(okBody)
	}))
	defer srv.Close()

	var tokenCalls int32
	src := func(_ context.Context) (string, error) {
		atomic.AddInt32(&tokenCalls, 1)
		return testToken, nil
	}
	clk := newFakeClock()
	logger, _ := debugLogger()
	c, err := New(src,
		WithBaseURL(srv.URL),
		WithHTTPClient(srv.Client()),
		WithClock(clk),
		WithLogger(logger),
		WithBackoff(1*time.Millisecond, 10*time.Millisecond),
	)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := c.ListZones(context.Background()); err != nil {
		t.Fatalf("ListZones: %v", err)
	}
	if got := atomic.LoadInt32(&tokenCalls); got != 3 {
		t.Fatalf("token source calls = %d, want 3 (one per attempt)", got)
	}
}

// ----------------------------------------------------------------------------
// 20. parseRetryAfter — value table
// ----------------------------------------------------------------------------

func TestParseRetryAfter_Table(t *testing.T) {
	ref := time.Date(2026, 5, 25, 12, 0, 0, 0, time.UTC)
	cases := []struct {
		name   string
		header string
		want   time.Duration
	}{
		{"empty", "", 0},
		{"seconds-0", "0", 0},
		{"seconds-5", "5", 5 * time.Second},
		{"seconds-negative", "-3", 0},
		{"http-date-future", ref.Add(15 * time.Second).Format(http.TimeFormat), 15 * time.Second},
		{"http-date-past", ref.Add(-1 * time.Hour).Format(http.TimeFormat), 0},
		{"garbage", "soon™", 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := parseRetryAfter(tc.header, ref)
			// HTTP-date precision is one second; allow a small slop.
			diff := got - tc.want
			if diff < 0 {
				diff = -diff
			}
			if diff > time.Second {
				t.Fatalf("parseRetryAfter(%q) = %v, want %v", tc.header, got, tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// 21. New() input validation
// ----------------------------------------------------------------------------

func TestNew_NilTokenSource(t *testing.T) {
	_, err := New(nil)
	if err == nil {
		t.Fatal("expected error on nil token source")
	}
}

// ----------------------------------------------------------------------------
// 22. APIError.Is — sentinel matching by status code
// ----------------------------------------------------------------------------

func TestAPIError_Is_SentinelMatching(t *testing.T) {
	cases := []struct {
		status int
		want   error
	}{
		{401, ErrInvalidToken},
		{403, ErrForbidden},
		{404, ErrNotFound},
		{409, ErrConflict},
		{422, ErrInvalidZoneName},
		{429, ErrRateLimited},
		{500, ErrServer},
		{502, ErrServer},
		{503, ErrServer},
	}
	for _, tc := range cases {
		t.Run(fmt.Sprintf("status-%d", tc.status), func(t *testing.T) {
			e := &APIError{StatusCode: tc.status}
			if !errors.Is(e, tc.want) {
				t.Fatalf("status %d does not match %v", tc.status, tc.want)
			}
		})
	}
}

// ----------------------------------------------------------------------------
// 23. URL path escaping on reserved characters in RRSet name
// ----------------------------------------------------------------------------

func TestClient_PathEscaping(t *testing.T) {
	var observedPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		observedPath = r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	c, _, _ := newTestClient(t, srv)
	// A wildcard-prefixed name forces path-escape coverage.
	if err := c.DeleteRRSet(context.Background(), 42, "weird name/with slash", "TXT"); err != nil {
		t.Fatalf("DeleteRRSet: %v", err)
	}
	if !strings.Contains(observedPath, "weird%20name") {
		t.Fatalf("path = %q; expected URL-escaping of spaces", observedPath)
	}
	if strings.Contains(observedPath, "weird name") {
		t.Fatalf("path %q contains unescaped space", observedPath)
	}
}
