<!-- SPDX-License-Identifier: MIT OR Apache-2.0 -->
# Harness job snippet

Drop the YAML below into `ci.yml` once the project has a real external sandbox to test against. The harness layer is the **gate** per [`ENGINEERING_PRINCIPLES.md` § 5](../../ENGINEERING_PRINCIPLES.md): no feature ticket lands before this is green.

The snippet is structured to match the rest of `ci.yml`. Two design choices are load-bearing:

- **`needs: test`** — run only after lint and test pass, so we don't burn an external-API call when the basics are red.
- **`if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name == github.repository`** — skip silently on PRs from forks. Forks don't get repo secrets, so a fork PR running this job would fail with no useful signal. The skip-silently pattern is what the sister projects (sharepoint-mcp, outlook-mcp) settled on so external contributions aren't blocked on something they can't provide.

Inside the job: restore the credential from a base64-encoded repo secret, write it where the harness expects, run the harness suite. The "skip when secret is empty" early-exit means a freshly-cloned project with no secret configured still lands in the green path until the maintainer flips the harness on.

```yaml
  harness:
    runs-on: ubuntu-latest
    needs: test
    if: github.event_name == 'push' || github.event.pull_request.head.repo.full_name == github.repository
    steps:
      - uses: actions/checkout@v6

      # Replace the language-setup steps with the project's stack.
      # Example for Python:
      # - uses: actions/setup-python@v6
      #   with:
      #     python-version: "3.11"
      # - uses: astral-sh/setup-uv@v7
      # - run: uv sync --extra dev

      - name: Restore harness credential from repo secret
        env:
          # Project-specific secret name. Sister projects use
          # SHAREPOINT_HARNESS_TOKEN_JSON / OUTLOOK_HARNESS_TOKEN_JSON.
          HARNESS_CRED_B64: ${{ secrets.HARNESS_CRED_B64 }}
        run: |
          if [ -z "$HARNESS_CRED_B64" ]; then
            echo "::warning::HARNESS_CRED_B64 not set — harness tests will be skipped."
            exit 0
          fi
          mkdir -p ~/.cache/<project-name>/harness
          printf '%s' "$HARNESS_CRED_B64" | base64 -d > ~/.cache/<project-name>/harness/cred.json
          chmod 600 ~/.cache/<project-name>/harness/cred.json
          echo "Harness credential restored ($(wc -c < ~/.cache/<project-name>/harness/cred.json) bytes)."

      - name: Run harness tests against the real external system
        run: ./tests/run_tests.sh harness
```

## Required-status-checks update

When you flip the harness job on, add `harness` to `STATUS_CHECKS` in `repo.ini` and re-run `.github/gh-scripts/setup-branch-protection.sh` so branch protection actually requires it.

## Token rotation

If the credential is a refresh token, it rotates every ~60–90 days for most providers. Add a recurring chore in `CLAUDE.md` and a `scripts/renew-harness-token.sh` (per [`ENGINEERING_PRINCIPLES.md` § 8](../../ENGINEERING_PRINCIPLES.md)) so renewing it is a one-command flow before CI starts failing on its own.
