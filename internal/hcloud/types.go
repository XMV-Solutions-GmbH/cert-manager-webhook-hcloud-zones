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

// actionEnvelope is the wire envelope returned by action endpoints such
// as POST /v1/zones/{id}/rrsets/{name}/{type}/actions/set_records.
type actionEnvelope struct {
	Action Action `json:"action"`
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

// SetRRSetRecordsRequest is the body of the records-replacing action
// POST /v1/zones/{id}/rrsets/{name}/{type}/actions/set_records.
//
// This is the only Hetzner Cloud Zones endpoint that changes the
// records of an existing RRSet — PATCH/PUT on the RRSet itself either
// 404 or refuse to touch records ("can't update records with this
// endpoint"). The action does NOT change the TTL; that stays whatever
// the RRSet was created with.
type SetRRSetRecordsRequest struct {
	Records []Record `json:"records"`
}

// Action is the asynchronous-operation envelope the Hetzner Cloud API
// returns from action endpoints (e.g. set_records). The webhook does
// not need to poll it to completion — cert-manager re-checks the DNS
// record on its own loop — but the command + status are decoded so the
// client can confirm the action was accepted and log it.
type Action struct {
	ID       int64  `json:"id"`
	Command  string `json:"command"`
	Status   string `json:"status"`
	Progress int    `json:"progress"`
}
