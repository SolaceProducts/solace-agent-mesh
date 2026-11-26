// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package extensions

import (
	"context"
	"encoding/json"
	"sync"
	"sync/atomic"
	"testing"
)

const testURI = "https://example.com/sam/ext/test/v1"

func TestDispatcher_RegisterAndDispatch(t *testing.T) {
	d := NewDispatcher()
	d.Register(testURI, "do", func(_ context.Context, _ RequestInfo, params json.RawMessage) (any, *Error) {
		var p struct {
			N int `json:"n"`
		}
		if err := json.Unmarshal(params, &p); err != nil {
			return nil, &Error{Code: CodeInternal, Message: err.Error()}
		}
		return map[string]int{"n2": p.N * 2}, nil
	})

	if !d.Has(testURI, "do") {
		t.Fatal("Has should return true after Register")
	}
	if !d.SupportsURI(testURI) {
		t.Fatal("SupportsURI should return true after Register")
	}

	res, errObj := d.Dispatch(t.Context(), testURI, "do", RequestInfo{}, json.RawMessage(`{"n":3}`))
	if errObj != nil {
		t.Fatalf("unexpected error: %v", errObj)
	}
	m, ok := res.(map[string]int)
	if !ok || m["n2"] != 6 {
		t.Fatalf("unexpected result: %#v", res)
	}
}

func TestDispatcher_UnknownURI(t *testing.T) {
	d := NewDispatcher()
	_, err := d.Dispatch(t.Context(), "https://example.com/sam/ext/missing/v1", "do", RequestInfo{}, nil)
	if err == nil || err.Code != CodeMethodNotFound {
		t.Fatalf("want method-not-found error, got %v", err)
	}
}

func TestDispatcher_UnknownMethod(t *testing.T) {
	d := NewDispatcher()
	d.Register(testURI, "do", func(_ context.Context, _ RequestInfo, _ json.RawMessage) (any, *Error) {
		return nil, nil
	})
	_, err := d.Dispatch(t.Context(), testURI, "nope", RequestInfo{}, nil)
	if err == nil || err.Code != CodeMethodNotFound {
		t.Fatalf("want method-not-found error, got %v", err)
	}
}

func TestDispatcher_HandlerErrorPropagates(t *testing.T) {
	d := NewDispatcher()
	d.Register(testURI, "do", func(_ context.Context, _ RequestInfo, _ json.RawMessage) (any, *Error) {
		return nil, &Error{Code: CodeNotFound, Message: "boom", Data: map[string]string{"id": "x"}}
	})
	_, err := d.Dispatch(t.Context(), testURI, "do", RequestInfo{}, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	if err.Code != CodeNotFound || err.Message != "boom" {
		t.Fatalf("error not propagated: %v", err)
	}
	data, ok := err.Data.(map[string]string)
	if !ok || data["id"] != "x" {
		t.Fatalf("data not propagated: %#v", err.Data)
	}
}

func TestDispatcher_ReplaceHandler(t *testing.T) {
	d := NewDispatcher()
	d.Register(testURI, "do", func(_ context.Context, _ RequestInfo, _ json.RawMessage) (any, *Error) {
		return "v1", nil
	})
	d.Register(testURI, "do", func(_ context.Context, _ RequestInfo, _ json.RawMessage) (any, *Error) {
		return "v2", nil
	})
	res, err := d.Dispatch(t.Context(), testURI, "do", RequestInfo{}, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if res != "v2" {
		t.Fatalf("want v2 (replaced), got %v", res)
	}
}

func TestDispatcher_ConcurrentDispatch(t *testing.T) {
	d := NewDispatcher()
	var calls int64
	d.Register(testURI, "do", func(_ context.Context, _ RequestInfo, _ json.RawMessage) (any, *Error) {
		atomic.AddInt64(&calls, 1)
		return "ok", nil
	})

	const goroutines = 50
	const perGoroutine = 20

	var wg sync.WaitGroup
	for range goroutines {
		wg.Go(func() {
			for range perGoroutine {
				if _, err := d.Dispatch(t.Context(), testURI, "do", RequestInfo{}, nil); err != nil {
					t.Errorf("dispatch error: %v", err)
				}
			}
		})
	}
	wg.Wait()

	if got := atomic.LoadInt64(&calls); got != goroutines*perGoroutine {
		t.Fatalf("calls = %d, want %d", got, goroutines*perGoroutine)
	}
}

func TestDispatcher_RequestInfoForwarded(t *testing.T) {
	d := NewDispatcher()
	var seen RequestInfo
	d.Register(testURI, "do", func(_ context.Context, info RequestInfo, _ json.RawMessage) (any, *Error) {
		seen = info
		return nil, nil
	})
	_, err := d.Dispatch(t.Context(), testURI, "do", RequestInfo{UserID: "u-1", ClientID: "gw-7"}, nil)
	if err != nil {
		t.Fatalf("unexpected: %v", err)
	}
	if seen.UserID != "u-1" || seen.ClientID != "gw-7" {
		t.Fatalf("info not forwarded: %#v", seen)
	}
}

func TestError_ImplementsErrorInterface(t *testing.T) {
	var err error = &Error{Code: CodeNotFound, Message: "x"}
	if err.Error() == "" {
		t.Fatal("empty error string")
	}
	var nilErr *Error
	if nilErr.Error() == "" {
		t.Fatal("nil-receiver Error() returned empty string")
	}
}

func TestDispatcher_SupportsURIFalseForUnknown(t *testing.T) {
	d := NewDispatcher()
	if d.SupportsURI(testURI) {
		t.Fatal("should not support unregistered URI")
	}
}
