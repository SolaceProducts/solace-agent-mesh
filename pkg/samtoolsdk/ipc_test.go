// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func newFakeIPCServer(t *testing.T, handle func(reqLine []byte, w *bufio.Writer)) *ipcClient {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "ipc") //nolint:usetesting // short socket path, see comment above
	if err != nil {
		t.Fatalf("mkdirtemp: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	sock := filepath.Join(dir, "s.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = ln.Close() })

	go func() {
		conn, acceptErr := ln.Accept()
		if acceptErr != nil {
			return
		}
		r := bufio.NewReader(conn)
		w := bufio.NewWriter(conn)
		for {
			line, readErr := r.ReadBytes('\n')
			if readErr != nil {
				return
			}
			handle(line, w)
		}
	}()

	c, err := newIPCClient(sock)
	if err != nil {
		t.Fatalf("newIPCClient: %v", err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

func echoReplyWithID(text string) func([]byte, *bufio.Writer) {
	return func(reqLine []byte, w *bufio.Writer) {
		var req ipcRequest
		_ = json.Unmarshal(reqLine, &req)
		b, _ := json.Marshal(ipcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"text":"` + text + `"}`)})
		_, _ = w.Write(append(b, '\n'))
		_ = w.Flush()
	}
}

func TestIPCCallLLM_Success(t *testing.T) {
	c := newFakeIPCServer(t, echoReplyWithID("hello"))
	got, err := c.CallLLM(context.Background(), "sys", "user", 0.1)
	if err != nil {
		t.Fatalf("CallLLM: %v", err)
	}
	if got != "hello" {
		t.Errorf("got %q, want %q", got, "hello")
	}
}

func TestIPCCallLLM_StallHonorsDeadlineThenPoisons(t *testing.T) {
	c := newFakeIPCServer(t, func([]byte, *bufio.Writer) {})

	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.CallLLM(ctx, "sys", "user", 0.1)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("expected a timeout error, got nil (call hung)")
	}
	if elapsed > 3*time.Second {
		t.Fatalf("CallLLM blocked %v; must return near the 150ms deadline", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded", err)
	}
	ctx2, cancel2 := context.WithTimeout(context.Background(), 500*time.Millisecond)
	defer cancel2()
	start2 := time.Now()
	_, err2 := c.CallLLM(ctx2, "sys", "user", 0.1)
	if !errors.Is(err2, ErrIPCConnBroken) {
		t.Errorf("second call err = %v, want ErrIPCConnBroken", err2)
	}
	if time.Since(start2) > 200*time.Millisecond {
		t.Errorf("poisoned call took %v; must short-circuit before any I/O", time.Since(start2))
	}
}

func TestIPCCallLLM_CancelUnblocksRead(t *testing.T) {
	c := newFakeIPCServer(t, func([]byte, *bufio.Writer) {})

	ctx, cancel := context.WithCancel(context.Background())
	go func() { time.Sleep(100 * time.Millisecond); cancel() }()

	start := time.Now()
	_, err := c.CallLLM(ctx, "sys", "user", 0.1)
	if err == nil {
		t.Fatal("expected a cancellation error")
	}
	if time.Since(start) > 3*time.Second {
		t.Fatalf("cancellation did not unblock the read (%v)", time.Since(start))
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want context.Canceled", err)
	}
}

func TestIPCCallLLM_ResponseIDMismatchPoisons(t *testing.T) {
	c := newFakeIPCServer(t, func(_ []byte, w *bufio.Writer) {
		b, _ := json.Marshal(ipcResponse{JSONRPC: "2.0", ID: "999", Result: json.RawMessage(`{"text":"stale"}`)})
		_, _ = w.Write(append(b, '\n'))
		_ = w.Flush()
	})
	_, err := c.CallLLM(context.Background(), "sys", "user", 0.1)
	if err == nil || !strings.Contains(err.Error(), "id mismatch") {
		t.Fatalf("first call err = %v, want an id-mismatch error", err)
	}
	if errors.Is(err, ErrIPCConnBroken) {
		t.Error("first call should report the mismatch itself, not the poison sentinel")
	}
	if _, err2 := c.CallLLM(context.Background(), "sys", "user", 0.1); !errors.Is(err2, ErrIPCConnBroken) {
		t.Errorf("second call err = %v, want ErrIPCConnBroken (poisoned by the mismatch)", err2)
	}
}

func TestIPCCallLLM_DefaultDeadlineCapsDeadlinelessContext(t *testing.T) {
	orig := defaultLLMCallTimeout
	defaultLLMCallTimeout = 150 * time.Millisecond
	defer func() { defaultLLMCallTimeout = orig }()

	c := newFakeIPCServer(t, func([]byte, *bufio.Writer) {})

	start := time.Now()
	_, err := c.CallLLM(context.Background(), "sys", "user", 0.1)
	if elapsed := time.Since(start); elapsed > 3*time.Second {
		t.Fatalf("deadline-less call blocked %v; the default cap must bound it", elapsed)
	}
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("err = %v, want context.DeadlineExceeded from the default cap", err)
	}
}

func TestIPCCallLLM_RemoteErrorDoesNotPoison(t *testing.T) {
	replies := 0
	c := newFakeIPCServer(t, func(reqLine []byte, w *bufio.Writer) {
		var req ipcRequest
		_ = json.Unmarshal(reqLine, &req)
		var resp ipcResponse
		if replies == 0 {
			resp = ipcResponse{JSONRPC: "2.0", ID: req.ID, Error: &ipcError{Code: 42, Message: "model overloaded"}}
		} else {
			resp = ipcResponse{JSONRPC: "2.0", ID: req.ID, Result: json.RawMessage(`{"text":"ok"}`)}
		}
		replies++
		b, _ := json.Marshal(resp)
		_, _ = w.Write(append(b, '\n'))
		_ = w.Flush()
	})

	_, err := c.CallLLM(context.Background(), "sys", "user", 0.1)
	if err == nil || !strings.Contains(err.Error(), "model overloaded") {
		t.Fatalf("first call err = %v, want the remote error surfaced", err)
	}
	got, err := c.CallLLM(context.Background(), "sys", "user", 0.1)
	if err != nil {
		t.Fatalf("second call after a remote error should succeed, got %v", err)
	}
	if got != "ok" {
		t.Errorf("got %q, want %q", got, "ok")
	}
}

func TestIPCCallLLM_SocketErrorReportedNotAsTimeout(t *testing.T) {
	c := newFakeIPCServer(t, func(_ []byte, w *bufio.Writer) {
		_, _ = w.Write([]byte(`{"jsonrpc":"2.0"`))
		_ = w.Flush()
	})
	go func() { time.Sleep(50 * time.Millisecond); _ = c.Close() }()

	_, err := c.CallLLM(context.Background(), "sys", "user", 0.1)
	if err == nil {
		t.Fatal("expected a read error after the connection closed")
	}
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, want a genuine I/O error, not a ctx error", err)
	}
	if _, err2 := c.CallLLM(context.Background(), "sys", "user", 0.1); !errors.Is(err2, ErrIPCConnBroken) {
		t.Errorf("second call err = %v, want ErrIPCConnBroken", err2)
	}
}
