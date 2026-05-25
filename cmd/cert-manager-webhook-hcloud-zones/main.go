// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

// Package main is the entry point for the cert-manager DNS-01 webhook that
// targets the Hetzner Cloud Zones API.
//
// The binary registers the hcloud-zones solver with the cert-manager webhook
// framework and starts the HTTPS server.  The group name — which must match
// the value in the Helm chart's webhook configuration — defaults to
// "acme.hcloud-zones.cert-manager.io" and can be overridden via the
// GROUP_NAME environment variable or the --group-name flag.
package main

import (
	"os"

	whcmd "github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"
	flag "github.com/spf13/pflag"

	"github.com/XMV-Solutions-GmbH/cert-manager-webhook-hcloud-zones/internal/solver"
)

const defaultGroupName = "acme.hcloud-zones.cert-manager.io"

func main() {
	groupName := flag.String(
		"group-name",
		envOrDefault("GROUP_NAME", defaultGroupName),
		"The API group name used for the webhook. Must match the value "+
			"configured in the cert-manager webhook resource. "+
			"Overridable via the GROUP_NAME environment variable.",
	)
	flag.Parse()

	whcmd.RunWebhookServer(*groupName, solver.New())
}

// envOrDefault returns the value of the named environment variable, or
// fallback when the variable is unset or empty.
func envOrDefault(name, fallback string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return fallback
}
