// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosqlintegration

package graphdb

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	tcneo4j "github.com/testcontainers/testcontainers-go/modules/neo4j"
)

const (
	neo4jImage     = "neo4j:5"
	neo4jUser      = "neo4j"
	neo4jPassword  = "test_password_123"
	startupTimeout = 180 * time.Second
)

var (
	neo4jURI  string
	neo4jSkip string
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout+5*time.Minute)
	defer cancel()

	var stop func(context.Context) error
	if !dockerAvailable(ctx) {
		neo4jSkip = "docker daemon not reachable"
		slog.Warn("docker daemon not reachable; neo4j dbintegration tests will skip")
	} else if uri, s, err := startNeo4j(ctx); err != nil {
		neo4jSkip = fmt.Sprintf("neo4j container start failed: %v", err)
		slog.Warn("neo4j container start failed", "err", err)
	} else {
		neo4jURI = uri
		stop = s
		slog.Info("neo4j container ready", "uri", uri)
	}

	code := m.Run()
	if stop != nil {
		_ = stop(context.Background())
	}
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

func startNeo4j(ctx context.Context) (string, func(context.Context) error, error) {
	c, err := tcneo4j.Run(ctx, neo4jImage, tcneo4j.WithAdminPassword(neo4jPassword))
	if err != nil {
		return "", nil, err
	}
	uri, err := c.BoltUrl(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return "", nil, fmt.Errorf("bolt url: %w", err)
	}
	return uri, func(ctx context.Context) error { return c.Terminate(ctx) }, nil
}

func connectedNeo4j(t *testing.T) *neo4jBackend {
	t.Helper()
	if neo4jSkip != "" {
		t.Skipf("neo4j: %s", neo4jSkip)
	}
	b := newNeo4jBackend(map[string]any{
		"backend":           "neo4j",
		"connection_string": neo4jURI,
		"username":          neo4jUser,
		"password":          neo4jPassword,
		"database":          "neo4j",
	})
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect neo4j: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

var labelCounter atomic.Uint64

func uniqueLabel(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("TT_%d", labelCounter.Add(1))
}

func deleteLabel(b *neo4jBackend, label string) {
	_, _ = b.ExecuteQuery(context.Background(), QueryParams{Query: fmt.Sprintf("MATCH (n:%s) DETACH DELETE n", label)})
}

func requireRecords(t *testing.T, qr *QueryResult, want int) {
	t.Helper()
	if qr == nil {
		t.Fatal("nil QueryResult")
	}
	if len(qr.Records) != want {
		t.Fatalf("records = %d, want %d (%+v)", len(qr.Records), want, qr.Records)
	}
}
