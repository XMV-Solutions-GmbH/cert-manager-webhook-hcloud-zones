// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

// Package solver implements the cert-manager DNS-01 challenge solver glue —
// the bridge between cert-manager's webhook.Solver interface and the
// project-internal building blocks:
//
//   - internal/routing — pure zone-apex → credential routing layer.
//   - internal/hcloud  — Hetzner Cloud Zones REST client.
//
// The solver is intentionally thin: it parses the per-issuer JSON config
// block, resolves the challenge FQDN to a credential, fetches the hcloud
// API token from the referenced Kubernetes Secret, looks up the Hetzner zone
// ID (with a small bounded-TTL cache), then asks the hcloud client to
// create or delete the `_acme-challenge` TXT RRSet.
//
// Per docs/app-concept.md §§ 6–7 the solver is:
//
//   - Stateless apart from a bounded TTL caches (zone-ID, token).
//   - Idempotent — repeated Present is a no-op or an Update; repeated
//     CleanUp treats "already gone" as success.
//   - Token-redacting in every log line it emits.
//
// Construction is via New; the resulting *Solver satisfies
// github.com/cert-manager/cert-manager/pkg/acme/webhook.Solver and is the
// only public surface needed by cmd/cert-manager-webhook-hcloud-zones (P2-5).
package solver
