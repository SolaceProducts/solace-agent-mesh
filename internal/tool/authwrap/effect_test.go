// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package authwrap

import (
	"context"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth/toolauth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
	"github.com/SolaceProducts/solace-agent-mesh/pkg/tools"
)

type perCallReader struct {
	*tools.ListArtifactsTool
}

func (perCallReader) CallEffectClass(args map[string]any) tool.EffectClass {
	if args["mode"] == "read" {
		return tool.DerivedEffect(tool.EffectReadOnly)
	}
	return tool.EffectClass{}
}

func TestWrappedToolForwardsEffect(t *testing.T) {
	t.Parallel()
	reader := &WrappedTool{inner: &tools.ListArtifactsTool{}}
	if got := tool.EffectClassOf(reader); got != tool.DeclaredEffect(tool.EffectReadOnly) {
		t.Errorf("wrapped list_artifacts = %+v, want its declared read_only", got)
	}
	web := &WrappedTool{inner: tools.NewWebRequestTool(nil)}
	if got := tool.CallEffectClassOf(web, map[string]any{"method": "GET"}); got.ReadOnly() {
		t.Errorf("wrapped web_request GET = %+v, want side_effecting", got)
	}
	refining := &WrappedTool{inner: perCallReader{&tools.ListArtifactsTool{}}}
	if got := tool.CallEffectClassOf(refining, map[string]any{"mode": "read"}); got != tool.DerivedEffect(tool.EffectReadOnly) {
		t.Errorf("wrapped per-call tool = %+v, want the inner per-call class forwarded", got)
	}
}

type resolvedProbe struct {
	*tools.ListArtifactsTool
	seen []tool.EffectClass
}

func (p *resolvedProbe) ExecuteResolved(_ context.Context, _ map[string]any, _ *tool.Context, effect tool.EffectClass) (*tool.Result, error) {
	p.seen = append(p.seen, effect)
	return tool.OK("ok"), nil
}

func TestWrappedToolForwardsTheResolvedEffectToItsInnerTool(t *testing.T) {
	t.Parallel()
	inner := &resolvedProbe{ListArtifactsTool: &tools.ListArtifactsTool{}}
	wrapped := Wrap(inner, toolauth.NewAuthHandler(&toolauth.AuthConfig{Type: "none"}, nil), nil)
	resolved, ok := wrapped.(tool.ResolvedEffectExecutor)
	if !ok {
		t.Fatalf("%T does not implement tool.ResolvedEffectExecutor", wrapped)
	}
	operator := tool.EffectClass{Effect: tool.EffectSideEffecting, Source: tool.EffectSourceOperator}
	if _, err := resolved.ExecuteResolved(t.Context(), map[string]any{}, &tool.Context{}, operator); err != nil {
		t.Fatal(err)
	}
	if len(inner.seen) != 1 || inner.seen[0] != operator {
		t.Errorf("inner tool saw %+v, want [%+v]", inner.seen, operator)
	}
}
