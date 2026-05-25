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
	"net/url"
	"strconv"
	"strings"
	"time"
)

// DefaultBaseURL is the canonical Hetzner Cloud API root. Tests
// override this via WithBaseURL.
const DefaultBaseURL = "https://api.hetzner.cloud"

// Sensible defaults; overridable via Option.
const (
	defaultRequestTimeout = 30 * time.Second
	defaultMaxRetries     = 6
	defaultInitialBackoff = 500 * time.Millisecond
	defaultMaxBackoff     = 30 * time.Second
	defaultUserAgent      = "cert-manager-webhook-hcloud-zones/dev"

	// redactedToken is the literal substituted for the bearer token in
	// every log line. Tests assert this is what shows up.
	redactedToken = "REDACTED"
)

// TokenSource returns the bearer token for the next request. It is
// re-invoked on every call so secret rotation in the upstream config
// is picked up without a client restart (see docs/app-concept.md § 6).
type TokenSource func(ctx context.Context) (string, error)

// StaticToken is a convenience constructor for the common case of a
// caller that already has the token in hand.
func StaticToken(token string) TokenSource {
	return func(_ context.Context) (string, error) { return token, nil }
}

// Client is the Hetzner Cloud Zones API client. Construct with New;
// the zero value is not usable.
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      TokenSource
	logger     *slog.Logger
	clock      Clock

	requestTimeout time.Duration
	maxRetries     int
	initialBackoff time.Duration
	maxBackoff     time.Duration
	userAgent      string
}

// Option configures a Client. Use the With* helpers below.
type Option func(*Client)

// WithHTTPClient injects a custom *http.Client. Tests use this to
// route through httptest.Server transports. Default: http.DefaultClient
// with a per-request timeout enforced via context.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// WithBaseURL overrides the API root. Default: DefaultBaseURL.
func WithBaseURL(u string) Option {
	return func(c *Client) {
		if u != "" {
			c.baseURL = strings.TrimRight(u, "/")
		}
	}
}

// WithLogger injects a slog.Logger. The client redacts the bearer
// token before emitting any log line; the configured logger sees only
// the redactedToken literal. Default: slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(c *Client) {
		if l != nil {
			c.logger = l
		}
	}
}

// WithClock injects a Clock. Tests use this to drive exponential
// backoff without real time.Sleep. Default: real wall clock.
func WithClock(k Clock) Option {
	return func(c *Client) {
		if k != nil {
			c.clock = k
		}
	}
}

// WithRequestTimeout bounds a single HTTP request. Default: 30s.
func WithRequestTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 {
			c.requestTimeout = d
		}
	}
}

// WithMaxRetries caps the number of retry attempts on retryable
// errors (429 and 5xx). Default: 6 (~2 minutes worst-case at default
// backoff per docs/app-concept.md § 6).
func WithMaxRetries(n int) Option {
	return func(c *Client) {
		if n >= 0 {
			c.maxRetries = n
		}
	}
}

// WithBackoff overrides the exponential-backoff start and ceiling.
func WithBackoff(initial, max time.Duration) Option {
	return func(c *Client) {
		if initial > 0 {
			c.initialBackoff = initial
		}
		if max > 0 {
			c.maxBackoff = max
		}
	}
}

// WithUserAgent sets the User-Agent header on every request.
func WithUserAgent(ua string) Option {
	return func(c *Client) {
		if ua != "" {
			c.userAgent = ua
		}
	}
}

// New constructs a Client. The TokenSource is required; pass
// StaticToken("...") for a fixed token, or a custom function that
// re-reads a Kubernetes Secret on each call.
func New(token TokenSource, opts ...Option) (*Client, error) {
	if token == nil {
		return nil, errors.New("hcloud: token source must not be nil")
	}
	c := &Client{
		baseURL:        DefaultBaseURL,
		httpClient:     http.DefaultClient,
		token:          token,
		logger:         slog.Default(),
		clock:          realClock{},
		requestTimeout: defaultRequestTimeout,
		maxRetries:     defaultMaxRetries,
		initialBackoff: defaultInitialBackoff,
		maxBackoff:     defaultMaxBackoff,
		userAgent:      defaultUserAgent,
	}
	for _, opt := range opts {
		opt(c)
	}
	return c, nil
}

// ----------------------------------------------------------------------------
// Public API — the four endpoints
// ----------------------------------------------------------------------------

// ListZones returns every zone visible to the bearer token. The
// Hetzner Cloud Zones API paginates; callers that need to enumerate
// very large projects should switch to a paginated helper (not
// needed for the webhook at MVP, where zone counts are small).
func (c *Client) ListZones(ctx context.Context) ([]Zone, error) {
	var out listZonesResponse
	if err := c.do(ctx, http.MethodGet, "/v1/zones", nil, &out); err != nil {
		return nil, err
	}
	return out.Zones, nil
}

// CreateRRSet creates a new RRSet on the given zone. Returns 409
// ErrConflict if an RRSet with the same name+type already exists; the
// higher-level webhook reacts by calling UpdateRRSet instead.
func (c *Client) CreateRRSet(ctx context.Context, zoneID int64, req CreateRRSetRequest) (*RRSet, error) {
	path := fmt.Sprintf("/v1/zones/%d/rrsets", zoneID)
	var out rrsetEnvelope
	if err := c.do(ctx, http.MethodPost, path, req, &out); err != nil {
		return nil, err
	}
	return &out.RRSet, nil
}

// UpdateRRSet patches an existing RRSet identified by (zone, name,
// type). Used for idempotent re-creates when CreateRRSet returns 409.
func (c *Client) UpdateRRSet(ctx context.Context, zoneID int64, name, rrType string, req UpdateRRSetRequest) (*RRSet, error) {
	path := fmt.Sprintf("/v1/zones/%d/rrsets/%s/%s", zoneID, url.PathEscape(name), url.PathEscape(rrType))
	var out rrsetEnvelope
	if err := c.do(ctx, http.MethodPatch, path, req, &out); err != nil {
		return nil, err
	}
	return &out.RRSet, nil
}

// DeleteRRSet removes an RRSet by (zone, name, type). A 404 from the
// API is surfaced as ErrNotFound so callers can implement idempotent
// CleanUp (treat "already gone" as success).
func (c *Client) DeleteRRSet(ctx context.Context, zoneID int64, name, rrType string) error {
	path := fmt.Sprintf("/v1/zones/%d/rrsets/%s/%s", zoneID, url.PathEscape(name), url.PathEscape(rrType))
	return c.do(ctx, http.MethodDelete, path, nil, nil)
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

// do executes a single API call with retry semantics:
//
//   - 2xx → decode into out (if non-nil) and return.
//   - 429 → honour Retry-After (delta-seconds or HTTP-date); retry up to maxRetries.
//   - 5xx → exponential backoff (initial * 2^n, capped at maxBackoff); retry up to maxRetries.
//   - 4xx (other) → typed APIError, no retry.
//
// The bearer token is fetched on every attempt so secret rotation is
// picked up without restart (per docs/app-concept.md § 6).
func (c *Client) do(ctx context.Context, method, path string, body, out any) error {
	var bodyBytes []byte
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("hcloud: marshal request body: %w", err)
		}
		bodyBytes = b
	}

	var lastErr error
	for attempt := 0; attempt <= c.maxRetries; attempt++ {
		if attempt > 0 {
			delay := c.backoffDelay(attempt, lastErr)
			c.logger.LogAttrs(ctx, slog.LevelDebug, "hcloud: retrying request",
				slog.String("method", method),
				slog.String("path", path),
				slog.Int("attempt", attempt),
				slog.Duration("delay", delay),
			)
			if err := c.clock.Sleep(ctx, delay); err != nil {
				return err
			}
		}

		err := c.doOnce(ctx, method, path, bodyBytes, out)
		if err == nil {
			return nil
		}
		lastErr = err

		if !isRetryable(err) {
			return err
		}
	}
	return lastErr
}

// doOnce performs a single attempt. On retryable failure it returns
// the typed *APIError so do() can read Retry-After.
func (c *Client) doOnce(ctx context.Context, method, path string, bodyBytes []byte, out any) error {
	token, err := c.token(ctx)
	if err != nil {
		return fmt.Errorf("hcloud: read token: %w", err)
	}

	reqCtx, cancel := context.WithTimeout(ctx, c.requestTimeout)
	defer cancel()

	var bodyReader io.Reader
	if bodyBytes != nil {
		bodyReader = bytes.NewReader(bodyBytes)
	}

	req, err := http.NewRequestWithContext(reqCtx, method, c.baseURL+path, bodyReader)
	if err != nil {
		return fmt.Errorf("hcloud: build request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if bodyBytes != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	c.logger.LogAttrs(ctx, slog.LevelDebug, "hcloud: request",
		slog.String("method", method),
		slog.String("url", c.baseURL+path),
		slog.String("authorization", "Bearer "+redactedToken),
	)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		// Network-level errors are retried: connection reset, DNS
		// blip, TLS handshake aborted, …. Encoded as APIError{500}
		// so the retry path sees them as retryable.
		c.logger.LogAttrs(ctx, slog.LevelWarn, "hcloud: transport error",
			slog.String("method", method),
			slog.String("path", path),
			slog.String("error", err.Error()),
		)
		return &APIError{StatusCode: 0, Code: "transport_error", Message: err.Error()}
	}
	defer func() { _ = resp.Body.Close() }()

	rawBody, _ := io.ReadAll(resp.Body)

	c.logger.LogAttrs(ctx, slog.LevelDebug, "hcloud: response",
		slog.String("method", method),
		slog.String("path", path),
		slog.Int("status", resp.StatusCode),
	)

	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		if out == nil || len(rawBody) == 0 {
			return nil
		}
		if err := json.Unmarshal(rawBody, out); err != nil {
			return fmt.Errorf("hcloud: decode %s %s: %w", method, path, err)
		}
		return nil
	}

	return c.parseAPIError(resp, rawBody)
}

// parseAPIError turns a non-2xx response into a typed *APIError. The
// Hetzner-side body shape is documented as
// {"error":{"code":"...","message":"..."}}; missing or unparseable
// bodies degrade gracefully to status-only errors. The client's clock
// is used as the reference instant for HTTP-date Retry-After headers
// so tests can drive the value deterministically.
func (c *Client) parseAPIError(resp *http.Response, body []byte) error {
	apiErr := &APIError{StatusCode: resp.StatusCode}

	if len(body) > 0 {
		var env hcloudErrorEnvelope
		if err := json.Unmarshal(body, &env); err == nil {
			apiErr.Code = env.Error.Code
			apiErr.Message = env.Error.Message
		}
	}
	if apiErr.Message == "" {
		apiErr.Message = http.StatusText(resp.StatusCode)
	}

	if resp.StatusCode == http.StatusTooManyRequests {
		apiErr.RetryAfter = parseRetryAfter(resp.Header.Get("Retry-After"), c.clock.Now())
	}

	return apiErr
}

// parseRetryAfter implements RFC 7231 §7.1.3: the header value is
// either delta-seconds (an integer) or an HTTP-date. now is injected
// so tests can pin the reference instant.
func parseRetryAfter(header string, now time.Time) time.Duration {
	header = strings.TrimSpace(header)
	if header == "" {
		return 0
	}
	if secs, err := strconv.Atoi(header); err == nil {
		if secs < 0 {
			return 0
		}
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(header); err == nil {
		d := t.Sub(now)
		if d < 0 {
			return 0
		}
		return d
	}
	return 0
}

// isRetryable returns true for transport errors, 429, and 5xx.
func isRetryable(err error) bool {
	var apiErr *APIError
	if !errors.As(err, &apiErr) {
		return false
	}
	if apiErr.StatusCode == 0 {
		// transport-level error
		return true
	}
	if apiErr.StatusCode == http.StatusTooManyRequests {
		return true
	}
	if apiErr.StatusCode >= 500 && apiErr.StatusCode < 600 {
		return true
	}
	return false
}

// backoffDelay returns the delay before the attempt-th retry. For 429
// responses it honours Retry-After; otherwise it grows exponentially
// from initialBackoff, capped at maxBackoff. attempt is 1-based.
func (c *Client) backoffDelay(attempt int, lastErr error) time.Duration {
	var apiErr *APIError
	if errors.As(lastErr, &apiErr) && apiErr.RetryAfter > 0 {
		// Cap at maxBackoff so a misbehaving server can't make us
		// sleep arbitrarily long. The webhook caller already enforces
		// a higher-level context deadline.
		if apiErr.RetryAfter > c.maxBackoff {
			return c.maxBackoff
		}
		return apiErr.RetryAfter
	}
	// Exponential: initialBackoff * 2^(attempt-1), capped.
	d := c.initialBackoff
	for i := 1; i < attempt; i++ {
		d *= 2
		if d >= c.maxBackoff {
			return c.maxBackoff
		}
	}
	return d
}
