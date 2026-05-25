// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

// Package hcloud implements a thin HTTP client for the Hetzner Cloud
// Zones API (https://api.hetzner.cloud/v1/zones).
//
// Only the four endpoints the cert-manager DNS-01 webhook needs are
// covered:
//
//   - GET    /v1/zones                                  — list zones
//   - POST   /v1/zones/{id}/rrsets                      — create an RRSet
//   - PATCH  /v1/zones/{id}/rrsets/{name}/{type}        — update an RRSet
//   - DELETE /v1/zones/{id}/rrsets/{name}/{type}        — delete an RRSet
//
// Authentication uses the new Hetzner Cloud convention of
// "Authorization: Bearer <token>". This client is intentionally
// decoupled from cert-manager packages and from the higher-level
// routing / solver layers; it is a pure transport.
//
// See docs/app-concept.md sections 5.3 and 6 for the operational
// guarantees this client implements (bounded retries with exponential
// backoff, Retry-After honouring, token redaction in all log paths).
package hcloud
