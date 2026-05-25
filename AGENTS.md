<!--
SPDX-License-Identifier: MIT OR Apache-2.0
SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
SPDX-FileContributor: David Koller <david.koller@xmv.de>
-->

# AGENTS.md — brief for AI coding agents

This is the canonical, tool-agnostic brief for AI agents working in this repo. Codex, Claude Code, GitHub Copilot, Cursor, Aider, and every other coding agent read this same file. The tool-specific files at the conventional locations (`CLAUDE.md`, `.github/copilot-instructions.md`) are five-line pointers back here — don't expect content there.

## Reading order

1. **This file** — what you're reading. AI-specific behaviour for this repo, plus project-specific facts (tech stack, overrides, header values).
2. **[`ENGINEERING_PRINCIPLES.md`](ENGINEERING_PRINCIPLES.md)** — XMV's project-agnostic engineering baseline. Same in every XMV OSS project. Read it in full: language, status workflow, three test layers, source-control rules, CI vigilance, doc-mirrors-repo, source-of-truth, PR discipline, licensing.
3. **[`README.md`](README.md)** — what this project is, for an end user.
4. **[`docs/app-concept.md`](docs/app-concept.md)** — the product/architecture/scope spec.

Anything that applies to **humans too** lives in `ENGINEERING_PRINCIPLES.md`, not here. This file is for the failure modes and operational rules that are specific to AI agents.

---

## Project facts

> **Template note.** Replace the `<PLACEHOLDER>` values in this section once the new repo has its first PR or first published release.

### What this repo is

`<PROJECT_NAME>` — one-sentence description.

Full vision and scope in [`docs/app-concept.md`](docs/app-concept.md). Read it before changing anything that touches the public surface.

### Project-specific docs

| Doc | Purpose |
|---|---|
| [`docs/app-concept.md`](docs/app-concept.md) | Vision, MVP scope, public surface, Testability section, open questions |
| [`docs/testconcept.md`](docs/testconcept.md) | Per-project instantiation of the three test layers (unit / integration / harness) |
| [`docs/proposals/`](docs/proposals/) | RFCs / spike notes / architectural decisions too big for a single issue |
| [`docs/markdown-style.md`](docs/markdown-style.md) | Markdown linting rules — read only when producing or editing Markdown |
| [`README.md`](README.md) | Quickstart for end users |
| [`CONTRIBUTING.md`](CONTRIBUTING.md) | Contribution flow |
| [`SECURITY.md`](SECURITY.md) | Vulnerability disclosure |
| [`CHANGELOG.md`](CHANGELOG.md) | Keep-a-changelog history |

### Tracker

**GitHub Issues + the repo-bound GitHub Project** at `https://github.com/<ORG>/<REPO>/issues`. See [`ENGINEERING_PRINCIPLES.md` § 2](ENGINEERING_PRINCIPLES.md). No `docs/todo.md` or other markdown TODO files.

Recommended labels: `type:feat` / `type:fix` / `type:chore` / `type:docs` / `type:test`; `area:<component>`; `priority:p0` / `p1` / `p2`. Add `agent:<tool-name>` (e.g. `agent:claude`, `agent:codex`) when an AI agent is the executor.

Issue body convention: `## Context`, `## Acceptance criteria` (checkbox list), `## Out of scope`, `## Links`. Milestones map to releases (`v0.1.0 — MVP`, `v0.2.0`, …).

### Tech stack

<!-- Languages, frameworks, runtimes, package managers, lint/format
     tooling, test frameworks, deployment/distribution surface. -->

- TBD

### Project-specific overrides of the engineering baseline

<!-- Document deviations from ENGINEERING_PRINCIPLES.md here, with the
     paragraph reference and a one-line justification. -->

- TBD

### License header for new source files

This project is dual-licensed **MIT OR Apache-2.0**, copyright **XMV Solutions GmbH**. Generic SPDX rules in [`ENGINEERING_PRINCIPLES.md` § 11](ENGINEERING_PRINCIPLES.md); concrete examples for this project below.

For Python, Shell, YAML, TOML, and most languages with `#` line comments:

```text
# SPDX-License-Identifier: MIT OR Apache-2.0
# SPDX-FileCopyrightText: <year> XMV Solutions GmbH
# SPDX-FileContributor: <git user.name> <<git user.email>>
```

For languages with `//` line comments (Go, Rust, JS/TS, Java, …):

```text
// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: <year> XMV Solutions GmbH
// SPDX-FileContributor: <git user.name> <<git user.email>>
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

### Harness workflow — XMV-maintainer convention

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

This is the only XMV-specific reference in this otherwise organisation-neutral OSS file. It exists because re-discovery cost (search for the cluster-template repo, read its scripts, infer the workflow) is high enough that recording the convention is worth the small organisational mention. External users see a paragraph that explicitly says "ignore this section, the supported procedure is documented in app-concept.md §5.4."

---

## How to behave as an AI agent in this repo

These rules are AI-specific. Anything that applies to humans too lives in `ENGINEERING_PRINCIPLES.md` — read that first, then apply the additions below.

### Stop and ask before external actions

External actions are anything visible outside this working copy. Before any of these, **stop and confirm with the user** unless the user has explicitly authorised the action in the current task:

- `git push`, `git push --force`, deleting a remote branch.
- `gh pr create`, `gh pr merge`, closing or reopening issues, posting issue/PR comments.
- `git tag -a` followed by `git push origin <tag>`, anything that triggers a release pipeline.
- `gh secret set`, secret rotation, anything touching repository or organisation settings.
- `gh release create`, package publishing.
- Any external API call that mutates remote state (cloud APIs, SaaS APIs, Slack, email, …).

A user authorising one external action authorises that action's stated scope only — not the next external action.

### Initialisation gate

If `docs/app-concept.md` is missing **or** the repo's GitHub Project board has not been set up yet, **stop and prompt the user**. Do not begin implementation. Per [`ENGINEERING_PRINCIPLES.md` § 5](ENGINEERING_PRINCIPLES.md) the harness layer must also be green before feature tickets enter "Doing" — verify this gate too on the first feature ticket.

### Self-verification before claiming done

After every code change, run the test harness. After every push, watch CI to completion (`gh run watch <id> --exit-status`) and react if it goes red. Never claim a task complete without:

- Tests pass locally (unit + integration; harness if relevant).
- CI on the pushed commit is green.
- Tracking issue is closed via the PR (or status moved on the Project board).
- Docs reflect what shipped (per [`ENGINEERING_PRINCIPLES.md` § 15](ENGINEERING_PRINCIPLES.md)).
- No new `(TBD)` markers without a follow-up issue filed.

### When you edit Markdown

Read [`docs/markdown-style.md`](docs/markdown-style.md) **first**. Strict markdownlint rules apply and the CI lint job will reject violations. The Markdown style rules live in a separate file precisely so they don't load as context when you're working on code that doesn't touch Markdown.

### Documentation scaling threshold

If `docs/app-concept.md` plus the relevant supporting docs exceed roughly **50k tokens (~200 KB combined)**, split into a two-level structure:

1. Keep `docs/app-concept.md` as an index — vision, summary, table of contents with links.
2. Move thematic deep-dives into `docs/app-concept/*.md` chapters (e.g. `architecture.md`, `security.md`, `api-design.md`).

Rationale: AI agents should use ≤1/3 of their context window for project instructions, leaving room for code and conversation.

### Iteration protocol

When implementing a feature:

```text
1. User describes feature requirement.
2. AI files a GitHub Issue capturing the work
   (## Context / ## Acceptance criteria / ## Out of scope / ## Links).
3. AI writes failing tests in the test harness (TDD).
4. AI runs tests (expected: FAIL).
5. AI implements minimal code.
6. AI runs tests (expected: PASS).
7. AI refactors while keeping tests green.
8. AI opens a PR that closes the issue ("Closes #N" in the PR body).
9. AI watches CI to completion; reverts or fixes if red.
```

For non-trivial changes, log the design decision under [`docs/proposals/`](docs/proposals/) before writing code (see that folder's README for the lifecycle).

### Pre-completion checklist

Before declaring work done, verify each item:

- [ ] Tests pass locally (unit + integration; harness if relevant).
- [ ] CI on the pushed commit is green.
- [ ] Tracking issue is closed via the PR (or status moved on the Project board).
- [ ] Docs reflect what shipped — README, CHANGELOG, app-concept, secrets, architecture, whichever apply.
- [ ] Commit messages follow Conventional Commits (`feat(scope): subject`, etc.; see [`ENGINEERING_PRINCIPLES.md` § 6](ENGINEERING_PRINCIPLES.md)).
- [ ] No `Co-Authored-By: <AI tool>` lines, no AI tool names in source comments or SPDX headers (see [`ENGINEERING_PRINCIPLES.md` § 12](ENGINEERING_PRINCIPLES.md)).
- [ ] No new `(TBD)` markers without a follow-up issue filed.
