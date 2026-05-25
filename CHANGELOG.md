<!-- SPDX-License-Identifier: MIT OR Apache-2.0 -->
# Changelog

All notable changes to this project will be documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.0.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

First substantive work on the `cert-manager-webhook-hcloud-zones` project itself: the foundational app concept plus the end-to-end harness scaffolding (test-app manifests + bring-your-own-kubeconfig runner). No release artefacts yet — `v0.1.0` is gated on the webhook implementation, Helm chart, and the GHCR publish pipeline (see `docs/app-concept.md` § 12 for the full ticket sequence).

Tracked in [GitHub Issues](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/issues).

### Added

- **`docs/app-concept.md`** (PR [#1](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/pull/1)) — the foundational design spec: motivation (legacy Hetzner DNS Console vs. the new Hetzner Cloud Zones API), MVP scope, the load-bearing zone-to-token routing decision (multi-project from a single deployment), the configuration & deployment model, the full test strategy (unit / integration / harness with Let's Encrypt staging), operational + security posture, publication plan (GHCR image + OCI chart + GH Pages + ArtifactHub), and the ticket sequence to `v0.1.0`.
- **`tests/harness/test-apps/`** (PR [#4](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/pull/4)) — the harness manifest set: `ClusterIssuer` (Let's Encrypt staging, two `apiTokenSecretRef` entries — one per Hetzner Cloud project, exercising the "one token, multiple zones" path for project B), three `Certificate` resources (one per harness zone), the accessory `Pod` / `Service` / `Ingress` backend, and a `kustomization.yaml`. Manifests carry `${RUN_ID}` / `${HARNESS_ZONE_*}` placeholders rendered by `envsubst` at harness runtime, so concurrent fires against the same zones cannot collide.
- **`tests/harness/run.sh`** (PR [#5](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/pull/5)) — the bring-your-own-kubeconfig harness runner. Validates required env vars (`HARNESS_KUBECONFIG`, `HCLOUD_TOKEN_PROJECT_{A,B}`, `HARNESS_ZONE_{A,B1,B2}`), installs cert-manager and the webhook chart from its **published GHCR OCI artifact** (the production install path — not a local working-tree build), applies the test-app manifests, waits for all three `Certificate`s to reach `Ready=True` on a shared 10-minute budget, then asserts issuer is LE staging, SANs match the expected per-fire FQDN, and `tls.key` parses. Exit codes: `0` success / `1` setup failure / `2` assertion failure. `--cleanup` is opt-in and is honoured only when every assertion has passed — failed runs intentionally leave cluster state in place for inspection.
- **README "How to run the harness" section** — operator-facing quick reference for the harness: prerequisites, the six required env vars, invocation, exit codes, the failed-run-leaves-state principle, and the long-lived `hcloud-token-project-{a,b}` Secrets that persist between fires for cluster warmth.

### Changed

- **`README.md`** — replaced the generic template README with the project-specific one (one-sentence pitch, "Why this exists" framing the legacy-DNS-Console vs. Cloud-Zones gap, roadmap to `v0.1.0`, documentation index, the new harness section). Previously this slot held the `oss-project-template` README; PR [#1](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/pull/1) replaced it.

## [v0.3.0] — 2026-05-09

LLM-product-independence pass. The template no longer privileges any specific AI coding tool: every agent reads the same canonical brief, and the tool-specific files are minimal pointers. Plus a cleanup of GitHub-Copilot-Chat-specific shortcuts that were either redundant or anti-patterns.

### Added

- **`AGENTS.md`** as the canonical, tool-agnostic AI-agent brief at the repo root. Every coding agent reads the same file: Codex auto-discovers `AGENTS.md` natively; `CLAUDE.md` (Claude Code) and `.github/copilot-instructions.md` (Copilot) are now five-line pointers that redirect any tool back to `AGENTS.md`.
- **`docs/markdown-style.md`** — Markdown linting rules (MD001-MD047, table format, code-block languages, nested code blocks, angle-bracket placeholder rule). Pulled out so the guidance is loaded only when an agent is actually producing or editing Markdown — not on every code-only task.

### Changed

- **`ENGINEERING_PRINCIPLES.md` § 1** — language rule tightened from generic "English" to "British English (en-GB)" with concrete examples (colour / initialise / behaviour / licence vs. license).
- **`ENGINEERING_PRINCIPLES.md` § 7** — README skeleton (one-sentence pitch as blockquote, "What is this for?", use-case dialogue) absorbed into the principles. Was previously in `.github/copilot-instructions.md`; moved here because it applies to humans too.
- **`ENGINEERING_PRINCIPLES.md` (cross-refs)** — `CLAUDE.md` references updated to `AGENTS.md` throughout (§ 0, § 7, § 10, § 11). `agent:claude` label example generalised to `agent:<tool>`.
- **`CLAUDE.md`** — reduced from a project-skeleton overlay (~120 lines) to a five-line pointer at `AGENTS.md`. The skeleton's content (project facts, tech stack, overrides, licence-header examples) moved to `AGENTS.md` because that's the canonical agent brief now.
- **`.github/copilot-instructions.md`** — reduced from ~530 lines to a five-line pointer at `AGENTS.md`. The AI-specific behaviour parts moved to `AGENTS.md`; the human-relevant parts (British English, README structure) absorbed into `ENGINEERING_PRINCIPLES.md`; the Markdown lint rules moved to `docs/markdown-style.md`.
- **`README.md`** + **`docs/app-concept.md`** structure trees updated to reflect the new layout.

### Removed

- Redundant content in `.github/copilot-instructions.md` that was already in `ENGINEERING_PRINCIPLES.md` — Required-OSS-Files table, Test-Harness-First section, Testing Pyramid, File Generation Standards, Version Control Standards, Org Information header, "Excellence by Default" buzzword principles. Each of these had a canonical home elsewhere; the duplicate is gone.
- **`.github/prompts/`** (entire directory, 6 files, ~160 lines) — Copilot-Chat slash-command shortcuts (`/check-pr`, `/merge-pr`, `/create-pr`, `/new-feature`, `/auto-merge-pr`, `/add-instruction`). Each was either redundant against `AGENTS.md` / PRINCIPLES / `PULL_REQUEST_TEMPLATE.md`, an anti-pattern (admin-bypass on `gh pr merge --admin`), or referenced renamed files (`copilot-instructions.md` no longer holds content). One file even had hardcoded leftovers from another project ("Talos API Rust client library"). No XMV-OSS-relevant content was lost.
- **4 of 6 scripts in `.github/gh-scripts/`** (~400 lines): `check-pr.sh`, `create-pr.sh`, `merge-pr.sh`, `new-feature.sh`. Each was a thin wrapper around a standard `gh` command (`gh pr checks` / `gh pr create` / `gh pr merge` / `git checkout -b`) coupled to the deleted prompt files. The 2 genuinely useful bootstrap scripts (`assign-repo-to-team.sh` and `setup-branch-protection.sh`) are kept — they read `repo.ini` and configure GitHub repo settings, which is exactly the kind of operation that `ENGINEERING_PRINCIPLES.md` § 8 mandates be scripted, not run ad-hoc.

## [v0.2.0] — 2026-05-09 — initial hardening pass

A hardening pass driven by lessons learned from the first two projects bootstrapped from this template (`sharepoint-mcp`, `outlook-mcp`). Everything in this release is application- and framework-independent — the template stays language- and domain-neutral.

### Added

- **`ENGINEERING_PRINCIPLES.md`** — the 434-line project-agnostic engineering baseline (language rule, status workflow, three test layers with harness as the AI-development gate, source-control rules, CI vigilance, doc-mirrors-repo, source-of-truth, PR discipline). Both sister projects carried an identical copy; promoting it to a first-class template artefact.
- **`CLAUDE.md`** — per-project overlay skeleton: tech stack placeholder, project-specific overrides table, licence/SPDX guidance, GitHub-Projects-as-tracker pointer.
- **`docs/proposals/`** — RFC layout: a `README.md` explaining when to use it (and when *not* to), and a `_template.md` with the canonical sections (Status, Context, Decision, Alternatives considered, Consequences, Implementation notes). Naming convention `YYYY-MM-DD-short-slug.md`. Lifecycle: Draft → Accepted → Implemented; or Superseded / Withdrawn — never rewritten in place.
- **`scripts/`** — directory placeholder with a `README.md` documenting the convention from § 8 of the engineering principles (shebang + SPDX, `set -euo pipefail`, idempotent, self-documenting, `--help` summary, exit codes).
- **`.github/workflows/HARNESS_JOB.md`** — copy-paste-ready harness job snippet for downstream projects, with the canonical "skip silently when secret is missing" guard so PRs from forks aren't blocked.

### Changed

- **GitHub Issues + GitHub Projects is now the canonical tracker** for every XMV OSS project, from day one. The older "markdown TODO/ISSUES files in `docs/`" pattern is retired. `ENGINEERING_PRINCIPLES.md` § 2 rewritten; § 7 and § 10 references updated; `.github/copilot-instructions.md` and `docs/testconcept.md` updated to file issues + close via PR; `docs/app-concept.md` structure tree drops `todo.md`.
- **README skeleton** in `.github/copilot-instructions.md` now includes the three sections that paid back in both sister projects: a one-sentence pitch as a blockquote (under 200 characters, has to stand alone on package registries), a "What is this for?" section (concrete user situation before features), and a use-case dialogue example (memorable headline workflow).
- **`.github/workflows/ci.yml`** — header comment block documenting the three-job shape (lint / test / harness) and what to swap in for language-specific bits. The bats-test job for the template's own tests is unchanged.
- **`.github/workflows/release.yml`** — header comment documenting the OIDC Trusted Publisher pattern (PyPI flavour, generalises to crates.io / npm).

### Removed

- **`docs/todo.md`** — retired in favour of GitHub Issues + Projects.

[Unreleased]: https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/commits/main
[v0.3.0]: https://github.com/XMV-Solutions-GmbH/oss-project-template/releases/tag/v0.3.0
[v0.2.0]: https://github.com/XMV-Solutions-GmbH/oss-project-template/releases/tag/v0.2.0
