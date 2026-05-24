<!--
SPDX-License-Identifier: MIT OR Apache-2.0
SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
SPDX-FileContributor: David Koller <david.koller@xmv.de>
-->

# App concept — `cert-manager-webhook-hcloud-zones`

> A [cert-manager](https://cert-manager.io) DNS-01 challenge solver for the **new** Hetzner Cloud Zones API (`https://api.hetzner.cloud/v1/zones`), supporting multiple Hetzner Cloud projects from a single deployment.

This document is the architecture + scope + test-strategy spec for the project. Read this before changing anything that touches the public surface (tool API, configuration shape, RBAC, container image, Helm chart).

The original [`REQUIREMENTS.md`](REQUIREMENTS.md) brief in `docs/` is preserved for historical context.

---

## 1. Motivation — why this project exists

### 1.1 Hetzner's DNS-product migration

Hetzner historically operated two products that both managed DNS records, with separate APIs and separate authentication tokens:

| Product | Authority | Auth header | Status |
|---|---|---|---|
| **Hetzner DNS Console** (legacy) | `https://dns.hetzner.com/api/v1` | `Auth-API-Token: <token>` | being wound down — no new zones |
| **Hetzner Cloud Zones** (current) | `https://api.hetzner.cloud/v1/zones` | `Authorization: Bearer <token>` | only place new zones can be created |

The two products do **not** share authentication: a token issued in the Hetzner Cloud Console cannot read or modify zones in the legacy DNS Console, and vice versa. The wire format also differs (the new API is part of Hetzner's standard Cloud REST surface; the legacy DNS Console was a separate product).

### 1.2 No existing cert-manager webhook targets the new API

A GitHub-wide search (mid-2026) for `cert-manager-webhook` projects that hit `api.hetzner.cloud/v1/zones` returns zero results. Every maintained variant (`vadimkim/`, `hetzner/`, `deyaeddin/`, `S-Bohn/`, `kadras-io/`) points at `dns.hetzner.com/api/v1` and uses `Auth-API-Token`.

Customers whose zones now live in the Cloud product therefore **cannot use DNS-01 challenges** with cert-manager today. They fall back to HTTP-01, which:

- requires the host to publicly resolve to the cluster ingress *before* a certificate can be issued (chicken-and-egg when staging a new domain for cutover);
- cannot issue wildcard certificates (`*.example.com`);
- requires the cluster's HTTP-80 ingress to be reachable from the public internet at certificate-issue time.

This project fills that gap.

### 1.3 What makes it different from a "drop-in port of the legacy webhook"

XMV — and every operator we've talked to — runs DNS zones spread across **multiple Hetzner Cloud projects**, each with its own API token. None of the existing legacy webhooks support that natively; they assume one token per `Issuer`. The N:M token↔zone routing (see § 3) is the load-bearing functional difference.

---

## 2. MVP scope

The first published release (`v0.1.0`) is the minimum viable, production-grade DNS-01 webhook:

### In scope

- **DNS-01 webhook** that implements the cert-manager webhook interface (`Present`, `CleanUp`).
- **Multi-token N:M routing**: configurable mapping of DNS-zone-suffix → `Secret`-ref holding the hcloud token for the project that owns the zone (see § 3).
- **Helm chart** following cert-manager's webhook convention (CRD-style `APIService`, `Deployment`, RBAC, ServiceAccount).
- **Multi-arch container image** (`linux/amd64` + `linux/arm64`) on GitHub Container Registry (`ghcr.io/xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones`).
- **End-to-end harness against the real Hetzner Cloud API** with Let's Encrypt staging (see § 5.4).
- **README + Helm-chart values reference + example ClusterIssuer** that get a new operator from zero to a green cert against a Hetzner zone in under 15 minutes.

### Out of scope for v0.1.0 (post-MVP candidates)

- A standalone CLI for issuing certificates without cert-manager.
- Managing application DNS records (A / AAAA / MX / CNAME).
- Provisioning Hetzner Cloud projects, zones, or tokens.
- Supporting the legacy `dns.hetzner.com` API. The existing `vadimkim/cert-manager-webhook-hetzner` already covers that.
- Web UI / dashboard.
- Per-tenant rate-limit budgeting (we follow Hetzner's `Retry-After` header; explicit budgets are a v0.2 candidate if needed).

---

## 3. Token-to-zone routing — the load-bearing design decision

### 3.1 The shape we need to support

| Scope | Example | Routing question |
|---|---|---|
| Single token, single zone | one cluster, one project, `example.com` | trivial — every challenge goes through the one token |
| Single token, multiple zones | one project owns `example.com`, `internal.example.org` | trivial — still one token |
| Multiple tokens, one zone each | project A owns `xmv.de`, project B owns `xmv-cloud.com` (XMV's actual setup) | which token for `_acme-challenge.foo.xmv.de`? |
| Multiple tokens, several zones each | project A owns `xmv.de` + `xmv.de.example`, project B owns `xmv-cloud.com` | same as above, more entries |

### 3.2 Proposed routing model — explicit zone-suffix → credential map

Each operator-defined credential entry declares (a) the `Secret`-ref to read its hcloud token from, and (b) the **list of DNS-zone suffixes** the token is authoritative for. The webhook routes each incoming challenge to the credential whose suffix list contains the longest match against the challenged FQDN.

Example `ClusterIssuer` solver block (shape — exact CRD names finalised at implementation time):

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-hcloud
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: ops@xmv.de
    privateKeySecretRef:
      name: letsencrypt-hcloud-account-key
    solvers:
      - dns01:
          webhook:
            groupName: hcloud-zones.cert-manager.xmv.de
            solverName: hcloud-zones
            config:
              credentials:
                - name: project-a
                  zones:
                    - xmv.de
                    - example.xmv.de
                  apiTokenSecretRef:
                    name: hcloud-token-project-a
                    namespace: cert-manager
                    key: token
                - name: project-b
                  zones:
                    - xmv-cloud.com
                  apiTokenSecretRef:
                    name: hcloud-token-project-b
                    namespace: cert-manager
                    key: token
```

#### Why explicit-map, not autodiscovery

- **Predictable**: the operator can read the config and tell exactly which token will be used for any FQDN. Autodiscovery (query each token at startup → build map) hides this in webhook state.
- **Cheap**: no per-fire API calls just to figure out routing.
- **Survives token rotation**: only the secret content changes; the map stays valid.
- **Fails closed**: if a challenge arrives for a zone not in any credential's list, the webhook errors out loudly rather than guessing.

Autodiscovery is left as a **diagnostic mode** (a `verify-config` Helm-chart hook or a `kubectl exec` debug command) that queries each token and warns if any declared zone isn't actually accessible. Not on the request-serving hot path.

### 3.3 Edge cases the routing must handle correctly

- **Overlapping suffixes** between two credentials (operator misconfiguration). The webhook must reject the configuration at load time, not route silently to one of them.
- **Subdomain delegation** — `xmv.de` is in project A, but `eu.xmv.de` was delegated to a zone in project B. Resolved by longest-match: a challenge for `foo.eu.xmv.de` matches the `eu.xmv.de` entry, not the `xmv.de` entry. Operator must list both suffixes explicitly.
- **Wildcard challenges** (`*.example.com`) — the FQDN passed to the webhook is `example.com` itself (with the `_acme-challenge.` prefix prepended by ACME); the routing logic is identical.
- **Mid-flight token rotation** — the webhook reads the `Secret` at challenge time (or with a bounded TTL cache, see § 6). A rotated `Secret` is picked up without restart.

---

## 4. Configuration & deployment model

### 4.1 Helm chart structure

Standard cert-manager-webhook layout:

```text
charts/cert-manager-webhook-hcloud-zones/
├── Chart.yaml
├── values.yaml
├── templates/
│   ├── deployment.yaml
│   ├── service.yaml
│   ├── apiservice.yaml         # the cert-manager-required APIService
│   ├── rbac.yaml
│   ├── serviceaccount.yaml
│   ├── pki.yaml                # serving certificate for the APIService (signed by cert-manager itself)
│   └── _helpers.tpl
└── README.md
```

`values.yaml` exposes:

- `image.repository` / `image.tag` — pinned to a released container image.
- `groupName` — webhook API group (defaults to `hcloud-zones.cert-manager.xmv.de`; operators override for their own DNS-01 namespace).
- `certManager.namespace` — where cert-manager runs (defaults to `cert-manager`).
- `certManager.serviceAccountName` — defaults to `cert-manager`.
- `replicas` — default 1 (no leader election needed; cert-manager retries on transient failures and the webhook is idempotent per § 6).
- `resources` — sane defaults; documented profile.
- `nodeSelector` / `tolerations` / `affinity` — pass-through.

### 4.2 Operator workflow (target experience)

1. Create one or more Hetzner Cloud projects in the Cloud Console; create the DNS zones; mint one API token per project (read+write on the project).
2. Create one Kubernetes `Secret` per project's token, in the `cert-manager` namespace.
3. `helm install cert-manager-webhook-hcloud-zones xmv/cert-manager-webhook-hcloud-zones`.
4. Author one `ClusterIssuer` (or per-namespace `Issuer`) with the credentials block (see § 3.2).
5. Reference the `ClusterIssuer` from any `Certificate` resource and get a real cert in 30–120 seconds.

Step 1 is unique to Hetzner-side setup and we point at Hetzner docs; steps 2–5 are the responsibility of this project's README and Helm chart values reference.

---

## 5. Test strategy

This section is deliberately the largest part of the concept. Per `ENGINEERING_PRINCIPLES.md` § 5, this is the gate that lets feature work proceed.

### 5.1 Three layers, three audiences

| Layer | Speed | What it verifies | Catches |
|---|---|---|---|
| **Unit** | seconds | Pure logic — routing decisions, suffix-match, config validation, error-mapping, retry/backoff timing, log-redaction | Implementation bugs in code we wrote |
| **Integration** | seconds–minutes | Wire-format contracts against a mock Hetzner API at the HTTP boundary (`httptest`-style server). Webhook talks to mock as if it were real Hetzner | Contract drift between our request shape and what Hetzner expects, **as long as the mock matches reality** |
| **Harness** | minutes–hours | End-to-end against the **real Hetzner Cloud Zones API** plus a real cert-manager plus a real ACME endpoint (Let's Encrypt staging), running on a real kind / k3d cluster | Reality. Eventual-consistency timing. Auth edge cases. Real error responses we didn't predict |

Three layers, three different jobs. Don't conflate them.

### 5.2 Unit tests

Standard Go `testing` + table-driven cases. Coverage targets:

- **Routing**: every row of the matrix in § 3.1 (single-token-single-zone, multi-token-multi-zone, overlapping-rejected, longest-match-wins, delegated-subdomain-routed-correctly, no-match-fails-closed).
- **Config validation**: every spec-field combination, every reject reason, every default value.
- **Retry/backoff**: timing assertions with an injected clock; no real sleeps in tests.
- **Log redaction**: capture log output, assert the token literal never appears at any verbosity level.
- **RBAC manifests**: render the Helm chart, assert the ServiceAccount only requests the minimum permissions enumerated in `REQUIREMENTS.md` § 6.4.

Goal: ≥10 unit tests per non-trivial module (per the auto-memory rule).

### 5.3 Integration tests with real-shape mock

The integration layer mocks Hetzner at the HTTP boundary. The mock **must** be derived from real Hetzner API responses, not from docs alone. The capture step:

1. Mint a throwaway hcloud token + a throwaway zone in a sandbox project.
2. With `curl` + `jq`, hit every endpoint the webhook will call: `GET /v1/zones`, `POST /v1/zones/{id}/rrsets`, `PATCH /v1/zones/{id}/rrsets/{name}/{type}`, `DELETE /v1/zones/{id}/rrsets/{name}/{type}`, plus every error path we know about (401 invalid token, 403 wrong project, 404 zone-not-found, 409 conflict-on-create, 429 rate-limit with `Retry-After` header, 5xx).
3. Save the raw JSON responses + headers verbatim under `tests/fixtures/hetzner-cloud/`.
4. Build the `httptest.Server` mock to replay these fixtures.

Why this matters (lesson learned across XMV's MCP servers): "**capture real responses first**" — docs drift from reality, silent mismatches pass unit tests with hand-rolled mocks but break against the real API. The integration mock is only useful if it's a real-API replay.

#### What integration tests cover

- Happy path: `Present` → record observed at zone → `CleanUp` → record gone.
- Eventual consistency: `Present` returns success only after polling confirms the record is visible on at least one of the zone's NS records (mock simulates a 5–20 s delay).
- Multi-token routing actually hits the right "project's mock server" for each challenge.
- All known error shapes from § 5.3 step 2 map to readable cert-manager `Challenge.status` messages.
- Idempotence: repeated `Present(same_fqdn, same_token)` does not create duplicate records; repeated `CleanUp` is a no-op if the record is gone.
- Token redaction: trace logs from the integration run contain no token literals.

### 5.4 Harness layer (real Hetzner + real cert-manager + Let's Encrypt staging)

The harness gate. **No feature ticket lands without a harness test that exercises the relevant code path against the real Hetzner API.**

#### 5.4.1 Test fixtures the operator (David) provides

- **`HCLOUD_TOKEN_PROJECT_A`** — a hcloud token from a project that owns **one** DNS zone, e.g. `harness.cmwhz-test.xmv.de`. Project A is single-zone for the simple-routing case.
- **`HCLOUD_TOKEN_PROJECT_B`** — a hcloud token from a different project that owns **multiple** DNS zones (e.g. `cmwhz-test-multi.xmv.de` and `cmwhz-test-other.xmv.de`). Project B is multi-zone for the N:M routing case.

Storage: the operator caches both tokens under `~/.cache/cert-manager-webhook-hcloud-zones/` with mode `0600`, outside any git working tree (mirrors the pattern used by every XMV MCP server's harness profile). CI receives the same values via repo secrets `HCLOUD_TOKEN_PROJECT_A` / `HCLOUD_TOKEN_PROJECT_B`.

#### 5.4.2 Dedicated harness subdomains — collision avoidance

All harness-issued ACME challenges are scoped to **subdomain prefixes that no production system uses**:

- Project A zone: `*.harness.cmwhz-test.xmv.de`
- Project B zones: `*.harness.cmwhz-test-multi.xmv.de`, `*.harness.cmwhz-test-other.xmv.de`

Each test run further prefixes a per-run identifier (UTC timestamp + 6-char random suffix) under `*.harness.…`, e.g.

```text
20260524T103401-a3f7b9.harness.cmwhz-test.xmv.de
```

so concurrent harness runs don't trample each other. The cleanup contract: every test must `defer` (Go) / `t.Cleanup()` the deletion of any TXT record it created, AND the harness suite runs a "garbage collect harness leftovers older than 24 h" sweep at the start of each fire as a safety net (in case a prior run crashed before cleanup).

#### 5.4.3 Test cluster

The harness brings up its own [kind](https://kind.sigs.k8s.io) cluster from a pinned config (`tests/harness/kind-config.yaml`) for each run. Bring-up sequence:

1. `kind create cluster --name cmwhz-harness-<run-id>`
2. `helm install cert-manager jetstack/cert-manager --version <pinned>`, wait for `Available` on all three Deployments.
3. `helm install cert-manager-webhook-hcloud-zones ./charts/...` — the chart-under-test, built from the working tree.
4. Apply two `ClusterIssuer` resources using Let's Encrypt **staging** (`https://acme-staging-v02.api.letsencrypt.org/directory`) — never production from the harness, to keep us off staging rate limits and out of LE's prod audit logs.
5. Apply the test-app manifests (see § 5.4.4).
6. Wait up to 5 minutes per test-app for `Certificate.Status.Conditions[?(@.type=='Ready')].status == 'True'`.
7. Assert the issued cert is from the staging CA (`(STAGING) Let's Encrypt`), parse + check SANs against the requested DNS names.
8. Tear down: delete the kind cluster + run the TXT-record GC.

#### 5.4.4 Test apps

Two minimal apps live in the project tree under `tests/harness/test-apps/`:

- **`test-app-zone-a`** — a single Pod with an `Ingress` + a `Certificate` resource requesting `app-a.harness.cmwhz-test.xmv.de`. Validates Project A's single-zone token path.
- **`test-app-zone-b-multi`** — two `Certificate` resources requesting `app-b1.harness.cmwhz-test-multi.xmv.de` and `app-b2.harness.cmwhz-test-other.xmv.de` (different zones, same Project B token). Validates N:M routing where one token covers multiple zones, and verifies the routing logic picks the right zone.

A third optional test scenario `test-app-cross-project` requests `app-cross.harness.cmwhz-test.xmv.de` + `app-cross.harness.cmwhz-test-multi.xmv.de` from a single `Certificate` (SAN list). Validates that one Certificate triggering challenges across two projects' tokens still works end-to-end.

#### 5.4.5 Error-path coverage

Per `ENGINEERING_PRINCIPLES.md` § 5: harness covers **both the sunny path and the key error paths**. At minimum:

- **Wrong token** for a zone (operator misconfigured the routing map) → cert-manager `Challenge.status` surfaces the 403 with a readable message.
- **Zone not in any credential's list** → fail-closed at the webhook, cert-manager retries, the operator sees a clear "no credential matches FQDN x" message.
- **Hetzner rate-limit** during a challenge → the webhook honours `Retry-After`, cert-manager retries, eventually succeeds. Verified via an integration-test fixture for the 429 case (we can't reliably induce real rate-limits in harness).
- **Stale TXT record** from a prior crashed run → the GC sweep removes it; the next `Present` for the same FQDN succeeds rather than failing on "duplicate".

#### 5.4.6 What the harness costs

- 1 kind cluster bring-up: ~30–60 s.
- 1 cert-manager + 1 webhook chart install: ~30–60 s.
- 1 Let's Encrypt staging cert issuance via DNS-01: 30–120 s wall-clock (TXT propagation + LE polling).
- 4–6 test-app cert issuances per harness fire: ~3–8 min total.
- Teardown + GC: ~30 s.

Total per harness fire: **8–15 min**, comfortably inside the per-job budget of either local or CI.

### 5.5 Harness-tests-in-CI: a project-specific decision

Per `ENGINEERING_PRINCIPLES.md` § 5, "harness tests in CI" is a per-project trade-off. For this project the decision is:

- **Always run harness locally** before a non-trivial push. The operator's machine has the two hcloud tokens; the runtime cost is bounded; running them is the fastest feedback loop.
- **Also run harness in CI**, gated on the two repo secrets being present, on the same set of triggers as integration tests (every PR + every push to main). Reasoning: the webhook is a security-adjacent surface (DNS records under our authoritative zones), and a contract drift on the Hetzner side would silently break production cert-issuance otherwise. The cost (~15 min CI wall-time per run + small Hetzner API quota) is justified.
- Harness on CI is **gated, not required** — a community PR from a contributor without access to the secrets sees the harness job skipped with an explanatory message; a maintainer review covers the gap before merge.

This decision is captured as a Decision Record per `ENGINEERING_PRINCIPLES.md` § 16 once the harness job is wired in.

---

## 6. Operational requirements (cross-cutting)

These are the runtime guarantees the implementation must hold:

1. **Idempotent challenge handling** — repeated `Present(fqdn, token)` does not create duplicate records; repeated `CleanUp` does not error if the record is already gone.
2. **Bounded retries with exponential backoff** for every Hetzner API call. Fail with a clear cert-manager-facing error after a documented maximum (default: 6 retries, ~2 minutes total).
3. **Honour `Retry-After`** on `429 Too Many Requests` responses. Don't second-guess the API.
4. **Stateless** — all configuration in `Issuer` / `ClusterIssuer` spec + referenced `Secret`s. No webhook-side persistent state, no leader election needed.
5. **Token caching with bounded TTL** — read each `Secret` at most once per N seconds (default: 30 s). Token rotation is picked up within one TTL; no webhook restart required.
6. **Observability** — one log line per challenge at default verbosity (zone, fqdn, outcome, latency); one log line per Hetzner API call at debug verbosity. Standard Prometheus metrics: challenges_total, challenge_errors_total, api_calls_total, api_errors_total — all labelled by zone, never by token.

---

## 7. Security posture

1. Container runs as non-root with `readOnlyRootFilesystem: true`. Only the cert-manager-required webhook port and an opt-in metrics port are exposed.
2. The Hetzner API token is read from a `Secret` at request time (or cached with the bounded TTL from § 6). Never written to logs, traces, metrics, or error responses returned to cert-manager. Verified by the unit-level log-redaction test (§ 5.2).
3. Token rotation supported without webhook restart (TTL-bounded re-read).
4. RBAC granted by the Helm chart: `get` / `list` / `watch` on the cert-manager Challenge CRDs (in the namespace where cert-manager runs); `get` on the credential `Secret` resources in the namespaces named in `Issuer` / `ClusterIssuer` spec; nothing else.
5. `NetworkPolicy` (opt-in via chart value) restricting egress to `api.hetzner.cloud:443` only.

---

## 8. Publication & distribution plan

The end-user surface is the **Helm chart** (which references the **container image**). Both need to be published somewhere cert-manager users can find them.

### 8.1 Container image

- Built by GitHub Actions on every push to `main` and on every tag.
- Multi-arch (`linux/amd64` + `linux/arm64`) via `buildx`.
- Published to **GitHub Container Registry**: `ghcr.io/xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones:<tag>`.
- Tagged: every release SemVer tag (`v0.1.0`, `v0.2.0`, …), `latest` (points at the most recent release), and `main` (every push to main, for early-adopter testing).
- Signed with `cosign` keyless signing; SBOM (CycloneDX) attached as an attestation.

### 8.2 Helm chart

Two distribution channels, both maintained from the same release pipeline:

1. **OCI artifact on GHCR**: `oci://ghcr.io/xmv-solutions-gmbh/charts/cert-manager-webhook-hcloud-zones`. `helm install` directly: `helm install cert-manager-webhook-hcloud-zones oci://ghcr.io/xmv-solutions-gmbh/charts/cert-manager-webhook-hcloud-zones --version 0.1.0`.
2. **GitHub Pages-hosted chart repo** at `xmv-solutions-gmbh.github.io/cert-manager-webhook-hcloud-zones` for users who prefer the classic `helm repo add` flow. Generated by `helm/chart-releaser-action` on every tag push (the existing pattern in `strapi-helm-charts` is a working template).

### 8.3 ArtifactHub listing

[ArtifactHub](https://artifacthub.io) is the de-facto discovery surface for cert-manager users looking for DNS-01 webhooks. We register the project there once `v0.1.0` ships:

- `artifacthub-repo.yml` in the chart directory with maintainer + security-contact metadata.
- Annotations on the chart (`artifacthub.io/changes`, `artifacthub.io/links`, `artifacthub.io/images`, `artifacthub.io/category: security`).
- Once approved, the chart shows up at `artifacthub.io/packages/helm/...` and is discoverable via `cert-manager + Hetzner` searches.

### 8.4 Hetzner-outreach

Once `v0.1.0` is stable in production at XMV (a few weeks of real-world certificate issuance with no incident):

- File a friendly note to Hetzner Support / community forum announcing the project, with a link.
- Open a PR against `cert-manager`'s own documentation listing of community DNS-01 webhooks adding the Hetzner Cloud Zones entry.
- Submit a talk / blog write-up on the migration pattern.

These are stretch goals; not gating MVP.

---

## 9. Open spike questions (resolved during implementation)

These are unknowns the implementer should investigate early and capture answers in `docs/spikes/` Decision Records:

1. **Hetzner Cloud Zones API eventual-consistency window** — empirically, after `POST /rrsets`, how long before the record is visible from at least one authoritative NS? Affects the polling timeout in `Present`. Measured during the fixture-capture step (§ 5.3).
2. **Exact response shape on `POST /rrsets` when the same record already exists** — 200 with unchanged state? 409 conflict? Defines our idempotence handling.
3. **Token-scope discovery** — can a hcloud token introspect its own permissions (which projects, which zones)? Affects the `verify-config` diagnostic mode in § 3.2.
4. **`groupName` collision risk** — what's the convention for community webhooks' `groupName`? Default to `hcloud-zones.cert-manager.xmv.de`; revisit if the project graduates out of XMV-Solutions-GmbH org.
5. **Helm chart hosting** — confirm `helm/chart-releaser-action` works as advertised for OCI publishing (the `strapi-helm-charts` repo's pipeline is our reference).
6. **cert-manager webhook gRPC vs HTTP** — recent cert-manager versions added an HTTP webhook protocol alongside the legacy gRPC. Decide which to implement (likely both; HTTP is the future).
7. **Language / runtime** — Go is the obvious default (cert-manager itself is Go; the reference `webhook-example` is Go; the existing community webhooks are Go). Worth a sanity check that no compelling reason exists to deviate.

---

## 10. Non-goals — recap from REQUIREMENTS.md

Documented in [`REQUIREMENTS.md`](REQUIREMENTS.md) § 8 and § 9; repeated here so the concept stands alone:

- Not a general DNS-record-management tool. Only `_acme-challenge.*` TXT records, only on cert-manager's request.
- Not a provisioning tool for Hetzner Cloud projects / zones / tokens.
- Not a legacy-API webhook. `vadimkim/cert-manager-webhook-hetzner` covers that niche.
- Not a CLI for issuing certs outside cert-manager.

---

## 11. Licence & attribution

- **Dual-licensed** MIT OR Apache-2.0 (XMV OSS standard; matches every other repo in the org).
- Copyright holder: XMV Solutions GmbH.
- SPDX headers per `ENGINEERING_PRINCIPLES.md` § 11.
- No AI attribution in commits, comments, SPDX `SPDX-FileContributor` lines, or release notes (`ENGINEERING_PRINCIPLES.md` § 12).

---

## 12. What to build first (ticket sequence)

Per `ENGINEERING_PRINCIPLES.md` § 5 (required ticket order), the implementation order is:

1. **Repo skeleton** — Go module, basic Makefile, lint + format + unit-test scaffolding.
2. **Hetzner Cloud Zones API client** — minimal HTTP client + the four endpoints we need (`GET /zones`, `POST /rrsets`, `PATCH /rrsets`, `DELETE /rrsets`). With unit tests against the fixture replay from § 5.3.
3. **Harness sandbox setup** — provision the two test zones in two Hetzner Cloud projects, capture the real-API fixtures, write the first harness test that proves end-to-end auth + a TXT-record create/read/delete cycle against the real API. **No feature tickets enter "Doing" before this is green** (per § 5 the harness layer is a hard gate).
4. **Routing + config validation** — the longest-suffix matcher, the config-validation pass that rejects overlapping suffixes, the YAML schema for the Issuer config block.
5. **cert-manager webhook integration** — implement the `Present` / `CleanUp` entry points, with integration tests via the fixture-replay mock.
6. **Helm chart** — Deployment, Service, APIService, RBAC, ServiceAccount, PKI, the values reference doc.
7. **End-to-end harness expansion** — both test-apps, the cross-project test-app, the GC sweep, the kind-cluster bring-up automation.
8. **Release pipeline** — image build + sign + SBOM, chart publishing (OCI + GH Pages), GitHub Release notes.
9. **ArtifactHub registration** — the metadata file + maintainer-flow once `v0.1.0` is tagged.

Each ticket carries an acceptance-criteria line for "harness test added + green" per `ENGINEERING_PRINCIPLES.md` § 5.
