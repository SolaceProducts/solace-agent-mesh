// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"context"
	"sync"
)

type inFlightSet struct {
	mu sync.Mutex
	m  map[ProviderUID]struct{}
}

func (s *inFlightSet) Claim(msgs []RawMessage) []RawMessage {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.m == nil {
		s.m = make(map[ProviderUID]struct{})
	}
	out := make([]RawMessage, 0, len(msgs))
	for _, m := range msgs {
		if _, dup := s.m[m.UID]; dup {
			continue
		}
		s.m[m.UID] = struct{}{}
		out = append(out, m)
	}
	return out
}

func (s *inFlightSet) Done(uid ProviderUID) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.m, uid)
}

func (s *inFlightSet) reset() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.m = nil
}

type inboundDriver interface {
	Run(ctx context.Context) error
	Ack(ctx context.Context, uid ProviderUID)
}

var (
	_ inboundDriver = (*Poller)(nil)
	_ inboundDriver = (*Idler)(nil)
)
