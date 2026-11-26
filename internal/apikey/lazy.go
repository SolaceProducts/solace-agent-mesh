// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apikey

import (
	"context"
	"sync"
)

type LazyAuthority struct {
	mu       sync.RWMutex
	delegate Authority
}

func NewLazyAuthority() *LazyAuthority {
	return &LazyAuthority{}
}

func (l *LazyAuthority) SetAuthority(a Authority) {
	l.mu.Lock()
	l.delegate = a
	l.mu.Unlock()
}

func (l *LazyAuthority) Disable() {
	l.SetAuthority(disabledAuthority{})
}

type disabledAuthority struct{}

func (disabledAuthority) Validate(context.Context, string) (Resolution, error) {
	return Resolution{}, ErrNotEnabled
}

func (l *LazyAuthority) Bound() bool {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.delegate != nil
}

func (l *LazyAuthority) Validate(ctx context.Context, keyHash string) (Resolution, error) {
	if l == nil {
		return Resolution{}, ErrNotEnabled
	}
	l.mu.RLock()
	d := l.delegate
	l.mu.RUnlock()
	if d == nil {
		return Resolution{}, ErrAuthorityUnavailable
	}
	return d.Validate(ctx, keyHash)
}

var _ Authority = (*LazyAuthority)(nil)
