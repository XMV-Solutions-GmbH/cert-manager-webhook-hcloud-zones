# SPDX-License-Identifier: MIT OR Apache-2.0
# SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
# SPDX-FileContributor: David Koller <david.koller@xmv.de>

BINARY := cert-manager-webhook-hcloud-zones
BIN_DIR := bin

.PHONY: build test lint tidy fmt

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
