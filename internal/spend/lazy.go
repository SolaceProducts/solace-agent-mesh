// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package spend

import (
	"context"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

var flushTimeout = 5 * time.Second

func Enabled(svc runtime.Services) bool {
	return svc != nil && svc.Broker() != nil
}

func CloseDetached(ctx context.Context, p *Publisher) error {
	flushCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), flushTimeout)
	defer cancel()
	return p.Close(flushCtx)
}

type LazyPublisher struct {
	svc       runtime.Services
	namespace string

	mu        sync.Mutex
	publisher *Publisher
	closed    bool
}

func NewLazyPublisher(svc runtime.Services, namespace string) *LazyPublisher {
	return &LazyPublisher{svc: svc, namespace: namespace}
}

func (l *LazyPublisher) Get() *Publisher {
	if l == nil || !Enabled(l.svc) {
		return nil
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.closed {
		return nil
	}
	if l.publisher == nil {
		l.publisher = NewPublisher(l.svc.Broker(), l.namespace, l.svc.Logger())
	}
	return l.publisher
}

func (l *LazyPublisher) Close(ctx context.Context) error {
	if l == nil {
		return nil
	}
	l.mu.Lock()
	p := l.publisher
	l.publisher, l.closed = nil, true
	l.mu.Unlock()
	if p == nil {
		return nil
	}
	return CloseDetached(ctx, p)
}
