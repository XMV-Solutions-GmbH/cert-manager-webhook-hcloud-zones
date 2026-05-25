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

## How to run the harness

The end-to-end harness lives under [`tests/harness/`](tests/harness/) and validates the **production install path** end-to-end against real Hetzner Cloud Zones, real cert-manager, and Let's Encrypt **staging**. It is a bring-your-own-kubeconfig runner — the harness does not provision a cluster.

See [`docs/app-concept.md` § 5](docs/app-concept.md) for the full Test Strategy; this section is the operator's quick reference.

### Prerequisites

- A running Kubernetes cluster — anything that gives you a kubeconfig works (kind, k3d, k3s, a managed cluster, your own bare metal). The harness only consumes `KUBECONFIG`; it never mutates the file.
- `kubectl`, `helm`, `envsubst`, `openssl` on `PATH`.
- The `cert-manager-webhook-hcloud-zones` Helm chart **published to GHCR** at the version you want to test. Until `v0.1.0` ships (the project is currently pre-MVP — see the status badge above), `tests/harness/run.sh` will fail at the Helm install step with a clear "chart not yet published" diagnostic. Once the chart releases, the harness is runnable end-to-end.
- Two Hetzner Cloud API tokens, each with read-write access to **DNS zones** in its respective Cloud project, plus three DNS zones you are willing to have the harness write `_acme-challenge` TXT records under.

### Required environment variables

| Variable | Purpose |
|---|---|
| `HARNESS_KUBECONFIG` | Path to the kubeconfig pointing at the target cluster. |
| `HCLOUD_TOKEN_PROJECT_A` | Hetzner Cloud API token for project A. Must have read-write on the zone set below. |
| `HCLOUD_TOKEN_PROJECT_B` | Hetzner Cloud API token for project B. Must have read-write on both project-B zones below. |
| `HARNESS_ZONE_A` | Apex of a DNS zone in project A (e.g. `zone-a.example.com` — no leading dot, no trailing dot). |
| `HARNESS_ZONE_B1` | Apex of the first DNS zone in project B. |
| `HARNESS_ZONE_B2` | Apex of the second DNS zone in project B (exercises the "one token, multiple zones" path). |

### Invocation

```bash
./tests/harness/run.sh             # leave artefacts in place (recommended default)
./tests/harness/run.sh --cleanup   # delete per-fire test resources on success
```

Exit codes:

- **0** — all three Certificates reached `Ready=True` and every post-Ready assertion (issuer is LE staging, SANs match the expected FQDN, `tls.key` parses) passed.
- **1** — setup failure (missing env var, missing tooling, unreachable cluster, Helm install failed, manifest apply failed).
- **2** — assertion failure (a Certificate never reached `Ready=True`, or one of the issuer / SAN / Secret assertions failed).

### Key principle — failed runs intentionally leave the cluster as-is

The harness validates the **production install path** (the chart pulled from GHCR as an OCI artifact, pinned to a release version) and treats **cluster state as debugging context**. On any assertion failure the script returns non-zero and leaves every resource in place — half-issued `Certificate` objects, failing `Challenge` events, partial `Secret`s, cert-manager logs, the webhook's logs — so the operator can investigate without first reconstructing the failure.

`--cleanup` is opt-in and **never overrides a failing state**: the flag is honoured only when every assertion has passed. See [`docs/app-concept.md` § 5.4.7](docs/app-concept.md) for the full rationale.

### Long-lived state between fires

The two Hetzner-token Secrets `hcloud-token-project-a` and `hcloud-token-project-b` are applied to the `cert-manager` namespace and **persist between harness runs** — cert-manager + the webhook chart + these token Secrets are treated as cluster warmth, not per-fire test state, and `--cleanup` does not remove them. Operators wanting a fully fresh cluster should delete them manually:

```bash
kubectl -n cert-manager delete secret hcloud-token-project-a hcloud-token-project-b
```

### Notes

- The ClusterIssuer in `tests/harness/test-apps/cluster-issuer.yaml` uses `groupName: acme.example.com` as a placeholder matching the chart's pre-release default. The authoritative default will be confirmed when `v0.1.0` ships; until then, override the manifest (or pass `--set groupName=...` to Helm) if you have installed the webhook with a different group name.
- The harness issues against **Let's Encrypt staging only**. Production endpoints are intentionally never referenced — see `docs/app-concept.md` § 5.4.7.

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
