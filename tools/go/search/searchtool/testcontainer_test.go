// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosqlintegration

package searchtool

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"sync/atomic"
	"testing"
	"time"

	"github.com/moby/moby/client"
	"github.com/testcontainers/testcontainers-go"
	"github.com/testcontainers/testcontainers-go/wait"
)

const (
	esImage         = "docker.elastic.co/elasticsearch/elasticsearch:8.15.0"
	opensearchImage = "opensearchproject/opensearch:2.13.0"
	startupTimeout  = 240 * time.Second
)

var (
	esEndpoint string
	esSkip     string
	osEndpoint string
	osSkip     string
)

func TestMain(m *testing.M) {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo})))

	ctx, cancel := context.WithTimeout(context.Background(), startupTimeout+8*time.Minute)
	defer cancel()

	var stops []func(context.Context) error

	if !dockerAvailable(ctx) {
		esSkip = "docker daemon not reachable"
		osSkip = "docker daemon not reachable"
		slog.Warn("docker daemon not reachable; search dbintegration tests will skip")
	} else {
		if ep, stop, err := startElasticsearch(ctx); err != nil {
			esSkip = fmt.Sprintf("elasticsearch container start failed: %v", err)
			slog.Warn("elasticsearch container start failed", "err", err)
		} else {
			esEndpoint = ep
			stops = append(stops, stop)
			slog.Info("elasticsearch container ready", "endpoint", ep)
		}

		if ep, stop, err := startOpenSearch(ctx); err != nil {
			osSkip = fmt.Sprintf("opensearch container start failed: %v", err)
			slog.Warn("opensearch container start failed", "err", err)
		} else {
			osEndpoint = ep
			stops = append(stops, stop)
			slog.Info("opensearch container ready", "endpoint", ep)
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

func startSearchContainer(ctx context.Context, req testcontainers.ContainerRequest) (string, func(context.Context) error, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: req,
		Started:          true,
	})
	if err != nil {
		return "", nil, err
	}
	ep, err := c.PortEndpoint(ctx, "9200/tcp", "http")
	if err != nil {
		_ = c.Terminate(ctx)
		return "", nil, fmt.Errorf("port endpoint: %w", err)
	}
	return ep, func(ctx context.Context) error { return c.Terminate(ctx) }, nil
}

func startElasticsearch(ctx context.Context) (string, func(context.Context) error, error) {
	return startSearchContainer(ctx, testcontainers.ContainerRequest{
		Image:        esImage,
		ExposedPorts: []string{"9200/tcp"},
		Env: map[string]string{
			"discovery.type":         "single-node",
			"xpack.security.enabled": "false",
			"ES_JAVA_OPTS":           "-Xms512m -Xmx512m",
		},
		WaitingFor: wait.ForHTTP("/_cluster/health").
			WithPort("9200/tcp").
			WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
			WithStartupTimeout(startupTimeout),
	})
}

func startSecuredElasticsearch(ctx context.Context, password string) (string, func(context.Context) error, error) {
	c, err := testcontainers.GenericContainer(ctx, testcontainers.GenericContainerRequest{
		ContainerRequest: testcontainers.ContainerRequest{
			Image:        esImage,
			ExposedPorts: []string{"9200/tcp"},
			Env: map[string]string{
				"discovery.type":                  "single-node",
				"xpack.security.enabled":          "true",
				"xpack.security.http.ssl.enabled": "false",
				"ELASTIC_PASSWORD":                password,
				"ES_JAVA_OPTS":                    "-Xms512m -Xmx512m",
			},
			WaitingFor: wait.ForHTTP("/_cluster/health").
				WithPort("9200/tcp").
				WithBasicAuth("elastic", password).
				WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
				WithStartupTimeout(startupTimeout),
		},
		Started: true,
	})
	if err != nil {
		return "", nil, err
	}
	ep, err := c.PortEndpoint(ctx, "9200/tcp", "http")
	if err != nil {
		_ = c.Terminate(ctx)
		return "", nil, fmt.Errorf("port endpoint: %w", err)
	}
	return ep, func(ctx context.Context) error { return c.Terminate(ctx) }, nil
}

func startOpenSearch(ctx context.Context) (string, func(context.Context) error, error) {
	return startSearchContainer(ctx, testcontainers.ContainerRequest{
		Image:        opensearchImage,
		ExposedPorts: []string{"9200/tcp"},
		Env: map[string]string{
			"discovery.type":              "single-node",
			"DISABLE_SECURITY_PLUGIN":     "true",
			"DISABLE_INSTALL_DEMO_CONFIG": "true",
			"OPENSEARCH_JAVA_OPTS":        "-Xms512m -Xmx512m",
		},
		WaitingFor: wait.ForHTTP("/_cluster/health").
			WithPort("9200/tcp").
			WithStatusCodeMatcher(func(status int) bool { return status == http.StatusOK }).
			WithStartupTimeout(startupTimeout),
	})
}

func connectedES(t *testing.T) *elasticsearchBackend {
	t.Helper()
	if esSkip != "" {
		t.Skipf("elasticsearch: %s", esSkip)
	}
	b := newElasticsearchBackend(map[string]any{
		"backend":           "elasticsearch",
		"connection_string": esEndpoint,
	})
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect elasticsearch: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

func connectedOpenSearch(t *testing.T) *openSearchBackend {
	t.Helper()
	if osSkip != "" {
		t.Skipf("opensearch: %s", osSkip)
	}
	b := newOpenSearchBackend(map[string]any{
		"backend":           "opensearch",
		"connection_string": osEndpoint,
	})
	if err := b.Connect(t.Context()); err != nil {
		t.Fatalf("connect opensearch: %v", err)
	}
	t.Cleanup(func() { _ = b.Close(context.Background()) })
	return b
}

func refreshIndex(t *testing.T, endpoint, index string, creds ...string) {
	t.Helper()
	req, err := http.NewRequestWithContext(t.Context(), http.MethodPost, endpoint+"/"+index+"/_refresh", nil)
	if err != nil {
		t.Fatalf("build refresh request: %v", err)
	}
	if len(creds) == 2 {
		req.SetBasicAuth(creds[0], creds[1])
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("refresh %s: %v", index, err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("refresh %s: status %d", index, resp.StatusCode)
	}
}

var indexCounter atomic.Uint64

func uniqueIndex(t *testing.T) string {
	t.Helper()
	return fmt.Sprintf("tt_%d", indexCounter.Add(1))
}

func requireHits(t *testing.T, qr *QueryResult, want int) {
	t.Helper()
	if qr == nil {
		t.Fatal("nil QueryResult")
	}
	if len(qr.Documents) != want {
		t.Fatalf("documents = %d, want %d (%+v)", len(qr.Documents), want, qr.Documents)
	}
}

func strPtr(s string) *string { return &s }
