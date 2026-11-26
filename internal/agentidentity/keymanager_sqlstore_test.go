// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package identity

import (
	"context"
	"errors"
	"log/slog"
	"os"
	"path/filepath"
	"testing"

	"github.com/google/uuid"

	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/session"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbtest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/dbutil"
)

var keyMgrTemplate *dbtest.Template

func TestMain(m *testing.M) {
	tmpl, err := dbtest.NewTemplateFromURL(
		os.Getenv("SAM_TEST_SESSION_DB_URL"),
		"identity_keymgr",
		func(u string) error {
			db, openErr := dbutil.Open(u, slog.Default())
			if openErr != nil {
				return openErr
			}
			defer func() { _ = db.Close() }()
			return session.NewSQLStore(db).Migrate(context.Background())
		},
	)
	if err != nil {
		panic("dbtest template: " + err.Error())
	}
	keyMgrTemplate = tmpl
	code := m.Run()
	keyMgrTemplate.Close()
	os.Exit(code)
}

func newTestSQLStore(t *testing.T) session.Store {
	t.Helper()
	db, err := dbutil.Open(keyMgrTemplate.Clone(t), slog.Default())
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return session.NewSQLStore(db)
}

func TestKeyManagerKeyDurableAcrossFileLossWithSQLStore(t *testing.T) {
	store := newTestSQLStore(t)
	dir := t.TempDir()
	keyFile := filepath.Join(dir, "agent.key")
	cfg := Config{Mode: "auto", FilePath: keyFile}

	km1, err := NewKeyManager(cfg, "test-agent", store, nil)
	if err != nil {
		t.Fatalf("first NewKeyManager: %v", err)
	}

	if rmErr := os.Remove(keyFile); rmErr != nil && !os.IsNotExist(rmErr) {
		t.Fatalf("simulate file wipe: %v", rmErr)
	}

	km2, err := NewKeyManager(cfg, "test-agent", store, nil)
	if err != nil {
		t.Fatalf("second NewKeyManager: %v", err)
	}

	if km1.IdentityKey() != km2.IdentityKey() {
		t.Errorf("identity key not durable across file loss: first=%q second=%q (key did not persist in the SQL store)",
			km1.IdentityKey(), km2.IdentityKey())
	}
}

func TestDBSessionKeyReturnsUUIDSessionID(t *testing.T) {
	appName, userID, sessionID := dbSessionKey("test-agent")

	if _, err := uuid.Parse(sessionID); err != nil {
		t.Errorf("dbSessionKey sessionID = %q, want a valid UUID: %v", sessionID, err)
	}
	if appName != "test-agent" {
		t.Errorf("appName = %q, want %q", appName, "test-agent")
	}
	if userID != "system_test-agent" {
		t.Errorf("userID = %q, want %q", userID, "system_test-agent")
	}
}

type firstGetMisses struct {
	session.Store
	missed bool
}

func (s *firstGetMisses) Get(ctx context.Context, appName, userID, sessionID string) (*session.Session, error) {
	if !s.missed {
		s.missed = true
		return nil, nil
	}
	return s.Store.Get(ctx, appName, userID, sessionID)
}

func TestKeyManagerUsesKeyAnotherReplicaSavedFirst(t *testing.T) {
	store := newTestSQLStore(t)
	first, err := NewKeyManager(Config{Mode: "auto", FilePath: filepath.Join(t.TempDir(), "a.key")}, "test-agent", store, nil)
	if err != nil {
		t.Fatalf("first NewKeyManager: %v", err)
	}

	second, err := NewKeyManager(Config{Mode: "auto", FilePath: filepath.Join(t.TempDir(), "b.key")}, "test-agent", &firstGetMisses{Store: store}, nil)
	if err != nil {
		t.Fatalf("second NewKeyManager: %v", err)
	}

	if first.IdentityKey() != second.IdentityKey() {
		t.Error("a replica that lost the insert kept its own key instead of the stored one")
	}
}

type getFails struct{ session.Store }

func (getFails) Get(context.Context, string, string, string) (*session.Session, error) {
	return nil, errors.New("database unavailable")
}

func TestKeyManagerFailsWhenStoredKeyCannotBeRead(t *testing.T) {
	keyFile := filepath.Join(t.TempDir(), "a.key")
	if _, err := NewKeyManager(Config{Mode: "auto", FilePath: keyFile}, "test-agent", getFails{newTestSQLStore(t)}, nil); err == nil {
		t.Fatal("NewKeyManager returned a key of its own while the stored key could not be read")
	}
	if _, err := os.Stat(keyFile); !os.IsNotExist(err) {
		t.Errorf("a key file was written while a session store is configured: %v", err)
	}
}
