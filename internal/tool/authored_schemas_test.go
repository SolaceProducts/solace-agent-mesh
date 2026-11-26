// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"testing"
)

func TestBuiltinTool_IsNotAProvenanceMarker(t *testing.T) {
	discovered := &discoveredSchemaTool{}

	if _, ok := Tool(discovered).(BuiltinTool); !ok {
		t.Fatal("fixture no longer satisfies BuiltinTool; the trap this pins has changed shape")
	}
	if HasAuthoredSchemas(discovered) {
		t.Error("a discovered-schema tool must not report authored schemas")
	}
}

func TestHasAuthoredSchemas(t *testing.T) {
	if !HasAuthoredSchemas(newAuthoredTool()) {
		t.Error("a tool declaring authored schemas should report true")
	}
	if HasAuthoredSchemas(&authoredTool{authored: false}) {
		t.Error("AuthoredSchemas() == false must not count as authored")
	}
}

type discoveredSchemaTool struct{}

func (*discoveredSchemaTool) Name() string                                 { return "discovered" }
func (*discoveredSchemaTool) Description() string                          { return "" }
func (*discoveredSchemaTool) Instructions() string                         { return "" }
func (*discoveredSchemaTool) IsLongRunning() bool                          { return false }
func (*discoveredSchemaTool) RawStringArgs() []string                      { return nil }
func (*discoveredSchemaTool) ContextParam() string                         { return "" }
func (*discoveredSchemaTool) Parameters() *JSONSchema                      { return nil }
func (*discoveredSchemaTool) ArtifactParams() map[string]ArtifactParamInfo { return nil }
func (*discoveredSchemaTool) Execute(context.Context, map[string]any, *Context) (*Result, error) {
	return OK("ok"), nil
}

type authoredTool struct {
	discoveredSchemaTool
	authored bool
}

func (t *authoredTool) AuthoredSchemas() bool { return t.authored }

func newAuthoredTool() *authoredTool { return &authoredTool{authored: true} }
