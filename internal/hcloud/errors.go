// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package hcloud

import (
	"errors"
	"fmt"
	"time"
)

// hcloudErrorEnvelope mirrors the documented Hetzner Cloud error shape:
//
//	{ "error": { "code": "...", "message": "...", "details": {...} } }
type hcloudErrorEnvelope struct {
	Error hcloudErrorBody `json:"error"`
}

type hcloudErrorBody struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

// APIError is the wire-level error returned by the Hetzner Cloud Zones
// API for any non-2xx response. The HTTP status code is preserved so
// callers can classify; the typed sentinels (ErrInvalidToken,
// ErrForbidden, ...) cover the well-known cases via errors.Is.
type APIError struct {
	StatusCode int
	Code       string        // Hetzner-side machine-readable code (e.g. "unauthorized")
	Message    string        // Hetzner-side human-readable message
	RetryAfter time.Duration // populated for 429 responses; zero otherwise
}

// Error implements the error interface. The token is never part of the
// message; callers may log this value freely.
func (e *APIError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("hcloud API error: status=%d code=%q message=%q", e.StatusCode, e.Code, e.Message)
	}
	return fmt.Sprintf("hcloud API error: status=%d message=%q", e.StatusCode, e.Message)
}

// Is supports errors.Is for the typed sentinels below. The match is
// driven by HTTP status code; the body shape is informational.
func (e *APIError) Is(target error) bool {
	switch target {
	case ErrInvalidToken:
		return e.StatusCode == 401
	case ErrForbidden:
		return e.StatusCode == 403
	case ErrNotFound:
		return e.StatusCode == 404
	case ErrConflict:
		return e.StatusCode == 409
	case ErrInvalidZoneName:
		return e.StatusCode == 422
	case ErrRateLimited:
		return e.StatusCode == 429
	case ErrServer:
		return e.StatusCode >= 500 && e.StatusCode < 600
	}
	return false
}

// Sentinel error values for the documented Hetzner Cloud Zones API
// failure modes. Match these with errors.Is on an *APIError or any
// error returned by the client.
var (
	// ErrInvalidToken — 401 Unauthorized. The bearer token is missing,
	// malformed, or revoked.
	ErrInvalidToken = errors.New("hcloud: invalid or revoked token")

	// ErrForbidden — 403 Forbidden. The token is well-formed but does
	// not grant access to the requested resource (e.g. the zone lives
	// in a different Hetzner Cloud project than the token).
	ErrForbidden = errors.New("hcloud: forbidden (token does not own this resource)")

	// ErrNotFound — 404 Not Found. The zone or RRSet does not exist.
	ErrNotFound = errors.New("hcloud: resource not found")

	// ErrConflict — 409 Conflict. Returned when creating an RRSet that
	// already exists; the higher-level webhook treats this as a signal
	// to switch to PATCH.
	ErrConflict = errors.New("hcloud: resource already exists (conflict)")

	// ErrInvalidZoneName — 422 Unprocessable Entity. The zone name is
	// not a registrable domain (Hetzner validates against the Public
	// Suffix List; see docs/app-concept.md § 3.4).
	ErrInvalidZoneName = errors.New("hcloud: invalid zone name (subdomain or unknown TLD)")

	// ErrRateLimited — 429 Too Many Requests. The client honoured the
	// Retry-After header but eventually gave up after exhausting its
	// retry budget.
	ErrRateLimited = errors.New("hcloud: rate limited")

	// ErrServer — any 5xx status code. The client retried with
	// exponential backoff before surfacing this.
	ErrServer = errors.New("hcloud: upstream server error")
)
