# SPDX-License-Identifier: MIT OR Apache-2.0
# SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
# SPDX-FileContributor: David Koller <david.koller@xmv.de>

FROM --platform=$BUILDPLATFORM golang:1.25 AS build

WORKDIR /src

# Download dependencies before copying source so Docker can cache this layer.
COPY go.mod go.sum ./
RUN go mod download

COPY . .

ARG TARGETOS
ARG TARGETARCH

# CGO disabled + GOOS/GOARCH from buildx → fully static binary compatible
# with distroless/static (which has no libc). Without CGO_ENABLED=0 the
# default go toolchain links against glibc and the resulting binary fails
# at exec with "no such file or directory" because the dynamic loader
# /lib64/ld-linux-x86-64.so.2 doesn't exist in the static base image.
RUN CGO_ENABLED=0 GOOS=${TARGETOS:-linux} GOARCH=${TARGETARCH} \
    go build -trimpath -ldflags="-s -w" \
    -o /webhook \
    ./cmd/cert-manager-webhook-hcloud-zones/

# ---------------------------------------------------------------------------
FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /webhook /webhook

USER nonroot:nonroot

ENTRYPOINT ["/webhook"]
