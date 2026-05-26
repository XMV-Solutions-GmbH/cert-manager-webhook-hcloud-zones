// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package solver

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"time"

	whapi "github.com/cert-manager/cert-manager/pkg/acme/webhook/apis/acme/v1alpha1"
	"k8s.io/client-go/rest"

	"github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/internal/hcloud"
	"github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/internal/routing"
)

// SolverName is the value returned by (*Solver).Name() and the string
// operators write into `solvers.dns01.webhook.solverName` on their
// Issuer / ClusterIssuer. Resolved per docs/app-concept.md § 3.2.
const SolverName = "hcloud-zones"

// Defaults for the operational knobs documented in
// docs/app-concept.md §§ 6–7.
const (
	defaultRRSetTTL       = 60                     // seconds
	defaultZoneCacheTTL   = 30 * time.Second       // per § 6.5
	defaultChallengeLabel = "_acme-challenge"      // ACME DNS-01 record label (zone-apex case)
	defaultRRSetType      = "TXT"                  // always TXT for DNS-01
	defaultRequestBudget  = 2 * time.Minute        // upper bound per ch fire
	defaultLoggerSource   = "cert-manager-webhook" // log component label
)

// ClientFactory builds an hcloud.Client for the given token. Abstracted so
// tests can wire the client to an httptest.Server without monkey-patching
// the hcloud package; the production factory in NewClientFactory just
// forwards to hcloud.New.
type ClientFactory func(token string) (HCloudClient, error)

// HCloudClient is the slice of *hcloud.Client the solver actually depends
// on. Defining it as an interface keeps the seam between the solver and
// the hcloud package mockable without dragging the whole client surface
// into the test scaffolding.
type HCloudClient interface {
	ListZones(ctx context.Context) ([]hcloud.Zone, error)
	CreateRRSet(ctx context.Context, zoneID int64, req hcloud.CreateRRSetRequest) (*hcloud.RRSet, error)
	UpdateRRSet(ctx context.Context, zoneID int64, name, rrType string, req hcloud.UpdateRRSetRequest) (*hcloud.RRSet, error)
	DeleteRRSet(ctx context.Context, zoneID int64, name, rrType string) error
}

// Solver wires routing + hcloud + Kubernetes Secret access into the
// cert-manager webhook.Solver contract. The zero value is not usable —
// construct via New.
type Solver struct {
	logger        *slog.Logger
	clientFactory ClientFactory
	secrets       SecretGetter
	zones         *zoneCache
	now           func() time.Time

	// rrsetTTL is the TTL (in seconds) the webhook sets on every
	// _acme-challenge TXT RRSet it creates. Operators typically leave
	// this at the default (60s); a lower value can shave a few seconds
	// off challenge wall-clock but risks resolver cache misses if
	// Hetzner's edge propagation slows.
	rrsetTTL int

	// clientFactorySet tracks whether WithClientFactory was supplied
	// so New can rebuild the default factory with the final logger
	// after all options have been applied.
	clientFactorySet bool

	// requestBudget bounds the wall-clock time of a single Present /
	// CleanUp call. Defends against runaway retries when both the
	// Hetzner API and cert-manager's outer retry loop misbehave.
	requestBudget time.Duration

	// kubeConfig is squirreled away by Initialize for later use by the
	// SecretGetter. cert-manager hands us the rest.Config on startup
	// (see webhook.Solver.Initialize) and never again, so the field is
	// strictly initialised → consumed once.
	kubeConfig *rest.Config
}

// Option configures a Solver. Use the With* helpers below; the production
// constructor wires sane defaults.
type Option func(*Solver)

// WithLogger injects a slog.Logger. The solver itself never logs the raw
// token; the underlying hcloud.Client also redacts before logging (see
// internal/hcloud client.go). Default: slog.Default().
func WithLogger(l *slog.Logger) Option {
	return func(s *Solver) {
		if l != nil {
			s.logger = l
		}
	}
}

// WithClientFactory overrides the hcloud client constructor. Tests use
// this to wire to an httptest.Server. Production callers should leave the
// default in place.
func WithClientFactory(f ClientFactory) Option {
	return func(s *Solver) {
		if f != nil {
			s.clientFactory = f
			s.clientFactorySet = true
		}
	}
}

// WithSecretGetter overrides the Kubernetes Secret resolver. Tests use a
// stub; production code receives the default kube-backed implementation
// once Initialize fires.
func WithSecretGetter(g SecretGetter) Option {
	return func(s *Solver) {
		if g != nil {
			s.secrets = g
		}
	}
}

// WithZoneCacheTTL overrides the bounded TTL of the zone-name → ID cache.
// Default: 30s per docs/app-concept.md § 6.5.
func WithZoneCacheTTL(d time.Duration) Option {
	return func(s *Solver) {
		if d > 0 {
			s.zones = newZoneCache(d, s.now)
		}
	}
}

// WithRRSetTTL overrides the TTL (in seconds) of the created TXT RRSet.
// Default: 60s.
func WithRRSetTTL(seconds int) Option {
	return func(s *Solver) {
		if seconds > 0 {
			s.rrsetTTL = seconds
		}
	}
}

// WithRequestBudget overrides the per-Present / per-CleanUp wall-clock
// budget. Default: 2 minutes.
func WithRequestBudget(d time.Duration) Option {
	return func(s *Solver) {
		if d > 0 {
			s.requestBudget = d
		}
	}
}

// WithClock injects a now-function. Tests use this to drive the zone
// cache TTL deterministically.
func WithClock(now func() time.Time) Option {
	return func(s *Solver) {
		if now != nil {
			s.now = now
			s.zones = newZoneCache(s.zones.ttl, now)
		}
	}
}

// DefaultClientFactory is the production ClientFactory. It builds a
// real *hcloud.Client wired to the public Hetzner Cloud Zones API; the
// returned client honours docs/app-concept.md §§ 6.2–6.4 (retries,
// backoff, Retry-After, log redaction) by default.
func DefaultClientFactory(logger *slog.Logger) ClientFactory {
	return func(token string) (HCloudClient, error) {
		opts := []hcloud.Option{}
		if logger != nil {
			opts = append(opts, hcloud.WithLogger(logger))
		}
		return hcloud.New(hcloud.StaticToken(token), opts...)
	}
}

// New constructs a Solver wired with the production defaults. Override
// any default via an Option.
//
// Initialize MUST be called by the cert-manager webhook framework before
// Present or CleanUp; the solver returns an error from those entry
// points if it has not been initialised. In tests, the Option-based
// constructor lets callers pre-supply a SecretGetter and client factory,
// so Initialize is a no-op in that path.
func New(opts ...Option) *Solver {
	s := &Solver{
		logger:        slog.Default(),
		now:           time.Now,
		rrsetTTL:      defaultRRSetTTL,
		requestBudget: defaultRequestBudget,
	}
	s.zones = newZoneCache(defaultZoneCacheTTL, s.now)

	for _, opt := range opts {
		opt(s)
	}

	// Build the default factory once every option has been applied so
	// it picks up the operator-supplied logger. WithClientFactory
	// suppresses this rebuild.
	if !s.clientFactorySet {
		s.clientFactory = DefaultClientFactory(s.logger)
	}

	return s
}

// Name returns the solver name. It matches the value cert-manager users
// write into `solvers.dns01.webhook.solverName`.
func (s *Solver) Name() string { return SolverName }

// Initialize is the post-start hook cert-manager invokes on the API
// server. The solver squirrels the rest.Config away and uses it to build
// the production SecretGetter on first need. The stopCh is unused: the
// solver holds no goroutines.
func (s *Solver) Initialize(kubeConfig *rest.Config, _ <-chan struct{}) error {
	if kubeConfig == nil {
		return errors.New("solver: Initialize called with nil rest.Config")
	}
	s.kubeConfig = kubeConfig

	// If the caller pre-wired a SecretGetter via WithSecretGetter
	// (test path), leave it alone. Otherwise build the default
	// Kubernetes-backed implementation now.
	if s.secrets == nil {
		getter, err := newKubeSecretGetter(kubeConfig)
		if err != nil {
			return err
		}
		s.secrets = getter
	}
	return nil
}

// Present provisions the `_acme-challenge` TXT RRSet for the challenge.
// The implementation is idempotent — re-presenting an FQDN whose record
// already carries the correct value is a no-op; a stale value triggers an
// UpdateRRSet PATCH.
func (s *Solver) Present(ch *whapi.ChallengeRequest) error {
	if ch == nil {
		return errors.New("solver: Present called with nil ChallengeRequest")
	}
	ctx, cancel := s.contextFor()
	defer cancel()

	cred, secretRef, client, zoneApex, err := s.prepare(ctx, ch)
	if err != nil {
		return err
	}

	zoneID, err := s.resolveZoneID(ctx, client, secretRef.String(), zoneApex)
	if err != nil {
		return s.wrapError("resolve zone", cred.Name, zoneApex, err)
	}

	recordName := relativeRecordName(ch.ResolvedFQDN, zoneApex)
	ttl := s.rrsetTTL
	req := hcloud.CreateRRSetRequest{
		Name:    recordName,
		Type:    defaultRRSetType,
		TTL:     &ttl,
		Records: []hcloud.Record{{Value: quoteTXT(ch.Key)}},
	}

	rrset, err := client.CreateRRSet(ctx, zoneID, req)
	switch {
	case err == nil:
		s.logger.LogAttrs(ctx, slog.LevelInfo, "solver: presented challenge",
			slog.String("credential", cred.Name),
			slog.String("zone", zoneApex),
			slog.String("record_name", recordName),
			slog.Int64("zone_id", zoneID),
			slog.String("rrset_id", rrset.ID),
		)
		return nil

	case errors.Is(err, hcloud.ErrConflict):
		// Idempotent path: the RRSet already exists. If the value
		// matches, no-op; otherwise PATCH to the new value.
		return s.reconcileExisting(ctx, client, cred.Name, zoneApex, recordName, zoneID, ch.Key)

	case errors.Is(err, hcloud.ErrNotFound):
		// Zone vanished between ListZones and Create — drop the
		// cached entry so the next Present re-resolves.
		s.zones.Invalidate(secretRef.String(), zoneApex)
		return s.wrapError("create RRSet", cred.Name, zoneApex, err)

	default:
		return s.wrapError("create RRSet", cred.Name, zoneApex, err)
	}
}

// CleanUp removes the TXT RRSet provisioned by Present. A 404 from the
// API is treated as success — the record is already gone, which is
// exactly the post-condition CleanUp guarantees.
func (s *Solver) CleanUp(ch *whapi.ChallengeRequest) error {
	if ch == nil {
		return errors.New("solver: CleanUp called with nil ChallengeRequest")
	}
	ctx, cancel := s.contextFor()
	defer cancel()

	cred, secretRef, client, zoneApex, err := s.prepare(ctx, ch)
	if err != nil {
		return err
	}

	zoneID, err := s.resolveZoneID(ctx, client, secretRef.String(), zoneApex)
	if err != nil {
		// If the zone itself is gone, the record is gone too — that
		// is success from CleanUp's point of view.
		if errors.Is(err, hcloud.ErrNotFound) {
			s.logger.LogAttrs(ctx, slog.LevelInfo, "solver: cleanup no-op (zone not found)",
				slog.String("credential", cred.Name),
				slog.String("zone", zoneApex),
			)
			return nil
		}
		return s.wrapError("resolve zone", cred.Name, zoneApex, err)
	}

	recordName := relativeRecordName(ch.ResolvedFQDN, zoneApex)
	err = client.DeleteRRSet(ctx, zoneID, recordName, defaultRRSetType)
	switch {
	case err == nil:
		s.logger.LogAttrs(ctx, slog.LevelInfo, "solver: cleaned up challenge",
			slog.String("credential", cred.Name),
			slog.String("zone", zoneApex),
			slog.String("record_name", recordName),
			slog.Int64("zone_id", zoneID),
		)
		return nil

	case errors.Is(err, hcloud.ErrNotFound):
		s.logger.LogAttrs(ctx, slog.LevelInfo, "solver: cleanup no-op (RRSet already gone)",
			slog.String("credential", cred.Name),
			slog.String("zone", zoneApex),
			slog.String("record_name", recordName),
		)
		return nil

	default:
		return s.wrapError("delete RRSet", cred.Name, zoneApex, err)
	}
}

// ----------------------------------------------------------------------------
// Internals
// ----------------------------------------------------------------------------

// prepare runs the shared per-call setup: parse config, route the FQDN,
// fetch the token, build the hcloud client. Both Present and CleanUp call
// it.
func (s *Solver) prepare(ctx context.Context, ch *whapi.ChallengeRequest) (
	cred *routing.Credential,
	secretRef SecretRef,
	client HCloudClient,
	zoneApex string,
	err error,
) {
	routingCfg, secretRefs, err := parseConfig(ch.Config, ch.ResourceNamespace)
	if err != nil {
		return nil, SecretRef{}, nil, "", err
	}

	cred, zoneApex, err = routingCfg.Resolve(ch.ResolvedFQDN)
	if err != nil {
		return nil, SecretRef{}, nil, "", fmt.Errorf("solver: route FQDN %q: %w", ch.ResolvedFQDN, err)
	}

	secretRef, ok := secretRefs[cred.Name]
	if !ok {
		// Defensive: parseConfig populates this map for every cred.
		return nil, SecretRef{}, nil, "", fmt.Errorf("solver: internal error — no secret ref for credential %q", cred.Name)
	}

	if s.secrets == nil {
		return nil, SecretRef{}, nil, "", errors.New("solver: not initialised — Initialize() must be called before Present/CleanUp, or WithSecretGetter must be supplied")
	}

	token, err := s.secrets.GetToken(ctx, secretRef)
	if err != nil {
		return nil, SecretRef{}, nil, "", err
	}

	client, err = s.clientFactory(token)
	if err != nil {
		return nil, SecretRef{}, nil, "", fmt.Errorf("solver: build hcloud client: %w", err)
	}
	return cred, secretRef, client, zoneApex, nil
}

// resolveZoneID looks up the Hetzner zone ID for the named zone-apex.
// Cached lookups are answered without an API call; misses fall through
// to ListZones and refresh the cache.
func (s *Solver) resolveZoneID(ctx context.Context, client HCloudClient, secretRefKey, zoneApex string) (int64, error) {
	if id, ok := s.zones.Lookup(secretRefKey, zoneApex); ok {
		return id, nil
	}
	zones, err := client.ListZones(ctx)
	if err != nil {
		return 0, err
	}
	for _, z := range zones {
		if z.Name == zoneApex {
			s.zones.Store(secretRefKey, zoneApex, z.ID)
			return z.ID, nil
		}
	}
	// Zone is not in the token's project: present this as a 404
	// equivalent so callers can errors.Is(err, hcloud.ErrNotFound).
	return 0, &hcloud.APIError{
		StatusCode: 404,
		Code:       "not_found",
		Message:    fmt.Sprintf("zone %q not found via this credential's token; verify the zone lives in the Hetzner Cloud project the credential points at", zoneApex),
	}
}

// reconcileExisting handles the 409 conflict path: read the current
// RRSet via an Update (UpdateRRSet is the only way to read the live
// record without paginating /rrsets). The implementation chooses
// "always PATCH to the desired value": Hetzner's PATCH is idempotent
// itself, so a no-op (same records) costs one request and saves the
// extra GET round-trip.
func (s *Solver) reconcileExisting(ctx context.Context, client HCloudClient, credName, zoneApex, recordName string, zoneID int64, key string) error {
	ttl := s.rrsetTTL
	req := hcloud.UpdateRRSetRequest{
		TTL:     &ttl,
		Records: []hcloud.Record{{Value: quoteTXT(key)}},
	}
	if _, err := client.UpdateRRSet(ctx, zoneID, recordName, defaultRRSetType, req); err != nil {
		return s.wrapError("update existing RRSet (conflict path)", credName, zoneApex, err)
	}
	s.logger.LogAttrs(ctx, slog.LevelInfo, "solver: reconciled existing challenge RRSet",
		slog.String("credential", credName),
		slog.String("zone", zoneApex),
		slog.String("record_name", recordName),
		slog.Int64("zone_id", zoneID),
	)
	return nil
}

// wrapError adds the operator-facing context the cert-manager Challenge
// status will display in `kubectl describe challenge`. The Hetzner
// APIError carries the wire-level details; the wrapper labels the stage
// + the credential + the zone so the operator can localise the failure.
func (s *Solver) wrapError(stage, credName, zoneApex string, cause error) error {
	return fmt.Errorf("solver: %s failed for credential %q zone %q: %w",
		stage, credName, zoneApex, cause)
}

// contextFor returns a per-call context bounded by the request budget.
func (s *Solver) contextFor() (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.Background(), s.requestBudget)
}

// quoteTXT renders a DNS-01 challenge key in the canonical RFC-1035
// quoted-string form Hetzner accepts (the API stores the value verbatim
// — wrapping in quotes is the convention every other webhook follows
// and matches the value `dig +short TXT _acme-challenge.example.com`
// will show).
func quoteTXT(key string) string {
	// strconv.Quote handles internal quotes by escaping them — the
	// ACME key is base64url so this never actually fires, but the
	// safety belt is free.
	return strconv.Quote(key)
}

// relativeRecordName derives the zone-relative RRSet name from the FQDN
// cert-manager hands us in ChallengeRequest.ResolvedFQDN and the zone
// apex the router picked. This is the load-bearing piece that makes the
// webhook work for subdomain certificates — without it, every challenge
// would write to `_acme-challenge` at the zone apex regardless of the
// actual cert's CN/SAN, and LE-validation would fail for any non-apex
// cert.
//
// Examples (zoneApex = "example.com"):
//
//	"_acme-challenge.example.com."         → "_acme-challenge"
//	"_acme-challenge.foo.example.com."     → "_acme-challenge.foo"
//	"_acme-challenge.bar.foo.example.com." → "_acme-challenge.bar.foo"
//
// The router (routing.Resolve) guarantees fqdn ends with zoneApex, so
// the defensive fallback to "_acme-challenge" only fires if a future
// refactor breaks that invariant — surfacing the misconfig in the
// Hetzner API error rather than silently writing to the apex.
func relativeRecordName(resolvedFQDN, zoneApex string) string {
	fqdn := strings.ToLower(strings.TrimSuffix(resolvedFQDN, "."))
	apex := strings.ToLower(zoneApex)
	if fqdn == apex {
		// Apex-only TXT (never produced by ACME's _acme-challenge.*
		// pattern, but defensive). Hetzner uses "@" for the apex name.
		return "@"
	}
	if suffix := "." + apex; strings.HasSuffix(fqdn, suffix) {
		return strings.TrimSuffix(fqdn, suffix)
	}
	// Router invariant broken — fall back to apex label so the API
	// surfaces a clear error rather than silently misrouting.
	return defaultChallengeLabel
}
