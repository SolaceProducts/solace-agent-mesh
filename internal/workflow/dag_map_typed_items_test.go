// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"context"
	"encoding/json"
	"errors"
	"math"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const mapOverToolRowsWorkflow = `
output_mapping: {results: "{{each.output.results}}"}
nodes:
  - {id: rows, type: tool, tool_name: rows_tool}
  - id: each
    type: map
    depends_on: [rows]
    items: "{{rows.output.result_preview}}"
    node: echo
  - {id: echo, type: tool, tool_name: echo_tool, input: {text: "{{_map_item.text}}"}}
`

type recordingTool struct {
	*fakeTool
	mu    sync.Mutex
	texts []string
}

func (t *recordingTool) Execute(_ context.Context, args map[string]any, _ *tool.Context) (*tool.Result, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	text, _ := args["text"].(string)
	t.texts = append(t.texts, text)
	return tool.OK("echoed"), nil
}

type row struct {
	Text string `json:"text"`
}

type untaggedRow struct {
	Name string
}

type idRow struct {
	ID int64 `json:"id"`
}

type namedByte uint8

type leakyRow struct{ secret string }

func (r leakyRow) MarshalJSON() ([]byte, error) { return nil, errors.New("cannot encode " + r.secret) }

type panickyRow struct{}

func (panickyRow) MarshalJSON() ([]byte, error) { panic("boom") }

type panicNilRow struct{}

var noPanicValue any

func (panicNilRow) MarshalJSON() ([]byte, error) { panic(noPanicValue) }

type panicSecretRow struct{}

func (panicSecretRow) MarshalJSON() ([]byte, error) { panic("s3cret") }

type valueErrorRow struct{}

func (valueErrorRow) MarshalJSON() ([]byte, error) {
	return nil, &json.UnsupportedValueError{Str: "s3cret"}
}

func cyclicMap() map[string]any {
	m := map[string]any{"id": 1}
	m["self"] = m
	return m
}

func cyclicList() []any {
	l := []any{1, nil}
	l[1] = l
	return l
}

type money struct{ Cents int64 }

func (m *money) MarshalJSON() ([]byte, error) { return []byte(`"$5"`), nil }

type pricedRow struct {
	Price money `json:"price"`
}

type anyRow struct {
	V any `json:"v"`
}

type bigUintRow struct {
	U uint64 `json:"u"`
}

type idsRow struct {
	IDs []int64 `json:"ids"`
}

type rawNumberRow struct {
	N json.RawMessage `json:"n"`
}

type namedMapRow map[string]any

type textRow struct {
	Text  string `json:"text"`
	Extra any    `json:"extra,omitempty"`
}

func TestAMapRunsOverTheRowsAnInProcessToolNodeReturns(t *testing.T) {
	both := []string{"late parcel", "wrong size"}
	cases := []struct {
		name      string
		rows      any
		wantTexts []string
		wantErr   string
	}{
		{name: "map rows", rows: []map[string]any{{"text": "late parcel"}, {"text": "wrong size"}}, wantTexts: both},
		{name: "string maps", rows: []map[string]string{{"text": "late parcel"}, {"text": "wrong size"}}, wantTexts: both},
		{name: "structs", rows: []row{{Text: "late parcel"}, {Text: "wrong size"}}, wantTexts: both},
		{name: "pointers", rows: []*row{{Text: "late parcel"}, {Text: "wrong size"}}, wantTexts: both},
		{name: "empty structs", rows: []row{}},
		{name: "nil structs", rows: []row(nil)},
		{name: "unencodable", rows: []chan int{make(chan int)}, wantErr: "a list whose item 0 cannot be used: unsupported type chan int"},
		{name: "unencodable later row", rows: []textRow{{Text: "late parcel"}, {Text: "wrong size", Extra: make(chan int)}},
			wantErr: "a list whose item 1 cannot be used: unsupported type chan int"},
		{name: "NaN in a kept row", rows: []map[string]any{{"text": "late parcel", "score": math.NaN()}},
			wantErr: "a list whose item 0 cannot be used: unsupported value NaN"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := newDecisionRun(t, mapOverToolRowsWorkflow, &fakeJev{}, false)
			run.tools["rows"].returnResult = tool.OK("rows", tool.WithData(map[string]any{"result_preview": tc.rows}))
			echo := &recordingTool{fakeTool: &fakeTool{name: "echo_tool"}}
			ts := tool.NewSet()
			for _, ft := range []tool.Tool{run.tools["rows"], echo} {
				if err := ts.Register(ft); err != nil {
					t.Fatalf("register: %v", err)
				}
			}
			run.e.instance.tools = ts

			run.execute(t)

			run.e.mu.Lock()
			failed, completed := run.e.state.ErrorState, run.e.state.CompletedNodes["each"]
			run.e.mu.Unlock()
			echo.mu.Lock()
			got := slices.Sorted(slices.Values(echo.texts))
			echo.mu.Unlock()
			if tc.wantErr != "" {
				if failed == nil || failed.FailedNodeID != "each" || !strings.Contains(failed.ErrorMessage, tc.wantErr) {
					t.Fatalf("error state = %+v, want the map node failed with %q", failed, tc.wantErr)
				}
				if len(got) != 0 || completed != "" {
					t.Errorf("echo ran %d times and the map completed as %q, want neither", len(got), completed)
				}
				return
			}
			if failed != nil {
				t.Fatalf("workflow failed: %+v", failed)
			}
			if len(tc.wantTexts) == 0 && completed != "map_empty" {
				t.Errorf("map node completed as %q, want map_empty", completed)
			}
			if !slices.Equal(got, tc.wantTexts) {
				t.Errorf("echo got texts %q, want each row's text once: %q", got, tc.wantTexts)
			}
		})
	}
}

func TestResolveMapItemsKeepsJSONShapedValuesAndConvertsTheRest(t *testing.T) {
	const big = int64(1<<62 + 1)
	cases := []struct {
		name     string
		items    any
		maxItems int
		want     []any
		wantErr  string
	}{
		{name: "rows keep int64", items: []map[string]any{{"id": int64(9007199254740993)}, {"id": int64(2)}},
			want: []any{map[string]any{"id": int64(9007199254740993)}, map[string]any{"id": int64(2)}}},
		{name: "rows kept exactly", items: []map[string]any{{"name": "a\xffb"}}, want: []any{map[string]any{"name": "a\xffb"}}},
		{name: "rows keep whole floats", items: []map[string]any{{"n": float64(3)}}, want: []any{map[string]any{"n": float64(3)}}},
		{name: "floats kept", items: []float64{1, 2.5}, want: []any{float64(1), 2.5}},
		{name: "small kinds kept", items: []float32{1.5}, want: []any{float32(1.5)}},
		{name: "int8s kept", items: []int8{1}, want: []any{int8(1)}},
		{name: "lists kept", items: [][]any{{"a", float64(3)}}, want: []any{[]any{"a", float64(3)}}},
		{name: "struct list keeps big ints", items: []idsRow{{IDs: []int64{1, big}}}, want: []any{map[string]any{"ids": []any{int64(1), big}}}},
		{name: "number past float64", items: []rawNumberRow{{N: json.RawMessage("1e999")}},
			wantErr: "item 0 cannot be used: a number out of range"},
		{name: "integer past uint64", items: []rawNumberRow{{N: json.RawMessage("18446744073709551616")}},
			want: []any{map[string]any{"n": float64(18446744073709551616)}}},
		{name: "cyclic map", items: []map[string]any{cyclicMap()}, wantErr: "item 0 cannot be used: unsupported value"},
		{name: "cyclic list", items: [][]any{cyclicList()}, wantErr: "item 0 cannot be used: unsupported value"},
		{name: "NaN in a typed container of a kept row", items: []map[string]any{{"x": []float64{math.NaN()}}},
			wantErr: "item 0 cannot be used: unsupported value NaN"},
		{name: "negative max_items cuts nothing", items: []int64{1, 2}, maxItems: -1, want: []any{int64(1), int64(2)}},
		{name: "panic(nil)", items: []panicNilRow{{}}, wantErr: "item 0 cannot be used: its JSON encoding panicked (*runtime.PanicNilError)"},
		{name: "panic inside a kept map", items: []map[string]any{{"x": panickyRow{}}},
			wantErr: "item 0 cannot be used: its JSON encoding panicked (string)"},
		{name: "panic inside a kept list", items: [][]any{{panickyRow{}}}, wantErr: "item 0 cannot be used: its JSON encoding panicked (string)"},
		{name: "number past float64 inside a list", items: []anyRow{{V: []json.RawMessage{json.RawMessage("1e999")}}},
			wantErr: "item 0 cannot be used: a number out of range"},
		{name: "number past float64 as an element", items: []json.RawMessage{json.RawMessage("1e999")},
			wantErr: "item 0 cannot be used: a number out of range"},
		{name: "a []any is used as given", items: []any{map[string]any{"id": int64(1)}, row{Text: "x"}},
			want: []any{map[string]any{"id": int64(1)}, row{Text: "x"}}},
		{name: "panic value stays private", items: []panicSecretRow{{}}, wantErr: "item 0 cannot be used: its JSON encoding panicked (string)"},
		{name: "unsupported value text stays private", items: []valueErrorRow{{}}, wantErr: "item 0 cannot be used: unsupported value"},
		{name: "named map type", items: []namedMapRow{{"t": "x"}}, want: []any{map[string]any{"t": "x"}}},
		{name: "interface elements", items: []error{errors.New("x")}, want: []any{map[string]any{}}},
		{name: "NaN inside a kept map", items: []map[string]any{{"f": math.NaN()}}, wantErr: "item 0 cannot be used: unsupported value NaN"},
		{name: "infinity inside a kept list", items: [][]any{{1.0, math.Inf(-1)}}, wantErr: "item 0 cannot be used: unsupported value -Inf"},
		{name: "NaN nested deeper", items: []map[string]any{{"ok": 1}, {"m": map[string]any{"l": []any{math.NaN()}}}},
			wantErr: "item 1 cannot be used: unsupported value NaN"},
		{name: "pointer-receiver marshaler field", items: []pricedRow{{Price: money{Cents: 500}}},
			want: []any{map[string]any{"price": "$5"}}},
		{name: "pointer-receiver marshaler element", items: []money{{Cents: 500}}, want: []any{"$5"}},
		{name: "uint64 past int64", items: []bigUintRow{{U: math.MaxUint64}}, want: []any{map[string]any{"u": uint64(math.MaxUint64)}}},
		{name: "max_items skips the rest", items: []anyRow{{V: 1}, {V: make(chan int)}}, maxItems: 1,
			want: []any{map[string]any{"v": int64(1)}}},
		{name: "array", items: [2]row{}, wantErr: "resolved to [2]workflow.row, expected list"},
		{name: "slice pointer", items: &[]row{}, wantErr: "resolved to *[]workflow.row, expected list"},
		{name: "later item fails", items: []anyRow{{V: 1}, {V: make(chan int)}},
			wantErr: "item 1 cannot be used: unsupported type chan int"},
		{name: "NaN element", items: []float64{1, math.NaN()}, wantErr: "item 1 cannot be used: unsupported value NaN"},
		{name: "infinite element", items: []float32{float32(math.Inf(1))}, wantErr: "item 0 cannot be used: unsupported value +Inf"},
		{name: "panicking encoder", items: []panickyRow{{}}, wantErr: "item 0 cannot be used: its JSON encoding panicked (string)"},
		{name: "byte enum", items: []namedByte{1}, wantErr: "a byte slice is data, not a list of items"},
		{name: "string maps", items: []map[string]string{{"a": "x"}}, want: []any{map[string]any{"a": "x"}}},
		{name: "structs", items: []row{{Text: "x"}, {Text: "y"}}, want: []any{map[string]any{"text": "x"}, map[string]any{"text": "y"}}},
		{name: "struct keeps big ints", items: []idRow{{ID: big}}, want: []any{map[string]any{"id": big}}},
		{name: "untagged struct keys", items: []untaggedRow{{Name: "x"}}, want: []any{map[string]any{"Name": "x"}}},
		{name: "typed nil element", items: []*row{nil}, want: []any{nil}},
		{name: "strings kept exactly", items: []string{"x", "a\xffb"}, want: []any{"x", "a\xffb"}},
		{name: "int64s", items: []int64{1, big}, want: []any{int64(1), big}},
		{name: "nil", items: []map[string]any(nil), want: nil},
		{name: "empty", items: []row{}, want: []any{}},
		{name: "bytes", items: []byte("ab"), wantErr: "resolved to []uint8, expected list"},
		{name: "raw message list", items: json.RawMessage(`[1,2]`), wantErr: "expected list"},
		{name: "named bytes", items: []namedByte{1, 2}, wantErr: "expected list"},
		{name: "scalar", items: 42, wantErr: "resolved to int, expected list"},
		{name: "NaN field", items: []struct{ F float64 }{{math.NaN()}},
			wantErr: "a list whose item 0 cannot be used: unsupported value NaN"},
		{name: "chans", items: []chan int{make(chan int)}, wantErr: "item 0 cannot be used: unsupported type chan int"},
		{name: "custom encoder error stays private", items: []leakyRow{{secret: "s3cret"}},
			wantErr: "item 0 cannot be used: its JSON encoding failed (*json.MarshalerError)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			node := &NodeConfig{ID: "map1", Type: "map", TemplateNode: "body1", ItemsExpression: tc.items, MaxItems: tc.maxItems}
			nodes := []*NodeConfig{node, {ID: "body1", Type: "agent", AgentName: "agentA", DependsOn: []string{"map1"}}}
			dag, _ := BuildDependencyGraph(nodes)
			e := newTestDAGExecutor(&WorkflowConfig{Nodes: nodes}, dag, NewExecutionState("wf", "e1"))
			defer e.execCtx.Cancel()

			items, err := e.resolveMapItems(node)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) || strings.Contains(err.Error(), "s3cret") {
					t.Fatalf("resolveMapItems(%T) err = %v, want one containing %q and no item data", tc.items, err, tc.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("resolveMapItems(%T) err = %v", tc.items, err)
			}
			if !reflect.DeepEqual(items, tc.want) {
				t.Errorf("resolveMapItems(%T) = %#v, want %#v", tc.items, items, tc.want)
			}
		})
	}
}
