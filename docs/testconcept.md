<!-- SPDX-License-Identifier: MIT OR Apache-2.0 -->
# Test concept — `cert-manager-webhook-hcloud-zones`

## 1. Goal

This webhook sits on the critical path of certificate issuance for every workload whose `Certificate` resolves through it. Its worst-case failure mode is not a loud crash — it is a *silent misroute*: writing the ACME DNS-01 challenge TXT record to the wrong Hetzner Cloud project, or to the wrong record name within the right zone. The challenge then never validates, Let's Encrypt fails the order, and the operator chases a ghost. Worse, in a multi-tenant set-up a misroute could in principle let credential A mutate zones it should never have been able to touch.

The test strategy therefore exists to:

- Catch routing bugs before they ship — the FQDN→credential decision in `internal/routing` is the load-bearing piece of logic and is treated as such.
- Validate the Hetzner Cloud Zones API contract against *captured real responses*, so that an unannounced wire-format change at Hetzner becomes a red unit test rather than a midnight outage.
- Prove end-to-end, against real Let's Encrypt STAGING and real Hetzner zones, that a freshly installed chart plus `ClusterIssuer` plus `Certificate` reaches `Ready=True` — covering the entire chain that no in-process test can reach (image build, framework flag wiring, DNS propagation, cert-manager CRD compatibility).

## 2. Test pyramid for this project

The pyramid maps one-to-one onto the package layout. Each layer guards a specific class of bug, named below.

### 2.1 Unit — routing (`internal/routing/routing_test.go`)

Pure-function tests with no mocks and no test doubles. `routing.Resolve` takes the parsed config and a FQDN and returns the credential reference plus the matched zone apex. Tests cover apex matches, longest-suffix wins, sub-label boundary correctness (no substring matches across labels), nil-receiver defence and full `ValidateConfig` semantics including duplicate-zone detection across credentials.

- **Bug class guarded**: silent misroute. A subtle bug here is the worst thing this codebase can ship.
- **Coverage**: 97.7%.

### 2.2 Unit — relative record name (`internal/solver/relative_name_test.go`)

Table-driven mapping of resolved FQDN plus zone apex to the relative record name we send to Hetzner. Eight cases: apex challenge, single-label sub, multi-label sub, the harness-style sub (the exact shape that surfaced the v0.1.3 regression — see § 4.1), trailing-dot defence, mixed-case FQDN, mixed-case apex, delegated-subdomain apex. Two additional tests pin the defensive fallback paths.

- **Bug class guarded**: silent wrong-record-name (the v0.1.3 regression). Pin aggressively; treat any change to `relativeRecordName` as requiring a new row.

### 2.3 Integration — Hetzner client (`internal/hcloud/client_test.go`)

`httptest.NewServer` stands in for the Hetzner API. Crucially, the fixtures it returns under `internal/hcloud/testdata/fixtures/` are *captured real responses* — `list_zones.json`, `create_rrset.json`, `update_rrset.json`, plus error fixtures `error_401.json` / `403` / `404` / `409` / `422` / `429` / `500`. Tests cover happy-path CRUD, every documented error code, `Retry-After` parsing (seconds and HTTP-date), exponential back-off with cap, retry exhaustion, 4xx non-retry, context-cancellation, path escaping, and token-source-per-attempt semantics. Two log-redaction tests assert that the bearer token never appears in error messages or debug output.

- **Bug class guarded**: silent wire-format mismatch when Hetzner changes a field shape or error envelope without notice. See § 7 for the capture-first rule.
- **Coverage**: 89.2%.

### 2.4 Integration — solver (`internal/solver/solver_test.go`)

Wires the routing layer plus the hcloud client (against an `httptest` mock) plus a stub `SecretGetter` into the cert-manager `webhook.Solver` contract. Tests cover `Initialize`, `Present` happy path, multi-project routing (the same `Present` flowing to the correct Hetzner project depending on the FQDN), idempotent re-`Present` with the same key, idempotent re-`Present` with a *different* key (update, not duplicate), `CleanUp` idempotence, wrong-token 403, no-matching-zone fail-closed, zone-not-found-at-Hetzner, rate-limit honouring of `Retry-After`, token redaction in logs, invalid-JSON config, empty credentials, duplicate-zone-across-credentials rejection, default-namespace fallback, default-key fallback, secret-getter error propagation, nil-challenge defence, zone-cache deduplication of `ListZones` calls.

- **Bug class guarded**: orchestration bugs — the layer above routing and below the harness. The mock makes this *fast*; the trade-off is that it can only test the behaviour we have thought to write a case for. The harness in § 2.5 catches what we have not thought of.
- **Coverage**: 71.2%.

### 2.5 Harness — `tests/harness/run.sh`

Bring-your-own-kubeconfig end-to-end runner. Reads six environment variables (`HARNESS_KUBECONFIG`, `HCLOUD_TOKEN_PROJECT_A`, `HCLOUD_TOKEN_PROJECT_B`, `HARNESS_ZONE_A`, `HARNESS_ZONE_B1`, `HARNESS_ZONE_B2` — full list in the script's `--help`), installs cert-manager and the webhook Helm chart from GHCR, applies the three test-app manifests under `tests/harness/test-apps/`, and waits for each `Certificate` to reach `Ready=True` against real Let's Encrypt STAGING. Post-Ready it asserts issuer URL, SANs, and that the materialised `Secret` contains a valid key. Exercises three Hetzner Cloud zones across two Cloud projects, so the multi-credential routing path is exercised against real Hetzner endpoints. On any assertion failure the script leaves the cluster state intact for inspection; `--cleanup` is honoured only on a fully-green run.

- **Bug class guarded**: anything the layers above cannot see. Image build / static-linking issues, framework-flag conflicts in `main`, DNS self-check quirks, cert-manager CRD compatibility breaks, real-world DNS propagation timing, real Hetzner rate-limit shape under load.

## 3. Coverage targets and current state

| Package | Coverage | Notes |
|---|---|---|
| `internal/routing` | 97.7% | Pure logic; the load-bearing decision. Target ≥95%. |
| `internal/hcloud` | 89.2% | Mock built from captured real responses. Target ≥85%. |
| `internal/solver` | 71.2% | Orchestration; the long tail is exercised by the harness rather than by adding more unit doubles. Target ≥70%. |
| `cmd/cert-manager-webhook-hcloud-zones` | 0% | By design — see below. |
| **Total** | **82.2%** | |

The `cmd/...` package is intentionally 0%-covered. cert-manager's `apis/webhook/cmd` framework owns the flag set, the HTTPS server, and the solver lifecycle; `main.go` is a four-line wiring call (`cmd.RunWebhookServer(...)`). There is no meaningful unit-testable surface — testing it would mean stubbing the framework, which would test the stubs and nothing else. The bug class that lives in `main` (framework-flag conflicts, static-binary linking) is inherently end-to-end and is covered by the harness; see § 4.2 and § 4.3.

Per-function coverage detail is reproducible locally with the command in § 5.

## 4. Three concrete failure-mode lessons

Each of the three bugs below shipped to a tagged release and was caught by the harness, not by the unit suite. They are the empirical justification for the pyramid as it stands.

### 4.1 The subdomain-record-name bug (v0.1.3)

**Bug.** The solver hard-coded the TXT record name to a constant `defaultChallengeName = "_acme-challenge"`, regardless of the actual cert's CN or SAN. Result: every TXT record was written to the zone apex. Apex certs worked. Every subdomain cert silently failed validation.

**Why each layer missed it.**

- *Routing* could not see it — routing's job is only to pick the credential and the zone apex; the record-name derivation lives in `solver`.
- *Solver unit test* shipped with a happy-path test that happened to use an apex FQDN, so the wrong-record-name behaviour was tautologically "correct" against the mock.
- *Harness* fired against `app-a.xmv-example.com` and the LE STAGING order failed; investigation traced it to a stuck-at-apex TXT record.

**Regression test added.** `internal/solver/relative_name_test.go` — table-driven with apex, one-label sub, multi-label sub, the exact harness-style FQDN that failed, trailing-dot defence, mixed-case variants, and delegated-subdomain apex. Eight rows, plus two additional tests for the defensive fallbacks.

### 4.2 The CGO / static-binary bug

**Bug.** The Dockerfile was missing `CGO_ENABLED=0` on the `go build` invocation. The resulting binary linked against glibc and would not execute on `gcr.io/distroless/static` (which has no libc).

**Why each layer missed it.**

- Every Go test ran in the build environment, which has glibc, so the binary was loadable there.
- No layer below the harness ever ran the container image; they all ran `go test` directly.

**Regression test added.** Harness-only — no unit-testable surface. The harness installs the published OCI chart, which pulls the actual image, which only runs if the binary is statically linked. A regression of this bug would surface as a `CrashLoopBackOff` on the webhook deployment at harness `helm install` time.

### 4.3 The pflag / framework flag-set bug

**Bug.** `main.go` called `pflag.Parse()` before handing off to cert-manager's webhook framework. `pflag` greedily consumed the framework's `--tls-cert-file` (and friends), so the framework saw an empty flag set and refused to start the HTTPS server.

**Why each layer missed it.**

- Nothing in `internal/...` instantiates the cert-manager framework — the framework is a `main`-package concern.
- The `cmd` package has no tests (see § 3) because there is nothing to unit-test once you remove the framework.

**Regression test added.** Harness-only — no unit-testable surface. A regression manifests as the webhook pod failing to come up; the harness's `helm install` rollout-status step fails before any `Certificate` is even applied.

## 5. How to run the tests locally

All commands are run from the repository root.

```bash
# Unit + integration — fast, no external dependencies.
go test ./...

# Same, with the race detector. Worth running before pushing because
# the zone cache and the solver's concurrent Present/CleanUp paths
# are the obvious places for a data race to hide.
go test -race ./...

# Coverage summary printed to stdout.
go test ./... -cover

# Coverage profile + HTML view for hunting uncovered branches.
go test ./... -coverprofile=/tmp/cov.out
go tool cover -html=/tmp/cov.out

# Per-function coverage report (the long tail).
go test ./... -coverprofile=/tmp/cov.out
go tool cover -func=/tmp/cov.out
```

`make test` runs `go test ./... -race -count=1 -v` — the canonical pre-push invocation.

For the end-to-end harness:

```bash
# All six env vars are required; the script's --help lists them in full
# and the script fails closed if any are missing.
export HARNESS_KUBECONFIG=...
export HCLOUD_TOKEN_PROJECT_A=...
export HCLOUD_TOKEN_PROJECT_B=...
export HARNESS_ZONE_A=...
export HARNESS_ZONE_B1=...
export HARNESS_ZONE_B2=...
tests/harness/run.sh           # leaves state on failure
tests/harness/run.sh --cleanup # tears down only on a fully-green run
```

Exit codes: `0` success, `1` setup failure, `2` assertion failure.

## 6. CI mapping

`.github/workflows/ci.yml` defines three jobs, all of which are required by branch protection (configured via `repo.ini > STATUS_CHECKS`):

| Job | What it runs | Gate |
|---|---|---|
| `lint` | `markdownlint-cli2` over `**/*.md` | Markdown style per `docs/markdown-style.md`. |
| `test` | `./tests/run_tests.sh` (repo-template shell scaffold) | Inherited from the OSS template; kept green for parity with sister projects. |
| `go` | `golangci-lint run ./...` then `make test` | The actual Go gate. `gofmt` is enforced via golangci-lint; `make test` runs the full `go test ./... -race -count=1 -v`. |

Per ENGINEERING_PRINCIPLES.md § 5 and § 6: run `make test` and `golangci-lint run ./...` locally before every push (CI is confirmation, not discovery), and watch CI through to completion after every push (trunk red is a P0 incident).

The harness is not run in CI. It needs live Hetzner credentials and live DNS zones, and it issues real (staging) certificates against Let's Encrypt — none of which belong in an unattended CI fire on every push. The harness is run by hand against a sandbox before each release, and the result is recorded in the release notes.

## 7. The "captured real responses" rule

For any new Hetzner Cloud Zones API surface we add support for, the workflow is:

1. Capture a real response from the live API first — `curl -i` with a sandbox token, save the full headers-plus-body to `internal/hcloud/testdata/fixtures/<operation>.json`.
2. Only then write the test mock against that fixture.
3. Only then write the production code that handles the captured shape.

The rationale is the standard one (see `feedback_capture_real_responses_first.md`): documentation drifts from reality, often silently. A mock written from docs and code written from the same docs will agree with each other and disagree with production — and the agreement passes the unit suite. A mock written from a captured response catches the divergence at the unit layer.

Edge-case error responses (e.g. a hypothetical 412 we cannot trigger on demand) may have to be written from the spec rather than from a capture; in that case the fixture file should carry a one-line comment stating that fact so the gap is visible to the next reader.

## 8. Operator-side caveats relevant to the harness

The harness has one known foot-gun specific to Hetzner: zones that are hosted on Hetzner Robot DNS rather than Hetzner Cloud Zones, *and* that carry an apex wildcard `CNAME`, can cause cert-manager's DNS self-check to resolve the TXT record through the public-recursive resolver chain rather than the authoritative nameservers, which in turn loses the just-written record because of caching. The mitigation is to pass `--dns01-recursive-nameservers-only` to cert-manager. This is an operator concern, not a webhook bug, but it shows up first as a harness failure and is therefore worth flagging here. The full story — including how to detect the affected topology and the exact flag wiring — is in `docs/app-concept.md` § Operator caveats.
