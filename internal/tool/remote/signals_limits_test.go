// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remote

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestExecute_WithoutASearchCounterSendsNoTurn(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{ToolName: "web_search_nocounter"}, &logs)
	capture := stubSTR(t, brk, "web_search_nocounter", func(remotetool.InvokeParams, int) map[string]any {
		return schemaResult(ragSignal(0, webSource("s0r0")))
	})
	sink := &signalSink{}
	if _, err := rt.Execute(context.Background(), map[string]any{}, &tool.Context{AgentName: testAgent, SendSignal: sink.send}); err != nil {
		t.Fatal(err)
	}
	if p := capture.all(); len(p) != 1 || p[0].SearchTurn != nil {
		t.Fatalf("invoke params = %+v, want one without a search_turn", p)
	}
	if n := len(sink.ragUpdates()); n != 0 {
		t.Fatalf("published %d signals, want 0", n)
	}
}

func TestExecute_AFailedResultPublishesNoSignals(t *testing.T) {
	t.Parallel()
	var logs bytes.Buffer
	rt, brk := newSignalTool(t, &ToolConfig{ToolName: "web_search_failed"}, &logs)
	stubSTR(t, brk, "web_search_failed", func(p remotetool.InvokeParams, _ int) map[string]any {
		result := schemaResult(ragSignal(*p.SearchTurn, webSource("s0r0")))
		result["status"] = "error"
		return result
	})
	sink := &signalSink{}
	result, err := rt.Execute(context.Background(), map[string]any{}, searchContext(sink))
	if err != nil || result.Status != tool.StatusError {
		t.Fatalf("Execute = %+v, %v", result, err)
	}
	if n := len(sink.ragUpdates()); n != 0 {
		t.Fatalf("published %d signals for a failed call, want 0", n)
	}
}

func TestDecodeResultSignal_BoundsSourceURLs(t *testing.T) {
	t.Parallel()
	three := 3
	withURL := func(u string) map[string]any {
		source := webSource("s3r0")
		source["sourceUrl"] = u
		return ragSignal(3, source)
	}
	longest := "https://example.com/" + strings.Repeat("a", 2048-len("https://example.com/"))
	for name, tc := range map[string]struct {
		url  string
		want error
	}{
		"2048 characters":   {longest, nil},
		"2049 characters":   {longest + "a", errSignalSourceScheme},
		"user and password": {"https://user:pass@example.com/p", errSignalSourceScheme},
		"user only":         {"https://user@example.com/p", errSignalSourceScheme},
	} {
		if _, err := decodeResultSignal(withURL(tc.url), &three); !errors.Is(err, tc.want) {
			t.Errorf("%s: err = %v, want %v", name, err, tc.want)
		}
	}
}

func TestDecodeResultSignal_NamesTheFailingSource(t *testing.T) {
	t.Parallel()
	three := 3
	bad := webSource("s3r1")
	bad["metadata"].(map[string]any)["source_type"] = "web"
	_, err := decodeResultSignal(ragSignal(3, webSource("s3r0"), bad), &three)
	if !errors.Is(err, errSignalMetadataKey) || !strings.Contains(err.Error(), "source 1") {
		t.Fatalf("err = %v, want the metadata-key reason for source 1", err)
	}
}
