// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package bootstrap

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func TestNewSkillEmbedResolveCheck(t *testing.T) {
	svc := &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}
	const agent, user, session = "agent", "u1", "s1"
	if _, err := artifact.SaveWithMetadata(t.Context(), svc, agent, user, session,
		"sales_data.json", []byte(`[{"month":"Jan"}]`), "application/json",
		artifact.MetadataOpts{}, slog.Default()); err != nil {
		t.Fatalf("seed artifact: %v", err)
	}

	check := newSkillEmbedResolveCheck(slog.Default())
	candidate := "«««template_liquid: data=\"sales_data.json\"\n{% for r in items %}{{ r.month }}{% endfor %}»»»"

	t.Run("present artifact resolves live", func(t *testing.T) {
		out, err := check(t.Context(), candidate, svc, agent, user, session)
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if !strings.Contains(out, "Jan") {
			t.Fatalf("expected resolved month, got %q", out)
		}
	})

	t.Run("missing artifact does not bind", func(t *testing.T) {
		out, err := check(t.Context(), candidate, svc, agent, user, "other-session")
		if err != nil {
			t.Fatalf("err: %v", err)
		}
		if strings.Contains(out, "Jan") {
			t.Fatalf("missing artifact should not have bound data, got %q", out)
		}
	})
}
