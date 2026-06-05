<!--
SPDX-License-Identifier: MIT OR Apache-2.0
SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
SPDX-FileContributor: David Koller <david.koller@xmv.de>
-->

# PROJECT_SPECIFICS.md — `cert-manager-webhook-hcloud-zones`

Project-specific content for `cert-manager-webhook-hcloud-zones`. Read after `AGENTS.md` per its reading order. Everything in here is specific to this repo; the generic agent rules live in `AGENTS.md` + `ENGINEERING_PRINCIPLES.md` + `PROJECT_MANAGEMENT_PRINCIPLES.md`.

## What this project is

`cert-manager-webhook-hcloud-zones` — a [cert-manager](https://cert-manager.io) DNS-01 challenge solver for the **new** Hetzner Cloud Zones API (`https://api.hetzner.cloud/v1/zones`), with first-class support for multiple Hetzner Cloud projects from a single deployment (N:1 routing: one webhook deployment maps each zone-apex to the token authoritative for it).

Full vision and scope in [`docs/app-concept.md`](docs/app-concept.md). Read it before changing anything that touches the public surface (tool API, configuration shape, RBAC, container image, Helm chart).

## Project-specific docs

| Doc | Purpose |
|---|---|
| [`docs/app-concept.md`](docs/app-concept.md) | Vision, MVP scope, public surface, Testability section, open questions |
| [`docs/REQUIREMENTS.md`](docs/REQUIREMENTS.md) | Original requirements brief, preserved for historical context |
| [`docs/testconcept.md`](docs/testconcept.md) | Per-project instantiation of the three test layers (unit / integration / harness) |
| [`docs/howto-oss.md`](docs/howto-oss.md) | How-to for the OSS release flow |
| [`docs/hetzner-oss-readiness.md`](docs/hetzner-oss-readiness.md) | Hetzner OSS readiness notes |
| [`docs/proposals/`](docs/proposals/) | RFCs / spike notes / architectural decisions too big for a single issue |
| [`docs/markdown-style.md`](docs/markdown-style.md) | Markdown linting rules — read only when producing or editing Markdown |
| [`README.md`](README.md) | Quickstart for end users |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Contribution flow |
| [`SECURITY.md`](SECURITY.md) | Vulnerability disclosure |
| [`CHANGELOG.md`](CHANGELOG.md) | Keep-a-changelog history |

## Tracker

**GitHub Issues + the repo-bound GitHub Project** at `https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/issues`. See `ENGINEERING_PRINCIPLES.md` § 2. No `docs/todo.md` or other markdown TODO files.

Recommended labels: `type:feat` / `type:fix` / `type:chore` / `type:docs` / `type:test`; `area:<component>`; `priority:p0` / `p1` / `p2`. Add `agent:<tool-name>` (e.g. `agent:claude`, `agent:codex`) when an AI agent is the executor.

Issue body convention: `## Context`, `## Acceptance criteria` (checkbox list), `## Out of scope`, `## Links`. Milestones map to releases (`v0.1.0 — MVP`, `v0.2.0`, …).

## Tech stack

- **Language:** Go (`go 1.25`); module `github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones`.
- **Frameworks / libraries:** cert-manager webhook apiserver (`github.com/cert-manager/cert-manager`), `k8s.io/{apiextensions-apiserver,apimachinery,client-go}`.
- **Build / packaging:** `Makefile`, multi-stage `Dockerfile`, container image published to GHCR, Helm chart under `charts/`.
- **Lint / format:** `golangci-lint` (`.golangci.yml`); `markdownlint-cli2` for Markdown (`.markdownlint.yaml`).
- **Tests:** Go unit/integration (`make test`); bats-based shell tests (`tests/run_tests.sh`); a Kubernetes harness layer (`tests/harness/run.sh`).
- **Distribution surface:** GHCR container image + Helm chart; published OSS project (OpenSSF Best Practices, cosign keyless signing, SBOM attestation).

## Project-specific overrides of the engineering baseline

- None at present. Document any deviation from `ENGINEERING_PRINCIPLES.md` here, with the paragraph reference and a one-line justification.

## License header for new source files

This project is dual-licensed **MIT OR Apache-2.0**, copyright **XMV Solutions GmbH**. Generic SPDX rules in `ENGINEERING_PRINCIPLES.md` § 11; concrete examples for this project below.

For Go, Rust, JS/TS, Java, and other languages with `//` line comments:

```text
// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: <year> XMV Solutions GmbH
// SPDX-FileContributor: <git user.name> <<git user.email>>
```

For Shell, YAML, TOML, Python, and most languages with `#` line comments:

```text
# SPDX-License-Identifier: MIT OR Apache-2.0
# SPDX-FileCopyrightText: <year> XMV Solutions GmbH
# SPDX-FileContributor: <git user.name> <<git user.email>>
```

For HTML / Markdown:

```html
<!--
SPDX-License-Identifier: MIT OR Apache-2.0
SPDX-FileCopyrightText: <year> XMV Solutions GmbH
SPDX-FileContributor: <name> <<email>>
-->
```

The first `SPDX-FileContributor` line is set when the file is created and is **never overwritten** — this honours the German *Urheberrecht*. New substantial contributors append additional lines. The agent populates the line from the current `git config user.name` / `user.email`.

## Harness workflow — XMV-maintainer convention

> **External contributors and forks**: ignore this section — it documents an internal shortcut for the XMV-maintainer team. The supported general procedure is in [`docs/app-concept.md` § 5.4](docs/app-concept.md): bring any Kubernetes cluster you have (kind, k3d, k3s, a managed cluster) and point `HARNESS_KUBECONFIG` at its kubeconfig before running the harness.
>
> **AI agents working with the XMV maintainer**: this section exists so the workflow doesn't have to be re-discovered every time the operator says "run the harness". When the user asks for a harness run, this is the expected sequence — not "spin up a fresh kind cluster from scratch", and not "improvise a cluster manually."

The harness needs a real K8s cluster with cert-manager. The XMV maintainer team brings one up from the sibling proprietary repo `xmv-cluster-gitops-template`, typically located at `~/git/xmv/xmv-cluster-gitops-template/` on the maintainer's workstation. The three-terminal workflow:

```bash
# Terminal A — bring the harness cluster up (one-time per test session)
cd ~/git/xmv/xmv-cluster-gitops-template
./scripts/harness-up.sh
# Prints kubeconfig path + "ready" on stdout

# Terminal B — run the OSS harness against that cluster
cd <this-repo>
export HARNESS_KUBECONFIG=/tmp/harness-cluster.kubeconfig   # path printed by harness-up.sh
./tests/harness/run.sh

# Terminal A — tear the cluster down (deletes Hetzner nodes; keeps Talos snapshot)
cd ~/git/xmv/xmv-cluster-gitops-template
./scripts/harness-down.sh
```

Harness secrets (`HCLOUD_TOKEN_PROJECT_A` / `HCLOUD_TOKEN_PROJECT_B` / `HARNESS_ZONE_A` / `HARNESS_ZONE_B1` / `HARNESS_ZONE_B2`) come from the XMV Infisical EU workspace, folder `/cert-manager-webhook-hcloud-zones/`. The universal-auth pull pattern is documented at the maintainer's workstation level (outside this repo).

This is the only XMV-specific reference in this otherwise organisation-neutral OSS repo. It exists because re-discovery cost (search for the cluster-template repo, read its scripts, infer the workflow) is high enough that recording the convention is worth the small organisational mention. External users see a paragraph that explicitly says "ignore this section, the supported procedure is documented in app-concept.md § 5.4."

## Documentation scaling threshold

If `docs/app-concept.md` plus the relevant supporting docs exceed roughly **50k tokens (~200 KB combined)**, split into a two-level structure:

1. Keep `docs/app-concept.md` as an index — vision, summary, table of contents with links.
2. Move thematic deep-dives into `docs/app-concept/*.md` chapters (e.g. `architecture.md`, `security.md`, `api-design.md`).

Rationale: AI agents should use ≤ 1/3 of their context window for project instructions, leaving room for code and conversation.

## Environments + URLs

- **Container image:** `ghcr.io/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones`.
- **Helm chart:** published under `charts/cert-manager-webhook-hcloud-zones` (GHCR OCI + Artifact Hub).
- **Harness secrets:** XMV Infisical EU workspace, folder `/cert-manager-webhook-hcloud-zones/` (see Harness workflow above).

## Glossary

- **N:1 routing** — a single webhook deployment serving multiple Hetzner Cloud projects, mapping each zone-apex to the credential whose `zones` list contains that apex.
- **Zone-apex** — the registered DNS zone root the webhook walks an FQDN up to in order to pick the authoritative credential.
- **Cloud Zones API** — the current Hetzner DNS product (`api.hetzner.cloud/v1/zones`, `Authorization: Bearer`), distinct from the legacy DNS Console (`dns.hetzner.com`, `Auth-API-Token`).
- **Harness** — the third test layer: the webhook exercised against a real Kubernetes cluster with cert-manager (see `docs/testconcept.md` and the Harness workflow section above).
