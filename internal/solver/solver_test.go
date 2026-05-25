// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package solver

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
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	whapi "github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/internal/hcloud"
)

// ----------------------------------------------------------------------------
// Test scaffolding
// ----------------------------------------------------------------------------

const (
	testTokenA = "hcloud-token-aaaa-DO-NOT-LEAK"
	testTokenB = "hcloud-token-bbbb-DO-NOT-LEAK"
	testKey    = "challenge-token-base64url-XYZ"
)

// stubSecretGetter resolves a SecretRef to a pre-canned token, recording
// every call for assertions.
type stubSecretGetter struct {
	mu      sync.Mutex
	tokens  map[string]string // SecretRef.String() → token
	calls   []SecretRef
	failOn  string // SecretRef.String() → return an error
	failErr error
}

func newStubSecretGetter() *stubSecretGetter {
	return &stubSecretGetter{tokens: make(map[string]string)}
}

func (g *stubSecretGetter) put(ref SecretRef, token string) *stubSecretGetter {
	g.tokens[ref.String()] = token
	return g
}

func (g *stubSecretGetter) GetToken(_ context.Context, ref SecretRef) (string, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.calls = append(g.calls, ref)
	if g.failOn != "" && ref.String() == g.failOn {
		return "", g.failErr
	}
	tok, ok := g.tokens[ref.String()]
	if !ok {
		return "", fmt.Errorf("stub: no token registered for %s", ref.String())
	}
	return tok, nil
}

func (g *stubSecretGetter) Calls() []SecretRef {
	g.mu.Lock()
	defer g.mu.Unlock()
	out := make([]SecretRef, len(g.calls))
	copy(out, g.calls)
	return out
}

// mockHCloudServer is a tiny in-memory replica of the Hetzner Cloud Zones
// endpoints the solver hits. Per-test handlers can override behaviour
// via the hooks below.
type mockHCloudServer struct {
	zones map[string]int64 // name → ID (config-time)

	mu       sync.Mutex
	rrsets   map[int64]hcloud.RRSet // zoneID → current RRSet
	requests []recordedRequest
	expected string // expected bearer token

	// Hooks let individual tests tweak behaviour.
	listHook       func(w http.ResponseWriter, r *http.Request) bool
	createHook     func(w http.ResponseWriter, r *http.Request, zoneID int64, req hcloud.CreateRRSetRequest) bool
	updateHook     func(w http.ResponseWriter, r *http.Request, zoneID int64) bool
	deleteHook     func(w http.ResponseWriter, r *http.Request, zoneID int64) bool
	rejectAuthWith int // non-zero → return this status on every request
}

type recordedRequest struct {
	method string
	path   string
	body   []byte
	auth   string
}

func newMockHCloudServer(expectedToken string, zones map[string]int64) *mockHCloudServer {
	return &mockHCloudServer{
		zones:    zones,
		rrsets:   make(map[int64]hcloud.RRSet),
		expected: expectedToken,
	}
}

func (m *mockHCloudServer) Requests() []recordedRequest {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]recordedRequest, len(m.requests))
	copy(out, m.requests)
	return out
}

func (m *mockHCloudServer) RRSet(zoneID int64) (hcloud.RRSet, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	r, ok := m.rrsets[zoneID]
	return r, ok
}

func (m *mockHCloudServer) Handler(t *testing.T) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		m.mu.Lock()
		m.requests = append(m.requests, recordedRequest{
			method: r.Method,
			path:   r.URL.Path,
			body:   body,
			auth:   r.Header.Get("Authorization"),
		})
		m.mu.Unlock()

		if m.rejectAuthWith != 0 {
			writeError(w, m.rejectAuthWith, "rejected", "auth rejected")
			return
		}
		if m.expected != "" && r.Header.Get("Authorization") != "Bearer "+m.expected {
			t.Errorf("bearer token mismatch on %s %s: got %q, want %q",
				r.Method, r.URL.Path, r.Header.Get("Authorization"), "Bearer "+m.expected)
			writeError(w, http.StatusUnauthorized, "unauthorized", "bad token")
			return
		}

		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/v1/zones":
			if m.listHook != nil && m.listHook(w, r) {
				return
			}
			m.serveListZones(w)

		case r.Method == http.MethodPost && strings.HasPrefix(r.URL.Path, "/v1/zones/") && strings.HasSuffix(r.URL.Path, "/rrsets"):
			zoneID := parseZoneID(t, r.URL.Path, "/v1/zones/", "/rrsets")
			var req hcloud.CreateRRSetRequest
			if err := json.Unmarshal(body, &req); err != nil {
				writeError(w, http.StatusBadRequest, "bad_json", err.Error())
				return
			}
			if m.createHook != nil && m.createHook(w, r, zoneID, req) {
				return
			}
			m.serveCreate(w, zoneID, req)

		case r.Method == http.MethodPatch && strings.Contains(r.URL.Path, "/rrsets/"):
			zoneID := parseZoneID(t, r.URL.Path, "/v1/zones/", "/rrsets/")
			if m.updateHook != nil && m.updateHook(w, r, zoneID) {
				return
			}
			var req hcloud.UpdateRRSetRequest
			_ = json.Unmarshal(body, &req)
			m.serveUpdate(w, zoneID, req)

		case r.Method == http.MethodDelete && strings.Contains(r.URL.Path, "/rrsets/"):
			zoneID := parseZoneID(t, r.URL.Path, "/v1/zones/", "/rrsets/")
			if m.deleteHook != nil && m.deleteHook(w, r, zoneID) {
				return
			}
			m.serveDelete(w, zoneID)

		default:
			t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
			http.NotFound(w, r)
		}
	}
}

func (m *mockHCloudServer) serveListZones(w http.ResponseWriter) {
	var zones []hcloud.Zone
	for name, id := range m.zones {
		zones = append(zones, hcloud.Zone{ID: id, Name: name, Mode: "primary", Status: "ok"})
	}
	writeJSON(w, http.StatusOK, map[string]any{"zones": zones})
}

func (m *mockHCloudServer) serveCreate(w http.ResponseWriter, zoneID int64, req hcloud.CreateRRSetRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, exists := m.rrsets[zoneID]; exists {
		writeError(w, http.StatusConflict, "rrset_exists", "rrset already exists")
		return
	}
	rrset := hcloud.RRSet{
		ID:      fmt.Sprintf("rrset-%d", zoneID),
		Name:    req.Name,
		Type:    req.Type,
		TTL:     req.TTL,
		Records: req.Records,
		ZoneID:  zoneID,
	}
	m.rrsets[zoneID] = rrset
	writeJSON(w, http.StatusCreated, map[string]any{"rrset": rrset})
}

func (m *mockHCloudServer) serveUpdate(w http.ResponseWriter, zoneID int64, req hcloud.UpdateRRSetRequest) {
	m.mu.Lock()
	defer m.mu.Unlock()
	rrset, ok := m.rrsets[zoneID]
	if !ok {
		writeError(w, http.StatusNotFound, "not_found", "rrset not found")
		return
	}
	if req.Records != nil {
		rrset.Records = req.Records
	}
	if req.TTL != nil {
		rrset.TTL = req.TTL
	}
	m.rrsets[zoneID] = rrset
	writeJSON(w, http.StatusOK, map[string]any{"rrset": rrset})
}

func (m *mockHCloudServer) serveDelete(w http.ResponseWriter, zoneID int64) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if _, ok := m.rrsets[zoneID]; !ok {
		writeError(w, http.StatusNotFound, "not_found", "rrset not found")
		return
	}
	delete(m.rrsets, zoneID)
	w.WriteHeader(http.StatusNoContent)
}

func parseZoneID(t *testing.T, path, prefix, suffix string) int64 {
	t.Helper()
	trim := strings.TrimPrefix(path, prefix)
	if i := strings.Index(trim, suffix); i >= 0 {
		trim = trim[:i]
	}
	var id int64
	if _, err := fmt.Sscanf(trim, "%d", &id); err != nil {
		t.Fatalf("parseZoneID(%q): %v", path, err)
	}
	return id
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

func writeError(w http.ResponseWriter, status int, code, msg string) {
	writeJSON(w, status, map[string]any{
		"error": map[string]any{"code": code, "message": msg},
	})
}

// captureBuf is a goroutine-safe bytes.Buffer for slog output capture.
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

func newCaptureLogger() (*slog.Logger, *captureBuf) {
	buf := &captureBuf{}
	h := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})
	return slog.New(h), buf
}

// buildConfig is a fluent helper for the JSON config blob a ChallengeRequest
// carries. Each call appends one credential entry.
type configBuilder struct {
	creds []CredentialConfig
}

func newConfig() *configBuilder { return &configBuilder{} }

func (b *configBuilder) credential(name string, zones []string, secretName, namespace, key string) *configBuilder {
	b.creds = append(b.creds, CredentialConfig{
		Name:  name,
		Zones: zones,
		APITokenSecretRef: cmmeta.SecretKeySelector{
			LocalObjectReference: cmmeta.LocalObjectReference{Name: secretName},
			Key:                  key,
		},
		Namespace: namespace,
	})
	return b
}

func (b *configBuilder) build(t *testing.T) *apiextensionsv1.JSON {
	t.Helper()
	raw, err := json.Marshal(Config{Credentials: b.creds})
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	return &apiextensionsv1.JSON{Raw: raw}
}

// challengeRequest constructs a ChallengeRequest for the test FQDN.
func challengeRequest(fqdn, key string, cfg *apiextensionsv1.JSON, namespace string) *whapi.ChallengeRequest {
	return &whapi.ChallengeRequest{
		Action:            whapi.ChallengeActionPresent,
		Type:              "dns-01",
		ResolvedFQDN:      fqdn,
		ResolvedZone:      fqdn, // unused by the solver
		DNSName:           strings.TrimPrefix(strings.TrimSuffix(fqdn, "."), "_acme-challenge."),
		Key:               key,
		ResourceNamespace: namespace,
		Config:            cfg,
	}
}

// wireFactory builds a ClientFactory whose hcloud.Client points at the
// given mock server. The returned factory returns ErrInvalidToken when
// the token doesn't match the mock's expected token, so wrong-token
// tests can route via the factory rather than the mock.
func wireFactory(t *testing.T, srv *httptest.Server) ClientFactory {
	t.Helper()
	return func(token string) (HCloudClient, error) {
		c, err := hcloud.New(
			hcloud.StaticToken(token),
			hcloud.WithBaseURL(srv.URL),
			hcloud.WithHTTPClient(srv.Client()),
			hcloud.WithBackoff(1*time.Millisecond, 5*time.Millisecond),
			hcloud.WithMaxRetries(2),
		)
		return c, err
	}
}

// ----------------------------------------------------------------------------
// 1. Happy path — Present creates a TXT RRSet, CleanUp removes it.
// ----------------------------------------------------------------------------

func TestSolver_Present_HappyPath(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)

	s := New(
		WithSecretGetter(getter),
		WithClientFactory(wireFactory(t, srv)),
	)

	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)
	ch := challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager")

	if err := s.Present(ch); err != nil {
		t.Fatalf("Present: %v", err)
	}
	rrset, ok := mock.RRSet(42)
	if !ok {
		t.Fatal("expected RRSet to exist on zone 42 after Present")
	}
	if rrset.Name != "_acme-challenge" || rrset.Type != "TXT" {
		t.Fatalf("rrset = %+v", rrset)
	}
	if len(rrset.Records) != 1 || rrset.Records[0].Value != `"`+testKey+`"` {
		t.Fatalf("rrset.Records = %+v", rrset.Records)
	}

	if err := s.CleanUp(ch); err != nil {
		t.Fatalf("CleanUp: %v", err)
	}
	if _, ok := mock.RRSet(42); ok {
		t.Fatal("expected RRSet to be deleted after CleanUp")
	}
}

// ----------------------------------------------------------------------------
// 2. Multi-project routing — two credentials, two projects, two mock servers.
// ----------------------------------------------------------------------------

func TestSolver_Present_MultiProjectRouting(t *testing.T) {
	mockA := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 1})
	srvA := httptest.NewServer(mockA.Handler(t))
	defer srvA.Close()

	mockB := newMockHCloudServer(testTokenB, map[string]int64{"example.org": 2})
	srvB := httptest.NewServer(mockB.Handler(t))
	defer srvB.Close()

	getter := newStubSecretGetter().
		put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA).
		put(SecretRef{Namespace: "cert-manager", Name: "tok-b", Key: "token"}, testTokenB)

	// Route by token at the factory level: A's token → srvA, B's → srvB.
	factory := func(token string) (HCloudClient, error) {
		switch token {
		case testTokenA:
			return wireFactory(t, srvA)(token)
		case testTokenB:
			return wireFactory(t, srvB)(token)
		default:
			return nil, fmt.Errorf("unexpected token %q", token)
		}
	}

	s := New(
		WithSecretGetter(getter),
		WithClientFactory(factory),
	)
	cfg := newConfig().
		credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").
		credential("project-b", []string{"example.org"}, "tok-b", "cert-manager", "token").
		build(t)

	if err := s.Present(challengeRequest("_acme-challenge.app.example.com.", testKey, cfg, "cert-manager")); err != nil {
		t.Fatalf("Present A: %v", err)
	}
	if err := s.Present(challengeRequest("_acme-challenge.app.example.org.", testKey+"-b", cfg, "cert-manager")); err != nil {
		t.Fatalf("Present B: %v", err)
	}

	if _, ok := mockA.RRSet(1); !ok {
		t.Fatal("expected RRSet on mock A zone 1")
	}
	if _, ok := mockB.RRSet(2); !ok {
		t.Fatal("expected RRSet on mock B zone 2")
	}
	// Cross-talk check: B's record value should match the B challenge, not A's.
	rrsetB, _ := mockB.RRSet(2)
	if rrsetB.Records[0].Value != `"`+testKey+`-b"` {
		t.Fatalf("B record value = %q; expected B-specific key", rrsetB.Records[0].Value)
	}
}

// ----------------------------------------------------------------------------
// 3. Idempotent Present — second Present with same key is a no-op (Update).
// ----------------------------------------------------------------------------

func TestSolver_Present_Idempotent_SameKey(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)

	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)
	ch := challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager")

	if err := s.Present(ch); err != nil {
		t.Fatalf("first Present: %v", err)
	}
	if err := s.Present(ch); err != nil {
		t.Fatalf("second Present (idempotent path): %v", err)
	}
	rrset, _ := mock.RRSet(42)
	if rrset.Records[0].Value != `"`+testKey+`"` {
		t.Fatalf("rrset record after re-present = %q", rrset.Records[0].Value)
	}
	// One PATCH was issued on the second call (Hetzner returns 409
	// on the second POST; the solver responds with a PATCH).
	var patches int
	for _, r := range mock.Requests() {
		if r.method == http.MethodPatch {
			patches++
		}
	}
	if patches != 1 {
		t.Fatalf("expected exactly 1 PATCH, got %d", patches)
	}
}

// ----------------------------------------------------------------------------
// 4. Idempotent Present — different key triggers PATCH to the new value.
// ----------------------------------------------------------------------------

func TestSolver_Present_Idempotent_DifferentKey(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)

	if err := s.Present(challengeRequest("_acme-challenge.example.com.", "first-key", cfg, "cert-manager")); err != nil {
		t.Fatalf("first Present: %v", err)
	}
	if err := s.Present(challengeRequest("_acme-challenge.example.com.", "second-key", cfg, "cert-manager")); err != nil {
		t.Fatalf("second Present: %v", err)
	}
	rrset, _ := mock.RRSet(42)
	if rrset.Records[0].Value != `"second-key"` {
		t.Fatalf("rrset record after re-present with new key = %q", rrset.Records[0].Value)
	}
}

// ----------------------------------------------------------------------------
// 5. Idempotent CleanUp — second call on a gone RRSet returns nil.
// ----------------------------------------------------------------------------

func TestSolver_CleanUp_Idempotent(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)
	ch := challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager")

	if err := s.Present(ch); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if err := s.CleanUp(ch); err != nil {
		t.Fatalf("first CleanUp: %v", err)
	}
	if err := s.CleanUp(ch); err != nil {
		t.Fatalf("second CleanUp (idempotent): %v", err)
	}
}

// ----------------------------------------------------------------------------
// 6. Wrong-token error — 403 from Hetzner surfaces a readable error.
// ----------------------------------------------------------------------------

func TestSolver_Present_WrongToken_403(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	mock.rejectAuthWith = http.StatusForbidden
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, "wrong-token")
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)

	err := s.Present(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hcloud.ErrForbidden) {
		t.Fatalf("err = %v, want ErrForbidden chained", err)
	}
	if !strings.Contains(err.Error(), "project-a") || !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("error %q should name the credential + zone for operator diagnosis", err.Error())
	}
}

// ----------------------------------------------------------------------------
// 7. No-match-fails-closed — FQDN matches no configured zone.
// ----------------------------------------------------------------------------

func TestSolver_Present_NoMatchingZone_FailsClosed(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)

	err := s.Present(challengeRequest("_acme-challenge.nope.example.org.", testKey, cfg, "cert-manager"))
	if err == nil {
		t.Fatal("expected error for unconfigured zone")
	}
	if !strings.Contains(err.Error(), "no configured zone-apex") {
		t.Fatalf("expected ErrNoMatch wrap; got %v", err)
	}
	if !strings.Contains(err.Error(), "example.com") {
		t.Fatalf("error should list configured zones (saw %q)", err.Error())
	}
	// And no API call was attempted.
	if got := len(mock.Requests()); got != 0 {
		t.Fatalf("expected 0 API requests on fail-closed; got %d", got)
	}
}

// ----------------------------------------------------------------------------
// 8. Zone-not-found at Hetzner — credential points at a project that doesn't
// own the configured zone-apex.
// ----------------------------------------------------------------------------

func TestSolver_Present_ZoneNotFoundAtHetzner(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"other.com": 99}) // does NOT include example.com
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)

	err := s.Present(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager"))
	if err == nil {
		t.Fatal("expected error")
	}
	if !errors.Is(err, hcloud.ErrNotFound) {
		t.Fatalf("err = %v, want ErrNotFound", err)
	}
	if !strings.Contains(err.Error(), "project-a") {
		t.Fatalf("error %q should mention the credential", err.Error())
	}
}

// ----------------------------------------------------------------------------
// 9. Rate-limit honoured — 429 with Retry-After, then eventual success.
// ----------------------------------------------------------------------------

func TestSolver_Present_RateLimit_RetryAfter(t *testing.T) {
	var hits int32
	zonesBody := map[string]any{"zones": []hcloud.Zone{{ID: 42, Name: "example.com", Mode: "primary"}}}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := atomic.AddInt32(&hits, 1)
		// First GET /v1/zones — return 429 with Retry-After: 0 so
		// the client retries immediately.
		if r.Method == http.MethodGet && r.URL.Path == "/v1/zones" && n == 1 {
			w.Header().Set("Retry-After", "0")
			writeError(w, http.StatusTooManyRequests, "rate_limit", "slow down")
			return
		}
		if r.Method == http.MethodGet && r.URL.Path == "/v1/zones" {
			writeJSON(w, http.StatusOK, zonesBody)
			return
		}
		if r.Method == http.MethodPost {
			writeJSON(w, http.StatusCreated, map[string]any{
				"rrset": hcloud.RRSet{ID: "rrset-42", Name: "_acme-challenge", Type: "TXT", ZoneID: 42},
			})
			return
		}
		t.Errorf("unexpected request: %s %s", r.Method, r.URL.Path)
	}))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)
	if err := s.Present(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager")); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if got := atomic.LoadInt32(&hits); got < 2 {
		t.Fatalf("expected ≥2 server hits (initial 429 + retry); got %d", got)
	}
}

// ----------------------------------------------------------------------------
// 10. Token redaction — no token literal in solver-level log output.
// ----------------------------------------------------------------------------

func TestSolver_TokenRedaction_NoLeakInLogs(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	logger, buf := newCaptureLogger()
	s := New(
		WithSecretGetter(getter),
		WithClientFactory(wireFactory(t, srv)),
		WithLogger(logger),
	)
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)
	ch := challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager")
	if err := s.Present(ch); err != nil {
		t.Fatalf("Present: %v", err)
	}
	if err := s.CleanUp(ch); err != nil {
		t.Fatalf("CleanUp: %v", err)
	}
	if logs := buf.String(); strings.Contains(logs, testTokenA) {
		t.Fatalf("token leaked into solver logs:\n%s", logs)
	}
}

// ----------------------------------------------------------------------------
// 11. Config validation — invalid JSON is rejected with a readable error.
// ----------------------------------------------------------------------------

func TestSolver_Present_InvalidJSON(t *testing.T) {
	s := New(WithSecretGetter(newStubSecretGetter()))
	ch := &whapi.ChallengeRequest{
		Type:              "dns-01",
		ResolvedFQDN:      "_acme-challenge.example.com.",
		Key:               testKey,
		ResourceNamespace: "cert-manager",
		Config:            &apiextensionsv1.JSON{Raw: []byte("{invalid json")},
	}
	err := s.Present(ch)
	if err == nil {
		t.Fatal("expected error on invalid JSON config")
	}
	if !strings.Contains(err.Error(), "parse webhook config") {
		t.Fatalf("expected parse error; got %v", err)
	}
}

// ----------------------------------------------------------------------------
// 12. Config validation — empty credentials block is rejected.
// ----------------------------------------------------------------------------

func TestSolver_Present_EmptyCredentials(t *testing.T) {
	s := New(WithSecretGetter(newStubSecretGetter()))
	raw, _ := json.Marshal(Config{})
	ch := &whapi.ChallengeRequest{
		Type:              "dns-01",
		ResolvedFQDN:      "_acme-challenge.example.com.",
		Key:               testKey,
		ResourceNamespace: "cert-manager",
		Config:            &apiextensionsv1.JSON{Raw: raw},
	}
	err := s.Present(ch)
	if err == nil {
		t.Fatal("expected error on empty credentials")
	}
	if !strings.Contains(err.Error(), "no credentials") {
		t.Fatalf("expected 'no credentials' error; got %v", err)
	}
}

// ----------------------------------------------------------------------------
// 13. Config validation — duplicate zone across credentials is rejected by
// the routing layer.
// ----------------------------------------------------------------------------

func TestSolver_Present_DuplicateZoneAcrossCredentials(t *testing.T) {
	s := New(WithSecretGetter(newStubSecretGetter()))
	cfg := newConfig().
		credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").
		credential("project-b", []string{"example.com"}, "tok-b", "cert-manager", "token").
		build(t)
	err := s.Present(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager"))
	if err == nil {
		t.Fatal("expected error on duplicate zone")
	}
	if !strings.Contains(err.Error(), "validate webhook config") {
		t.Fatalf("expected validation error; got %v", err)
	}
}

// ----------------------------------------------------------------------------
// 14. Solver Name() returns the documented constant.
// ----------------------------------------------------------------------------

func TestSolver_Name(t *testing.T) {
	s := New()
	if got := s.Name(); got != "hcloud-zones" {
		t.Fatalf("Name() = %q, want %q", got, SolverName)
	}
}

// ----------------------------------------------------------------------------
// 15. Solver without Initialize and without WithSecretGetter errors clearly.
// ----------------------------------------------------------------------------

func TestSolver_NotInitialised(t *testing.T) {
	s := New(WithClientFactory(func(string) (HCloudClient, error) { return nil, nil }))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)
	err := s.Present(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager"))
	if err == nil || !strings.Contains(err.Error(), "not initialised") {
		t.Fatalf("expected 'not initialised' error; got %v", err)
	}
}

// ----------------------------------------------------------------------------
// 16. Initialize with nil rest.Config errors clearly.
// ----------------------------------------------------------------------------

func TestSolver_Initialize_NilConfig(t *testing.T) {
	s := New()
	if err := s.Initialize(nil, nil); err == nil {
		t.Fatal("expected error on nil rest.Config")
	}
}

// ----------------------------------------------------------------------------
// 17. CleanUp with a zone-not-found at lookup time returns success (a stricter
// CleanUp semantics test than #5 — verifies the zone-resolution path).
// ----------------------------------------------------------------------------

func TestSolver_CleanUp_ZoneNotFound_IsSuccess(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{}) // empty
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)
	err := s.CleanUp(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager"))
	if err != nil {
		t.Fatalf("CleanUp with zone-not-found should be a no-op success; got %v", err)
	}
}

// ----------------------------------------------------------------------------
// 18. Zone-cache deduplicates ListZones across consecutive Present calls.
// ----------------------------------------------------------------------------

func TestSolver_ZoneCache_DeduplicatesListZones(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(
		WithSecretGetter(getter),
		WithClientFactory(wireFactory(t, srv)),
		WithZoneCacheTTL(1*time.Hour),
	)
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)

	for i := 0; i < 3; i++ {
		ch := challengeRequest("_acme-challenge.example.com.", fmt.Sprintf("key-%d", i), cfg, "cert-manager")
		// First call creates, subsequent two go through the conflict
		// path; both should reuse the cached zone ID.
		if err := s.Present(ch); err != nil {
			t.Fatalf("Present #%d: %v", i, err)
		}
	}
	var listZonesHits int
	for _, r := range mock.Requests() {
		if r.method == http.MethodGet && r.path == "/v1/zones" {
			listZonesHits++
		}
	}
	if listZonesHits != 1 {
		t.Fatalf("expected exactly 1 GET /v1/zones (cached); got %d", listZonesHits)
	}
}

// ----------------------------------------------------------------------------
// 19. Default-namespace fallback — credential without namespace inherits the
// ChallengeRequest's ResourceNamespace.
// ----------------------------------------------------------------------------

func TestSolver_Present_DefaultNamespaceFallback(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "issuer-ns", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))

	// No explicit Namespace on the credential — it should inherit from
	// ChallengeRequest.ResourceNamespace.
	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "", "token").build(t)
	if err := s.Present(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "issuer-ns")); err != nil {
		t.Fatalf("Present with default-namespace fallback: %v", err)
	}
	calls := getter.Calls()
	if len(calls) != 1 || calls[0].Namespace != "issuer-ns" {
		t.Fatalf("SecretGetter calls = %+v; expected one call with namespace 'issuer-ns'", calls)
	}
}

// ----------------------------------------------------------------------------
// 20. Default key fallback — credential without explicit key reads `token`.
// ----------------------------------------------------------------------------

func TestSolver_Present_DefaultKeyFallback(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter().put(SecretRef{Namespace: "cert-manager", Name: "tok-a", Key: "token"}, testTokenA)
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))

	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "").build(t)
	if err := s.Present(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager")); err != nil {
		t.Fatalf("Present with default-key fallback: %v", err)
	}
	calls := getter.Calls()
	if len(calls) != 1 || calls[0].Key != "token" {
		t.Fatalf("SecretGetter calls = %+v; expected key='token' default", calls)
	}
}

// ----------------------------------------------------------------------------
// 21. SecretGetter error surfaces with operator-readable context.
// ----------------------------------------------------------------------------

func TestSolver_Present_SecretGetterError(t *testing.T) {
	mock := newMockHCloudServer(testTokenA, map[string]int64{"example.com": 42})
	srv := httptest.NewServer(mock.Handler(t))
	defer srv.Close()

	getter := newStubSecretGetter()
	getter.failOn = "cert-manager/tok-a#token"
	getter.failErr = errors.New("secret missing")
	s := New(WithSecretGetter(getter), WithClientFactory(wireFactory(t, srv)))

	cfg := newConfig().credential("project-a", []string{"example.com"}, "tok-a", "cert-manager", "token").build(t)
	err := s.Present(challengeRequest("_acme-challenge.example.com.", testKey, cfg, "cert-manager"))
	if err == nil || !strings.Contains(err.Error(), "secret missing") {
		t.Fatalf("expected secret-getter error to propagate; got %v", err)
	}
}

// ----------------------------------------------------------------------------
// 22. Nil ChallengeRequest is rejected by both entry points.
// ----------------------------------------------------------------------------

func TestSolver_NilChallengeRequest(t *testing.T) {
	s := New(WithSecretGetter(newStubSecretGetter()))
	if err := s.Present(nil); err == nil {
		t.Fatal("Present(nil) should error")
	}
	if err := s.CleanUp(nil); err == nil {
		t.Fatal("CleanUp(nil) should error")
	}
}
