# SPDX-License-Identifier: MIT OR Apache-2.0
# SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
# SPDX-FileContributor: David Koller <david.koller@xmv.de>

BINARY := cert-manager-webhook-hcloud-zones
BIN_DIR := bin
CHART_DIR := charts/cert-manager-webhook-hcloud-zones

.PHONY: build test lint tidy fmt helm-lint helm-template

build:
	go build -o $(BIN_DIR)/$(BINARY) ./cmd/$(BINARY)

test:
	go test ./... -race -count=1 -v

lint:
	golangci-lint run ./...

tidy:
	go mod tidy

fmt:
	gofmt -w .

helm-lint:
	helm lint --strict $(CHART_DIR)

helm-template:
	helm template smoketest $(CHART_DIR) --namespace cert-manager >/dev/null
	helm template smoketest $(CHART_DIR) --namespace cert-manager --set networkPolicy.enabled=true >/dev/null
