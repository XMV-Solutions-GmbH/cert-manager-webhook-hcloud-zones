<!-- SPDX-License-Identifier: MIT OR Apache-2.0 -->
# cert-manager-webhook-hcloud-zones

[![Licence](https://img.shields.io/badge/licence-MIT%20OR%20Apache--2.0-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-pre--MVP-orange.svg)](docs/app-concept.md)

A [cert-manager](https://cert-manager.io) DNS-01 challenge solver for the **new** Hetzner Cloud Zones API (`https://api.hetzner.cloud/v1/zones`), with first-class support for multiple Hetzner Cloud projects from a single deployment.

> **Project status: pre-MVP.** The app concept is under review. No release yet, no container image yet, no Helm chart yet. Watch this space (or [Issues](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/issues)) for `v0.1.0`.

---

## Why this exists

Hetzner has migrated DNS-zone ownership from its legacy DNS Console (`dns.hetzner.com`) to the Hetzner Cloud Zones API (`api.hetzner.cloud/v1/zones`). The two products use **different authentication tokens** and **different wire formats**, and a token issued in one cannot manage zones in the other.

Every existing cert-manager DNS-01 webhook for Hetzner targets the legacy API. Customers whose zones now live in the Cloud product therefore cannot use DNS-01 challenges — they fall back to HTTP-01, which can't issue wildcards and can't issue certs before the cluster's ingress is publicly resolvable.

This webhook fills that gap, and adds the routing model needed for operators who keep zones spread across **multiple Hetzner Cloud projects** (the common XMV setup, and the load-bearing functional difference from the legacy webhooks).

For the full motivation, scope, and architecture see [`docs/app-concept.md`](docs/app-concept.md). For the original brief that kicked the project off see [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md).

---

## Roadmap to v0.1.0

The ticket sequence is in [`docs/app-concept.md` § 12](docs/app-concept.md). Top-level milestones:

1. App concept reviewed + approved (this PR).
2. Hetzner Cloud Zones API client with captured-real-response fixtures.
3. Harness setup against real Hetzner + Let's Encrypt staging.
4. Routing + config validation.
5. cert-manager webhook integration.
6. Helm chart + release pipeline (GHCR image + OCI chart + GH Pages).
7. ArtifactHub registration.

---

## Documentation

| Document | Purpose |
|---|---|
| [`docs/app-concept.md`](docs/app-concept.md) | Architecture, scope, **test strategy** — the design spec |
| [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) | Original requirements brief (historical reference) |
| [`ENGINEERING_PRINCIPLES.md`](ENGINEERING_PRINCIPLES.md) | Cross-project engineering baseline (XMV OSS standard) |
| [`AGENTS.md`](AGENTS.md) | Canonical AI-agent brief for contributors using Codex / Claude Code / Copilot |
| [`SECURITY.md`](SECURITY.md) | Vulnerability reporting policy |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Contribution guide |

---

## Licence

Dual-licensed under:

- Apache License, Version 2.0 ([`LICENSE-APACHE`](LICENSE-APACHE) or <http://www.apache.org/licenses/LICENSE-2.0>)
- MIT licence ([`LICENSE-MIT`](LICENSE-MIT) or <http://opensource.org/licenses/MIT>)

at your option.

Unless you explicitly state otherwise, any contribution intentionally submitted for inclusion in this project, as defined in the Apache-2.0 licence, shall be dual licensed as above, without any additional terms or conditions.

---

## Contact

- **Organisation**: XMV Solutions GmbH
- **Email**: <oss@xmv.de>
- **Website**: <https://xmv.de/en/oss/>
- **GitHub**: [@XMV-Solutions-GmbH](https://github.com/XMV-Solutions-GmbH)
