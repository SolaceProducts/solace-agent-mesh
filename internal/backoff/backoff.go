// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package backoff

import (
	"context"
	"math/rand/v2"
	"time"
)

func Sleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

type Exponential struct {
	Initial time.Duration
	Max     time.Duration
	Jitter  float64

	current time.Duration
}

func (b *Exponential) Wait(ctx context.Context) error {
	if b.current == 0 {
		b.current = b.Initial
	}
	d := b.current
	if b.Jitter > 0 {
		delta := float64(d) * b.Jitter * (2*rand.Float64() - 1)
		d = max(time.Duration(float64(d)+delta), 0)
	}
	if err := Sleep(ctx, d); err != nil {
		return err
	}
	next := b.current * 2
	if b.Max > 0 && next > b.Max {
		next = b.Max
	}
	b.current = next
	return nil
}

func (b *Exponential) Next() time.Duration {
	if b.current == 0 {
		b.current = b.Initial
	}
	d := b.current
	if b.Jitter > 0 {
		delta := float64(d) * b.Jitter * (2*rand.Float64() - 1)
		d = max(time.Duration(float64(d)+delta), 0)
	}
	next := b.current * 2
	if b.Max > 0 && next > b.Max {
		next = b.Max
	}
	b.current = next
	return d
}

func (b *Exponential) Reset() {
	b.current = 0
}

func (b *Exponential) Current() time.Duration {
	if b.current == 0 {
		return b.Initial
	}
	return b.current
}
