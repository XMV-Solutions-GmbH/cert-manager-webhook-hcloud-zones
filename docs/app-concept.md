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

Operators routinely run DNS zones spread across **multiple Hetzner Cloud projects**, each with its own API token. None of the existing legacy webhooks support that natively; they assume one token per `Issuer`. The N:1 zone-to-token routing across multiple projects (see § 3) is the load-bearing functional difference.

---

## 2. MVP scope

The first published release (`v0.1.0`) is the minimum viable, production-grade DNS-01 webhook:

### In scope

- **DNS-01 webhook** that implements the cert-manager webhook interface (`Present`, `CleanUp`).
- **Multi-project routing**: configurable mapping of DNS-zone-apex → `Secret`-ref holding the hcloud token for the project that owns the zone (see § 3).
- **Helm chart** following cert-manager's webhook convention (CRD-style `APIService`, `Deployment`, RBAC, ServiceAccount).
- **Multi-arch container image** (`linux/amd64` + `linux/arm64`) on GitHub Container Registry (`ghcr.io/xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones`).
- **End-to-end harness against the real Hetzner Cloud API** with Let's Encrypt staging (see § 5.4).
- **README + Helm-chart values reference + example ClusterIssuer** that get a new operator from zero to a green cert against a Hetzner zone in under 15 minutes.

### Out of scope for v0.1.0 (post-MVP candidates)

- A standalone CLI for issuing certificates without cert-manager.
- Managing application DNS records (A / AAAA / CNAME / MX). Those are managed outside this webhook by an orthogonal layer — operators typically use either (a) a wildcard A-record pointing at the cluster ingress (`*.example.com → <cluster-IP>`, then any sub-name reaches the cluster), (b) [external-dns](https://github.com/kubernetes-sigs/external-dns) reconciling DNS records from Kubernetes resources, or (c) IaC tooling (Terraform / OpenTofu / Pulumi). This webhook only ever touches `_acme-challenge.*` TXT records for the ACME DNS-01 protocol.
- Provisioning Hetzner Cloud projects, zones, or tokens.
- Supporting the legacy `dns.hetzner.com` API. The existing `vadimkim/cert-manager-webhook-hetzner` already covers that.
- Web UI / dashboard.
- Per-tenant rate-limit budgeting (we follow Hetzner's `Retry-After` header; explicit budgets are a v0.2 candidate if needed).

---

## 3. Zone-to-token routing — the load-bearing design decision

### 3.1 The shape we need to support

| Scope | Example | Routing question |
|---|---|---|
| Single token, single zone | one cluster, one project, `example.com` | trivial — every challenge goes through the one token |
| Single token, multiple zones | one project owns `example.com` *and* `example.net` | trivial — still one token; the credential lists both zones |
| Multiple tokens, one zone each | project A owns `example.com`, project B owns `example.org` | which token for `_acme-challenge.foo.example.com`? |
| Multiple tokens, several zones each | project A owns `example.com` + `example.net`, project B owns `example.org` + `example.de` | same as above, more entries |

The webhook does **not** need to handle subdomain delegation between zones (e.g. `example.com` in one project, `eu.example.com` as a delegated subzone in another) — Hetzner Cloud Zones rejects creating a zone whose name is a subdomain of any registrable domain. See the resolved decision in § 3.4.

**An app deploying a `Certificate` for `app.foo.example.com` does NOT need any entry added to the webhook config.** The operator lists each Hetzner-side **zone-apex** (e.g. `example.com`) exactly once; the webhook routes any FQDN under that zone — `app.example.com`, `*.example.com`, `_acme-challenge.deep.nested.example.com` — to the same configured credential. Config in the webhook stays decoupled from per-app routing under the zone.

### 3.2 Proposed routing model — explicit zone-apex → credential map (exact match)

Each operator-defined credential entry declares (a) the `Secret`-ref to read its hcloud token from, and (b) the **list of zone-apex names** the token is authoritative for (the same strings that appear as zone names in the Hetzner Cloud Console). The webhook routes each incoming challenge to the credential whose zone-apex list contains the zone-apex of the challenged FQDN — strict, exact match against the zone name, not against arbitrary suffixes.

Example `ClusterIssuer` solver block (shape — exact CRD names finalised at implementation time):

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt-hcloud
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: ops@example.com
    privateKeySecretRef:
      name: letsencrypt-hcloud-account-key
    solvers:
      - dns01:
          webhook:
            groupName: acme.example.com         # operator-chosen; see § 9 spike
            solverName: hcloud-zones
            config:
              credentials:
                - name: project-a
                  zones:
                    - example.com
                    - example.net
                  apiTokenSecretRef:
                    name: hcloud-token-project-a
                    namespace: cert-manager
                    key: token
                - name: project-b
                  zones:
                    - example.org
                  apiTokenSecretRef:
                    name: hcloud-token-project-b
                    namespace: cert-manager
                    key: token
```

The routing algorithm for an incoming challenge FQDN:

1. Strip the `_acme-challenge.` prefix (cert-manager always prepends it).
2. Walk from the FQDN towards the root, stripping leading labels one at a time, checking each remaining suffix against the configured set of zone-apex names.
3. First exact match wins → use that credential's token.
4. If no configured zone-apex is an exact match for any prefix-stripped suffix of the FQDN → fail-closed, return a clear error to cert-manager identifying the FQDN that didn't match any zone.

Because Hetzner forbids subdomain zones (§ 3.4), there is **at most one** configured zone-apex that can match any given FQDN. The algorithm reduces to "find the suffix that's in the configured set"; no precedence rule needed.

#### Why explicit-map, not autodiscovery

- **Predictable**: the operator can read the config and tell exactly which token will be used for any FQDN. Autodiscovery (query each token at startup → build map) hides this in webhook state.
- **Cheap**: no per-fire API calls just to figure out routing.
- **Survives token rotation**: only the secret content changes; the map stays valid.
- **Fails closed**: if a challenge arrives for a zone not in any credential's list, the webhook errors out loudly rather than guessing.

Autodiscovery is left as a **diagnostic mode** (a `verify-config` Helm-chart hook or a `kubectl exec` debug command) that queries each token and warns if any declared zone isn't actually accessible. Not on the request-serving hot path.

### 3.3 Edge cases the routing must handle correctly

- **Duplicate zone-apex across credentials** (operator misconfiguration — same zone listed under two credentials). The webhook must reject the configuration at load time, not route silently to one of them.
- **Configured zone-apex that's actually a subdomain** (operator wrote e.g. `app.example.com` in the `zones:` list, treating it like a delegated subzone). Defence-in-depth: reject at config-load time with a clear "zone-apex names must be registrable domains; Hetzner does not support subdomain zones — see [link to FAQ]" error. Hetzner would reject the corresponding API call anyway, but failing fast at startup gives the operator a better error.
- **Wildcard challenges** (`*.example.com`) — the FQDN passed to the webhook is `example.com` itself (with the `_acme-challenge.` prefix prepended by ACME); routing is identical to the non-wildcard case.
- **FQDN with no matching zone-apex in any credential** — fail-closed, return an error string that includes the FQDN and the list of configured zones so the operator can diagnose the misconfiguration from `kubectl describe challenge`.
- **Mid-flight token rotation** — the webhook reads the `Secret` at challenge time (or with a bounded TTL cache, see § 6). A rotated `Secret` is picked up without restart.

### 3.4 Decision record — exact-match, not longest-suffix-match

**Decision (resolved during concept review, 2026-05-25)**: the routing algorithm is exact-match on the zone-apex of the incoming FQDN, NOT longest-suffix-match across an arbitrary suffix list.

**Why**: an earlier draft of this concept used longest-suffix-match to support the case where a parent zone (`example.com`) lives in one project and a delegated subzone (`eu.example.com`) lives in a different project. Research confirmed this is impossible at the Hetzner Cloud Zones API level: zone names are validated against the **Public Suffix List** and must be of the form `<label>.<public-suffix>`. The API explicitly rejects zone names that are subdomains of registrable domains, returning `422 invalid TLD`, regardless of who owns the parent zone or whether it exists in Hetzner at all.

Citations:

- Hetzner DNS FAQ — Zones: <https://docs.hetzner.com/networking/dns/faq/zones/> — Q "Why can the domain `<sub.domain.tld>` not be created? (unknown TLD)" — A: **"Subzones are not supported."**
- Ansible `hetzner.hcloud.zone` module documentation (generated from the Cloud API schema): <https://docs.ansible.com/projects/ansible/latest/collections/hetzner/hcloud/zone_module.html> — `name` parameter: **"All names with well-known public suffixes (e.g. .de, .com, .co.uk) are supported"**; **"Subdomains are not supported"**.
- Hetzner DNS migration notes: <https://docs.hetzner.com/networking/dns/migration-to-hetzner-console/features-and-differences/> — confirms new validations and strict naming rules on the Cloud Zones product.

**Implication**: routing logic is simpler (exact set membership lookup instead of suffix-walk-with-precedence), config-validation gains a defence-in-depth check rejecting any configured zone-apex that's itself a subdomain of a public suffix, and the test matrix loses the "longest-match-wins" and "delegated-subdomain-routed-correctly" rows.

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
- `groupName` — webhook API group (operator-chosen FQDN that uniquely identifies this webhook deployment; the published chart default lives under a publisher-owned namespace, see § 9 spike).
- `certManager.namespace` — where cert-manager runs (defaults to `cert-manager`).
- `certManager.serviceAccountName` — defaults to `cert-manager`.
- `replicas` — default 1 (no leader election needed; cert-manager retries on transient failures and the webhook is idempotent per § 6).
- `resources` — sane defaults; documented profile.
- `nodeSelector` / `tolerations` / `affinity` — pass-through.

### 4.2 Operator workflow (target experience)

1. Create one or more Hetzner Cloud projects in the Cloud Console; create the DNS zones; mint one API token per project (read+write on the project).
2. Create one Kubernetes `Secret` per project's token, in the `cert-manager` namespace.
3. `helm install cert-manager-webhook-hcloud-zones xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones`.
4. Author one `ClusterIssuer` (or per-namespace `Issuer`) with the credentials block (see § 3.2).
5. Reference the `ClusterIssuer` from any `Certificate` resource and get a real cert in 30–120 seconds. **App-side `Certificate` resources may use any subdomain under a configured zone — no further webhook-config changes needed.**

Step 1 is unique to Hetzner-side setup and we point at Hetzner docs; steps 2–5 are the responsibility of this project's README and Helm chart values reference.

**Out-of-scope reminder**: the A / AAAA / CNAME records that point app FQDNs at the cluster ingress are managed by a completely separate layer — typically a one-time wildcard A-record (`*.example.com → <cluster-IP>`), [external-dns](https://github.com/kubernetes-sigs/external-dns), or IaC. This webhook does not see, create, or modify any of those records; it only handles `_acme-challenge.*` TXT records for ACME DNS-01. Operators arriving from "the webhook talks to Hetzner DNS, surely it can manage my app records too?" intuition: no, deliberately so (see § 2).

---

## 5. Test strategy

This section is deliberately the largest part of the concept. Per `ENGINEERING_PRINCIPLES.md` § 5, this is the gate that lets feature work proceed.

### 5.1 Three layers, three audiences

| Layer | Speed | What it verifies | Catches |
|---|---|---|---|
| **Unit** | seconds | Pure logic — routing decisions, zone-apex match, config validation, error-mapping, retry/backoff timing, log-redaction | Implementation bugs in code we wrote |
| **Integration** | seconds–minutes | Wire-format contracts against a mock Hetzner API at the HTTP boundary (`httptest`-style server). Webhook talks to mock as if it were real Hetzner | Contract drift between our request shape and what Hetzner expects, **as long as the mock matches reality** |
| **Harness** | minutes–hours | End-to-end against the **real Hetzner Cloud Zones API** plus a real cert-manager plus a real ACME endpoint (Let's Encrypt staging), running on a real kind / k3d cluster | Reality. Eventual-consistency timing. Auth edge cases. Real error responses we didn't predict |

Three layers, three different jobs. Don't conflate them.

### 5.2 Unit tests

Standard Go `testing` + table-driven cases. Coverage targets:

- **Routing**: every row of the matrix in § 3.1 (single-token-single-zone, single-token-multi-zone, multi-token-one-zone-each, multi-token-multi-zone-each, exact-match-on-zone-apex, no-match-fails-closed-with-readable-error, FQDN-prefix-stripping-and-walk).
- **Config validation**: duplicate zone-apex across credentials → rejected; zone-apex that's a subdomain of a public suffix → rejected (defence in depth per § 3.3); every spec-field combination; every reject reason; every default value.
- **Retry/backoff**: timing assertions with an injected clock; no real sleeps in tests.
- **Log redaction**: capture log output, assert the token literal never appears at any verbosity level.
- **RBAC manifests**: render the Helm chart, assert the ServiceAccount only requests the minimum permissions enumerated in `REQUIREMENTS.md` § 6.4.

Goal: ≥10 unit tests per non-trivial module (per the auto-memory rule).

### 5.3 Integration tests with real-shape mock

The integration layer mocks Hetzner at the HTTP boundary. The mock **must** be derived from real Hetzner API responses, not from docs alone. The capture step:

1. Mint a throwaway hcloud token + a throwaway zone in a sandbox project.
2. With `curl` + `jq`, hit every endpoint the webhook will call: `GET /v1/zones`, `POST /v1/zones/{id}/rrsets`, `PATCH /v1/zones/{id}/rrsets/{name}/{type}`, `DELETE /v1/zones/{id}/rrsets/{name}/{type}`, plus every error path we know about (401 invalid token, 403 wrong project, 404 zone-not-found, 409 conflict-on-create, 422 invalid-zone-name, 429 rate-limit with `Retry-After` header, 5xx).
3. Save the raw JSON responses + headers verbatim under `tests/fixtures/hetzner-cloud/`.
4. Build the `httptest.Server` mock to replay these fixtures.

Why this matters (a lesson learned the hard way across multiple external-API integrations in this organisation's MCP servers): "**capture real responses first**" — docs drift from reality, silent mismatches pass unit tests with hand-rolled mocks but break against the real API. The integration mock is only useful if it's a real-API replay.

#### What integration tests cover

- Happy path: `Present` → record observed at zone → `CleanUp` → record gone.
- Eventual consistency: `Present` returns success only after polling confirms the record is visible on at least one of the zone's NS records (mock simulates a 5–20 s delay).
- Multi-project routing actually hits the right "project's mock server" for each challenge.
- All known error shapes from § 5.3 step 2 map to readable cert-manager `Challenge.status` messages.
- Idempotence: repeated `Present(same_fqdn, same_token)` does not create duplicate records; repeated `CleanUp` is a no-op if the record is gone.
- Token redaction: trace logs from the integration run contain no token literals.

### 5.4 Harness layer (real Hetzner + real cert-manager + Let's Encrypt staging)

The harness gate. **No feature ticket lands without a harness test that exercises the relevant code path against the real Hetzner API.**

#### 5.4.1 Test fixtures the operator provides

The harness needs two operator-provided hcloud tokens, scoped to **distinct Hetzner Cloud projects** with the following zone layout:

- **`HCLOUD_TOKEN_PROJECT_A`** — token for a project that owns **one** dedicated harness DNS zone. Symbolic name: `<harness-zone-a>` (e.g. `harness-a.example.com` if you control `example.com`, or any zone-apex you've dedicated to harness use). Exercises the single-zone routing case.
- **`HCLOUD_TOKEN_PROJECT_B`** — token for a different project that owns **two** dedicated harness DNS zones. Symbolic names: `<harness-zone-b1>` and `<harness-zone-b2>`. Exercises the multi-zone-per-project case.

Storage: the operator caches both tokens under `~/.cache/cert-manager-webhook-hcloud-zones/` with mode `0600`, outside any git working tree (mirrors the pattern used by every harness profile in this organisation). The mapping from symbolic names (`<harness-zone-a>` etc.) to real Hetzner zone names lives in the same local cache (`harness-zones.env` or equivalent), not in the public repo. CI receives the token values via repo secrets `HCLOUD_TOKEN_PROJECT_A` / `HCLOUD_TOKEN_PROJECT_B` and the zone names via `HARNESS_ZONE_A` / `HARNESS_ZONE_B1` / `HARNESS_ZONE_B2`.

The operator is free to use any zone names that meet the constraints (distinct apex, no production records, dedicated to harness use); the harness suite does not encode any specific zone name.

#### 5.4.2 Dedicated harness zones — collision avoidance

All harness-issued ACME challenges live in zones the operator has **dedicated to harness use** — no production records, no overlap with operational systems. Production systems must never use a harness zone.

Each test run further prefixes a per-run identifier (UTC timestamp + 6-char random suffix) under the harness zone, so concurrent harness runs don't trample each other. Example FQDN (with `<harness-zone-a>` = `harness-a.example.com`):

```text
app-a-20260525T103401-a3f7b9.harness-a.example.com
```

Cleanup contract: every test must `defer` (Go) / `t.Cleanup()` the deletion of any TXT record it created. The harness suite additionally runs a "garbage collect harness leftovers older than 24 h" sweep at the start of each fire as a safety net (in case a prior run crashed before cleanup).

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

Two minimal apps live in the project tree under `tests/harness/test-apps/`. They reference zones via the symbolic names from § 5.4.1; the test runner substitutes the operator's real zone values from `HARNESS_ZONE_*` at apply-time.

- **`test-app-zone-a`** — a single Pod with an `Ingress` + a `Certificate` resource requesting `app-a-<run-id>.<harness-zone-a>`. Validates Project A's single-zone token path.
- **`test-app-zone-b-multi`** — two `Certificate` resources requesting `app-b1-<run-id>.<harness-zone-b1>` and `app-b2-<run-id>.<harness-zone-b2>` (different zones, same Project B token). Validates the multi-zone-per-project case: the routing logic picks the right zone, the same token handles both.

A third optional test scenario `test-app-cross-project` requests `app-cross-<run-id>.<harness-zone-a>` + `app-cross-<run-id>.<harness-zone-b1>` from a single `Certificate` (SAN list). Validates that one Certificate triggering challenges across two projects' tokens still works end-to-end.

#### 5.4.5 Error-path coverage

Per `ENGINEERING_PRINCIPLES.md` § 5: harness covers **both the sunny path and the key error paths**. At minimum:

- **Wrong token** for a zone (operator misconfigured the routing map: zone in credential A actually owned by project B) → cert-manager `Challenge.status` surfaces the 403 with a readable message.
- **Zone not in any credential's list** → fail-closed at the webhook (per § 3.3), cert-manager retries, the operator sees a clear "no credential matches FQDN x; configured zones: [...]" message.
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
- **Also run harness in CI**, gated on the two repo secrets being present, on the same set of triggers as integration tests (every PR + every push to main). Reasoning: the webhook is a security-adjacent surface (DNS records under authoritative zones), and a contract drift on the Hetzner side would silently break production cert-issuance otherwise. The cost (~15 min CI wall-time per run + small Hetzner API quota) is justified.
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
2. **GitHub Pages-hosted chart repo** at `xmv-solutions-gmbh.github.io/cert-manager-webhook-hcloud-zones` for users who prefer the classic `helm repo add` flow. Generated by `helm/chart-releaser-action` on every tag push (the existing pattern in the `strapi-helm-charts` repo is a working template).

### 8.3 ArtifactHub listing

[ArtifactHub](https://artifacthub.io) is the de-facto discovery surface for cert-manager users looking for DNS-01 webhooks. We register the project there once `v0.1.0` ships:

- `artifacthub-repo.yml` in the chart directory with maintainer + security-contact metadata.
- Annotations on the chart (`artifacthub.io/changes`, `artifacthub.io/links`, `artifacthub.io/images`, `artifacthub.io/category: security`).
- Once approved, the chart shows up at `artifacthub.io/packages/helm/...` and is discoverable via `cert-manager + Hetzner` searches.

### 8.4 Hetzner outreach

Once `v0.1.0` is stable in production (a few weeks of real-world certificate issuance with no incident):

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
4. **`groupName` default value for the published Helm chart** — community-webhook convention is "deploy-owner's domain"; the published default lives under the publisher's OSS namespace and operators may override. Confirm the chosen default doesn't collide with any existing community webhook before publication.
5. **Helm chart hosting** — confirm `helm/chart-releaser-action` works as advertised for OCI publishing (the `strapi-helm-charts` repo's pipeline is our reference).
6. **cert-manager webhook gRPC vs HTTP** — recent cert-manager versions added an HTTP webhook protocol alongside the legacy gRPC. Decide which to implement (likely both; HTTP is the future).
7. **Language / runtime** — Go is the obvious default (cert-manager itself is Go; the reference `webhook-example` is Go; the existing community webhooks are Go). Worth a sanity check that no compelling reason exists to deviate.

### Resolved during concept review

- **Are delegated subdomain zones possible in Hetzner Cloud Zones?** No — see § 3.4 for the decision record and citations. Routing is exact-match on zone-apex.

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
3. **Harness sandbox setup** — provision two operator-dedicated test zones (one in each test project), capture the real-API fixtures, write the first harness test that proves end-to-end auth + a TXT-record create/read/delete cycle against the real API. **No feature tickets enter "Doing" before this is green** (per § 5 the harness layer is a hard gate).
4. **Routing + config validation** — the exact-match zone-apex resolver, the config-validation pass that rejects duplicate zones across credentials and configured zone-apex names that are themselves subdomains of a public suffix, the YAML schema for the Issuer config block.
5. **cert-manager webhook integration** — implement the `Present` / `CleanUp` entry points, with integration tests via the fixture-replay mock.
6. **Helm chart** — Deployment, Service, APIService, RBAC, ServiceAccount, PKI, the values reference doc.
7. **End-to-end harness expansion** — both test-apps, the cross-project test-app, the GC sweep, the kind-cluster bring-up automation.
8. **Release pipeline** — image build + sign + SBOM, chart publishing (OCI + GH Pages), GitHub Release notes.
9. **ArtifactHub registration** — the metadata file + maintainer-flow once `v0.1.0` is tagged.

Each ticket carries an acceptance-criteria line for "harness test added + green" per `ENGINEERING_PRINCIPLES.md` § 5.
