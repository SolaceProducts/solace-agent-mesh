// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bindings

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"maps"
	"reflect"
	"slices"
	"sync"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/backoff"
	"github.com/SolaceProducts/solace-agent-mesh/internal/llm"
	"github.com/SolaceProducts/solace-agent-mesh/internal/modelclient"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
)

var ErrAliasUnbound = errors.New("model alias unbound")

var ErrNotChatModel = errors.New("model alias is a decision model, not a chat model")

const aliasErrFmt = "%w: %q"

var errRegistryClosed = errors.New("bindings registry closed")

const (
	defaultRetryInitial = time.Second
	defaultRetryMax     = 30 * time.Second
)

type Fetcher func(ctx context.Context, alias string) (map[string]any, error)

type ClientBuilder func(ctx context.Context, rawConfig map[string]any, logger *slog.Logger) (llm.Client, error)

func DefaultClientBuilder(ctx context.Context, rawConfig map[string]any, logger *slog.Logger) (llm.Client, error) {
	return buildClient(ctx, rawConfig, logger, nil)
}

func NewClientBuilder(grants llm.GrantTokens) ClientBuilder {
	return func(ctx context.Context, rawConfig map[string]any, logger *slog.Logger) (llm.Client, error) {
		return buildClient(ctx, rawConfig, logger, grants)
	}
}

func buildClient(ctx context.Context, rawConfig map[string]any, logger *slog.Logger, grants llm.GrantTokens) (llm.Client, error) {
	built, err := modelclient.Build(ctx, rawConfig, modelclient.Options{Logger: logger, Grants: grants})
	if err != nil {
		return nil, err
	}
	return built.Client, nil
}

type BindingsRegistry struct {
	mu      sync.RWMutex
	models  map[string]*modelBinding
	hooks   []func(key string)
	wants   []func(key string) bool
	fetcher Fetcher
	builder ClientBuilder
	logger  *slog.Logger
	grants  llm.GrantTokens

	lineMu sync.Mutex
	lines  map[string]*pullLine

	retryInitial time.Duration
	retryMax     time.Duration
	pullCtx      context.Context
	stopPulls    context.CancelFunc
}

type pullLine struct {
	queue   []bool
	current *pull
	next    *pull
	wake    chan struct{}
}

type pull struct {
	done chan struct{}
	res  *resolvedBinding
	err  error
}

type Option func(*BindingsRegistry)

func WithFetcher(f Fetcher) Option {
	return func(r *BindingsRegistry) { r.fetcher = f }
}

func WithLogger(l *slog.Logger) Option {
	return func(r *BindingsRegistry) {
		if l != nil {
			r.logger = l.With("logger_name", "bindings-registry")
		}
	}
}

func WithClientBuilder(b ClientBuilder) Option {
	return func(r *BindingsRegistry) {
		if b != nil {
			r.builder = b
		}
	}
}

func WithGrantTokens(g llm.GrantTokens) Option {
	return func(r *BindingsRegistry) { r.grants = g }
}

func WithRetryBackoff(initial, maxWait time.Duration) Option {
	return func(r *BindingsRegistry) {
		if initial > 0 && maxWait >= initial {
			r.retryInitial, r.retryMax = initial, maxWait
		}
	}
}

func New(opts ...Option) *BindingsRegistry {
	r := &BindingsRegistry{
		models:       make(map[string]*modelBinding),
		lines:        make(map[string]*pullLine),
		logger:       slog.Default().With("logger_name", "bindings-registry"),
		builder:      DefaultClientBuilder,
		retryInitial: defaultRetryInitial,
		retryMax:     defaultRetryMax,
	}
	for _, opt := range opts {
		opt(r)
	}
	r.pullCtx, r.stopPulls = context.WithCancel(context.Background())
	return r
}

func (r *BindingsRegistry) Close() {
	r.stopPulls()
}

func (r *BindingsRegistry) OnChange(fn func(key string)) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.hooks = append(r.hooks, fn)
	r.mu.Unlock()
}

func (r *BindingsRegistry) Want(fn func(key string) bool) {
	if fn == nil {
		return
	}
	r.mu.Lock()
	r.wants = append(r.wants, fn)
	r.mu.Unlock()
}

func (r *BindingsRegistry) wanted(key string) bool {
	r.mu.RLock()
	wants := slices.Clone(r.wants)
	r.mu.RUnlock()
	for _, fn := range wants {
		if fn(key) {
			return true
		}
	}
	return false
}

func (r *BindingsRegistry) changed(key string) {
	r.mu.RLock()
	hooks := slices.Clone(r.hooks)
	r.mu.RUnlock()
	for _, fn := range hooks {
		fn(key)
	}
}

func (r *BindingsRegistry) ModelClient(alias string) (llm.Client, error) {
	b := r.lookup(alias)
	switch {
	case b == nil:
		return nil, fmt.Errorf(aliasErrFmt, ErrAliasUnbound, alias)
	case b.decision:
		return nil, fmt.Errorf(aliasErrFmt, ErrNotChatModel, alias)
	}
	return b.client, nil
}

func (r *BindingsRegistry) UpsertModel(ctx context.Context, alias string, rawConfig map[string]any) error {
	if alias == "" {
		return errors.New("alias must be non-empty")
	}
	if rawConfig == nil {
		return fmt.Errorf("raw config for alias %q must be non-nil", alias)
	}

	decision := samapp.IsDecisionModel(rawConfig)
	var live *liveClient
	if !decision {
		client, err := r.builder(ctx, rawConfig, r.logger)
		if err != nil {
			return fmt.Errorf("build client for alias %q: %w", alias, err)
		}
		live = newLiveClient(client)
	}

	configCopy := cloneRaw(rawConfig)

	r.mu.Lock()
	b, ok := r.models[alias]
	if !ok {
		b = newModelBinding(alias, live, configCopy)
		b.decision = decision
		r.models[alias] = b
		r.mu.Unlock()
	} else {
		b.rawConfig = configCopy
		b.decision = decision
		b.replace(live)
		r.mu.Unlock()
	}

	r.logger.Debug("model binding upserted", "alias", alias)
	r.changed(alias)
	return nil
}

func (r *BindingsRegistry) DeleteModel(alias string) bool {
	if alias == "" {
		return false
	}
	r.mu.Lock()
	b, ok := r.models[alias]
	if ok {
		delete(r.models, alias)
		b.replace(nil)
	}
	r.mu.Unlock()
	if ok {
		r.logger.Info("model binding deleted", "alias", alias)
		r.changed(alias)
	}
	return ok
}

func (r *BindingsRegistry) Notify(key string) {
	if key == "" || r.fetcher == nil {
		return
	}
	r.lineMu.Lock()
	defer r.lineMu.Unlock()
	if _, pulling := r.lines[key]; !pulling && r.lookup(key) == nil && !r.wanted(key) {
		return
	}
	r.enqueueLocked(key, true)
}

func (r *BindingsRegistry) Refresh(key string) {
	if key == "" || r.fetcher == nil {
		return
	}
	r.lineMu.Lock()
	defer r.lineMu.Unlock()
	r.enqueueLocked(key, true)
}

func (r *BindingsRegistry) ResolveOrFetch(ctx context.Context, alias string) (llm.Client, error) {
	res, err := r.resolveOrFetch(ctx, alias)
	if err != nil {
		return nil, err
	}
	if res.decision {
		return nil, fmt.Errorf(aliasErrFmt, ErrNotChatModel, alias)
	}
	return res.client, nil
}

func (r *BindingsRegistry) GrantTokens() llm.GrantTokens {
	if r == nil {
		return nil
	}
	return r.grants
}

func (r *BindingsRegistry) ResolveRawOrFetch(ctx context.Context, alias string) (map[string]any, error) {
	res, err := r.resolveOrFetch(ctx, alias)
	if err != nil {
		return nil, err
	}
	return cloneRaw(res.raw), nil
}

func (r *BindingsRegistry) ResolveWithRawOrFetch(ctx context.Context, alias string) (llm.Client, map[string]any, error) {
	res, err := r.resolveOrFetch(ctx, alias)
	if err != nil {
		return nil, nil, err
	}
	if res.decision {
		return nil, nil, fmt.Errorf(aliasErrFmt, ErrNotChatModel, alias)
	}
	return res.client, cloneRaw(res.raw), nil
}

type resolvedBinding struct {
	client   llm.Client
	raw      map[string]any
	decision bool
}

func (r *BindingsRegistry) resolveOrFetch(ctx context.Context, alias string) (*resolvedBinding, error) {
	if alias == "" {
		return nil, errors.New("alias must be non-empty")
	}

	if b := r.lookup(alias); b != nil {
		return b, nil
	}

	if r.fetcher == nil {
		return nil, fmt.Errorf("%w: %q (no fetcher configured)", ErrAliasUnbound, alias)
	}

	p := r.awaitPull(alias)
	select {
	case <-p.done:
		if p.err != nil {
			return nil, p.err
		}
		return p.res, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func (r *BindingsRegistry) awaitPull(key string) *pull {
	r.lineMu.Lock()
	defer r.lineMu.Unlock()
	if b := r.lookup(key); b != nil {
		done := make(chan struct{})
		close(done)
		return &pull{done: done, res: b}
	}
	l := r.lines[key]
	if l != nil && l.current != nil {
		return l.current
	}
	switch {
	case l == nil:
		l = r.enqueueLocked(key, false)
	case len(l.queue) == 0:
		l.queue = append(l.queue, false)
	default:
		select {
		case l.wake <- struct{}{}:
		default:
		}
	}
	if l.next == nil {
		l.next = &pull{done: make(chan struct{})}
	}
	return l.next
}

func (r *BindingsRegistry) enqueueLocked(key string, change bool) *pullLine {
	l := r.lines[key]
	if l == nil {
		l = &pullLine{wake: make(chan struct{}, 1)}
		r.lines[key] = l
		go r.runLine(key, l)
	}
	l.queue = append(l.queue, change)
	return l
}

func (r *BindingsRegistry) runLine(key string, l *pullLine) {
	bo := backoff.Exponential{Initial: r.retryInitial, Max: r.retryMax, Jitter: 0.2}
	failures := 0
	for {
		r.lineMu.Lock()
		if len(l.queue) == 0 {
			delete(r.lines, key)
			r.lineMu.Unlock()
			return
		}
		change := l.queue[0]
		p := l.next
		if p == nil {
			p = &pull{done: make(chan struct{})}
		}
		l.next, l.current = nil, p
		r.lineMu.Unlock()

		var unreachable bool
		p.res, unreachable, p.err = r.pullOnce(key, change)

		retry := unreachable && change
		r.lineMu.Lock()
		l.current = nil
		close(p.done)
		if !retry {
			l.queue = l.queue[1:]
			r.lineMu.Unlock()
			bo.Reset()
			failures = 0
			continue
		}
		r.lineMu.Unlock()

		failures++
		r.logger.Warn("model binding pull failed; keeping the current binding and retrying", "alias", key, "attempt", failures, "error", p.err)
		if !r.waitRetry(&bo, l) {
			r.endLine(key, l)
			return
		}
	}
}

func (r *BindingsRegistry) waitRetry(bo *backoff.Exponential, l *pullLine) bool {
	t := time.NewTimer(bo.Next())
	defer t.Stop()
	select {
	case <-t.C:
	case <-l.wake:
	case <-r.pullCtx.Done():
		return false
	}
	return true
}

func (r *BindingsRegistry) endLine(key string, l *pullLine) {
	r.lineMu.Lock()
	defer r.lineMu.Unlock()
	delete(r.lines, key)
	if l.next != nil {
		l.next.err = errRegistryClosed
		close(l.next.done)
		l.next = nil
	}
}

func (r *BindingsRegistry) pullOnce(key string, change bool) (res *resolvedBinding, unreachable bool, err error) {
	raw, err := r.fetcher(r.pullCtx, key)
	if err != nil {
		return nil, true, fmt.Errorf("fetch alias %q: %w", key, err)
	}
	if raw == nil {
		if r.lookup(key) != nil {
			if samapp.IsProtectedModelAlias(key) {
				r.logger.Error("platform has no config for a system model alias; keeping the current binding", "alias", key)
			} else {
				r.DeleteModel(key)
			}
		}
		return nil, false, fmt.Errorf("%w: %q (platform has no config)", ErrAliasUnbound, key)
	}
	if b := r.lookup(key); b != nil && reflect.DeepEqual(b.raw, cloneRaw(raw)) {
		return b, false, nil
	}
	if upsertErr := r.UpsertModel(r.pullCtx, key, raw); upsertErr != nil {
		if change {
			r.logger.Warn("pulled model config does not build; keeping the current binding", "alias", key, "error", upsertErr)
		}
		return nil, false, upsertErr
	}
	b := r.lookup(key)
	if b == nil {
		return nil, false, fmt.Errorf("%w: %q (binding vanished after upsert)", ErrAliasUnbound, key)
	}
	return b, false, nil
}

func (r *BindingsRegistry) lookup(alias string) *resolvedBinding {
	r.mu.RLock()
	b, ok := r.models[alias]
	if !ok {
		r.mu.RUnlock()
		return nil
	}
	raw := b.rawConfig
	decision := b.decision
	r.mu.RUnlock()
	if decision {
		return &resolvedBinding{raw: raw, decision: true}
	}
	if b.current.Load() == nil {
		return nil
	}
	return &resolvedBinding{client: b, raw: raw}
}

func (r *BindingsRegistry) Snapshot() map[string]any {
	r.mu.RLock()
	defer r.mu.RUnlock()
	models := make(map[string]any, len(r.models))
	for alias, b := range r.models {
		models[alias] = cloneRaw(b.rawConfig)
	}
	return map[string]any{
		"models": models,
	}
}

func cloneRaw(m map[string]any) map[string]any {
	if m == nil {
		return nil
	}
	data, err := json.Marshal(m)
	if err != nil {
		out := make(map[string]any, len(m))
		maps.Copy(out, m)
		return out
	}
	var out map[string]any
	if err := json.Unmarshal(data, &out); err != nil {
		return m
	}
	return out
}
