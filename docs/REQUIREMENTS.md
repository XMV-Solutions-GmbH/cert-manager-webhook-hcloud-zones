<!--
SPDX-License-Identifier: MIT OR Apache-2.0
SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
SPDX-FileContributor: David Koller <david.koller@xmv.de>

Original requirements brief written before the project was spun out to
its own repository. Preserved here for historical reference; the
current authoritative scope lives in `app-concept.md` and `../README.md`.
-->

# cert-manager-webhook-hcloud-zones — requirements

A [cert-manager](https://cert-manager.io) DNS-01 challenge solver for the **new Hetzner Cloud DNS API** (`https://api.hetzner.cloud/v1/zones`), the successor of the legacy "Hetzner DNS Console" product (`https://dns.hetzner.com/api/v1`).

This document captures requirements only. **No implementation choices, no API contracts, no code structure** — those are for the implementer.

## 1. Why this is needed

### 1.1 The Hetzner DNS landscape changed

Hetzner historically operated two products that both managed DNS records, with separate APIs and separate authentication:

| Product | Authority | Auth header | Used by all existing OSS tooling | Future of the product |
|---|---|---|---|---|
| **Hetzner DNS Console** (legacy) | `https://dns.hetzner.com/api/v1` | `Auth-API-Token: <token>` | yes — every `cert-manager-webhook-hetzner` variant on GitHub targets this | being wound down |
| **Hetzner Cloud Console** (current) | `https://api.hetzner.cloud/v1/zones` | `Authorization: Bearer <token>` | no — no public cert-manager webhook implementation found | the surviving product |

At the time of writing (mid-2026), Hetzner's new DNS-on-Cloud product is the only place where new DNS zones can be created. Customers with existing zones in the legacy product can migrate them across; new zones cannot be created in the legacy product any more. The two products do **not** share authentication: a token issued in the Hetzner Cloud Console cannot read or modify zones in the legacy DNS Console, and vice versa.

### 1.2 No existing webhook covers the new API

A GitHub-wide search for `cert-manager-webhook` projects that target `api.hetzner.cloud/v1/zones` returns zero results as of 2026-05. All maintained variants (`vadimkim/`, `hetzner/`, `deyaeddin/`, `S-Bohn/`, `kadras-io/`) point at `dns.hetzner.com/api/v1` and use `Auth-API-Token`. Customers whose zones live in the new Cloud product therefore cannot use DNS-01 challenges with cert-manager today — they are forced to fall back to HTTP-01, which:

- requires the host to publicly resolve to the cluster ingress before a certificate can be issued (chicken-and-egg when staging a domain for cutover);
- cannot issue wildcard certificates (`*.example.com`);
- requires the cluster's HTTP-80 ingress to be reachable from the public internet at certificate-issue time.

A small, focused OSS project that fills this gap would be useful to the wider community.

## 2. Functional requirements

The webhook **must** implement cert-manager's DNS-01 webhook interface as published in `github.com/cert-manager/cert-manager`. Specifically:

1. **Present** a TXT record at `_acme-challenge.<fqdn>` with the challenge token supplied by ACME, on the correct authoritative zone, using the new Hetzner Cloud Zones API.
2. **Clean up** that TXT record after the ACME server has validated the challenge (or after a configured timeout).
3. Tolerate the API's eventual consistency: poll until the record is observed at the zone's authoritative name servers before signalling success back to cert-manager.

## 3. Authentication requirements

This is the requirement that distinguishes this webhook from any existing one.

XMV operates several DNS zones across **separate Hetzner Cloud projects**, each with its own API token. The webhook must support all three of the following scopes simultaneously, without per-zone redeployment:

| Scope | Example | Must work |
|---|---|---|
| Single token, single zone | one cluster, one project, one zone (`example.com`) | yes |
| Single token, multiple zones | one cluster, one project, multiple zones (`example.com`, `internal.example.org`) | yes |
| Multiple tokens, one zone each | one cluster, multiple projects, one zone per project (`xmv.de` in project A, `xmv-cloud.com` in project B) — XMV's actual setup | **yes — this is the primary use case** |
| Multiple tokens, several zones each | one cluster, multiple projects, several zones in some projects | yes |

The webhook must therefore allow the operator to **register multiple credentials and route each ACME challenge to the credential that owns the relevant zone**. The matching mechanism (suffix-based, label-based, explicit map, etc.) is left to the implementation; the only hard requirement is that the operator can express the mapping declaratively in the cert-manager `Issuer` / `ClusterIssuer` resource (or a companion CRD), with no per-domain redeployment.

Credentials must be supplied via Kubernetes `Secret` resources referenced from the `Issuer` / `ClusterIssuer` configuration. The plain-text token must not appear in `Issuer` spec fields, nor in webhook logs at any verbosity level.

## 4. Multi-tenancy requirements

It is normal that a single Hetzner Cloud DNS zone is shared across several consumers — for example, multiple Kubernetes clusters each request certificates for hosts under the same zone. The webhook must remain correct in this scenario:

- Each `_acme-challenge.<fqdn>` TXT record is unique per challenged hostname; two clusters challenging the same hostname concurrently is an operator misconfiguration (one host can be served by only one cluster's ingress at a time), but two clusters challenging different hostnames under the same zone is the common case and must work without races.
- A clean-up of one cluster's TXT record must not delete another cluster's record at a different name under the same zone.

## 5. Operational requirements

1. **Single container image, multi-architecture** — at minimum `linux/amd64` and `linux/arm64` so the webhook runs unmodified on ARM-based clusters.
2. **Distributed as a Helm chart** that follows cert-manager's webhook conventions: a CRD-style API service, RBAC, the Deployment, and the cert-manager-required `APIService` registration. The chart values surface the credential-Secret references and the zone↔token mapping.
3. **Idempotent challenge handling** — repeated `Present` for the same `(fqdn, token)` must not create duplicate records; repeated `CleanUp` must not error if the record is already gone.
4. **Bounded retries with exponential backoff** for Hetzner API calls; fail loudly after a documented maximum so cert-manager can surface a clear error.
5. **Observability**: per-challenge log line (zone, fqdn, outcome, latency) at default verbosity; per-API-call log line at debug verbosity.
6. **No state outside the cluster**. The webhook is stateless; all required configuration is in the `Issuer`/`ClusterIssuer` spec and referenced `Secret` resources.

## 6. Security requirements

1. The container runs as a non-root user with a read-only root filesystem; only the cert-manager-required ports are exposed.
2. The Hetzner API token is read from a Kubernetes `Secret` at request time (or cached with a bounded TTL). It is never written to logs, traces, metrics, or error responses returned to cert-manager.
3. Token rotation is supported without webhook restart — updating the underlying `Secret` is sufficient.
4. RBAC in the chart grants the webhook ServiceAccount the minimal permissions needed: read on the cert-manager Challenge CRDs, read on the credential `Secret` resources in the configured namespace(s); nothing else.

## 7. Compatibility requirements

1. Compatible with the current `cert-manager` stable channel (one minor-version window before / after).
2. Compatible with `letsencrypt-prod` and `letsencrypt-staging` ACME servers; also expected to work with any ACME-v02-compliant CA that supports DNS-01.
3. No assumption that the cluster has internet egress to `dns.hetzner.com` — only `api.hetzner.cloud` is contacted.

## 8. Non-requirements (explicitly out of scope)

- Managing application DNS records (A, AAAA, MX, CNAME). The webhook only manipulates TXT records under the `_acme-challenge.*` prefix, and only at cert-manager's request.
- Provisioning Hetzner Cloud projects, zones, or tokens. The operator is expected to have these in place.
- Supporting the legacy `dns.hetzner.com` API. That is the niche the existing `vadimkim/cert-manager-webhook-hetzner` already covers; this project's reason to exist is the gap on the new API.
- A standalone CLI for issuing certificates outside cert-manager. cert-manager is the only required client.

## 9. Reference material

For implementers:

- cert-manager webhook reference implementation: `github.com/cert-manager/webhook-example`
- cert-manager DNS-01 design notes: `cert-manager.io/docs/configuration/acme/dns01/`
- Hetzner Cloud Zones API: `docs.hetzner.cloud/reference/cloud#zones`
- Hetzner Cloud Zones authentication: `docs.hetzner.cloud/reference/cloud#authentication`
- Existing legacy-API webhook (for shape comparison, not implementation): `github.com/vadimkim/cert-manager-webhook-hetzner`

## 10. Notes on the requirements

This brief was written by XMV Solutions GmbH as a precursor to spinning out an OSS project. Once the project moves to its own public repository, this document is superseded by that repository's `README`. Until then, comments / amendments are welcome via the file's git history in this monorepo workspace.
