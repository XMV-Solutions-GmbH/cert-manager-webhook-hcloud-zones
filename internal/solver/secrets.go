// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package solver

import (
	"context"
	"errors"
	"fmt"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
)

// SecretGetter resolves a SecretRef to the raw token bytes at challenge
// time. The interface exists so tests can stub Kubernetes out without
// pulling in a fake clientset, and so a future implementation can layer
// caching on top of the default kubernetes.Clientset-backed getter.
type SecretGetter interface {
	GetToken(ctx context.Context, ref SecretRef) (string, error)
}

// kubeSecretGetter is the production SecretGetter. It hits the Kubernetes
// API every time it is asked — the bounded-TTL cache lives in the Solver
// (see docs/app-concept.md § 6.5), not here, so this layer stays trivial
// to reason about.
type kubeSecretGetter struct {
	client kubernetes.Interface
}

// newKubeSecretGetter constructs the default production getter from a
// kubeconfig-style rest.Config.
func newKubeSecretGetter(cfg *rest.Config) (*kubeSecretGetter, error) {
	if cfg == nil {
		return nil, errors.New("solver: rest.Config is nil; Initialize must be called before Present/CleanUp")
	}
	client, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("solver: build Kubernetes client: %w", err)
	}
	return &kubeSecretGetter{client: client}, nil
}

// GetToken reads the named Secret, extracts the configured key, and
// returns the token string. The returned error string never carries the
// token literal; the SecretRef is safe to embed because it only names
// (namespace, name, key) — none of which are secret material.
func (g *kubeSecretGetter) GetToken(ctx context.Context, ref SecretRef) (string, error) {
	secret, err := g.client.CoreV1().Secrets(ref.Namespace).Get(ctx, ref.Name, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("solver: get Secret %s/%s: %w", ref.Namespace, ref.Name, err)
	}
	raw, ok := secret.Data[ref.Key]
	if !ok {
		return "", fmt.Errorf(
			"solver: Secret %s/%s has no key %q; configure data.%s with the hcloud token",
			ref.Namespace, ref.Name, ref.Key, ref.Key)
	}
	token := strings.TrimSpace(string(raw))
	if token == "" {
		return "", fmt.Errorf("solver: Secret %s/%s key %q is empty", ref.Namespace, ref.Name, ref.Key)
	}
	return token, nil
}
