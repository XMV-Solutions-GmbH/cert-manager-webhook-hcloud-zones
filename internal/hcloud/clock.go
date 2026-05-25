// SPDX-License-Identifier: MIT OR Apache-2.0
// SPDX-FileCopyrightText: 2026 XMV Solutions GmbH
// SPDX-FileContributor: David Koller <david.koller@xmv.de>

package hcloud

import (
	"context"
	"time"
)

// Clock abstracts the wall clock so tests can drive backoff without
// real time.Sleep. The default implementation (realClock) delegates to
// the stdlib time package; tests inject a fake.
type Clock interface {
	// Now returns the current instant.
	Now() time.Time

	// Sleep blocks for d, or returns early if ctx is cancelled. The
	// return value is the residual context error (or nil on full
	// sleep).
	Sleep(ctx context.Context, d time.Duration) error
}

type realClock struct{}

func (realClock) Now() time.Time { return time.Now() }

func (realClock) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
