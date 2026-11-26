// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosqlintegration

package documentdb

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
	"github.com/testcontainers/testcontainers-go/modules/localstack"
	tcmongo "github.com/testcontainers/testcontainers-go/modules/mongodb"
)

const (
	mongoImage      = "mongo:7"
	localstackImage = "localstack/localstack:3"
	startupTimeout  = 180 * time.Second
)

var (
	mongoURI           string
	mongoSkip          string
	localstackEndpoint string
	dynamoSkip         string
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout+5*time.Minute)
	defer cancel()

	var stops []func(context.Context) error

	if !dockerAvailable(ctx) {
		mongoSkip = "docker daemon not reachable"
		dynamoSkip = "docker daemon not reachable"
		slog.Warn("docker daemon not reachable; document-db dbintegration tests will skip")
	} else {
		if uri, stop, err := startMongo(ctx); err != nil {
			mongoSkip = fmt.Sprintf("mongo container start failed: %v", err)
			slog.Warn("mongo container start failed", "err", err)
		} else {
			mongoURI = uri
			stops = append(stops, stop)
			slog.Info("mongo container ready", "uri", uri)
		}

		if ep, stop, err := startLocalStack(ctx); err != nil {
			dynamoSkip = fmt.Sprintf("localstack container start failed: %v", err)
			slog.Warn("localstack container start failed", "err", err)
		} else {
			localstackEndpoint = ep
			stops = append(stops, stop)
			slog.Info("localstack container ready", "endpoint", ep)
		}
	}

	code := m.Run()
	for _, stop := range stops {
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

func startMongo(ctx context.Context) (string, func(context.Context) error, error) {
	c, err := tcmongo.Run(ctx, mongoImage)
	if err != nil {
		return "", nil, err
	}
	uri, err := c.ConnectionString(ctx)
	if err != nil {
		_ = c.Terminate(ctx)
		return "", nil, fmt.Errorf("connection string: %w", err)
	}
	return uri, func(ctx context.Context) error { return c.Terminate(ctx) }, nil
}

func startLocalStack(ctx context.Context) (string, func(context.Context) error, error) {
	c, err := localstack.Run(ctx, localstackImage)
	if err != nil {
		return "", nil, err
	}
	ep, err := c.PortEndpoint(ctx, "4566/tcp", "http")
	if err != nil {
		_ = c.Terminate(ctx)
		return "", nil, fmt.Errorf("port endpoint: %w", err)
	}
	return ep, func(ctx context.Context) error { return c.Terminate(ctx) }, nil
}

func connectedMongo(t *testing.T, collection string) *mongoBackend {
	t.Helper()
	if mongoSkip != "" {
		t.Skipf("mongo: %s", mongoSkip)
	}
	b := newMongoBackend(map[string]any{
		"connection_string": mongoURI,
		"database":          "testdb",
		"collection":        collection,
	})
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect mongo: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

var collectionCounter atomic.Uint64

func uniqueCollection(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("tt_%d", collectionCounter.Add(1))
}

func requireDocs(t *testing.T, qr *QueryResult, want int) {
	t.Helper()
	if qr == nil {
		t.Fatal("nil QueryResult")
	}
	if len(qr.Documents) != want {
		t.Fatalf("documents = %d, want %d (%+v)", len(qr.Documents), want, qr.Documents)
	}
}
