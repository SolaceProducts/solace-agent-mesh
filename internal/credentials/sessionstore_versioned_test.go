// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package credentials

import (
	"context"
	"errors"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
)

func TestSetAtWithAnotherWriter(t *testing.T) {
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}
	tests := []struct {
		name        string
		seed        []string
		otherWrites []string
		wantErr     error
		wantValue   string
	}{
		{name: "no row and no other writer creates it", wantValue: "mine"},
		{name: "no row but another writer created it first keeps theirs", otherWrites: []string{"theirs"}, wantErr: auth.ErrCredentialChanged, wantValue: "theirs"},
		{name: "row unchanged since read is replaced", seed: []string{"old"}, wantValue: "mine"},
		{name: "row another writer replaced after read keeps theirs", seed: []string{"old"}, otherWrites: []string{"theirs"}, wantErr: auth.ErrCredentialChanged, wantValue: "theirs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			mine, km, shared := setupStore(t)
			other := NewSessionStore(shared, km, nil)
			for _, v := range tt.seed {
				if err := mine.Set(ctx, scope, "token", v, 0); err != nil {
					t.Fatalf("Set(%q) error = %v", v, err)
				}
			}
			read, err := mine.GetVersioned(ctx, scope, "token")
			if err != nil {
				t.Fatalf("GetVersioned() error = %v", err)
			}
			for _, v := range tt.otherWrites {
				if setErr := other.Set(ctx, scope, "token", v, 0); setErr != nil {
					t.Fatalf("other Set(%q) error = %v", v, setErr)
				}
			}

			written, err := mine.SetAt(ctx, scope, "token", "mine", 0, read)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("SetAt() error = %v, want %v", err, tt.wantErr)
			}
			if err == nil {
				reread, getErr := mine.GetVersioned(ctx, scope, "token")
				if getErr != nil || reread.Version != written.Version || written.Value != "mine" {
					t.Errorf("SetAt() returned %+v, a fresh read has version %d (error %v)", written, reread.Version, getErr)
				}
			}
			got, err := mine.Get(ctx, scope, "token")
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if got != tt.wantValue {
				t.Errorf("Get() after SetAt = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

func TestSetAtReplacesSoftDeletedRow(t *testing.T) {
	ctx := t.Context()
	store, _, _ := setupStore(t)
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}
	if err := store.Set(ctx, scope, "token", "old", 0); err != nil {
		t.Fatalf("Set() error = %v", err)
	}
	if err := store.Delete(ctx, scope, "token"); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}
	read, err := store.GetVersioned(ctx, scope, "token")
	if err != nil {
		t.Fatalf("GetVersioned() error = %v", err)
	}

	if _, err := store.SetAt(ctx, scope, "token", "new", 0, read); err != nil {
		t.Errorf("SetAt() on a soft-deleted row error = %v, want nil", err)
	}
	if got, _ := store.Get(ctx, scope, "token"); got != "new" {
		t.Errorf("Get() after SetAt = %q, want %q", got, "new")
	}
}

func TestDeleteAtWithAnotherWriter(t *testing.T) {
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}
	tests := []struct {
		name        string
		otherWrites []string
		wantErr     error
		wantValue   string
	}{
		{name: "row unchanged since read is deleted", wantValue: ""},
		{name: "row another writer replaced after read is kept", otherWrites: []string{"theirs"}, wantErr: auth.ErrCredentialChanged, wantValue: "theirs"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			mine, km, shared := setupStore(t)
			other := NewSessionStore(shared, km, nil)
			if err := mine.Set(ctx, scope, "token", "old", 0); err != nil {
				t.Fatalf("Set() error = %v", err)
			}
			read, err := mine.GetVersioned(ctx, scope, "token")
			if err != nil {
				t.Fatalf("GetVersioned() error = %v", err)
			}
			for _, v := range tt.otherWrites {
				if setErr := other.Set(ctx, scope, "token", v, 0); setErr != nil {
					t.Fatalf("other Set(%q) error = %v", v, setErr)
				}
			}

			err = mine.DeleteAt(ctx, scope, "token", read)
			if !errors.Is(err, tt.wantErr) {
				t.Errorf("DeleteAt() error = %v, want %v", err, tt.wantErr)
			}
			got, err := mine.Get(ctx, scope, "token")
			if err != nil {
				t.Fatalf("Get() error = %v", err)
			}
			if got != tt.wantValue {
				t.Errorf("Get() after DeleteAt = %q, want %q", got, tt.wantValue)
			}
		})
	}
}

type writeAfterReadStore struct {
	session.Store
	afterGet func()
}

func (s *writeAfterReadStore) Get(ctx context.Context, appName, userID, sessionID string) (*session.Session, error) {
	sess, err := s.Store.Get(ctx, appName, userID, sessionID)
	if hook := s.afterGet; hook != nil && sess != nil {
		s.afterGet = nil
		hook()
	}
	return sess, err
}

func TestVersionedWriteWhenAnotherWriterSavesBetweenItsReadAndItsSave(t *testing.T) {
	scope := Scope{AgentName: "test-agent", AppName: "myapp", UserID: "alice"}
	tests := []struct {
		name  string
		write func(ctx context.Context, s *SessionStore, read auth.VersionedCredential) error
	}{
		{name: "SetAt", write: func(ctx context.Context, s *SessionStore, read auth.VersionedCredential) error {
			_, err := s.SetAt(ctx, scope, "token", "mine", 0, read)
			return err
		}},
		{name: "DeleteAt", write: func(ctx context.Context, s *SessionStore, read auth.VersionedCredential) error {
			return s.DeleteAt(ctx, scope, "token", read)
		}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx := t.Context()
			seeded, km, shared := setupStore(t)
			if err := seeded.Set(ctx, scope, "token", "old", 0); err != nil {
				t.Fatalf("Set() error = %v", err)
			}
			other := NewSessionStore(shared, km, nil)
			racing := &writeAfterReadStore{Store: shared}
			mine := NewSessionStore(racing, km, nil)
			read, err := mine.GetVersioned(ctx, scope, "token")
			if err != nil {
				t.Fatalf("GetVersioned() error = %v", err)
			}
			racing.afterGet = func() {
				if err := other.Set(ctx, scope, "token", "theirs", 0); err != nil {
					t.Errorf("other Set() error = %v", err)
				}
			}

			if err := tt.write(ctx, mine, read); !errors.Is(err, auth.ErrCredentialChanged) {
				t.Errorf("%s() error = %v, want %v", tt.name, err, auth.ErrCredentialChanged)
			}
			if got, err := other.Get(ctx, scope, "token"); err != nil || got != "theirs" {
				t.Errorf("Get() = (%q, %v), want %q", got, err, "theirs")
			}
		})
	}
}
