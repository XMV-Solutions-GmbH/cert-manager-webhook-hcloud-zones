// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package solver

import "testing"

// TestRelativeRecordName_Cases pins the apex/subdomain mapping behaviour
// the solver relies on. The pre-v0.1.3 bug shipped a constant
// `defaultChallengeName = "_acme-challenge"` everywhere, which silently
// wrote the TXT record to the zone apex regardless of the actual cert's
// CN/SAN — making the webhook only work for apex-only certs. The harness
// run against LE STAGING for `app-a.xmv-example.com` is what surfaced
// it; the table here guards the fix.
func TestRelativeRecordName_Cases(t *testing.T) {
	cases := []struct {
		name         string
		resolvedFQDN string
		zoneApex     string
		want         string
	}{
		{
			name:         "apex challenge — cert for the zone itself",
			resolvedFQDN: "_acme-challenge.example.com.",
			zoneApex:     "example.com",
			want:         "_acme-challenge",
		},
		{
			name:         "one-label subdomain",
			resolvedFQDN: "_acme-challenge.app.example.com.",
			zoneApex:     "example.com",
			want:         "_acme-challenge.app",
		},
		{
			name:         "multi-label subdomain",
			resolvedFQDN: "_acme-challenge.foo.bar.example.com.",
			zoneApex:     "example.com",
			want:         "_acme-challenge.foo.bar",
		},
		{
			name:         "harness-style subdomain (the LE STAGING regression)",
			resolvedFQDN: "_acme-challenge.app-a-20260526t124657-56b45f.xmv-example.com.",
			zoneApex:     "xmv-example.com",
			want:         "_acme-challenge.app-a-20260526t124657-56b45f",
		},
		{
			name:         "FQDN without trailing dot — cert-manager normally sends one but defend",
			resolvedFQDN: "_acme-challenge.app.example.com",
			zoneApex:     "example.com",
			want:         "_acme-challenge.app",
		},
		{
			name:         "mixed case FQDN — lower-case the result",
			resolvedFQDN: "_acme-challenge.App.Example.COM.",
			zoneApex:     "example.com",
			want:         "_acme-challenge.app",
		},
		{
			name:         "mixed case zone apex",
			resolvedFQDN: "_acme-challenge.app.example.com.",
			zoneApex:     "EXAMPLE.COM",
			want:         "_acme-challenge.app",
		},
		{
			name:         "delegated subdomain zone — apex is harness.xmv-solutions.de",
			resolvedFQDN: "_acme-challenge.app-a-20260526.harness.xmv-solutions.de.",
			zoneApex:     "harness.xmv-solutions.de",
			want:         "_acme-challenge.app-a-20260526",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := relativeRecordName(tc.resolvedFQDN, tc.zoneApex)
			if got != tc.want {
				t.Fatalf("relativeRecordName(%q, %q) = %q; want %q",
					tc.resolvedFQDN, tc.zoneApex, got, tc.want)
			}
		})
	}
}

// TestRelativeRecordName_RouterInvariantBroken exercises the defensive
// fallback path. The router (routing.Resolve) guarantees the FQDN ends
// with the picked zoneApex, so this branch is unreachable through the
// normal API; the test pins behaviour for future refactors.
func TestRelativeRecordName_RouterInvariantBroken(t *testing.T) {
	got := relativeRecordName("_acme-challenge.other.tld.", "example.com")
	if got != defaultChallengeLabel {
		t.Fatalf("expected fallback to %q on broken invariant, got %q",
			defaultChallengeLabel, got)
	}
}

// TestRelativeRecordName_BareApex covers the (cert-manager-never-fires-this)
// path where someone hands us the zone apex itself without the
// `_acme-challenge.` prefix. We return `@` (Hetzner's apex label) so the
// downstream API call is at least well-formed.
func TestRelativeRecordName_BareApex(t *testing.T) {
	got := relativeRecordName("example.com.", "example.com")
	if got != "@" {
		t.Fatalf("expected @ for bare apex, got %q", got)
	}
}
