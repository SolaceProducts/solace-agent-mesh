// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import "context"

type IdleEventKind int

const (
	IdleEventExists IdleEventKind = iota

	IdleEventExpunge
)

type IdleEvent struct {
	Kind IdleEventKind
}

type IdleCapableProvider interface {
	EmailProvider
	Idle(ctx context.Context) (events <-chan IdleEvent, done <-chan struct{}, err error)
	StopIdle(ctx context.Context) error
}
