// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"errors"
	"sync"

	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
	"github.com/SolaceProducts/solace-agent-mesh/internal/spend"
)

type spendPublishers struct {
	svc         runtime.Services
	mu          sync.Mutex
	byNamespace map[string]*spend.Publisher
}

func newSpendPublishers(svc runtime.Services) *spendPublishers {
	return &spendPublishers{svc: svc, byNamespace: make(map[string]*spend.Publisher)}
}

func (s *spendPublishers) forNamespace(namespace string) *spend.Publisher {
	if !spend.Enabled(s.svc) {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	p, ok := s.byNamespace[namespace]
	if !ok {
		p = spend.NewPublisher(s.svc.Broker(), namespace, s.svc.Logger())
		s.byNamespace[namespace] = p
	}
	return p
}

func (s *spendPublishers) close(ctx context.Context) error {
	s.mu.Lock()
	publishers := s.byNamespace
	s.byNamespace = make(map[string]*spend.Publisher)
	s.mu.Unlock()

	var errs []error
	for _, p := range publishers {
		errs = append(errs, spend.CloseDetached(ctx, p))
	}
	return errors.Join(errs...)
}
