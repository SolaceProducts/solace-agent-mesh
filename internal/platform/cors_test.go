// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestCORS_ExposesLocation(t *testing.T) {
	brk := broker.NewDevBroker()
	if err := brk.Connect(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = brk.Disconnect(context.Background()) })
	core := NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": "sqlite:///" + filepath.Join(t.TempDir(), "platform.db"),
	}, ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(brk, nil).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/v1/platform/health", nil)
	req.Header.Set("Origin", "http://desktop.example")
	rec := httptest.NewRecorder()
	core.Handler().ServeHTTP(rec, req)

	if got := rec.Header().Get("Access-Control-Expose-Headers"); got != "Location" {
		t.Errorf("Access-Control-Expose-Headers = %q, want Location", got)
	}
}
