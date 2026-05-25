# SPDX-License-Identifier: MIT OR Apache-2.0
# SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
# SPDX-FileContributor: David Koller <david.koller@xmv.de>

FROM golang:1.25 AS build

WORKDIR /src

# Download dependencies before copying source so Docker can cache this layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN go build -trimpath -ldflags="-s -w" \
    -o /webhook \
    ./cmd/cert-manager-webhook-hcloud-zones/

# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /webhook /webhook

USER nonroot:nonroot

ENTRYPOINT ["/webhook"]
