// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package runtime

import (
	"context"
	"io"
	"log/slog"
	"net"
	"net/http"
	"testing"
	"time"
)

func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func freeAddr(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("reserve port: %v", err)
	}
	addr := l.Addr().String()
	_ = l.Close()
	return addr
}

func TestStartPprofServer_DisabledReturnsCallableNoopStop(t *testing.T) {
	t.Setenv("SAM_PPROF_ADDR", "")
	stop := StartPprofServer(discardLogger())
	if stop == nil {
		t.Fatal("expected a non-nil stop func")
	}
	stop(context.Background())
}

func TestStartPprofServer_EnabledServesAndShutsDown(t *testing.T) {
	addr := freeAddr(t)
	t.Setenv("SAM_PPROF_ADDR", addr)
	stop := StartPprofServer(discardLogger())

	url := "http://" + addr + "/debug/pprof/"
	var resp *http.Response
	var err error
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err = http.Get(url) //nolint:noctx // short-lived test request
		if err == nil {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("pprof endpoint never came up: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Errorf("GET %s = %d, want 200", url, resp.StatusCode)
	}
	_ = resp.Body.Close()

	stop(context.Background())

	if _, err := http.Get(url); err == nil { //nolint:noctx // short-lived test request
		t.Error("expected connection error after shutdown, got success")
	}
}
