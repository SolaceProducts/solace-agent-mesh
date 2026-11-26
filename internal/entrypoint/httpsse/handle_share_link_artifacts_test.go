// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

type failListKeysService struct {
	artifact.Service
}

func (failListKeysService) ListKeys(context.Context, artifact.KeyScope) ([]string, error) {
	return nil, errors.New("boom: artifact store unavailable")
}

func TestListSharedArtifacts_ListErrorReturnsEmpty(t *testing.T) {
	h := &shareLinkHandler{
		artSvc: &artifact.ScopedService{
			Inner:     failListKeysService{Service: memory.New()},
			Scope:     "namespace",
			Namespace: "test-ns",
		},
		logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
	}

	link := &store.SharedLink{UserID: "user-1", SessionID: "sess-1"}
	got := h.listSharedArtifacts(t.Context(), link, h.scopeFromLoadedTasks("user-1", link, nil))
	if len(got) != 0 {
		t.Fatalf("expected empty slice on list error, got %d entries", len(got))
	}
}

func TestListSharedArtifacts_NilServiceReturnsEmpty(t *testing.T) {
	h := &shareLinkHandler{logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	link := &store.SharedLink{UserID: "user-1", SessionID: "sess-1"}
	if got := h.listSharedArtifacts(t.Context(), link, h.scopeFromLoadedTasks("user-1", link, nil)); len(got) != 0 {
		t.Fatalf("expected empty slice with nil artSvc, got %d entries", len(got))
	}
}
