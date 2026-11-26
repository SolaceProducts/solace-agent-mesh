// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package delivery

import (
	"context"
	"sync"
	"sync/atomic"
)

type Delivery struct {
	BrokerMessageID string
	Redelivered     bool

	ack   func()
	holds atomic.Int32
	kept  atomic.Bool

	mu    sync.Mutex
	acked bool
	onAck []func()
}

type ctxKey struct{}

func Attach(ctx context.Context, brokerMessageID string, redelivered bool, ack func()) (context.Context, *Delivery) {
	d := &Delivery{BrokerMessageID: brokerMessageID, Redelivered: redelivered, ack: ack}
	d.holds.Store(1)
	return context.WithValue(ctx, ctxKey{}, d), d
}

func From(ctx context.Context) *Delivery {
	d, _ := ctx.Value(ctxKey{}).(*Delivery)
	return d
}

func (d *Delivery) Hold() func() {
	for {
		n := d.holds.Load()
		if n <= 0 {
			return func() {
			}
		}
		if d.holds.CompareAndSwap(n, n+1) {
			break
		}
	}
	var once sync.Once
	return func() { once.Do(d.release) }
}

func (d *Delivery) Keep() {
	d.kept.Store(true)
	d.Hold()
}

func (d *Delivery) Kept() bool { return d.kept.Load() }

func (d *Delivery) Release() { d.release() }

func (d *Delivery) OnAck(fn func()) {
	d.mu.Lock()
	if !d.acked {
		d.onAck = append(d.onAck, fn)
		d.mu.Unlock()
		return
	}
	d.mu.Unlock()
	fn()
}

func (d *Delivery) release() {
	if d.holds.Add(-1) != 0 {
		return
	}
	if d.ack != nil {
		d.ack()
	}
	d.mu.Lock()
	d.acked = true
	fns := d.onAck
	d.onAck = nil
	d.mu.Unlock()
	for _, fn := range fns {
		fn()
	}
}
