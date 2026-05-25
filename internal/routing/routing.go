// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

// Package routing implements the zone-apex → credential routing layer and the
// load-time config validation pass described in docs/app-concept.md §§ 3.2–3.3.
//
// This package is intentionally pure: it only operates on configuration data
// and FQDN strings. It does not touch the Kubernetes API, the Hetzner Cloud
// API, or any other I/O. The Hetzner client (internal/hcloud) and the
// cert-manager solver glue (internal/solver) wire it into a running webhook in
// later sub-tasks.
//
// Per § 3.4 the Hetzner Cloud Zones API forbids subdomain zones, so at most
// one configured zone-apex can match any given FQDN. The resolver therefore
// walks from leaf to root, label by label, and returns the first suffix that
// is present in the configured set — an exact membership lookup, not a
// longest-suffix-match.
package routing

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// acmeChallengePrefix is the label cert-manager prepends to every FQDN it asks
// us to provision a TXT record for. The resolver strips it before walking the
// suffix chain.
const acmeChallengePrefix = "_acme-challenge."

// Credential is a single operator-defined routing entry: one Hetzner Cloud API
// token (referenced by Secret name) authoritative for a list of zone-apex
// names.
//
// APITokenSecretRef holds the opaque string identifier for the Kubernetes
// Secret holding the hcloud token. This package never resolves the reference;
// it is carried opaquely so the caller (the solver) can look the Secret up at
// challenge time.
type Credential struct {
	// Name is the operator-chosen identifier for this credential, used in
	// error messages so misconfiguration is diagnosable from
	// `kubectl describe challenge` output.
	Name string

	// Zones is the list of zone-apex names this credential is authoritative
	// for (the same strings that appear as zone names in the Hetzner Cloud
	// Console). Each entry must be a registrable domain — see
	// ValidateConfig for the rules enforced at load time.
	Zones []string

	// APITokenSecretRef is the opaque reference string for the Kubernetes
	// Secret holding the hcloud API token. Resolution of the reference is
	// the caller's responsibility; this package treats it as an opaque
	// string carried through to the matched routing decision.
	APITokenSecretRef string
}

// Config is the full set of routing entries for a single Issuer / ClusterIssuer
// solver block.
type Config struct {
	Credentials []Credential
}

// ErrNoMatch is returned by Resolve when no configured zone-apex matches the
// FQDN. The returned error wraps this sentinel so callers can errors.Is(err,
// ErrNoMatch) without parsing the message.
var ErrNoMatch = errors.New("no configured zone-apex matches FQDN")

// Resolve finds the credential whose zone-apex list contains an exact suffix
// of fqdn. The fqdn argument may include or omit the cert-manager
// `_acme-challenge.` prefix; a trailing dot is also tolerated. The returned
// zone-apex is the matching suffix string (without trailing dot); the returned
// credential is a pointer into the Config and must not be mutated by the
// caller.
//
// Resolve does not run ValidateConfig — call ValidateConfig at config-load
// time to surface duplicates and malformed entries with a clear error, then
// rely on Resolve to be fast and allocation-free on the request hot path.
func (c *Config) Resolve(fqdn string) (*Credential, string, error) {
	if c == nil {
		return nil, "", fmt.Errorf("%w: config is nil", ErrNoMatch)
	}

	name := normaliseFQDN(fqdn)
	if name == "" {
		return nil, "", fmt.Errorf("%w: FQDN is empty", ErrNoMatch)
	}

	// Build the index every call. The map is small (one entry per
	// configured zone across all credentials, expected O(10) total) and the
	// caller is welcome to memoise via sync.Once at a higher layer if it
	// matters; keeping Resolve pure makes testing trivial.
	index := make(map[string]int, len(c.Credentials)*2)
	for i := range c.Credentials {
		for _, zone := range c.Credentials[i].Zones {
			index[strings.ToLower(zone)] = i
		}
	}

	// Walk leaf → root, label by label.
	candidate := name
	for {
		if idx, ok := index[candidate]; ok {
			return &c.Credentials[idx], candidate, nil
		}
		dot := strings.IndexByte(candidate, '.')
		if dot < 0 {
			break
		}
		candidate = candidate[dot+1:]
	}

	return nil, "", fmt.Errorf("%w: %q (configured zones: %s)",
		ErrNoMatch, name, formatConfiguredZones(c))
}

// normaliseFQDN strips the `_acme-challenge.` prefix (if present), removes a
// trailing dot, and lower-cases the result. The cert-manager webhook contract
// passes ResolvedFQDN already lowercased and dot-terminated, but we accept any
// of the obvious shapes so callers do not have to remember which.
func normaliseFQDN(fqdn string) string {
	out := strings.ToLower(strings.TrimSpace(fqdn))
	out = strings.TrimSuffix(out, ".")
	out = strings.TrimPrefix(out, acmeChallengePrefix)
	return out
}

// formatConfiguredZones returns a deterministic comma-separated list of every
// configured zone-apex across every credential, used in the ErrNoMatch error
// string so operators can see at a glance what zones are configured vs. what
// they asked for.
func formatConfiguredZones(c *Config) string {
	var zones []string
	for i := range c.Credentials {
		zones = append(zones, c.Credentials[i].Zones...)
	}
	sort.Strings(zones)
	if len(zones) == 0 {
		return "<none>"
	}
	return strings.Join(zones, ", ")
}

// ValidateConfig runs every load-time check the routing layer can perform
// without talking to Hetzner or Kubernetes:
//
//   - empty / malformed entries (missing name, empty zone list, empty zone
//     string, zone with leading/trailing dot, zone with wildcard label);
//   - duplicate zone-apex across credentials (operator misconfiguration: same
//     zone listed under two credentials);
//   - configured zone-apex that is itself a subdomain of a registrable domain
//     (defence in depth — Hetzner Cloud Zones rejects such zones at
//     creation, but failing fast at config-load time gives the operator a far
//     better error than "challenges silently fail at issuance time").
//
// The subdomain heuristic is intentionally simple: a configured zone-apex
// must have exactly two labels (e.g. example.com) OR end in one of the
// well-known multi-part public suffixes enumerated in multiPartPublicSuffixes
// (e.g. example.co.uk). A full Public Suffix List dependency is overkill for
// the MVP; if a future operator hits a false positive, swapping in
// golang.org/x/net/publicsuffix is a one-function-replacement change tracked
// as a v0.2 candidate.
//
// ValidateConfig returns the first error it encounters. Callers that want a
// full report can wrap it in a loop, but operationally the first failure is
// almost always the diagnostic the operator needs.
func ValidateConfig(config *Config) error {
	if config == nil {
		return errors.New("config is nil")
	}
	if len(config.Credentials) == 0 {
		return errors.New("config has no credentials; at least one credential is required")
	}

	// Track which credential first declared each zone, so the duplicate
	// error can name both offenders.
	firstSeenBy := make(map[string]string, len(config.Credentials)*2)
	credNames := make(map[string]struct{}, len(config.Credentials))

	for i := range config.Credentials {
		cred := &config.Credentials[i]

		if strings.TrimSpace(cred.Name) == "" {
			return fmt.Errorf("credential at index %d has empty name", i)
		}
		if _, dup := credNames[cred.Name]; dup {
			return fmt.Errorf("duplicate credential name %q", cred.Name)
		}
		credNames[cred.Name] = struct{}{}

		if strings.TrimSpace(cred.APITokenSecretRef) == "" {
			return fmt.Errorf("credential %q has empty apiTokenSecretRef", cred.Name)
		}

		if len(cred.Zones) == 0 {
			return fmt.Errorf("credential %q has no zones; at least one zone is required", cred.Name)
		}

		seenInCred := make(map[string]struct{}, len(cred.Zones))
		for _, zone := range cred.Zones {
			if err := validateZoneString(zone, cred.Name); err != nil {
				return err
			}
			normalised := strings.ToLower(zone)
			if _, dup := seenInCred[normalised]; dup {
				return fmt.Errorf("credential %q lists zone %q more than once",
					cred.Name, zone)
			}
			seenInCred[normalised] = struct{}{}

			if other, dup := firstSeenBy[normalised]; dup {
				return fmt.Errorf(
					"zone %q is listed by both credential %q and credential %q; "+
						"each zone-apex must appear in exactly one credential",
					zone, other, cred.Name)
			}
			firstSeenBy[normalised] = cred.Name
		}
	}

	return nil
}

// validateZoneString applies the per-zone-string syntactic and public-suffix
// checks. Splitting this out keeps ValidateConfig readable.
func validateZoneString(zone, credName string) error {
	if zone == "" {
		return fmt.Errorf("credential %q has an empty zone string", credName)
	}
	if strings.TrimSpace(zone) != zone {
		return fmt.Errorf("credential %q zone %q has leading or trailing whitespace",
			credName, zone)
	}
	if strings.HasPrefix(zone, ".") {
		return fmt.Errorf("credential %q zone %q has a leading dot", credName, zone)
	}
	if strings.HasSuffix(zone, ".") {
		return fmt.Errorf("credential %q zone %q has a trailing dot; "+
			"zone-apex names are bare (no terminating dot)", credName, zone)
	}
	if strings.Contains(zone, "*") {
		return fmt.Errorf("credential %q zone %q contains a wildcard; "+
			"zone-apex names are literal, not patterns", credName, zone)
	}
	if strings.Contains(zone, "..") {
		return fmt.Errorf("credential %q zone %q contains an empty label", credName, zone)
	}
	for _, r := range zone {
		switch {
		case r >= 'a' && r <= 'z':
		case r >= 'A' && r <= 'Z':
		case r >= '0' && r <= '9':
		case r == '-' || r == '.':
		default:
			return fmt.Errorf("credential %q zone %q contains invalid character %q; "+
				"only [a-z0-9-.] allowed (IDN must be punycoded)",
				credName, zone, r)
		}
	}

	if isSubdomainOfRegistrable(zone) {
		return fmt.Errorf(
			"credential %q zone %q looks like a subdomain of a registrable domain; "+
				"Hetzner Cloud Zones does not support subdomain zones "+
				"(see docs/app-concept.md § 3.4) — use the zone-apex instead",
			credName, zone)
	}
	return nil
}

// multiPartPublicSuffixes is the conservative allow-list of multi-part public
// suffixes the validator recognises. Anything ending in one of these is
// allowed to have three labels (e.g. example.co.uk). Everything else must
// have exactly two labels.
//
// This is deliberately a small static set rather than the full Public Suffix
// List; the trade-off is documented in ValidateConfig's godoc. False
// positives can be worked around by an operator (they will see a clear error
// pointing them at this list) until v0.2 swaps in publicsuffix.List.
var multiPartPublicSuffixes = []string{
	".co.uk", ".co.jp", ".co.kr", ".co.nz", ".co.za", ".co.in", ".co.il",
	".com.au", ".com.br", ".com.cn", ".com.mx", ".com.sg", ".com.tr",
	".com.tw", ".com.hk", ".com.ar", ".com.pl", ".com.ua",
	".net.au", ".net.nz", ".net.cn", ".net.br",
	".org.uk", ".org.au", ".org.nz", ".org.za",
	".ac.uk", ".ac.jp", ".ac.nz", ".ac.za",
	".gov.uk", ".gov.au",
}

// isSubdomainOfRegistrable returns true if zone has more labels than a
// registrable domain would: more than 2 labels and not ending in any of the
// multiPartPublicSuffixes. The intent is to reject e.g. `app.example.com`
// while accepting `example.com` and `example.co.uk`.
func isSubdomainOfRegistrable(zone string) bool {
	z := strings.ToLower(zone)
	labels := strings.Split(z, ".")
	if len(labels) < 2 {
		// Single-label "TLDs" like `localhost` are rejected separately by
		// Hetzner; we accept them through here and let Hetzner refuse,
		// since the operator may legitimately experiment against a local
		// test zone. Returning false (= not a subdomain) keeps the
		// "subdomain-of-registrable" check focused.
		return false
	}
	if len(labels) == 2 {
		return false
	}
	for _, sfx := range multiPartPublicSuffixes {
		if strings.HasSuffix("."+z, sfx) && len(labels) == 3 {
			return false
		}
	}
	return true
}
