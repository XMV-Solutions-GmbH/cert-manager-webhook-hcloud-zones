# OSS Project Template

## Vision

A production-ready template for professional open source projects with AI-assisted development support. Every repository created from this template should be ready for world-class open source development from day one.

## Problem Statement

Starting a new open source project requires significant boilerplate: licence files, contribution guidelines, CI/CD workflows, security policies, and AI instructions. Teams waste time recreating these structures for each new project.

## Target Audience

- Developers starting new open source projects
- Organisations publishing open source software with professional standards
- Teams using AI-assisted development (any agent — Codex, Claude Code, Copilot, Cursor, …)

## Core Features

- [x] Complete OSS project structure (README, CONTRIBUTING, LICENCE, etc.)
- [x] Tool-agnostic AI-agent brief (`AGENTS.md`) consumed by Codex, Claude Code, Copilot, etc.
- [x] GitHub bootstrap scripts (branch protection, team assignment) driven by `repo.ini`
- [x] Dual licence (MIT OR Apache-2.0) for maximum compatibility
- [x] Comprehensive documentation templates

## Architecture Overview

### Repository Structure

```text
.
├── .github/
│   ├── copilot-instructions.md    # 5-line pointer → AGENTS.md
│   ├── CODEOWNERS                 # Code review assignment
│   ├── ISSUE_TEMPLATE/            # Bug report & feature request templates
│   ├── PULL_REQUEST_TEMPLATE.md   # PR template with checklist
│   ├── gh-scripts/                # One-shot bootstrap scripts (assign team + branch protection)
│   └── workflows/                 # CI/CD pipelines
├── docs/
│   ├── app-concept.md             # Project concept (this file)
│   ├── howto-oss.md               # OSS setup guide
│   ├── markdown-style.md          # Markdown linting rules
│   ├── proposals/                 # RFCs / architectural decisions
│   └── testconcept.md             # Testing strategy
├── scripts/                       # Operational scripts
├── tests/
│   └── run_tests.sh               # Single test entry point
├── AGENTS.md                      # Canonical AI-agent brief
├── CHANGELOG.md                   # Version history
├── CLAUDE.md                      # Pointer to AGENTS.md
├── CODE_OF_CONDUCT.md             # Community standards
├── CONTRIBUTING.md                # Contribution guidelines
├── ENGINEERING_PRINCIPLES.md      # Project-agnostic engineering baseline
├── LICENSE                        # MIT licence
├── LICENSE-APACHE                 # Apache 2.0 licence
├── LICENSE-MIT                    # MIT licence
├── README.md                      # Project overview
├── repo.ini                       # Project configuration
└── SECURITY.md                    # Security policy

> Backlog and resolved-issue records live in **GitHub Issues + the repo-bound GitHub Project** (see [`ENGINEERING_PRINCIPLES.md` § 2](../ENGINEERING_PRINCIPLES.md)), not in markdown files.
```

## Tech Stack

| Component | Technology | Rationale |
| --------- | ---------- | --------- |
| Scripts | Bash | Universal availability on dev machines |
| CI/CD | GitHub Actions | Native GitHub integration |
| Testing | bats-core | Established bash testing framework |
| Linting | markdownlint | Consistent documentation |
| AI | GitHub Copilot | Primary AI assistant target |

## Non-Functional Requirements

- **Idempotent scripts** — Safe to re-run without side effects
- **No external dependencies** — Works with standard Unix tools + gh CLI
- **British English** — All artefacts use en-GB spelling
