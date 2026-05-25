<!--
SPDX-License-Identifier: MIT OR Apache-2.0
SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
SPDX-FileContributor: David Koller <david.koller@xmv.de>
-->

# cert-manager-webhook-hcloud-zones

A Helm chart that installs the cert-manager DNS-01 challenge solver for the
[Hetzner Cloud Zones API](https://docs.hetzner.cloud/reference/cloud#zones).

This webhook is the successor to the legacy `cert-manager-webhook-hetzner` /
`cert-manager-webhook-hetzner-dns` charts that targeted the deprecated standalone
DNS Console API. It speaks the new Hetzner Cloud Zones API (in public beta as of
May 2026) and supports routing zones across multiple Hetzner Cloud projects with
separate API tokens.

## Prerequisites

- Kubernetes 1.27 or newer.
- [cert-manager](https://cert-manager.io/) v1.13 or newer installed in the
  cluster.
- A Hetzner Cloud project (or several) with the Zones API enabled and a token
  scoped to `Zone: Read & Write`.

## Installation

```bash
helm repo add xmv https://xmv-solutions-gmbh.github.io/cert-manager-webhook-hcloud-zones
helm repo update
helm install cert-manager-webhook-hcloud-zones \
  xmv/cert-manager-webhook-hcloud-zones \
  --namespace cert-manager \
  --create-namespace=false
```

Install directly from the source tree for testing:

```bash
helm install cert-manager-webhook-hcloud-zones \
  ./charts/cert-manager-webhook-hcloud-zones \
  --namespace cert-manager
```

## Configuration

| Key | Default | Description |
|---|---|---|
| `groupName` | `acme.hcloud-zones.xmv.de` | API group under which the webhook registers its APIService. Must match the `solver.webhook.groupName` referenced by every ACME Issuer that delegates DNS-01 challenges to this webhook. |
| `solverName` | `hcloud-zones` | Solver name reported by the webhook. Must match the `solver.webhook.solverName` on every ACME Issuer that targets this webhook. |
| `certManager.namespace` | `cert-manager` | Namespace where cert-manager is installed. |
| `certManager.serviceAccountName` | `cert-manager` | ServiceAccount cert-manager runs as. The chart grants this account `create` on the solver's challenge API group. |
| `image.repository` | `ghcr.io/xmv-solutions-gmbh/cert-manager-webhook-hcloud-zones` | Container image repository. |
| `image.tag` | `""` (defaults to `.Chart.AppVersion`) | Container image tag. |
| `image.pullPolicy` | `IfNotPresent` | Image pull policy. |
| `image.pullSecrets` | `[]` | List of image pull secrets (each entry an object with a `name` field). |
| `nameOverride` | `""` | Override the chart-name portion of resource names. |
| `fullnameOverride` | `""` | Override the full release-qualified resource name. |
| `replicaCount` | `1` | Number of webhook replicas. Two or more recommended for HA. |
| `service.type` | `ClusterIP` | Service type. |
| `service.port` | `443` | Service port the APIService aggregator targets. |
| `resources` | `requests: cpu 50m / mem 64Mi`, `limits: cpu 200m / mem 128Mi` | Container resource requests and limits. |
| `podSecurityContext` | non-root (uid 1000), `seccompProfile: RuntimeDefault` | Pod-level security context. |
| `securityContext` | `allowPrivilegeEscalation: false`, read-only root FS, drop all capabilities | Container-level security context. |
| `nodeSelector` | `{}` | Pod node selector. |
| `tolerations` | `[]` | Pod tolerations. |
| `affinity` | `{}` | Pod affinity. |
| `topologySpreadConstraints` | `[]` | Pod topology spread constraints. |
| `podAnnotations` | `{}` | Extra pod annotations. |
| `podLabels` | `{}` | Extra pod labels. |
| `priorityClassName` | `""` | Pod priority class. |
| `livenessProbe` | `initialDelaySeconds: 10`, `periodSeconds: 10`, `timeoutSeconds: 5`, `failureThreshold: 3` | Liveness probe tuning (path and port are fixed by the chart). |
| `readinessProbe` | `initialDelaySeconds: 5`, `periodSeconds: 10`, `timeoutSeconds: 5`, `failureThreshold: 3` | Readiness probe tuning (path and port are fixed by the chart). |
| `networkPolicy.enabled` | `false` | Whether to render an egress-restricting `NetworkPolicy`. |
| `networkPolicy.dnsNamespaceSelector` | `{matchLabels: {kubernetes.io/metadata.name: kube-system}}` | Namespace selector used to allow DNS egress. |
| `networkPolicy.extraEgress` | `[]` | Additional egress rules merged into the policy. |

## Using the webhook

Create a Secret with your Hetzner Cloud token in the cert-manager namespace:

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: hcloud-token
  namespace: cert-manager
type: Opaque
stringData:
  token: <YOUR_HCLOUD_TOKEN>
```

Reference it from an ACME `Issuer` or `ClusterIssuer`:

```yaml
apiVersion: cert-manager.io/v1
kind: ClusterIssuer
metadata:
  name: letsencrypt
spec:
  acme:
    server: https://acme-v02.api.letsencrypt.org/directory
    email: you@example.com
    privateKeySecretRef:
      name: letsencrypt-account
    solvers:
      - dns01:
          webhook:
            groupName: acme.hcloud-zones.xmv.de
            solverName: hcloud-zones
            config:
              tokenSecretRef:
                name: hcloud-token
                key: token
```

Multi-project setups use one Secret per Hetzner Cloud project and one solver
entry per project, each scoped via `dnsZones`. See the project's
[`docs/app-concept.md`](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/blob/main/docs/app-concept.md)
section 3 for the routing model.

## Uninstall

```bash
helm uninstall cert-manager-webhook-hcloud-zones --namespace cert-manager
```

The chart's PKI Secrets are owned by the cert-manager `Certificate` resources
and are cleaned up automatically. Hetzner-token Secrets you created out-of-band
are not removed.

## Licence

Dual-licensed under [MIT](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/blob/main/LICENSE-MIT)
OR [Apache-2.0](https://github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/blob/main/LICENSE-APACHE).
