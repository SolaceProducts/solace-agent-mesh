// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"bytes"
	"crypto/tls"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"testing"
	"time"
)

func streamHandler(done chan<- struct{}) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprint(w, "data: hello\n\n")
		w.(http.Flusher).Flush()
		<-r.Context().Done()
		done <- struct{}{}
	})
}

func openStream(t *testing.T, client *http.Client, url string) {
	t.Helper()
	resp, err := client.Get(url)
	if err != nil {
		t.Fatalf("open stream %s: %v", url, err)
	}
	t.Cleanup(func() { _ = resp.Body.Close() })
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("open stream %s: status %d", url, resp.StatusCode)
	}
}

func stopAndWaitForStreams(t *testing.T, proxy *GatewayProxy, done <-chan struct{}, n int) {
	t.Helper()
	start := time.Now()
	if err := proxy.Stop(t.Context()); err != nil {
		t.Fatalf("Stop returned %v, want nil when the shutdown timeout passes", err)
	}
	if elapsed := time.Since(start); elapsed < proxy.timeouts.ShutdownTimeout || elapsed >= 2*proxy.timeouts.ShutdownTimeout {
		t.Errorf("Stop took %s, want about the shutdown timeout %s", elapsed, proxy.timeouts.ShutdownTimeout)
	}
	for i := range n {
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Fatalf("%d of %d stream handlers still running after Stop", n-i, n)
		}
	}
}

func TestGatewayProxy_StopClosesOpenStreams(t *testing.T) {
	done := make(chan struct{}, 1)
	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.timeouts.ShutdownTimeout = 500 * time.Millisecond
	proxy.RegisterPrefixHandler("/", streamHandler(done))
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	openStream(t, http.DefaultClient, fmt.Sprintf("http://%s/stream", proxy.Addr()))
	stopAndWaitForStreams(t, proxy, done, 1)
}

func TestGatewayProxy_StopLetsShortRequestsFinish(t *testing.T) {
	entered := make(chan struct{})
	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.timeouts.ShutdownTimeout = 2 * time.Second
	proxy.RegisterPrefixHandler("/", http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		time.Sleep(200 * time.Millisecond)
		_, _ = fmt.Fprint(w, "done")
	}))
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	type result struct {
		status int
		body   string
		err    error
	}
	got := make(chan result, 1)
	go func() {
		resp, err := http.Get(fmt.Sprintf("http://%s/work", proxy.Addr()))
		if err != nil {
			got <- result{err: err}
			return
		}
		defer func() { _ = resp.Body.Close() }()
		body, err := io.ReadAll(resp.Body)
		got <- result{status: resp.StatusCode, body: string(body), err: err}
	}()
	<-entered

	start := time.Now()
	if err := proxy.Stop(t.Context()); err != nil {
		t.Fatalf("Stop returned %v, want nil", err)
	}
	if elapsed := time.Since(start); elapsed >= proxy.timeouts.ShutdownTimeout {
		t.Errorf("Stop took %s; it should return once the request finishes, before the %s shutdown timeout", elapsed, proxy.timeouts.ShutdownTimeout)
	}
	r := <-got
	if r.err != nil || r.status != http.StatusOK || r.body != "done" {
		t.Fatalf("in-flight request: status=%d body=%q err=%v, want 200 \"done\"", r.status, r.body, r.err)
	}
}

func TestGatewayProxy_StopClosesOpenStreams_DualPortTLS(t *testing.T) {
	certFile, keyFile := generateSelfSignedCert(t, t.TempDir())
	done := make(chan struct{}, 2)
	proxy := NewGatewayProxy("127.0.0.1:0", slog.Default())
	proxy.timeouts.ShutdownTimeout = 500 * time.Millisecond
	proxy.RegisterPrefixHandler("/", streamHandler(done))
	proxy.SetTLS(&ProxyTLSConfig{CertFile: certFile, KeyFile: keyFile}, "127.0.0.1:0")
	if err := proxy.Start(t.Context()); err != nil {
		t.Fatal(err)
	}

	tlsClient := &http.Client{Transport: &http.Transport{
		TLSClientConfig:   &tls.Config{InsecureSkipVerify: true},
		ForceAttemptHTTP2: true,
	}}
	openStream(t, http.DefaultClient, fmt.Sprintf("http://%s/stream", proxy.Addr()))
	openStream(t, tlsClient, fmt.Sprintf("https://%s/stream", proxy.TLSAddr()))
	stopAndWaitForStreams(t, proxy, done, 2)
}

func TestShutdownTimeoutFromEnv(t *testing.T) {
	tests := []struct {
		name     string
		value    string
		want     time.Duration
		wantWarn bool
	}{
		{name: "unset uses the default", want: DefaultServerTimeouts.ShutdownTimeout},
		{name: "set by the chart", value: "8", want: 8 * time.Second},
		{name: "not a number", value: "five", want: DefaultServerTimeouts.ShutdownTimeout, wantWarn: true},
		{name: "zero", value: "0", want: DefaultServerTimeouts.ShutdownTimeout, wantWarn: true},
		{name: "negative", value: "-5", want: DefaultServerTimeouts.ShutdownTimeout, wantWarn: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv(ShutdownTimeoutEnv, tt.value)
			var logs bytes.Buffer
			if got := ShutdownTimeoutFromEnv(slog.New(slog.NewTextHandler(&logs, nil))); got != tt.want {
				t.Errorf("ShutdownTimeoutFromEnv() = %s, want %s", got, tt.want)
			}
			if warned := strings.Contains(logs.String(), "level=WARN"); warned != tt.wantWarn {
				t.Errorf("WARN logged = %v, want %v: %s", warned, tt.wantWarn, logs.String())
			}
		})
	}
}
