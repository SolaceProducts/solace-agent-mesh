// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package searchtool

import (
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

const (
	unresponsiveClusterWait = 40 * time.Second
	stalledWriteWait        = 100 * time.Second
)

func startBlackholeServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var (
		mu    sync.Mutex
		conns []net.Conn
	)
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			mu.Lock()
			conns = append(conns, c)
			mu.Unlock()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		mu.Lock()
		defer mu.Unlock()
		for _, c := range conns {
			_ = c.Close()
		}
	})
	return "http://" + ln.Addr().String()
}

func TestDynamicSchema_UnresponsiveClusterReportsClusterStatus(t *testing.T) {
	for _, backend := range []string{"elasticsearch", "opensearch"} {
		t.Run(backend, func(t *testing.T) {
			t.Parallel()
			endpoint := startBlackholeServer(t)

			done := make(chan *sdk.SchemaOverride, 1)
			go func() {
				done <- dynamicSchema(map[string]any{
					"backend":           backend,
					"connection_string": endpoint,
				})
			}()

			select {
			case override := <-done:
				if override == nil {
					t.Fatal("dynamicSchema returned nil")
				}
				if !strings.Contains(override.Instructions, "## Cluster Status") {
					t.Errorf("Instructions lack a Cluster Status section:\n%s", override.Instructions)
				}
				if strings.Contains(override.Instructions, "## Index Schema") {
					t.Errorf("Instructions claim an index schema for an unresponsive cluster:\n%s", override.Instructions)
				}
			case <-time.After(unresponsiveClusterWait):
				t.Fatalf("dynamicSchema still blocked on an unresponsive cluster after %s", unresponsiveClusterWait)
			}
		})
	}
}

func TestOutboundHTTPTransport_BoundsConnectAndResponseHeaders(t *testing.T) {
	tr := outboundHTTPTransport(false)

	if tr.DialContext == nil {
		t.Error("DialContext is nil: connecting to a black-holed address has no dial timeout")
	}
	if tr.TLSHandshakeTimeout <= 0 {
		t.Errorf("TLSHandshakeTimeout = %s, want > 0", tr.TLSHandshakeTimeout)
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Errorf("ResponseHeaderTimeout = %s, want > 0", tr.ResponseHeaderTimeout)
	}
}

func TestOutboundHTTPTransport_KeepsDefaultTransportPooling(t *testing.T) {
	def := http.DefaultTransport.(*http.Transport)
	tr := outboundHTTPTransport(false)

	if tr.IdleConnTimeout != def.IdleConnTimeout || tr.IdleConnTimeout <= 0 {
		t.Errorf("IdleConnTimeout = %s, want the default %s: idle connections of a per-query client would never close", tr.IdleConnTimeout, def.IdleConnTimeout)
	}
	if tr.MaxIdleConns != def.MaxIdleConns || tr.MaxIdleConns <= 0 {
		t.Errorf("MaxIdleConns = %d, want the default %d", tr.MaxIdleConns, def.MaxIdleConns)
	}
	if !tr.ForceAttemptHTTP2 {
		t.Error("ForceAttemptHTTP2 = false, want true")
	}
	if tr.ExpectContinueTimeout != def.ExpectContinueTimeout || tr.ExpectContinueTimeout <= 0 {
		t.Errorf("ExpectContinueTimeout = %s, want the default %s", tr.ExpectContinueTimeout, def.ExpectContinueTimeout)
	}
}

func TestOutboundHTTPTransport_FallbackWhenDefaultTransportReplaced(t *testing.T) {
	original := http.DefaultTransport
	http.DefaultTransport = roundTripperStub{}
	t.Cleanup(func() { http.DefaultTransport = original })

	tr := outboundHTTPTransport(false)

	if tr.IdleConnTimeout != 90*time.Second || tr.MaxIdleConns != 100 || !tr.ForceAttemptHTTP2 {
		t.Errorf("fallback transport lost pooling: IdleConnTimeout=%s MaxIdleConns=%d ForceAttemptHTTP2=%v", tr.IdleConnTimeout, tr.MaxIdleConns, tr.ForceAttemptHTTP2)
	}
	if tr.ResponseHeaderTimeout <= 0 {
		t.Errorf("ResponseHeaderTimeout = %s, want > 0", tr.ResponseHeaderTimeout)
	}
}

func TestElasticsearchIndex_StalledWriteIsSentOnceAndReturns(t *testing.T) {
	t.Parallel()
	var indexRequests atomic.Int32
	release := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Elastic-Product", "Elasticsearch")
		if r.Method == http.MethodGet && r.URL.Path == "/" {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"version":{"number":"8.19.0"}}`))
			return
		}
		indexRequests.Add(1)
		<-release
	}))
	t.Cleanup(srv.Close)
	t.Cleanup(func() { close(release) })

	backend := newElasticsearchBackend(map[string]any{"connection_string": srv.URL})
	if err := backend.Connect(t.Context()); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	op := "index"
	done := make(chan error, 1)
	go func() {
		_, err := backend.ExecuteQuery(t.Context(), QueryParams{Index: "docs", Operation: &op, Query: `{"title":"x"}`})
		done <- err
	}()

	select {
	case err := <-done:
		if err == nil {
			t.Fatal("ExecuteQuery succeeded against a stalled cluster")
		}
	case <-time.After(stalledWriteWait):
		t.Fatalf("ExecuteQuery still blocked or retrying after %s; stalled index request reached the cluster %d times, want it to fail after exactly 1", stalledWriteWait, indexRequests.Load())
	}
	if got := indexRequests.Load(); got != 1 {
		t.Errorf("stalled index request reached the cluster %d times, want exactly 1: a timed-out write must not be replayed", got)
	}
}

func TestElasticsearchConnect_RefusedConnectionReturnsError(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	if err := ln.Close(); err != nil {
		t.Fatalf("close listener: %v", err)
	}

	backend := newElasticsearchBackend(map[string]any{"connection_string": "http://" + addr})
	if err := backend.Connect(t.Context()); err == nil {
		t.Fatal("Connect succeeded against a closed port")
	}
}

type roundTripperStub struct{}

func (roundTripperStub) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, http.ErrNotSupported
}
