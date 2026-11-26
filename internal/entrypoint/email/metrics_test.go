// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"log/slog"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type fakeMetrics struct {
	mu     sync.Mutex
	gauges []gaugeCall
}

type gaugeCall struct {
	name   string
	value  float64
	labels []string
}

func (f *fakeMetrics) Counter(string, int64, ...string)           {}
func (f *fakeMetrics) Histogram(string, time.Duration, ...string) {}
func (f *fakeMetrics) Gauge(name string, value float64, labels ...string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.gauges = append(f.gauges, gaugeCall{name: name, value: value, labels: append([]string(nil), labels...)})
}

func (f *fakeMetrics) gaugesByName(name string) []gaugeCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make([]gaugeCall, 0, len(f.gauges))
	for _, g := range f.gauges {
		if g.name == name {
			out = append(out, g)
		}
	}
	return out
}

func TestPoller_EmitsLastSuccessfulAuthGauge(t *testing.T) {
	t.Parallel()
	fp := NewFakeProvider(nil)
	if err := fp.Connect(context.Background()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	q := NewMessageQueue(QueueConfig{
		Capacity: 1,
		Workers:  1,
		Handler:  func(_ context.Context, _ RawMessage) {},
		Logger:   slog.Default(),
	})
	q.Start(t.Context())
	defer func() { _ = q.Stop(t.Context()) }()

	m := &fakeMetrics{}
	p := &Poller{
		Provider:        fp,
		Queue:           q,
		IntervalSeconds: 0,
		FetchLimit:      10,
		Logger:          slog.Default(),
		Metrics:         m,
	}

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() {
		_ = p.Run(ctx)
		close(done)
	}()

	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if len(m.gaugesByName(MetricLastSuccessfulAuthSecondsAgo)) > 0 {
			cancel()
			<-done
			gauges := m.gaugesByName(MetricLastSuccessfulAuthSecondsAgo)
			if got := gauges[0].value; got < 0 {
				t.Errorf("gauge value = %v, want non-negative", got)
			}
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	cancel()
	<-done
	t.Fatalf("expected at least one %q gauge emission within 1s; got %d total gauges",
		MetricLastSuccessfulAuthSecondsAgo, len(m.gauges))
}

func TestPoller_SkipsGaugeWhenAuthTimeIsZero(t *testing.T) {
	t.Parallel()
	fp := &neverConnectsProvider{FakeProvider: NewFakeProvider(nil)}

	q := NewMessageQueue(QueueConfig{
		Capacity: 1,
		Workers:  1,
		Handler:  func(_ context.Context, _ RawMessage) {},
		Logger:   slog.Default(),
	})
	q.Start(t.Context())
	defer func() { _ = q.Stop(t.Context()) }()

	m := &fakeMetrics{}
	p := &Poller{
		Provider:        fp,
		Queue:           q,
		IntervalSeconds: 0,
		FetchLimit:      10,
		Logger:          slog.Default(),
		Metrics:         m,
	}

	ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
	defer cancel()
	done := make(chan struct{})
	go func() {
		_ = p.Run(ctx)
		close(done)
	}()
	<-done

	if got := len(m.gaugesByName(MetricLastSuccessfulAuthSecondsAgo)); got != 0 {
		t.Errorf("expected 0 staleness gauge emissions when auth time is zero, got %d", got)
	}
	if cycles := atomic.LoadInt64(&fp.fetchCalls); cycles == 0 {
		t.Fatal("FetchNew was never called; the poll loop did not run")
	}
}

type neverConnectsProvider struct {
	*FakeProvider
	fetchCalls int64
}

func (n *neverConnectsProvider) Connect(_ context.Context) error { return nil }
func (n *neverConnectsProvider) IsConnected() bool               { return true }
func (n *neverConnectsProvider) LastSuccessfulAuthAt() time.Time { return time.Time{} }
func (n *neverConnectsProvider) FetchNew(ctx context.Context, limit int) ([]RawMessage, error) {
	atomic.AddInt64(&n.fetchCalls, 1)
	return n.FakeProvider.FetchNew(ctx, limit)
}
