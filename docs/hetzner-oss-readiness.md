<!-- SPDX-License-Identifier: MIT OR Apache-2.0 -->
# Hetzner OSS Marketplace readiness audit

This document audits `cert-manager-webhook-hcloud-zones` against the requirements typically asked for by Hetzner's open-source programme and the wider OSS-marketplace ecosystem the project would also list against (ArtifactHub, the cert-manager.io webhook directory, the OpenSSF Best Practices badge programme, and REUSE compliance).

The goal is **submission readiness** — a sober list of what we already ship, what is thin or missing, and the smallest set of next steps that unblocks a credible application. Where Hetzner's exact policy is not documented publicly, the audit says so explicitly rather than guessing.

> **Verify with Hetzner OSS programme before submission.** Hetzner's open-source page and any current programme rules are the authoritative source. Read the public page and any application form they publish, and reconcile this audit against them before you click "submit".

---

## 1. What is already in place

The repository ships the usual OSS hygiene set. Item-by-item:

| Area | Status | Evidence |
|---|---|---|
| Licence | In place | Dual MIT / Apache-2.0. Root `LICENSE` mirrors `LICENSE-MIT`; `LICENSE-APACHE` is the full Apache text. CI's `test` job diffs `LICENSE` against `LICENSE-MIT` to keep them in sync. |
| SPDX headers | In place (sample) | Every `.go`, `.sh`, `.yaml`, and `.md` file sampled (`README.md`, `cmd/.../main.go`, `internal/solver/*`, `tests/harness/run.sh`, `charts/.../Chart.yaml`, `.golangci.yml`, `.github/CODEOWNERS`) carries `SPDX-License-Identifier: MIT OR Apache-2.0`. Copyright year is `2026` throughout. |
| README | In place | `README.md` covers purpose ("why this exists" — Hetzner DNS-product migration), roadmap, harness instructions, dual-licence, and full contact block (org, email, website, GitHub). |
| CONTRIBUTING | Present | `CONTRIBUTING.md` exists. **Caveat:** the file still references the template name and slug in places — see § 2. |
| CODE_OF_CONDUCT | In place | Contributor Covenant text at `CODE_OF_CONDUCT.md`. |
| SECURITY policy | In place | `SECURITY.md` with private-disclosure address (`oss@xmv.de`), 48-hour acknowledgement, 7-day initial assessment. **Caveat:** the supported-versions table still uses `1.x.x` placeholders that don't match the actual pre-1.0 versioning — see § 2. |
| CHANGELOG | In place | `CHANGELOG.md` follows Keep-a-Changelog, currently spans `v0.1.0` → `v0.1.4`. |
| Signed releases | In place | Release workflow signs the GHCR image keylessly with cosign (Sigstore OIDC). `v0.1.0`, `v0.1.3`, `v0.1.4` published. |
| SBOM | In place | `release.yml` runs `anchore/sbom-action` (SPDX-JSON) and attaches the SBOM as a cosign attestation on the image digest. |
| Multi-arch container image | In place | `release.yml` builds `linux/amd64` + `linux/arm64` via `docker/build-push-action` with QEMU + Buildx. Image lives at `ghcr.io/xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones`. |
| Helm chart | In place | `charts/cert-manager-webhook-hcloud-zones/` is packaged on every tag and pushed to GHCR as an OCI artifact (`oci://ghcr.io/xmv-solutions-gmbh/charts/cert-manager-webhook-hcloud-zones`). Chart README documents installation. |
| ArtifactHub chart annotations | In place | `Chart.yaml` already carries `artifacthub.io/category`, `artifacthub.io/license`, `artifacthub.io/links`, and `artifacthub.io/prerelease`. The chart is ready to be registered as a repository on ArtifactHub. |
| CI workflow | In place | `.github/workflows/ci.yml` defines `lint` (markdownlint), `test` (licence-diff + bats), and `go` (golangci-lint + `make test`). All three are green on `main` at the time of writing. |
| Dependabot | In place | `.github/dependabot.yml` is configured. |
| Issue templates | In place | `bug_report.md` and `feature_request.md` under `.github/ISSUE_TEMPLATE/`. |
| PR template | In place | `.github/PULL_REQUEST_TEMPLATE.md`. |
| CODEOWNERS | In place | `.github/CODEOWNERS` routes review to `@XMV-Solutions-GmbH/open-source`. |
| Test coverage report | In place (report), not uploaded | `make test` runs `go test`; coverage is computed locally but **not** uploaded to any third-party service. See § 2. |
| Harness against real Hetzner | In place | `tests/harness/run.sh` exercises the production install path against real Hetzner Cloud Zones, real cert-manager, and Let's Encrypt staging. |

---

## 2. What is missing or thin

Gaps grouped by likely reviewer concern. Severity (**blocker** / **strong recommend** / **nice to have**) is the auditor's judgement, not a Hetzner statement.

### 2.1 OSS-marketplace expectations

- **OpenSSF Best Practices badge** — *strong recommend*. There is no `.github/workflows/scorecard.yml`, no badge URL in the README, and no public passing-badge entry at `bestpractices.coreinfrastructure.org`. The badge is widely treated as a baseline trust signal by infrastructure-adjacent listings.
- **OpenSSF Scorecard workflow** — *strong recommend*. Related to the above: a `scorecard.yml` workflow publishing results to the Scorecard API would let reviewers (and us) see the project's score over time.
- **ArtifactHub repository registration** — *strong recommend*. The chart already carries the right annotations, but the repository itself has not been registered at `artifacthub.io/packages/search` (no record found). Registration is a one-time form-fill on the ArtifactHub side pointing at the OCI chart URL.
- **cert-manager.io webhook directory entry** — *strong recommend*. The cert-manager docs maintain a list of community webhooks. We are not on it. A PR to `cert-manager/website` adds us.
- **REUSE / SPDX compliance check in CI** — *nice to have*. SPDX headers are present, but no automated `reuse lint` step enforces them on new files. A single GitHub Action would close the gap.
- **CITATION.cff** — *nice to have*. Useful for academic / write-ups; not a blocker.
- **Governance doc** — *nice to have*. `MAINTAINERS.md` or `GOVERNANCE.md` makes the project's decision-making explicit. For a single-org project this can be one paragraph.
- **Codecov (or equivalent) upload** — *nice to have*. `repo.ini` still references `CODECOV_TOKEN`, but the `go` job in CI does not upload coverage anywhere. Either wire it up or remove the dangling reference.
- **Published documentation site** — *nice to have*. All docs live as `.md` files in the repo. A GitHub Pages site (e.g. MkDocs Material) is not in place. For a webhook of this scope, in-repo docs are arguably sufficient.

### 2.2 Repo hygiene that the audit found while looking

These are unrelated to Hetzner per se but a reviewer will notice them.

- **`CONTRIBUTING.md` still references the template** — *strong recommend* fix. The file opens with "Contributing to OSS Project Template" and clones from `oss-project-template`. Cheap fix; another agent in this branch is touching documentation, so coordinate.
- **`SECURITY.md` supported-versions table is wrong for a pre-1.0 project** — *strong recommend* fix. The table lists `1.x.x` as supported and `< 1.0` as unsupported, which contradicts the actual `v0.1.x` release line.
- **`SECURITY.md` subject template still says `project-name`** — *nice to have*. Should reference `cert-manager-webhook-hcloud-zones`.
- **No GitHub branch protection on `main`** — *strong recommend* fix. `gh api repos/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/branches/main/protection` returns 404 ("Branch not protected"). `repo.ini` now declares the desired state (`lint,test,go` required, code-owner reviews on); running `.github/gh-scripts/setup-branch-protection.sh` applies it.
- **GitHub repo metadata** — *nice to have*. The repo description is good; `homepage` is null and `topics` is empty. Both improve discoverability on GitHub search and any aggregator pulling from the GitHub API.

### 2.3 Hetzner-specific gaps

- **Explicit Hetzner Cloud Zones API mention in the README headline** — *in place*. The first sentence already names "the new Hetzner Cloud Zones API (`https://api.hetzner.cloud/v1/zones`)". Good.
- **A "Why Hetzner?" or "Built for Hetzner Cloud" callout** — *nice to have*. The README explains the gap but does not explicitly position the project as part of the Hetzner Cloud ecosystem. A two-line section near the top — "Built for Hetzner Cloud users running cert-manager" — improves the pitch for a Hetzner-curated showcase.
- **Free-tier-friendly install path** — *in place*. The harness needs only a kubeconfig and two Hetzner Cloud API tokens; no paid SaaS dependency. The Helm install is one command against any cluster.
- **Working `helm install` story** — *in place*. Chart is published to GHCR as OCI; chart README documents the install.
- **Responsive-maintainer contact** — *in place*. `repo.ini`, `SECURITY.md`, and `README.md` all expose `oss@xmv.de`. SLA stated in `SECURITY.md` (48h acknowledge / 7d assessment).

---

## 3. Concrete next steps, by impact

Ordered by likely review impact, smallest item first when impact ties.

| # | Step | Effort | Owner | Blocks submission? |
|---|---|---|---|---|
| 1 | Fix `CONTRIBUTING.md` template artefacts (title + clone URL). | 5 min | Self | No, but obvious to any reviewer |
| 2 | Fix `SECURITY.md` supported-versions table to reflect the `v0.1.x` line and pre-1.0 policy. Replace `project-name` placeholder. | 10 min | Self | No |
| 3 | Run `.github/gh-scripts/setup-branch-protection.sh` so `main` is actually protected and the required checks (`lint,test,go`) are enforced. | 5 min | Self | No, but reviewers check this |
| 4 | Add `topics` (`cert-manager`, `hetzner`, `dns01`, `kubernetes`, `webhook`, `acme`) and `homepage` to the GitHub repo via `gh repo edit`. | 5 min | Self | No |
| 5 | Register the chart repository on ArtifactHub (one form; points at `oci://ghcr.io/xmv-solutions-gmbh/charts`). Add the resulting badge to the README. | 15 min | Self (ArtifactHub side is self-service) | No, but expected by listings |
| 6 | Submit OpenSSF Best Practices self-assessment at `bestpractices.coreinfrastructure.org` and add the badge to the README. Aim for passing tier on first submission. | 1–2 hours | Self | No, but the badge is the standard "trustworthy OSS" signal |
| 7 | Add `.github/workflows/scorecard.yml` (OpenSSF Scorecard). Publish results, add badge. | 30 min | Self | No |
| 8 | Add a "Built for Hetzner Cloud" callout near the top of the README. Two sentences. | 5 min | Self | No, but strengthens the Hetzner pitch |
| 9 | Open PR against `cert-manager/website` to add this webhook to the community-webhooks list. | 30 min + upstream review time | Self (submit) + cert-manager maintainers (merge) | No |
| 10 | Decide on coverage upload: either wire `codecov/codecov-action@v5` into the `go` job and set `CODECOV_TOKEN`, or remove `CODECOV_TOKEN_SECRET` from `repo.ini`. | 30 min | Self | No |
| 11 | Add `MAINTAINERS.md` (or `GOVERNANCE.md`) — one paragraph stating that XMV Solutions GmbH maintains the project, with the contact route already in `SECURITY.md`. | 15 min | Self | No |
| 12 | Add a `reuse lint` step to the `lint` CI job so SPDX coverage cannot regress. | 30 min | Self | No |
| 13 | Submit application to the Hetzner OSS programme using the form/process linked from their public OSS page. | 30 min | Self + Hetzner-side review | This is the submission |

---

## 4. Submission checklist

Pragmatic ready-to-submit gate. Tick every box before sending the application.

- [ ] `repo.ini` reflects the live repository (done in this branch).
- [ ] `CONTRIBUTING.md` no longer references the template (step 1).
- [ ] `SECURITY.md` supported-versions table matches the actual release line (step 2).
- [ ] `main` branch protection enforced with `lint,test,go` checks (step 3).
- [ ] GitHub repo has `topics` and `homepage` set (step 4).
- [ ] At least one tagged release with image, chart, signature, and SBOM all present on GHCR (already true at `v0.1.4`).
- [ ] Chart registered on ArtifactHub with a green status badge (step 5).
- [ ] OpenSSF Best Practices badge at *passing* tier or higher, linked from README (step 6).
- [ ] README leads with the Hetzner-Cloud-Zones value proposition (already true; reinforce with step 8 if you have five minutes).
- [ ] Maintainer contact in three places: README, `SECURITY.md`, `repo.ini` (already true).
- [ ] `helm install` works against a fresh cluster using only public artefacts (already true at `v0.1.4`; smoke-test once more before submission).
- [ ] Hetzner-side application form completed (step 13).

Items 1–4 and 8 are <30 minutes of total work and remove every "lazy maintainer" signal a reviewer might pick up on. Items 5 and 6 (ArtifactHub + OpenSSF badge) are the two highest-leverage additions for any OSS-marketplace listing and should land **before** the Hetzner application goes in.

---

## 5. Top three must-do before submission

1. **Fix the template residue** in `CONTRIBUTING.md` and `SECURITY.md`. A reviewer who opens either file currently sees boilerplate that contradicts the rest of the project.
2. **Earn and display the OpenSSF Best Practices badge** at passing tier. This is the single most widely recognised "trustworthy OSS" signal in the infrastructure space and costs an afternoon.
3. **Register the Helm chart on ArtifactHub**. The chart already carries the right annotations; what's missing is the one-time repository registration. Without it, the project is invisible to the Kubernetes-package discoverability surface that any Hetzner-Cloud user installing a webhook would consult first.
