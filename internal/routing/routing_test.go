// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package routing

import (
	"errors"
	"strings"
	"testing"
)

// -----------------------------------------------------------------------------
// Resolve — one t.Run per matrix row from docs/app-concept.md § 3.1.
// -----------------------------------------------------------------------------

func TestResolve(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      Config
		fqdn        string
		wantCred    string
		wantZone    string
		wantErrIs   error
		wantErrText string
	}{
		{
			name: "single token, single zone — trivial happy path",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:     "_acme-challenge.example.com",
			wantCred: "project-a",
			wantZone: "example.com",
		},
		{
			name: "single token, multi zone — FQDN matches second zone in list",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com", "example.net"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:     "_acme-challenge.app.example.net",
			wantCred: "project-a",
			wantZone: "example.net",
		},
		{
			name: "multi token, one zone each — pick the right credential",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
					{Name: "project-b", Zones: []string{"example.org"}, APITokenSecretRef: "tok-b"},
				},
			},
			fqdn:     "_acme-challenge.app.example.org",
			wantCred: "project-b",
			wantZone: "example.org",
		},
		{
			name: "multi token, multi zone each — second project's second zone",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com", "example.net"}, APITokenSecretRef: "tok-a"},
					{Name: "project-b", Zones: []string{"example.org", "example.de"}, APITokenSecretRef: "tok-b"},
				},
			},
			fqdn:     "_acme-challenge.deep.nested.example.de",
			wantCred: "project-b",
			wantZone: "example.de",
		},
		{
			name: "FQDN prefix stripped — leaf-to-root walk finds the apex three labels in",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:     "_acme-challenge.app.foo.example.com",
			wantCred: "project-a",
			wantZone: "example.com",
		},
		{
			name: "FQDN without _acme-challenge. prefix is also accepted",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:     "app.example.com",
			wantCred: "project-a",
			wantZone: "example.com",
		},
		{
			name: "wildcard FQDN — cert-manager passes example.com itself after prepending the prefix",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:     "_acme-challenge.example.com",
			wantCred: "project-a",
			wantZone: "example.com",
		},
		{
			name: "trailing dot in FQDN is tolerated",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:     "_acme-challenge.app.example.com.",
			wantCred: "project-a",
			wantZone: "example.com",
		},
		{
			name: "case-insensitive matching — FQDN upper-case, zone lower-case",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:     "_acme-challenge.APP.EXAMPLE.COM",
			wantCred: "project-a",
			wantZone: "example.com",
		},
		{
			name: "no match — fails closed with error listing configured zones",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
					{Name: "project-b", Zones: []string{"example.org"}, APITokenSecretRef: "tok-b"},
				},
			},
			fqdn:        "_acme-challenge.app.unknown.tld",
			wantErrIs:   ErrNoMatch,
			wantErrText: "example.com, example.org",
		},
		{
			name: "no match — apex of unrelated TLD, error names the offending FQDN",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:        "_acme-challenge.foo.example.net",
			wantErrIs:   ErrNoMatch,
			wantErrText: `"foo.example.net"`,
		},
		{
			name: "empty FQDN returns ErrNoMatch",
			config: Config{
				Credentials: []Credential{
					{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			fqdn:      "",
			wantErrIs: ErrNoMatch,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			cred, zone, err := tc.config.Resolve(tc.fqdn)
			if tc.wantErrIs != nil {
				if !errors.Is(err, tc.wantErrIs) {
					t.Fatalf("expected error wrapping %v, got %v", tc.wantErrIs, err)
				}
				if tc.wantErrText != "" && !strings.Contains(err.Error(), tc.wantErrText) {
					t.Fatalf("expected error to contain %q, got %q", tc.wantErrText, err.Error())
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if cred == nil {
				t.Fatalf("expected credential, got nil")
			}
			if cred.Name != tc.wantCred {
				t.Errorf("credential name: got %q, want %q", cred.Name, tc.wantCred)
			}
			if zone != tc.wantZone {
				t.Errorf("zone: got %q, want %q", zone, tc.wantZone)
			}
		})
	}
}

// TestResolveNilReceiver checks that calling Resolve on a nil *Config returns
// a clean ErrNoMatch rather than panicking — the solver may construct a
// pointer once and pass it around.
func TestResolveNilReceiver(t *testing.T) {
	t.Parallel()
	var c *Config
	_, _, err := c.Resolve("_acme-challenge.example.com")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch from nil receiver, got %v", err)
	}
}

// TestResolveDoesNotMatchSubLabelSubstring guards against a possible bug
// where a sloppy implementation might treat "anotherexample.com" as ending in
// "example.com". Suffix matching must respect label boundaries.
func TestResolveDoesNotMatchSubLabelSubstring(t *testing.T) {
	t.Parallel()
	c := Config{
		Credentials: []Credential{
			{Name: "project-a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
		},
	}
	_, _, err := c.Resolve("_acme-challenge.notexample.com")
	if !errors.Is(err, ErrNoMatch) {
		t.Fatalf("expected ErrNoMatch for notexample.com (different registrable domain), got %v", err)
	}
}

// -----------------------------------------------------------------------------
// ValidateConfig — one t.Run per reject reason.
// -----------------------------------------------------------------------------

func TestValidateConfig(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		config      *Config
		wantErrText string // substring; empty == expect no error
	}{
		{
			name: "valid — single credential, single two-label zone",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
		},
		{
			name: "valid — multi-part public suffix accepted (example.co.uk)",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.co.uk"}, APITokenSecretRef: "tok-a"},
				},
			},
		},
		{
			name: "valid — multi-part public suffix accepted (example.com.au)",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.com.au"}, APITokenSecretRef: "tok-a"},
				},
			},
		},
		{
			name: "valid — two credentials, disjoint zones",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.com", "example.net"}, APITokenSecretRef: "tok-a"},
					{Name: "b", Zones: []string{"example.org"}, APITokenSecretRef: "tok-b"},
				},
			},
		},
		{
			name:        "nil config rejected",
			config:      nil,
			wantErrText: "config is nil",
		},
		{
			name:        "empty credentials list rejected",
			config:      &Config{Credentials: []Credential{}},
			wantErrText: "no credentials",
		},
		{
			name: "duplicate zone across credentials rejected with both credential names",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
					{Name: "b", Zones: []string{"example.com"}, APITokenSecretRef: "tok-b"},
				},
			},
			wantErrText: `"a"`, // both credential names should appear; "a" is the first.
		},
		{
			name: "duplicate zone within one credential rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.com", "example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "more than once",
		},
		{
			name: "duplicate zone case-insensitive (Example.COM vs example.com)",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"Example.COM"}, APITokenSecretRef: "tok-a"},
					{Name: "b", Zones: []string{"example.com"}, APITokenSecretRef: "tok-b"},
				},
			},
			wantErrText: "each zone-apex must appear in exactly one credential",
		},
		{
			name: "subdomain of registrable domain rejected — app.example.com",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"app.example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "subdomain of a registrable domain",
		},
		{
			name: "subdomain of multi-part PSL rejected — app.example.co.uk",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"app.example.co.uk"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "subdomain of a registrable domain",
		},
		{
			name: "empty credential name rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "empty name",
		},
		{
			name: "whitespace-only credential name rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "   ", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "empty name",
		},
		{
			name: "duplicate credential name rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
					{Name: "a", Zones: []string{"example.org"}, APITokenSecretRef: "tok-b"},
				},
			},
			wantErrText: "duplicate credential name",
		},
		{
			name: "empty zone list rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "no zones",
		},
		{
			name: "empty zone string rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{""}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "empty zone string",
		},
		{
			name: "zone with leading dot rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{".example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "leading dot",
		},
		{
			name: "zone with trailing dot rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.com."}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "trailing dot",
		},
		{
			name: "zone with wildcard rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"*.example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "wildcard",
		},
		{
			name: "zone with empty label rejected (double dot)",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example..com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "empty label",
		},
		{
			name: "zone with invalid character rejected (underscore)",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"exa_mple.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "invalid character",
		},
		{
			name: "zone with leading/trailing whitespace rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{" example.com"}, APITokenSecretRef: "tok-a"},
				},
			},
			wantErrText: "whitespace",
		},
		{
			name: "empty apiTokenSecretRef rejected",
			config: &Config{
				Credentials: []Credential{
					{Name: "a", Zones: []string{"example.com"}, APITokenSecretRef: ""},
				},
			},
			wantErrText: "empty apiTokenSecretRef",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			err := ValidateConfig(tc.config)
			if tc.wantErrText == "" {
				if err != nil {
					t.Fatalf("expected no error, got %v", err)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tc.wantErrText)
			}
			if !strings.Contains(err.Error(), tc.wantErrText) {
				t.Fatalf("expected error to contain %q, got %q", tc.wantErrText, err.Error())
			}
		})
	}
}

// TestValidateConfigDuplicateZoneNamesBothCredentials confirms the
// duplicate-zone error names BOTH credentials so the operator can fix the
// misconfiguration without grep'ing.
func TestValidateConfigDuplicateZoneNamesBothCredentials(t *testing.T) {
	t.Parallel()
	cfg := &Config{
		Credentials: []Credential{
			{Name: "alpha", Zones: []string{"example.com"}, APITokenSecretRef: "tok-a"},
			{Name: "beta", Zones: []string{"example.com"}, APITokenSecretRef: "tok-b"},
		},
	}
	err := ValidateConfig(cfg)
	if err == nil {
		t.Fatal("expected duplicate-zone error, got nil")
	}
	if !strings.Contains(err.Error(), "alpha") || !strings.Contains(err.Error(), "beta") {
		t.Errorf("expected both credential names in error, got %q", err.Error())
	}
}
