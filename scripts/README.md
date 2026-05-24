<!-- SPDX-License-Identifier: MIT OR Apache-2.0 -->
# `scripts/`

Reproducible operational scripts. Per [`ENGINEERING_PRINCIPLES.md` § 8](../ENGINEERING_PRINCIPLES.md):

> Any environment setup, tool install, or one-off operational step must be **scripted and committed to the repo**, never run ad-hoc on a live shell.

## What goes here

| Pattern | Purpose |
|---|---|
| `scripts/setup-dev-env.sh` | One-shot dev environment install. Idempotent. Supports the maintainer's primary OSes. |
| `scripts/bootstrap-<thing>.sh` | One-shot infra / cluster / database bootstrap. Idempotent. |
| `scripts/<task>.sh` | Routine operations: backup, restore, smoke test, secret rotation, harness-token refresh, …. Idempotent where the underlying operation allows. |

## Conventions

- **Shebang first**, then the SPDX header.
- **`set -euo pipefail`** at the top of every Bash script — fail fast on errors / unset vars / failed pipes.
- **Idempotent by default**. Re-running the script against an already-configured environment is a no-op or a noisy-but-safe re-apply, never a corruption.
- **Self-documenting**. Every script starts with a short comment block describing what it does, what prerequisites it expects, what it will mutate, and what env vars it reads.
- **No hidden state**. If a script needs a secret, it reads it from a named env var or a documented file path — never from `~/.something_undocumented`.
- **Argument parsing** with `getopts` or a simple `case` statement. Print a `--help` summary if invoked with no args.
- **Exit codes**: `0` = success, non-zero = failure. Never `exit 0` on a known-bad path.

## What does NOT go here

- Build artefacts or generated files.
- Source code that's part of the application — that goes under the language's normal layout (`src/`, `pkg/`, etc.).
- Long-lived ops runbooks — those go in `docs/`. A runbook *can* link to a script, but the script is the authoritative version of any automatable step.

## Why this exists at template level

If a step isn't scripted, the next person to need it has to re-derive it. That's a slow-moving outage. The template ships an empty `scripts/` directory with this README so the convention is visible from day one — no project ever has to ask "where do operational scripts go?".

Delete this README only after the project has at least one real script in here. Until then, it's the only thing telling future contributors where to put their next bootstrap.
