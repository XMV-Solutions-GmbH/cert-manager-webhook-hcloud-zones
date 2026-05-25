// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package solver

import (
	"encoding/json"
	"fmt"
	"strings"

	cmmeta "github.com/cert-manager/cert-manager/pkg/apis/meta/v1"
	apiextensionsv1 "k8s.io/apiextensions-apiserver/pkg/apis/apiextensions/v1"

	"github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/internal/routing"
)

// Config is the JSON-shaped per-issuer configuration block carried inside
// `solvers.dns01.webhook.config` of a cert-manager Issuer / ClusterIssuer.
//
// Field names are JSON-tagged to match docs/app-concept.md § 3.2.
type Config struct {
	// Credentials is the list of operator-defined hcloud projects this
	// Issuer can route challenges to. At least one entry is required.
	Credentials []CredentialConfig `json:"credentials"`
}

// CredentialConfig is one entry in Config.Credentials.
type CredentialConfig struct {
	// Name is the operator-chosen identifier for the credential. Echoed
	// in error messages so misrouted FQDNs are diagnosable from
	// `kubectl describe challenge`.
	Name string `json:"name"`

	// Zones is the list of zone-apex names this credential is
	// authoritative for. Each entry must satisfy
	// routing.ValidateConfig's per-zone syntax rules; see that function
	// for the full list.
	Zones []string `json:"zones"`

	// APITokenSecretRef points at the Kubernetes Secret holding the
	// hcloud API token. The Secret is read at challenge time (per
	// docs/app-concept.md § 6.5) so token rotation is picked up without
	// a webhook restart. Namespace is required: a ClusterIssuer can
	// reference Secrets in any namespace.
	APITokenSecretRef cmmeta.SecretKeySelector `json:"apiTokenSecretRef"`

	// Namespace overrides APITokenSecretRef.Namespace when set. The
	// cert-manager SecretKeySelector type does not carry a namespace
	// field — it inherits the Issuer's resource namespace. We model
	// the namespace as an explicit sibling field so ClusterIssuer
	// configs can name a fixed namespace (the cert-manager convention
	// for webhook configs; see vadimkim/cert-manager-webhook-hetzner).
	Namespace string `json:"namespace,omitempty"`
}

// SecretRef is the resolved (namespace, name, key) triple identifying the
// Kubernetes Secret + entry the webhook should read at challenge time.
type SecretRef struct {
	Namespace string
	Name      string
	Key       string
}

// String renders the SecretRef as `namespace/name#key` — a stable opaque
// form used as the APITokenSecretRef value in the routing layer and as a
// log-safe identifier (the value never contains the token itself).
func (r SecretRef) String() string {
	return r.Namespace + "/" + r.Name + "#" + r.Key
}

// parseConfig unmarshals the apiextensionsv1.JSON blob carried by a
// ChallengeRequest into a Config, then validates it via the routing layer.
//
// It returns:
//
//   - the routing.Config the resolver will use,
//   - a name → SecretRef map so the solver can find the Secret for a
//     resolved credential without re-walking the config,
//   - or an error pinning down the misconfiguration.
func parseConfig(raw *apiextensionsv1.JSON, defaultNamespace string) (*routing.Config, map[string]SecretRef, error) {
	if raw == nil || len(raw.Raw) == 0 {
		return nil, nil, fmt.Errorf("solver: webhook config is empty; expected a `credentials` block per docs/app-concept.md § 3.2")
	}

	var cfg Config
	if err := json.Unmarshal(raw.Raw, &cfg); err != nil {
		return nil, nil, fmt.Errorf("solver: parse webhook config: %w", err)
	}

	if len(cfg.Credentials) == 0 {
		return nil, nil, fmt.Errorf("solver: webhook config has no credentials; at least one is required")
	}

	routingCfg := &routing.Config{
		Credentials: make([]routing.Credential, 0, len(cfg.Credentials)),
	}
	secretRefs := make(map[string]SecretRef, len(cfg.Credentials))

	for i, cred := range cfg.Credentials {
		ref, err := resolveSecretRef(cred, defaultNamespace, i)
		if err != nil {
			return nil, nil, err
		}

		routingCfg.Credentials = append(routingCfg.Credentials, routing.Credential{
			Name:              cred.Name,
			Zones:             cred.Zones,
			APITokenSecretRef: ref.String(),
		})
		secretRefs[cred.Name] = ref
	}

	if err := routing.ValidateConfig(routingCfg); err != nil {
		return nil, nil, fmt.Errorf("solver: validate webhook config: %w", err)
	}

	return routingCfg, secretRefs, nil
}

// resolveSecretRef merges the explicit Namespace field with the
// SecretKeySelector and the cert-manager-supplied default namespace into
// the final SecretRef the solver will dereference at challenge time.
//
// Precedence (highest first):
//  1. CredentialConfig.Namespace — an explicit operator declaration.
//  2. defaultNamespace — supplied by cert-manager as
//     ChallengeRequest.ResourceNamespace (Issuer's namespace, or the
//     cluster-resource-namespace for ClusterIssuer).
//
// The Key defaults to "token" so the common case ("data: { token: <b64> }")
// works without spelling it out in YAML.
func resolveSecretRef(cred CredentialConfig, defaultNamespace string, idx int) (SecretRef, error) {
	name := strings.TrimSpace(cred.APITokenSecretRef.Name)
	if name == "" {
		return SecretRef{}, fmt.Errorf(
			"solver: credential at index %d (%q): apiTokenSecretRef.name is empty",
			idx, cred.Name)
	}

	namespace := strings.TrimSpace(cred.Namespace)
	if namespace == "" {
		namespace = strings.TrimSpace(defaultNamespace)
	}
	if namespace == "" {
		return SecretRef{}, fmt.Errorf(
			"solver: credential %q: apiTokenSecretRef namespace is empty and no default namespace is available; "+
				"set the `namespace` field on the credential or run via an Issuer (not ClusterIssuer) "+
				"so cert-manager supplies the resource namespace",
			cred.Name)
	}

	key := strings.TrimSpace(cred.APITokenSecretRef.Key)
	if key == "" {
		key = "token"
	}

	return SecretRef{Namespace: namespace, Name: name, Key: key}, nil
}
