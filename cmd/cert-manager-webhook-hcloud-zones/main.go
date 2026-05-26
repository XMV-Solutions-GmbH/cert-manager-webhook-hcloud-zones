// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

// Package main is the entry point for the cert-manager DNS-01 webhook that
// targets the Hetzner Cloud Zones API.
//
// The binary registers the hcloud-zones solver with the cert-manager webhook
// framework and starts the HTTPS server. The group name — which must match
// the value in the Helm chart's webhook configuration — defaults to
// "acme.hcloud-zones.cert-manager.io" and is overridable via the GROUP_NAME
// environment variable.
//
// The webhook framework's RunWebhookServer wraps a cobra command that owns
// the actual flag set (--tls-cert-file, --tls-private-key-file, --secure-port,
// --v, ...). We must NOT call flag.Parse() ourselves — doing so swallows
// those flags and the framework command exits with "unknown flag". See
// https://github.com/cert-manager/webhook-example for the canonical pattern.
package main

import (
	"os"

	whcmd "github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"

	"github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/internal/solver"
)

const defaultGroupName = "acme.hcloud-zones.cert-manager.io"

func main() {
	whcmd.RunWebhookServer(envOrDefault("GROUP_NAME", defaultGroupName), solver.New())
}

// envOrDefault returns the value of the named environment variable, or
// fallback when the variable is unset or empty.
func envOrDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
