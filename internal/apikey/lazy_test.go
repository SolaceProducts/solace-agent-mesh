// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apikey

import (
	"context"
	"errors"
	"sync"
	"testing"
)

type stubAuthority struct {
	resolution Resolution
	err        error

	mu      sync.Mutex
	gotHash string
}

func (s *stubAuthority) Validate(_ context.Context, keyHash string) (Resolution, error) {
	s.mu.Lock()
	s.gotHash = keyHash
	s.mu.Unlock()
	return s.resolution, s.err
}

func (s *stubAuthority) lastHash() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.gotHash
}

func TestLazyAuthority_UnboundReportsUnavailable(t *testing.T) {
	t.Parallel()
	lazy := NewLazyAuthority()

	if lazy.Bound() {
		t.Fatal("a fresh holder reports itself bound")
	}
	_, err := lazy.Validate(t.Context(), "any-hash")
	if !errors.Is(err, ErrAuthorityUnavailable) {
		t.Fatalf("Validate error = %v, want ErrAuthorityUnavailable", err)
	}
}

func TestLazyAuthority_BoundDelegates(t *testing.T) {
	t.Parallel()
	stub := &stubAuthority{resolution: Resolution{KeyID: "k1", OwnerSub: "system:ci"}}
	lazy := NewLazyAuthority()
	lazy.SetAuthority(stub)

	if !lazy.Bound() {
		t.Error("holder reports unbound after SetAuthority")
	}
	got, err := lazy.Validate(t.Context(), "hash-abc")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.KeyID != "k1" {
		t.Errorf("KeyID = %q, want k1", got.KeyID)
	}
	if got := stub.lastHash(); got != "hash-abc" {
		t.Errorf("delegate received hash %q, want hash-abc", got)
	}
}

func TestLazyAuthority_DisabledReportsNotEnabled(t *testing.T) {
	t.Parallel()
	lazy := NewLazyAuthority()
	lazy.Disable()

	if !lazy.Bound() {
		t.Error("a disabled holder reports itself unresolved, which would leave callers answering 503 forever")
	}
	_, err := lazy.Validate(t.Context(), "any-hash")
	if !errors.Is(err, ErrNotEnabled) {
		t.Fatalf("Validate error = %v, want ErrNotEnabled", err)
	}
	if errors.Is(err, ErrAuthorityUnavailable) {
		t.Error("disabled must not report as unavailable; the two mean different things to an operator")
	}
}

func TestLazyAuthority_SetAuthorityReplaces(t *testing.T) {
	t.Parallel()
	first := &stubAuthority{resolution: Resolution{KeyID: "first"}}
	second := &stubAuthority{resolution: Resolution{KeyID: "second"}}
	lazy := NewLazyAuthority()
	lazy.SetAuthority(first)
	lazy.SetAuthority(second)

	got, err := lazy.Validate(t.Context(), "h")
	if err != nil {
		t.Fatalf("Validate: %v", err)
	}
	if got.KeyID != "second" {
		t.Errorf("KeyID = %q, want second", got.KeyID)
	}
}

func TestLazyAuthority_ConcurrentSetAndValidate(t *testing.T) {
	t.Parallel()
	lazy := NewLazyAuthority()
	stub := &stubAuthority{resolution: Resolution{KeyID: "installed"}}

	var wg sync.WaitGroup
	for range 16 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := lazy.Validate(t.Context(), "h")
			if err != nil && !errors.Is(err, ErrAuthorityUnavailable) {
				t.Errorf("a reader saw %v, want success or unavailable", err)
			}
		}()
	}
	wg.Add(1)
	go func() {
		defer wg.Done()
		lazy.SetAuthority(stub)
	}()
	wg.Wait()

	if !lazy.Bound() {
		t.Fatal("the holder is still pending after the write")
	}
	got, err := lazy.Validate(t.Context(), "h")
	if err != nil || got.KeyID != "installed" {
		t.Errorf("Validate = %+v, %v, want the installed authority's resolution", got, err)
	}
}

func TestLazyAuthority_TheLastResolutionWins(t *testing.T) {
	t.Parallel()

	t.Run("disable then bind serves keys", func(t *testing.T) {
		t.Parallel()
		lazy := NewLazyAuthority()
		lazy.Disable()
		lazy.SetAuthority(&stubAuthority{resolution: Resolution{KeyID: "live"}})

		got, err := lazy.Validate(t.Context(), "h")
		if err != nil || got.KeyID != "live" {
			t.Errorf("Validate = %+v, %v, want the live authority", got, err)
		}
	})

	t.Run("bind then disable refuses keys", func(t *testing.T) {
		t.Parallel()
		lazy := NewLazyAuthority()
		lazy.SetAuthority(&stubAuthority{resolution: Resolution{KeyID: "live"}})
		lazy.Disable()

		if _, err := lazy.Validate(t.Context(), "h"); !errors.Is(err, ErrNotEnabled) {
			t.Errorf("Validate error = %v, want ErrNotEnabled", err)
		}
	})
}
