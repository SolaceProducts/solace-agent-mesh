// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
)

type connectingBroker struct {
	broker.Broker
	errs  []error
	calls int
	lost  func()
}

func (b *connectingBroker) Connect(context.Context) error {
	b.calls++
	if b.calls <= len(b.errs) {
		return b.errs[b.calls-1]
	}
	return nil
}

func (b *connectingBroker) OnConnectionLost(fn func()) { b.lost = fn }

var errUnreachable = errors.New("unreachable")

func quietLogger() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func neverPermanent(error) bool { return false }

func TestConnectWithRetry_RetriesUntilConnected(t *testing.T) {
	brk := &connectingBroker{errs: []error{errUnreachable, errUnreachable}}
	retry := connectRetry{interval: time.Millisecond, deadline: time.Minute, permanent: neverPermanent}

	if err := connectWithRetry(t.Context(), brk, retry, quietLogger()); err != nil {
		t.Fatalf("connectWithRetry: %v", err)
	}
	if brk.calls != 3 {
		t.Errorf("Connect called %d times, want 3", brk.calls)
	}
}

func TestConnectWithRetry_PermanentErrorFailsAtOnce(t *testing.T) {
	brk := &connectingBroker{errs: []error{errUnreachable}}
	retry := connectRetry{interval: time.Millisecond, deadline: time.Minute, permanent: func(error) bool { return true }}

	if err := connectWithRetry(t.Context(), brk, retry, quietLogger()); !errors.Is(err, errUnreachable) {
		t.Fatalf("err = %v, want %v", err, errUnreachable)
	}
	if brk.calls != 1 {
		t.Errorf("Connect called %d times, want 1", brk.calls)
	}
}

func TestConnectWithRetry_GivesUpAtDeadline(t *testing.T) {
	brk := &connectingBroker{errs: []error{errUnreachable, errUnreachable}}
	retry := connectRetry{interval: time.Millisecond, deadline: 0, permanent: neverPermanent}

	if err := connectWithRetry(t.Context(), brk, retry, quietLogger()); !errors.Is(err, errUnreachable) {
		t.Fatalf("err = %v, want %v", err, errUnreachable)
	}
	if brk.calls != 1 {
		t.Errorf("Connect called %d times, want 1", brk.calls)
	}
}

func TestConnectWithRetry_StopsWhenContextEnds(t *testing.T) {
	brk := &connectingBroker{errs: []error{errUnreachable}}
	retry := connectRetry{interval: time.Hour, deadline: time.Hour, permanent: neverPermanent}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	if err := connectWithRetry(ctx, brk, retry, quietLogger()); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

func TestExitOnConnectionLost_ExitsTheProcess(t *testing.T) {
	code := -1
	original := exitProcess
	exitProcess = func(c int) { code = c }
	t.Cleanup(func() { exitProcess = original })
	brk := &connectingBroker{}

	exitOnConnectionLost(brk, quietLogger())
	brk.lost()

	if code != exitCodeConnectionLost {
		t.Fatalf("exit code %d, want %d", code, exitCodeConnectionLost)
	}
}

func TestExitOnConnectionLost_BrokerWithoutCapability(t *testing.T) {
	exitOnConnectionLost(broker.NewDevBroker(), quietLogger())
}

func TestResolveHealthAddrFlag_UsesConfigPortWhenFlagNotSet(t *testing.T) {
	cfg := map[string]any{"management_server": map[string]any{"port": 9191}}
	if got := ResolveHealthAddrFlag(cfg, ":8090"); got != ":9191" {
		t.Errorf("ResolveHealthAddrFlag = %q, want :9191", got)
	}
}

func TestInterimHealth_LiveAndNotReady(t *testing.T) {
	srv := httptest.NewServer(interimHealthHandler())
	t.Cleanup(srv.Close)

	for path, want := range map[string]int{"/health": http.StatusOK, "/ready": http.StatusServiceUnavailable} {
		resp, err := http.Get(srv.URL + path)
		if err != nil {
			t.Fatalf("GET %s: %v", path, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != want {
			t.Errorf("GET %s = %d, want %d", path, resp.StatusCode, want)
		}
	}
}

type probingBroker struct {
	connectingBroker
	addr  string
	ready int
}

func (b *probingBroker) Connect(ctx context.Context) error {
	if resp, err := http.Get("http://" + b.addr + "/ready"); err == nil {
		b.ready = resp.StatusCode
		_ = resp.Body.Close()
	}
	return b.connectingBroker.Connect(ctx)
}

func TestConnectStandaloneBroker_InterimHealthOnlyWhileConnecting(t *testing.T) {
	addr := freeAddr(t)
	brk := &probingBroker{addr: addr}
	if err := ConnectStandaloneBroker(t.Context(), brk, addr, quietLogger()); err != nil {
		t.Fatalf("ConnectStandaloneBroker: %v", err)
	}
	if brk.ready != http.StatusServiceUnavailable {
		t.Errorf("GET /ready during the connect = %d, want 503", brk.ready)
	}

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("address still in use after stop: %v", err)
	}
	_ = ln.Close()
}

func TestConnectStandaloneBroker_FailureStopsInterimHealth(t *testing.T) {
	addr := freeAddr(t)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	err := ConnectStandaloneBroker(ctx, &connectingBroker{errs: []error{errUnreachable}}, addr, quietLogger())
	if err == nil {
		t.Fatal("ConnectStandaloneBroker succeeded, want an error")
	}
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("address still in use after a failed connect: %v", err)
	}
	_ = ln.Close()
}

func holdInterimServe(t *testing.T) (release func(), served <-chan error) {
	t.Helper()
	gate, done := make(chan struct{}), make(chan error, 1)
	original := serveInterimHealth
	serveInterimHealth = func(srv *http.Server, l net.Listener) error {
		<-gate
		err := original(srv, l)
		done <- err
		return err
	}
	t.Cleanup(func() { serveInterimHealth = original })
	var once sync.Once
	release = func() { once.Do(func() { close(gate) }) }
	t.Cleanup(release)
	return release, done
}

func TestInterimHealth_AStopBeforeServeRunsFreesTheAddress(t *testing.T) {
	release, served := holdInterimServe(t)
	interim, err := startInterimHealth("127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := interim.listener.Addr().String()

	interim.stop()
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("address still in use after the stop: %v", err)
	}
	_ = ln.Close()

	release()
	if err = <-served; !errors.Is(err, http.ErrServerClosed) {
		t.Errorf("Serve after the stop returned %v, want http.ErrServerClosed", err)
	}
	interim.stop()
	(*interimHealth)(nil).stop()
}

func TestConnectStandaloneBroker_AddressInUse(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	err = ConnectStandaloneBroker(t.Context(), &connectingBroker{}, ln.Addr().String(), quietLogger())
	if err == nil || !strings.Contains(err.Error(), "interim health") {
		t.Fatalf("err = %v, want an interim health listen error", err)
	}
}

func TestConnectStandaloneBroker_NoHealthAddress(t *testing.T) {
	if err := ConnectStandaloneBroker(t.Context(), &connectingBroker{}, "", quietLogger()); err != nil {
		t.Fatalf("ConnectStandaloneBroker: %v", err)
	}
}

func freeAddr(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}
