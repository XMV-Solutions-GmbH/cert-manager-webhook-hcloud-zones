<!-- SPDX-License-Identifier: MIT OR Apache-2.0 -->
# cert-manager-webhook-hcloud-zones

[![Licence](https://img.shields.io/badge/licence-MIT%20OR%20Apache--2.0-blue.svg)](LICENSE)
[![Status](https://img.shields.io/badge/status-v0.1.4%20%E2%80%94%20DNS--01%20LE--staging%20verified-brightgreen.svg)](docs/app-concept.md)
[![Image](https://img.shields.io/badge/ghcr.io-0.1.4-blue.svg)](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/pkgs/container/cert-manager-webhook-hcloud-zones)
[![Helm chart](https://img.shields.io/badge/helm%20chart-0.1.4-blue.svg)](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/pkgs/container/charts%2Fcert-manager-webhook-hcloud-zones)
[![Coverage](https://img.shields.io/badge/coverage-82.2%25-brightgreen.svg)](#test-coverage)
[![Signed](https://img.shields.io/badge/cosign-keyless%20signed-success.svg)](#verifying-release-artefacts)
[![SBOM](https://img.shields.io/badge/SBOM-SPDX--JSON%20attested-success.svg)](#verifying-release-artefacts)

A [cert-manager](https://cert-manager.io) DNS-01 challenge solver for the **new** Hetzner Cloud Zones API (`https://api.hetzner.cloud/v1/zones`), with first-class support for multiple Hetzner Cloud projects from a single deployment.

> **Status: v0.1.4** — released to GHCR, end-to-end verified against Let's Encrypt **staging** across three certificates spanning two Hetzner Cloud projects. Production-issuance is unchanged from staging from the webhook's perspective; operators choosing to point an Issuer at the LE production endpoint do so at their own risk and rate-limit.

---

## Why this exists

Hetzner has migrated DNS-zone ownership from its legacy DNS Console (`dns.hetzner.com`) to the Hetzner Cloud Zones API (`api.hetzner.cloud/v1/zones`). The two products use **different authentication tokens** and **different wire formats**, and a token issued in one cannot manage zones in the other.

Every existing cert-manager DNS-01 webhook for Hetzner targets the legacy API. Customers whose zones now live in the Cloud product therefore cannot use DNS-01 challenges — they fall back to HTTP-01, which can't issue wildcards and can't issue certs before the cluster's ingress is publicly resolvable.

This webhook fills that gap.

---

## What's different from existing webhooks

**N:1 routing.** A single deployment of this webhook can serve **multiple Hetzner Cloud projects** from one cert-manager installation, by mapping each zone-apex to the token authoritative for it. The configuration is a list of `{name, zones, apiTokenSecretRef, namespace}` credentials; at challenge time the webhook walks the FQDN up to its registered zone-apex and picks the credential whose `zones` list contains that apex.

This is the load-bearing functional difference from the legacy-API webhooks: operators who keep zones spread across several Hetzner Cloud projects (the common XMV setup) can issue certificates for all of them from one Issuer without per-project webhook installs or per-project Issuer fan-out.

For the full design rationale see [`docs/app-concept.md` § 3](docs/app-concept.md).

---

## Quick install

### 1. Install cert-manager itself (if you haven't already)

```bash
helm repo add jetstack https://charts.jetstack.io
helm repo update
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager --create-namespace \
  --version v1.20.2 \
  --set installCRDs=true
```

### 2. Install this webhook from GHCR (OCI Helm chart)

```bash
helm upgrade --install cert-manager-webhook-hcloud-zones \
  oci://ghcr.io/xmv-solutions-gmbh/charts/cert-manager-webhook-hcloud-zones \
  --version 0.1.4 \
  --namespace cert-manager
```

This installs the multi-arch (`linux/amd64` + `linux/arm64`) image `ghcr.io/xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones:0.1.4`. The chart defaults `groupName` to `acme.hcloud-zones.xmv.de` and `solverName` to `hcloud-zones`; every Issuer below must match those values.

### 3. Create the Hetzner Cloud API-token Secret(s)

One Secret per Hetzner Cloud project. Tokens need read-write access to **DNS Zones** in that project.

```bash
kubectl -n cert-manager create secret generic hcloud-token-project-a \
  --from-literal=token='<hetzner-cloud-api-token-for-project-a>'
```

### 4. Create a `ClusterIssuer` (or `Issuer`)

> **Important — config schema.** The webhook's configuration lives under `webhook.config.credentials`, **a list of `{name, zones, apiTokenSecretRef, namespace}` entries**. There is no top-level `apiTokenSecretRef`; an outer `selector.dnsZones` is not needed and would in fact prevent cross-zone routing from a single Issuer. The canonical example is [`tests/harness/test-apps/cluster-issuer.yaml`](tests/harness/test-apps/cluster-issuer.yaml).

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-staging
spec:
  acme:
    server: https://acme-staging-v02.api.letsencrypt.org/directory
    email: you@example.com
    privateKeySecretRef:
      name: letsencrypt-staging-account-key
    solvers:
      - dns01:
          webhook:
            groupName: acme.hcloud-zones.xmv.de
            solverName: hcloud-zones
            config:
              credentials:
                - name: project-a
                  zones:
                    - example.com
                  apiTokenSecretRef:
                    name: hcloud-token-project-a
                    key: token
                  namespace: cert-manager
                # Add further entries for further Hetzner Cloud projects.
                # A single credential can list multiple zones if one token
                # is authoritative for several zones in the same project.
                # - name: project-b
                #   zones:
                #     - other.example.com
                #     - third.example.com
                #   apiTokenSecretRef:
                #     name: hcloud-token-project-b
                #     key: token
                #   namespace: cert-manager
```

Then issue a `Certificate` referencing the Issuer in the normal way.

---

## Caveat — Hetzner-Robot-DNS-hosted zones with a wildcard CNAME

If your Hetzner Cloud DNS zone's registry delegation actually points at **Hetzner Robot DNS** nameservers (`ns1.first-ns.de`, `robotns2.second-ns.de`, `robotns3.second-ns.com`) rather than the Cloud-DNS nameservers (`hydrogen.ns.hetzner.com`, …), **and** the zone contains a wildcard `*.<zone> CNAME …` record, cert-manager's default authoritative-nameserver DNS-01 self-check hangs in an infinite "not yet propagated" loop — even though every public recursive resolver returns the TXT record correctly.

The fix is to switch cert-manager's self-check to recursive-only resolution against public resolvers. With the cert-manager Helm chart, set `extraArgs` on the controller:

```bash
helm upgrade --install cert-manager jetstack/cert-manager \
  --namespace cert-manager --create-namespace \
  --version v1.20.2 \
  --set installCRDs=true \
  --set "extraArgs={--dns01-recursive-nameservers-only=true,--dns01-recursive-nameservers=1.1.1.1:53\,9.9.9.9:53}"
```

(The backslash escapes the comma so Helm passes a single multi-value argument string through to cert-manager.)

If your zone is delegated to Hetzner Cloud nameservers directly, or you don't run wildcard CNAMEs, the default authoritative self-check works fine and you can ignore this section. The harness exercises this exact pathology, which is why it ships with the recursive-only flag enabled (see [`tests/harness/run.sh`](tests/harness/run.sh)).

---

## Test coverage

Overall: **82.2%** statement coverage across the production packages.

| Package | Coverage | What it covers |
|---|---|---|
| `internal/routing` | 97.7% | Zone-apex registration and FQDN→credential lookup. Tested near-exhaustively because the bug class here is "silently misroute to the wrong project". |
| `internal/hcloud` | 89.2% | Hetzner Cloud Zones API client. Mocks are derived from real captured server responses, with edge-case coverage for malformed payloads and HTTP error shapes. |
| `internal/solver` | 71.2% | cert-manager `webhook.Solver` implementation (`Present` / `CleanUp`), zone cache, config parsing. The remaining surface is the cobra-command path inherited from the cert-manager webhook framework, which has no meaningful unit-test seam. |
| `cmd/cert-manager-webhook-hcloud-zones` | 0% | `main.go` — three lines of glue around `cmd.RunWebhookServer`. Intentionally exercised by the harness, not by unit tests. |

The end-to-end harness layer (`tests/harness/run.sh`) covers the rest: real cluster, real cert-manager, real Hetzner Cloud Zones API, real Let's Encrypt **staging**. See [`docs/testconcept.md`](docs/testconcept.md) for the three-layer test strategy (unit / integration / harness).

---

## Documentation

| Document | Purpose |
|---|---|
| [`docs/app-concept.md`](docs/app-concept.md) | Architecture, scope, public surface — the design spec |
| [`docs/testconcept.md`](docs/testconcept.md) | Per-project instantiation of the unit / integration / harness layers |
| [`CHANGELOG.md`](CHANGELOG.md) | Keep-a-changelog history |
| [`ENGINEERING_PRINCIPLES.md`](ENGINEERING_PRINCIPLES.md) | Cross-project engineering baseline (XMV OSS standard) |
| [`AGENTS.md`](AGENTS.md) | Canonical AI-agent brief for contributors using Codex / Claude Code / Copilot |
| [`SECURITY.md`](SECURITY.md) | Vulnerability reporting policy |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Contribution guide |
| [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) | Original requirements brief (historical reference) |

---

## How to run the harness

The end-to-end harness lives under [`tests/harness/`](tests/harness/) and validates the **production install path** end-to-end against real Hetzner Cloud Zones, real cert-manager, and Let's Encrypt **staging**. It is a bring-your-own-kubeconfig runner — the harness does not provision a cluster.

See [`docs/testconcept.md`](docs/testconcept.md) for the full Test Strategy; this section is the operator's quick reference.

### Prerequisites

- A running Kubernetes cluster — anything that gives you a kubeconfig works (kind, k3d, k3s, a managed cluster, your own bare metal). The harness only consumes `KUBECONFIG`; it never mutates the file.
- `kubectl`, `helm`, `envsubst`, `openssl` on `PATH`.
- The published Helm chart at `oci://ghcr.io/xmv-solutions-gmbh/charts/cert-manager-webhook-hcloud-zones` (pulled automatically at the version pinned in [`tests/harness/run.sh`](tests/harness/run.sh)).
- Two Hetzner Cloud API tokens, each with read-write access to **DNS Zones** in its respective Cloud project, plus three DNS zones you are willing to have the harness write `_acme-challenge` TXT records under.

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
./tests/harness/run.sh --cleanup   # delete per-fire test resources on a fully-green run
```

`--cleanup` is opt-in and is honoured **only when every assertion has passed** — on any failure the flag is silently ignored. The intent is that a failed run leaves the cluster in its failure state so the operator can inspect events, logs, and partial Secrets without first reconstructing the failure. Even in the cleanup path, cert-manager, the webhook chart, and the two `hcloud-token-project-{a,b}` Secrets are deliberately retained as cluster warmth for subsequent fires.

### Exit codes

- **0** — all three Certificates reached `Ready=True` and every post-Ready assertion (issuer is LE staging, SANs match the expected per-fire FQDN, `tls.key` parses) passed.
- **1** — setup failure (missing env var, missing tooling, unreachable cluster, Helm install failed, manifest apply failed).
- **2** — assertion failure (a Certificate never reached `Ready=True`, or one of the issuer / SAN / Secret assertions failed).

### Notes

- The ClusterIssuer in `tests/harness/test-apps/cluster-issuer.yaml` uses `groupName: acme.hcloud-zones.xmv.de`, matching the chart default.
- The harness issues against **Let's Encrypt staging only**. Production endpoints are intentionally never referenced.

---

## Verifying release artefacts

The release pipeline ([`/.github/workflows/release.yml`](.github/workflows/release.yml)) signs each image with **cosign keyless** (Sigstore + GitHub OIDC) and attests an **SPDX-JSON SBOM** to the image as a DSSE-enveloped OCI referrer.

Verify the v0.1.4 image signature:

```bash
cosign verify \
  --certificate-identity-regexp '^https://github\.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/\.github/workflows/release\.yml@refs/tags/v0\.1\.4$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones:0.1.4
```

Verify the SBOM attestation:

```bash
cosign verify-attestation \
  --type spdxjson \
  --certificate-identity-regexp '^https://github\.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/\.github/workflows/release\.yml@refs/tags/v0\.1\.4$' \
  --certificate-oidc-issuer https://token.actions.githubusercontent.com \
  ghcr.io/xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones:0.1.4
```

Both commands exit `0` on a verified signature. The SBOM payload is the DSSE-enveloped predicate inside the verify-attestation output; pipe through `jq -r '.payload | @base64d | fromjson | .predicate'` to extract it.

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
