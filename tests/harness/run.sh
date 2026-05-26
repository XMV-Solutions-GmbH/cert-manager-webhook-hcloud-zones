#!/usr/bin/env bash
# SPDX-License-Identifier: MIT OR Apache-2.0
# SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
# SPDX-FileContributor: David Koller <david.koller@xmv.de>
#
# tests/harness/run.sh — bring-your-own-kubeconfig harness runner for
# the cert-manager-webhook-hcloud-zones project.
#
# Reads its inputs from environment variables (kubeconfig path, two
# Hetzner Cloud API tokens, three DNS zones), installs cert-manager
# and this project's webhook Helm chart from their published OCI /
# repository sources, applies the test-app manifests under
# tests/harness/test-apps/ (with envsubst-driven placeholder
# substitution), and asserts that three Let's Encrypt STAGING
# certificates reach Ready=True with the expected issuer + SANs +
# materialised Secrets.
#
# Operational principle — harness state is debugging context: on any
# assertion failure the script leaves all resources in place for
# inspection. The --cleanup flag is opt-in and is honoured ONLY when
# every assertion has passed; on failure the flag is silently
# ignored. See issue #2 for the full rationale.
#
# Exit codes:
#   0  all certificates reached Ready=True and all assertions passed
#   1  setup failure (missing env, missing tooling, helm install
#      failed, kubeconfig unreachable, manifest apply failed, etc.)
#   2  assertion failure (a Certificate never reached Ready, or one
#      of the post-Ready assertions on issuer / SANs / Secret failed)
#
# Vendor-neutrality: this script consumes a kubeconfig and a set of
# Hetzner credentials/zones via environment variables. It makes no
# assumption about how the cluster was provisioned (kind, k3d, k3s,
# managed service, BYO — all equivalent), and it does not reference
# any operator-specific paths, repos or zone names.

set -euo pipefail

# ---------------------------------------------------------------------------
# Constants
# ---------------------------------------------------------------------------

# cert-manager Helm chart: pinned to the current latest-stable release
# at the time this script was written. Renovate keeps this current
# (see issue #2 § Open questions — "Latest stable, pinned"). No
# legacy-version support.
readonly CERT_MANAGER_CHART_VERSION="v1.20.2"
readonly CERT_MANAGER_HELM_REPO_NAME="jetstack"
readonly CERT_MANAGER_HELM_REPO_URL="https://charts.jetstack.io"
readonly CERT_MANAGER_NAMESPACE="cert-manager"

# This project's webhook Helm chart, installed from its published
# OCI registry release. The chart MUST be published to GHCR before
# this script can succeed — see issue #2 § "production-realistic
# install path". Until the v0.1.0 chart lands, the helm install
# step below will fail with a clear "chart not yet released"
# message, surfacing the external blocker without masking it.
readonly WEBHOOK_CHART_OCI="oci://ghcr.io/xmv-solutions-gmbh/charts/cert-manager-webhook-hcloud-zones"
readonly WEBHOOK_CHART_VERSION="0.1.2"
readonly WEBHOOK_RELEASE_NAME="cert-manager-webhook-hcloud-zones"

# Namespace where the harness test-app + Certificates + ClusterIssuer
# accessory resources live. The ClusterIssuer itself is cluster-scoped
# but its referenced apiTokenSecretRef Secrets must live in the
# cert-manager namespace (cert-manager's resolver looks there for
# ClusterIssuer-referenced Secrets by convention).
readonly TEST_APP_NAMESPACE="default"

# Overall timeout for all three Certificates to reach Ready=True.
# 10 minutes is generous: DNS-01 challenges typically resolve well
# within 2-3 minutes once propagation completes; the extra headroom
# absorbs slow Hetzner DNS rollout + LE staging order processing.
readonly CERT_READY_TIMEOUT_SECONDS=600

# Directory holding the test-app manifests (sub-task 2-1's output).
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
readonly SCRIPT_DIR
readonly TEST_APPS_DIR="${SCRIPT_DIR}/test-apps"

# Manifests to envsubst-render and apply, in dependency order. The
# ClusterIssuer must exist before the Certificates reference it; the
# Pod + Service + Ingress are the accessory backend (see test-apps/
# README context).
readonly MANIFESTS=(
  "cluster-issuer.yaml"
  "pod.yaml"
  "service.yaml"
  "ingress.yaml"
  "certificate-a.yaml"
  "certificate-b1.yaml"
  "certificate-b2.yaml"
)

# Certificate resource names — kept in lockstep with the manifest set
# (sub-task 2-1). The script asserts each one reaches Ready=True.
readonly CERT_NAMES=("app-a" "app-b1" "app-b2")

# ---------------------------------------------------------------------------
# Logging helpers
# ---------------------------------------------------------------------------

log() {
  printf '[%s] %s\n' "$(date -u +%H:%M:%SZ)" "$*" >&2
}

err() {
  printf '[%s] ERROR: %s\n' "$(date -u +%H:%M:%SZ)" "$*" >&2
}

die_setup() {
  err "$*"
  exit 1
}

die_assert() {
  err "$*"
  exit 2
}

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------

CLEANUP_REQUESTED=0

usage() {
  cat >&2 <<'USAGE'
Usage: tests/harness/run.sh [--cleanup] [--help]

Required environment variables:
  HARNESS_KUBECONFIG       Path to a kubeconfig pointing at the target
                           cluster. The script never modifies the file.
  HCLOUD_TOKEN_PROJECT_A   Hetzner Cloud API token for project A.
  HCLOUD_TOKEN_PROJECT_B   Hetzner Cloud API token for project B.
  HARNESS_ZONE_A           DNS zone in Hetzner project A (e.g. zone-a.example.com).
  HARNESS_ZONE_B1          First DNS zone in Hetzner project B.
  HARNESS_ZONE_B2          Second DNS zone in Hetzner project B (same token as B1).

Options:
  --cleanup                Delete test resources after a fully-green run.
                           Silently ignored on any assertion failure
                           (harness state is debugging context).
  --help                   Print this message and exit 0.

Exit codes:
  0   success
  1   setup failure
  2   assertion failure
USAGE
}

while [[ $# -gt 0 ]]; do
  case "$1" in
    --cleanup)
      CLEANUP_REQUESTED=1
      shift
      ;;
    --help|-h)
      usage
      exit 0
      ;;
    *)
      err "unknown argument: $1"
      usage
      exit 1
      ;;
  esac
done

readonly CLEANUP_REQUESTED

# ---------------------------------------------------------------------------
# Environment validation
# ---------------------------------------------------------------------------

REQUIRED_ENV=(
  HARNESS_KUBECONFIG
  HCLOUD_TOKEN_PROJECT_A
  HCLOUD_TOKEN_PROJECT_B
  HARNESS_ZONE_A
  HARNESS_ZONE_B1
  HARNESS_ZONE_B2
)

missing_env=()
for var in "${REQUIRED_ENV[@]}"; do
  if [[ -z "${!var:-}" ]]; then
    missing_env+=("${var}")
  fi
done

if (( ${#missing_env[@]} > 0 )); then
  err "missing required environment variables: ${missing_env[*]}"
  err "see 'tests/harness/run.sh --help' for the full list."
  exit 1
fi

if [[ ! -r "${HARNESS_KUBECONFIG}" ]]; then
  die_setup "HARNESS_KUBECONFIG points at unreadable path: ${HARNESS_KUBECONFIG}"
fi

# Generate a run-id used to suffix every test FQDN, preventing
# collisions between concurrent harness fires against the same zones.
# Format: <UTC timestamp>-<6 hex chars>, e.g. 20260525t153012-a1b2c3.
# All-lowercase so the value is RFC-1123 compliant when embedded into
# DNS hostnames downstream (Ingress validation rejects upper-case 'T').
if ! command -v openssl >/dev/null 2>&1; then
  die_setup "openssl not found on PATH (required to generate RUN_ID suffix)."
fi
RUN_ID="$(date -u +%Y%m%dt%H%M%S)-$(openssl rand -hex 3)"
export RUN_ID
log "RUN_ID=${RUN_ID}"

# ---------------------------------------------------------------------------
# Tooling preflight
# ---------------------------------------------------------------------------

for bin in kubectl helm envsubst; do
  if ! command -v "${bin}" >/dev/null 2>&1; then
    die_setup "required binary not found on PATH: ${bin}"
  fi
done

# All kubectl/helm invocations route through this kubeconfig. We use
# KUBECONFIG via the environment rather than --kubeconfig flags so
# that any sub-tool helm spawns inherits it too.
export KUBECONFIG="${HARNESS_KUBECONFIG}"

log "verifying kubeconfig reachability (read-only cluster-info)…"
if ! kubectl cluster-info >/dev/null 2>&1; then
  die_setup "kubectl cluster-info failed; cannot reach the cluster via HARNESS_KUBECONFIG."
fi

# ---------------------------------------------------------------------------
# State tracking for the cleanup decision
# ---------------------------------------------------------------------------

# Set to 1 once every assertion has passed. The cleanup path is taken
# ONLY when CLEANUP_REQUESTED=1 AND ALL_GREEN=1 — the harness state
# is debugging context (issue #2 § "Operational principle").
ALL_GREEN=0

# ---------------------------------------------------------------------------
# Step 1 — install cert-manager
# ---------------------------------------------------------------------------

log "ensuring helm repo '${CERT_MANAGER_HELM_REPO_NAME}' (${CERT_MANAGER_HELM_REPO_URL}) is configured…"
if ! helm repo add "${CERT_MANAGER_HELM_REPO_NAME}" "${CERT_MANAGER_HELM_REPO_URL}" >/dev/null 2>&1; then
  # `helm repo add` exits non-zero if the name is already in use with
  # a different URL; we tolerate the already-exists case via `helm
  # repo update` below, which would surface a real connectivity error.
  log "helm repo add reported the repo already exists or could not be added; continuing."
fi
helm repo update "${CERT_MANAGER_HELM_REPO_NAME}" >/dev/null

log "installing cert-manager ${CERT_MANAGER_CHART_VERSION} into namespace '${CERT_MANAGER_NAMESPACE}'…"
if ! helm upgrade --install cert-manager \
      "${CERT_MANAGER_HELM_REPO_NAME}/cert-manager" \
      --namespace "${CERT_MANAGER_NAMESPACE}" \
      --create-namespace \
      --version "${CERT_MANAGER_CHART_VERSION}" \
      --set installCRDs=true \
      --set "extraArgs={--dns01-recursive-nameservers-only=true,--dns01-recursive-nameservers=1.1.1.1:53\,9.9.9.9:53}" \
      --wait \
      --timeout 5m; then
  die_setup "helm upgrade --install cert-manager failed."
fi

# Rationale for `--dns01-recursive-nameservers-only`:
#
# cert-manager's default DNS-01 self-check walks the FQDN up to the zone
# apex, discovers the zone's authoritative nameservers, and queries each
# of them directly with RD=1 for the expected TXT record. In the harness
# we use a Hetzner Cloud DNS zone (`xmv-solutions.de`) whose registry
# delegation actually points at Hetzner *Robot* DNS hostnames
# (`ns1.first-ns.de`, `robotns2.second-ns.de`, `robotns3.second-ns.com`)
# rather than the Cloud-DNS hostnames (`hydrogen.ns.hetzner.com`, …).
# Empirically, that combination — Robot-NS-delegated zone PLUS a
# wildcard `*.<zone> CNAME …` in the same zone — makes the authoritative
# self-check hang in an infinite "not yet propagated" loop even though
# every public recursive resolver returns the TXT record correctly.
# Switching to a recursive-only check (Cloudflare + Quad9) sidesteps the
# pathology, leaves the LE-side validation flow unchanged, and is
# the configuration the README will recommend for any harness consumer
# whose zones share that shape.

# ---------------------------------------------------------------------------
# Step 2 — install cert-manager-webhook-hcloud-zones (this project)
# ---------------------------------------------------------------------------

log "installing ${WEBHOOK_RELEASE_NAME} ${WEBHOOK_CHART_VERSION} from ${WEBHOOK_CHART_OCI}…"
if ! helm upgrade --install "${WEBHOOK_RELEASE_NAME}" \
      "${WEBHOOK_CHART_OCI}" \
      --version "${WEBHOOK_CHART_VERSION}" \
      --namespace "${CERT_MANAGER_NAMESPACE}" \
      --wait \
      --timeout 5m; then
  err "helm upgrade --install ${WEBHOOK_RELEASE_NAME} failed."
  err "if the failure mode is 'manifest unknown' / 'chart not found',"
  err "the chart at ${WEBHOOK_CHART_OCI}:${WEBHOOK_CHART_VERSION} is not yet published."
  err "this is the documented external blocker from issue #2 sub-task 2-2 —"
  err "CI must build and publish the chart to GHCR before the harness can run."
  exit 1
fi

# ---------------------------------------------------------------------------
# Step 3 — create the two Hetzner Cloud API token Secrets
# ---------------------------------------------------------------------------
#
# Secrets live in the cert-manager namespace so that the
# ClusterIssuer's apiTokenSecretRef entries resolve. They are
# applied via stdin (kubectl create --dry-run=client -o yaml |
# kubectl apply -f -) so we never write tokens to disk and the
# command is idempotent across re-runs.

apply_token_secret() {
  local secret_name="$1" token_value="$2"
  log "applying Secret ${CERT_MANAGER_NAMESPACE}/${secret_name}…"
  if ! kubectl create secret generic "${secret_name}" \
        --namespace "${CERT_MANAGER_NAMESPACE}" \
        --from-literal=token="${token_value}" \
        --dry-run=client -o yaml \
        | kubectl apply -f - >/dev/null; then
    die_setup "failed to apply Secret ${secret_name}."
  fi
}

apply_token_secret "hcloud-token-project-a" "${HCLOUD_TOKEN_PROJECT_A}"
apply_token_secret "hcloud-token-project-b" "${HCLOUD_TOKEN_PROJECT_B}"

# ---------------------------------------------------------------------------
# Step 4 — render and apply test-app manifests via envsubst
# ---------------------------------------------------------------------------

if [[ ! -d "${TEST_APPS_DIR}" ]]; then
  die_setup "test-apps directory not found: ${TEST_APPS_DIR}"
fi

# Whitelist the variables envsubst is allowed to expand. Without this
# any literal $VAR in a manifest would be greedily substituted from
# the surrounding shell environment, which is a footgun if a manifest
# ever uses shell-syntax-looking strings for unrelated reasons. The
# single quotes are deliberate — envsubst is the consumer, not the
# shell, so the `${...}` tokens must stay literal here.
# shellcheck disable=SC2016
ENVSUBST_VARS='${RUN_ID} ${HARNESS_ZONE_A} ${HARNESS_ZONE_B1} ${HARNESS_ZONE_B2}'

for manifest in "${MANIFESTS[@]}"; do
  manifest_path="${TEST_APPS_DIR}/${manifest}"
  if [[ ! -f "${manifest_path}" ]]; then
    die_setup "manifest missing: ${manifest_path}"
  fi
  log "applying manifest ${manifest} (envsubst-rendered)…"
  if ! envsubst "${ENVSUBST_VARS}" < "${manifest_path}" \
        | kubectl apply -n "${TEST_APP_NAMESPACE}" -f - >/dev/null; then
    die_setup "failed to apply manifest ${manifest}."
  fi
done

# ---------------------------------------------------------------------------
# Step 5 — wait for each Certificate to reach Ready=True
# ---------------------------------------------------------------------------
#
# Single shared 10-minute budget across all three certificates. We
# compute the remaining budget before each wait so that a slow first
# cert eats into the budget of the next two — failing fast rather
# than allowing the total wall clock to balloon to 3 × 10min.

start_epoch=$(date -u +%s)
deadline_epoch=$(( start_epoch + CERT_READY_TIMEOUT_SECONDS ))

for cert_name in "${CERT_NAMES[@]}"; do
  now_epoch=$(date -u +%s)
  remaining=$(( deadline_epoch - now_epoch ))
  if (( remaining <= 0 )); then
    die_assert "ran out of time budget before checking Certificate '${cert_name}' (10-minute shared budget exhausted)."
  fi
  log "waiting up to ${remaining}s for Certificate ${TEST_APP_NAMESPACE}/${cert_name} to become Ready=True…"
  if ! kubectl wait --namespace "${TEST_APP_NAMESPACE}" \
        --for=condition=Ready=true \
        --timeout="${remaining}s" \
        "certificate/${cert_name}" >/dev/null; then
    die_assert "Certificate ${cert_name} did not reach Ready=True within the time budget."
  fi
  log "Certificate ${cert_name} is Ready."
done

# ---------------------------------------------------------------------------
# Step 6 — assert issuer (STAGING), SANs, and Secret materialisation
# ---------------------------------------------------------------------------
#
# For each Certificate:
#   - look up the spec.secretName,
#   - decode tls.crt from the Secret,
#   - parse the certificate Issuer and SANs via openssl,
#   - check Issuer subject contains "STAGING" (LE staging CA — we
#     never accept a prod-issued cert in the harness),
#   - check SAN list contains exactly the expected FQDN,
#   - check tls.key decodes as a valid private key.

# Expected FQDN per cert, in lockstep with CERT_NAMES.
declare -A EXPECTED_FQDN=(
  ["app-a"]="app-a-${RUN_ID}.${HARNESS_ZONE_A}"
  ["app-b1"]="app-b1-${RUN_ID}.${HARNESS_ZONE_B1}"
  ["app-b2"]="app-b2-${RUN_ID}.${HARNESS_ZONE_B2}"
)

assert_cert() {
  local cert_name="$1" expected_fqdn="$2"

  local secret_name
  secret_name="$(kubectl get certificate "${cert_name}" \
                 --namespace "${TEST_APP_NAMESPACE}" \
                 -o jsonpath='{.spec.secretName}')"
  if [[ -z "${secret_name}" ]]; then
    die_assert "Certificate ${cert_name}: spec.secretName is empty."
  fi

  local tls_crt_b64 tls_key_b64
  tls_crt_b64="$(kubectl get secret "${secret_name}" \
                 --namespace "${TEST_APP_NAMESPACE}" \
                 -o jsonpath='{.data.tls\.crt}')"
  tls_key_b64="$(kubectl get secret "${secret_name}" \
                 --namespace "${TEST_APP_NAMESPACE}" \
                 -o jsonpath='{.data.tls\.key}')"
  if [[ -z "${tls_crt_b64}" || -z "${tls_key_b64}" ]]; then
    die_assert "Certificate ${cert_name}: Secret ${secret_name} is missing tls.crt or tls.key."
  fi

  local tls_crt_pem tls_key_pem
  tls_crt_pem="$(printf '%s' "${tls_crt_b64}" | base64 -d 2>/dev/null || true)"
  tls_key_pem="$(printf '%s' "${tls_key_b64}" | base64 -d 2>/dev/null || true)"
  if [[ -z "${tls_crt_pem}" || -z "${tls_key_pem}" ]]; then
    die_assert "Certificate ${cert_name}: failed to base64-decode tls.crt or tls.key."
  fi

  local issuer
  if ! issuer="$(printf '%s' "${tls_crt_pem}" | openssl x509 -noout -issuer 2>/dev/null)"; then
    die_assert "Certificate ${cert_name}: tls.crt is not a parseable X.509 certificate."
  fi
  # The LE staging intermediate Common Name historically contains the
  # word "STAGING" (e.g. "(STAGING) Artificial Apricot R3"). Matching
  # on that substring is robust to LE rotating their intermediate
  # naming so long as they keep the staging marker.
  if ! grep -qi 'STAGING' <<<"${issuer}"; then
    die_assert "Certificate ${cert_name}: issuer does not look like LE staging — got: ${issuer}"
  fi

  local sans
  sans="$(printf '%s' "${tls_crt_pem}" \
          | openssl x509 -noout -ext subjectAltName 2>/dev/null \
          || true)"
  if ! grep -q "DNS:${expected_fqdn}\b" <<<"${sans}"; then
    die_assert "Certificate ${cert_name}: SAN list does not contain expected FQDN '${expected_fqdn}'. Got: ${sans}"
  fi

  # Validate the private key parses. We don't pin the algorithm here
  # (manifests set ECDSA P-256, but accepting any well-formed key
  # keeps the assertion resilient to future manifest tweaks).
  if ! printf '%s' "${tls_key_pem}" | openssl pkey -noout 2>/dev/null; then
    die_assert "Certificate ${cert_name}: tls.key is not a parseable private key."
  fi

  log "Certificate ${cert_name}: assertions passed (issuer=staging, SAN=${expected_fqdn}, key=valid)."
}

for cert_name in "${CERT_NAMES[@]}"; do
  assert_cert "${cert_name}" "${EXPECTED_FQDN[${cert_name}]}"
done

ALL_GREEN=1
log "all 3 certificates Ready and all assertions passed."

# ---------------------------------------------------------------------------
# Step 7 — cleanup decision
# ---------------------------------------------------------------------------
#
# Per issue #2 § "Operational principle: harness state is debugging
# context":
#
#   - --cleanup AND all green → delete test resources (kept here:
#     ClusterIssuer + the per-fire Certificates + accessory test-app
#     Pod / Service / Ingress; deliberately KEEP cert-manager + the
#     webhook chart + the token Secrets so the cluster stays warm
#     for subsequent fires).
#   - any other path → leave everything for inspection.
#
# We only reach this point when ALL_GREEN=1 (assertion failures exit
# early via die_assert). The CLEANUP_REQUESTED guard still applies.

if (( CLEANUP_REQUESTED == 1 && ALL_GREEN == 1 )); then
  log "--cleanup requested and run is fully green; deleting per-fire test resources…"
  for manifest in "${MANIFESTS[@]}"; do
    manifest_path="${TEST_APPS_DIR}/${manifest}"
    log "deleting resources from ${manifest}…"
    envsubst "${ENVSUBST_VARS}" < "${manifest_path}" \
      | kubectl delete -n "${TEST_APP_NAMESPACE}" --ignore-not-found=true -f - >/dev/null \
      || log "warning: best-effort delete of ${manifest} reported an error; continuing."
  done
  log "cleanup complete (cert-manager + webhook chart + token Secrets retained)."
else
  if (( CLEANUP_REQUESTED == 0 )); then
    log "--cleanup not requested; leaving all test resources in place."
  fi
  # The CLEANUP_REQUESTED=1 + ALL_GREEN=0 branch is unreachable here
  # because failure exits earlier — left implicit for clarity.
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------

log "SUMMARY"
log "  RUN_ID         : ${RUN_ID}"
log "  zone A         : ${HARNESS_ZONE_A}        cert: app-a-${RUN_ID}.${HARNESS_ZONE_A}"
log "  zone B1        : ${HARNESS_ZONE_B1}       cert: app-b1-${RUN_ID}.${HARNESS_ZONE_B1}"
log "  zone B2        : ${HARNESS_ZONE_B2}       cert: app-b2-${RUN_ID}.${HARNESS_ZONE_B2}"
log "  cleanup        : $(( CLEANUP_REQUESTED == 1 && ALL_GREEN == 1 ? 1 : 0 ))"
log "  result         : PASS"
exit 0
