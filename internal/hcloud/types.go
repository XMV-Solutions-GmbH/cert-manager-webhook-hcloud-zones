// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package hcloud

import "time"

// Zone is the Hetzner Cloud Zones representation of a DNS zone.
//
// Only fields the webhook actually consumes are modelled; unknown
// fields decoded from JSON responses are ignored.
type Zone struct {
	ID         int64     `json:"id"`
	Name       string    `json:"name"`
	Mode       string    `json:"mode,omitempty"`   // "primary" | "secondary"
	Status     string    `json:"status,omitempty"` // "ok" | "updating" | ...
	TTL        int       `json:"ttl,omitempty"`
	RecordsCnt int       `json:"records_count,omitempty"`
	Created    time.Time `json:"created,omitempty"`
}

// Record is a single resource record inside an RRSet.
type Record struct {
	Value   string `json:"value"`
	Comment string `json:"comment,omitempty"`
}

// RRSet is a resource-record set as returned and accepted by the
// Hetzner Cloud Zones API.
type RRSet struct {
	ID      string            `json:"id,omitempty"`
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	TTL     *int              `json:"ttl,omitempty"`
	Records []Record          `json:"records,omitempty"`
	Protect bool              `json:"protected,omitempty"`
	ZoneID  int64             `json:"zone_id,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// listZonesResponse is the wire envelope returned by GET /v1/zones.
type listZonesResponse struct {
	Zones []Zone `json:"zones"`
	Meta  meta   `json:"meta,omitempty"`
}

// rrsetEnvelope is the wire envelope used by single-RRSet endpoints.
type rrsetEnvelope struct {
	RRSet RRSet `json:"rrset"`
}

// meta is the pagination envelope returned by list endpoints. The
// webhook does not paginate at the moment (zone counts are small) but
// the field is decoded so callers can observe it for diagnostics.
type meta struct {
	Pagination pagination `json:"pagination"`
}

type pagination struct {
	Page         int `json:"page"`
	PerPage      int `json:"per_page"`
	PreviousPage int `json:"previous_page"`
	NextPage     int `json:"next_page"`
	LastPage     int `json:"last_page"`
	TotalEntries int `json:"total_entries"`
}

// CreateRRSetRequest is the body of POST /v1/zones/{id}/rrsets.
type CreateRRSetRequest struct {
	Name    string            `json:"name"`
	Type    string            `json:"type"`
	TTL     *int              `json:"ttl,omitempty"`
	Records []Record          `json:"records"`
	Labels  map[string]string `json:"labels,omitempty"`
}

// UpdateRRSetRequest is the body of PATCH /v1/zones/{id}/rrsets/{name}/{type}.
//
// All fields are optional; unset fields are not modified server-side.
type UpdateRRSetRequest struct {
	TTL     *int              `json:"ttl,omitempty"`
	Records []Record          `json:"records,omitempty"`
	Labels  map[string]string `json:"labels,omitempty"`
}
