// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"sync"
)

type LazyService struct {
	mu       sync.RWMutex
	delegate IdentityService
}

func NewLazyService() *LazyService {
	return &LazyService{}
}

func (l *LazyService) SetDelegate(svc IdentityService) {
	l.mu.Lock()
	l.delegate = svc
	l.mu.Unlock()
}

func (l *LazyService) Delegate() IdentityService {
	l.mu.RLock()
	defer l.mu.RUnlock()
	return l.delegate
}

func (l *LazyService) Resolve(ctx context.Context, claims map[string]any) (*Resolution, error) {
	l.mu.RLock()
	d := l.delegate
	l.mu.RUnlock()
	if d == nil {
		return &Resolution{EnrichmentStatus: EnrichmentNotConfigured}, nil
	}
	return d.Resolve(ctx, claims)
}

func (l *LazyService) SearchUsers(ctx context.Context, query string, limit int) ([]*Profile, error) {
	l.mu.RLock()
	d := l.delegate
	l.mu.RUnlock()
	if d == nil {
		return []*Profile{}, nil
	}
	return d.SearchUsers(ctx, query, limit)
}

func (l *LazyService) SearchConfigured(ctx context.Context) (bool, error) {
	l.mu.RLock()
	d := l.delegate
	l.mu.RUnlock()
	probe, ok := d.(interface {
		SearchConfigured(ctx context.Context) (bool, error)
	})
	if !ok {
		return false, nil
	}
	return probe.SearchConfigured(ctx)
}

func (l *LazyService) GetUserProfile(ctx context.Context, userID string) (map[string]any, error) {
	l.mu.RLock()
	d := l.delegate
	l.mu.RUnlock()
	if d == nil {
		return nil, nil
	}
	return d.GetUserProfile(ctx, userID)
}

func (l *LazyService) GetTimeOff(ctx context.Context, userID, start, end string) ([]TimeOffEntry, error) {
	l.mu.RLock()
	d := l.delegate
	l.mu.RUnlock()
	if d == nil {
		return nil, nil
	}
	return d.GetTimeOff(ctx, userID, start, end)
}

func (l *LazyService) GetUserProfilePicture(ctx context.Context, userID string) ([]byte, string, error) {
	l.mu.RLock()
	d := l.delegate
	l.mu.RUnlock()
	if d == nil {
		return nil, "", nil
	}
	return d.GetUserProfilePicture(ctx, userID)
}

var _ IdentityService = (*LazyService)(nil)
