// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package webuisettings

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/sync/singleflight"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
)

type Fetcher func(ctx context.Context) (*Settings, error)

type Registry struct {
	cached        atomic.Pointer[Settings]
	mu            sync.Mutex
	gen           atomic.Uint64
	failedAt      atomic.Int64
	failureWindow time.Duration
	warming       atomic.Bool
	refreshSignal chan struct{}
	retryInitial  time.Duration
	retryMax      time.Duration
	resync        time.Duration

	fetcher  Fetcher
	resolves singleflight.Group
	logger   *slog.Logger
}

const warmTimeout = 10 * time.Second

const (
	refreshRetryInitial = time.Second
	refreshRetryMax     = 30 * time.Second
	refreshRetryJitter  = 0.2
)

const resyncInterval = 5 * time.Minute

const negativeCacheWindow = 5 * time.Second

var errFetchRecentlyFailed = errors.New("webui settings fetch failed recently; not retried yet")

var sentinelEmpty = &Settings{}

func NewRegistry(fetcher Fetcher, logger *slog.Logger) *Registry {
	if logger == nil {
		logger = slog.Default()
	}
	return &Registry{
		fetcher:       fetcher,
		failureWindow: negativeCacheWindow,
		refreshSignal: make(chan struct{}, 1),
		retryInitial:  refreshRetryInitial,
		retryMax:      refreshRetryMax,
		resync:        resyncInterval,
		logger:        logger.With("logger_name", "webui-settings-registry"),
	}
}

func (r *Registry) Get(ctx context.Context) (*Settings, error) {
	if cached := r.cached.Load(); cached != nil {
		if cached == sentinelEmpty {
			return nil, nil
		}
		return cached, nil
	}

	if r.fetcher == nil {
		return nil, nil
	}

	if r.recentlyFailed() {
		return nil, errFetchRecentlyFailed
	}

	firstFailure := r.failedAt.Load() == 0

	fetchCtx := context.WithoutCancel(ctx)
	ch := r.resolves.DoChan("settings", func() (any, error) {
		if cached := r.cached.Load(); cached != nil {
			return cached, nil
		}
		gen := r.gen.Load()
		row, err := r.fetcher(fetchCtx)
		if err != nil {
			r.failedAt.Store(time.Now().UnixNano())
			return nil, fmt.Errorf("fetch webui settings: %w", err)
		}
		r.failedAt.Store(0)
		val := row
		if val == nil {
			val = sentinelEmpty
		}
		r.publishIfCurrent(gen, val)
		return val, nil
	})

	select {
	case res := <-ch:
		if res.Err != nil {
			if firstFailure {
				r.logger.Warn("webui settings fetch failed; falling back to configured values", "error", res.Err)
			}
			return nil, res.Err
		}
		row := res.Val.(*Settings)
		if row == sentinelEmpty {
			return nil, nil
		}
		return row, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *Registry) Cached() (*Settings, bool) {
	if cached := r.cached.Load(); cached != nil {
		if cached == sentinelEmpty {
			return nil, true
		}
		return cached, true
	}
	r.Warm()
	return nil, false
}

func (r *Registry) Warm() {
	if r.fetcher == nil || r.recentlyFailed() {
		return
	}
	if !r.warming.CompareAndSwap(false, true) {
		return
	}
	go func() {
		defer r.warming.Store(false)
		ctx, cancel := context.WithTimeout(context.Background(), warmTimeout)
		defer cancel()
		_, _ = r.Get(ctx)
	}()
}

func (r *Registry) Refresh() {
	select {
	case r.refreshSignal <- struct{}{}:
	default:
	}
}

func (r *Registry) RunRefreshLoop(ctx context.Context) {
	if r.fetcher == nil {
		return
	}
	bo := backoff.Exponential{Initial: r.retryInitial, Max: r.retryMax, Jitter: refreshRetryJitter}
	resync := time.NewTicker(r.resync)
	defer resync.Stop()
	var retry *time.Timer
	defer func() {
		if retry != nil {
			retry.Stop()
		}
	}()
	for {
		var retryC <-chan time.Time
		if retry != nil {
			retryC = retry.C
		}
		select {
		case <-ctx.Done():
			return
		case <-r.refreshSignal:
		case <-retryC:
		case <-resync.C:
		}
		if retry != nil {
			retry.Stop()
			retry = nil
		}
		if err := r.refresh(ctx); err != nil {
			if ctx.Err() != nil {
				return
			}
			r.logger.Warn("webui settings refresh failed; keeping the cached values and retrying", "error", err)
			retry = time.NewTimer(bo.Next())
			continue
		}
		bo.Reset()
	}
}

func (r *Registry) refresh(ctx context.Context) error {
	r.mu.Lock()
	gen := r.gen.Add(1)
	r.mu.Unlock()

	pullCtx, cancel := context.WithTimeout(ctx, warmTimeout)
	defer cancel()
	row, err := r.fetcher(pullCtx)
	if err != nil {
		r.failedAt.Store(time.Now().UnixNano())
		return err
	}
	r.failedAt.Store(0)
	val := row
	if val.IsEmpty() {
		val = sentinelEmpty
	}
	r.publishIfCurrent(gen, val)
	return nil
}

func (r *Registry) Invalidate() {
	r.mu.Lock()
	r.gen.Add(1)
	r.cached.Store(nil)
	r.mu.Unlock()
	r.failedAt.Store(0)
}

func (r *Registry) publishIfCurrent(gen uint64, val *Settings) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.gen.Load() == gen {
		r.cached.Store(val)
	}
}

func (r *Registry) recentlyFailed() bool {
	failedAt := r.failedAt.Load()
	if failedAt == 0 || r.failureWindow <= 0 {
		return false
	}
	return time.Since(time.Unix(0, failedAt)) < r.failureWindow
}
