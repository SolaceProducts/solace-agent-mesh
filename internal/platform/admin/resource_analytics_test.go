// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build product_analytics

package platform

import (
	"context"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/productanalytics"
)

type analyticsCaptureSink struct {
	mu     sync.Mutex
	events []productanalytics.Event
}

func (c *analyticsCaptureSink) Send(_ context.Context, events []productanalytics.Event) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.events = append(c.events, events...)
	return nil
}

func (c *analyticsCaptureSink) all() []productanalytics.Event {
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]productanalytics.Event(nil), c.events...)
}

func namesOf(events []productanalytics.Event) []string {
	names := make([]string, 0, len(events))
	for _, e := range events {
		names = append(names, e.Name)
	}
	return names
}
