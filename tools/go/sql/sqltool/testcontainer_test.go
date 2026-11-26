// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build dbintegration

package sqltool

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"os"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	"golang.org/x/sync/errgroup"
)

const (
	postgresImage = "postgres:16-alpine"
	mysqlImage    = "mysql:8.4"
	mssqlImage    = "mcr.microsoft.com/mssql/server:2022-latest"
)

const (
	startupTimeoutDefault = 90 * time.Second
	pullTimeout           = 10 * time.Minute
)

type dbHarness interface {
	Dialect() Dialect
	NewDB(t *testing.T) *sql.DB
}

type harnessRegistry struct {
	mu       sync.RWMutex
	harness  map[Dialect]dbHarness
	skipMsg  map[Dialect]string
	stopFns  []func(context.Context) error
	stopOnce sync.Once
}

var registry = &harnessRegistry{
	harness: map[Dialect]dbHarness{},
	skipMsg: map[Dialect]string{},
}

func (r *harnessRegistry) set(d Dialect, h dbHarness, stop func(context.Context) error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.harness[d] = h
	if stop != nil {
		r.stopFns = append(r.stopFns, stop)
	}
}

func (r *harnessRegistry) skip(d Dialect, msg string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.skipMsg[d] = msg
}

func (r *harnessRegistry) get(t *testing.T, d Dialect) dbHarness {
	t.Helper()
	r.mu.RLock()
	defer r.mu.RUnlock()
	if msg, ok := r.skipMsg[d]; ok {
		t.Skipf("%s: %s", d, msg)
	}
	h, ok := r.harness[d]
	if !ok {
		t.Skipf("%s: harness not initialised", d)
	}
	return h
}

func (r *harnessRegistry) shutdown(ctx context.Context) {
	r.stopOnce.Do(func() {
		r.mu.Lock()
		fns := r.stopFns
		r.stopFns = nil
		r.mu.Unlock()
		for _, fn := range fns {
			if err := fn(ctx); err != nil {
				slog.Warn("container stop failed", "err", err)
			}
		}
	})
}

type dialectStarter struct {
	dialect Dialect
	image   string
	start   func(ctx context.Context) (dbHarness, func(context.Context) error, error)
}

func TestMain(m *testing.M) {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(logger)

	ctx, cancel := context.WithTimeout(context.Background(), pullTimeout+startupTimeoutDefault+30*time.Second)
	defer cancel()

	if !dockerAvailable(ctx) {
		slog.Warn("docker daemon not reachable; all dbintegration tests will skip")
		code := m.Run()
		os.Exit(code)
	}

	starters := []dialectStarter{
		{DialectPostgres, postgresImage, startPostgresHarness},
		{DialectMySQL, mysqlImage, startMySQLHarness},
		{DialectMSSQL, mssqlImage, startMSSQLHarness},
	}

	prePullImages(ctx, starters)

	startAll(ctx, starters)

	code := m.Run()
	registry.shutdown(context.Background())
	os.Exit(code)
}

func dockerAvailable(ctx context.Context) bool {
	provider, err := testcontainers.ProviderDocker.GetProvider()
	if err != nil {
		return false
	}
	dp, ok := provider.(*testcontainers.DockerProvider)
	if !ok {
		return false
	}
	pingCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, err = dp.Client().Ping(pingCtx, client.PingOptions{})
	return err == nil
}

func prePullImages(ctx context.Context, starters []dialectStarter) {
	provider, err := testcontainers.ProviderDocker.GetProvider()
	if err != nil {
		slog.Warn("get docker provider failed; skipping pre-pull", "err", err)
		return
	}
	dp, ok := provider.(*testcontainers.DockerProvider)
	if !ok {
		slog.Warn("provider is not DockerProvider; skipping pre-pull")
		return
	}

	pullCtx, cancel := context.WithTimeout(ctx, pullTimeout)
	defer cancel()

	g, gctx := errgroup.WithContext(pullCtx)
	for _, s := range starters {
		g.Go(func() error {
			start := time.Now()
			if err := dp.PullImage(gctx, s.image); err != nil {
				slog.Warn("image pull failed",
					"dialect", s.dialect, "image", s.image, "err", err)
				return nil
			}
			slog.Info("image ready",
				"dialect", s.dialect, "image", s.image,
				"elapsed_ms", time.Since(start).Milliseconds())
			return nil
		})
	}
	_ = g.Wait()
}

func startAll(ctx context.Context, starters []dialectStarter) {
	g, gctx := errgroup.WithContext(ctx)
	for _, s := range starters {
		g.Go(func() error {
			start := time.Now()
			startCtx, cancel := context.WithTimeout(gctx, startupTimeoutDefault)
			defer cancel()

			h, stop, err := s.start(startCtx)
			elapsed := time.Since(start).Milliseconds()
			if err != nil {
				slog.Warn("container start failed",
					"dialect", s.dialect, "elapsed_ms", elapsed, "err", err)
				registry.skip(s.dialect, fmt.Sprintf("container start failed: %v", err))
				return nil
			}
			slog.Info("container ready", "dialect", s.dialect, "elapsed_ms", elapsed)
			registry.set(s.dialect, h, stop)
			return nil
		})
	}
	_ = g.Wait()
}

func sanitiseDBName(name string) string {
	var b strings.Builder
	b.Grow(len(name))
	for _, r := range strings.ToLower(name) {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteRune('_')
		}
	}
	return b.String()
}

var dbNameCounter atomic.Uint64

func uniqueDBName(t *testing.T, prefix string) string {
	t.Helper()
	tag := sanitiseDBName(t.Name())
	const maxTag = 24
	if len(tag) > maxTag {
		tag = tag[:maxTag]
	}
	return fmt.Sprintf("%s_%s_%d", prefix, tag, dbNameCounter.Add(1))
}
