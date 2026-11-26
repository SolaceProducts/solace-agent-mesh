// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"log/slog"
	"net/http"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

func TestRegisteringTheRoutesLetsTheCoreReadSkillBundles(t *testing.T) {
	core := platpkg.NewPlatformCore(map[string]any{
		"namespace":    "test-ns",
		"database_url": platformCoreDBURL(t),
	}, platpkg.ExtensionPoints{})
	core.SetServices(runtime.NewRuntime(nil, nil, runtime.WithLogger(slog.New(slog.DiscardHandler))).Services())
	if err := core.Init(t.Context(), nil); err != nil {
		t.Fatalf("init platform core: %v", err)
	}
	t.Cleanup(func() { _ = core.Stop(t.Context()) })
	if core.SkillFiles() != nil {
		t.Fatal("the core reads skill bundles before its routes register")
	}

	RegisterRoutesWithDeps(http.NewServeMux(), core, &RouteDeps{})
	files := core.SkillFiles()
	if files == nil {
		t.Fatal("the routes registered no skill file reader on the core")
	}
	_, err := files.SkillFile(t.Context(), idgen.New(), "SKILL.md")
	if _, notFound := errors.AsType[*NotFoundError](err); !notFound {
		t.Errorf("reading an unknown skill's SKILL.md = %v, want not found", err)
	}
}
