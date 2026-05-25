// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

// Package main is the entry point for the cert-manager DNS-01 webhook that
// targets the Hetzner Cloud Zones API.
//
// This stub exists so the Go module compiles. The real wiring — registering
// the solver via github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd
// and loading the project-to-token routing config — lands in sub-task P2-5.
package main

import (
	// Anchor the cert-manager webhook framework dependency in go.mod so
	// the skeleton already pins the version P2-5 will consume. The blank
	// import is replaced by a real `cmd.RunWebhookServer(...)` call in
	// P2-5.
	_ "github.com/cert-manager/cert-manager/pkg/acme/webhook/cmd"
)

func main() {
	// TODO(P2-5): construct the Hetzner-Zones solver and call
	// cmd.RunWebhookServer(groupName, solver).
}
