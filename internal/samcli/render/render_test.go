// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package render

import (
	"bufio"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

var updateDrawn = flag.Bool("update", false, "rewrite the testdata/streams/*.drawn.json files")

type drawnBubble struct {
	TaskID       string         `json:"taskId"`
	IsError      bool           `json:"isError"`
	IsWarning    bool           `json:"isWarning"`
	InputRequest string         `json:"inputRequest"`
	Segments     []drawnSegment `json:"segments"`
}

type drawnSegment struct {
	Kind   string `json:"kind"`
	Text   string `json:"text,omitempty"`
	Name   string `json:"name,omitempty"`
	Status string `json:"status,omitempty"`
}

func drawn(bubbles []Bubble) []drawnBubble {
	out := []drawnBubble{}
	for _, b := range bubbles {
		d := drawnBubble{TaskID: b.TaskID, IsError: b.IsError, IsWarning: b.IsWarning, InputRequest: b.InputRequest, Segments: []drawnSegment{}}
		for _, s := range Segments(b) {
			d.Segments = append(d.Segments, drawnSegment{Kind: s.Kind, Text: s.Text, Name: s.Name, Status: s.Status})
		}
		out = append(out, d)
	}
	return out
}

func loadStream(t *testing.T, path string) []Event {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	var events []Event
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev Event
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		events = append(events, ev)
	}
	return events
}

func streamPaths(t *testing.T) []string {
	t.Helper()
	paths, err := filepath.Glob(filepath.Join("testdata", "streams", "*.jsonl"))
	if err != nil || len(paths) == 0 {
		t.Fatalf("no streams in testdata/streams: %v", err)
	}
	return paths
}

func TestFoldDrawsWhatTheWebUIDraws(t *testing.T) {
	for _, path := range streamPaths(t) {
		name := strings.TrimSuffix(filepath.Base(path), ".jsonl")
		t.Run(name, func(t *testing.T) {
			got, err := json.MarshalIndent(drawn(Fold(loadStream(t, path))), "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			got = append(got, '\n')
			wantPath := strings.TrimSuffix(path, ".jsonl") + ".drawn.json"
			if *updateDrawn {
				if err = os.WriteFile(wantPath, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			want, err := os.ReadFile(wantPath)
			if err != nil {
				t.Fatalf("%v (run with -update to create it)", err)
			}
			if string(got) != string(want) {
				t.Errorf("drawn bubbles differ from %s\ngot:\n%s\nwant:\n%s", wantPath, got, want)
			}
		})
	}
}

func TestCheckFindsRenderingProblems(t *testing.T) {
	cases := map[string][]string{
		"peer_table_glued":        {"malformed_table"},
		"inline_file_returned":    {},
		"leaks":                   {"internal_note", "raw_embed", "unclosed_fence"},
		"timeout":                 {"warning_bubble"},
		"file_only_answer":        {},
		"peer_artifact_forwarded": {},
		"ask_user":                {},
	}
	for name, want := range cases {
		t.Run(name, func(t *testing.T) {
			bubbles := Fold(loadStream(t, filepath.Join("testdata", "streams", name+".jsonl")))
			got := []string{}
			for _, f := range Check(bubbles, Options{}) {
				if !slices.Contains(got, f.Check) {
					got = append(got, f.Check)
				}
			}
			slices.Sort(got)
			slices.Sort(want)
			if !slices.Equal(got, want) {
				t.Errorf("checks = %v, want %v", got, want)
			}
		})
	}
}

func TestCheckFindsATableShownTwiceWithRewordedHeaders(t *testing.T) {
	text := "| SKU | Qty |\n|---|---|\n| SKU-1 | 4 |\n| SKU-2 | 0 |\n| SKU-3 | 9 |\n\n" +
		"Here it is again:\n\n| Item | On hand |\n|---|---|\n| SKU-1 | 4 |\n| SKU-2 | 0 |\n| SKU-3 | 9 |\n"
	bubbles := []Bubble{{Role: roleAgent, TaskID: "t1", Parts: []Part{{Kind: partText, Text: text}}, Complete: true}}
	if !slices.ContainsFunc(Check(bubbles, Options{}), func(f Finding) bool { return f.Check == "duplicate_table" }) {
		t.Error("a table repeated with reworded headers was not reported")
	}
}

func TestCheckReportsCardsForArtifactsTheSessionDoesNotHave(t *testing.T) {
	bubbles := Fold(loadStream(t, filepath.Join("testdata", "streams", "file_only_answer.jsonl")))
	if HasErrors(Check(bubbles, Options{Artifacts: []string{"stock.csv"}})) {
		t.Error("a card for a stored artifact was reported")
	}
	if !HasErrors(Check(bubbles, Options{Artifacts: []string{}})) {
		t.Error("a card for a missing artifact was not reported")
	}
}

func TestFromChatTasksReadsTheBubblesTheBrowserSaved(t *testing.T) {
	bubbles := `[{"type":"user","text":"show stock"},` +
		`{"type":"agent","text":"Here.","parts":[{"kind":"text","text":"Here. "},{"kind":"artifact","name":"stock.csv","status":"completed"}]},` +
		`{"type":"agent","isError":true,"text":"The request timed out."}]`
	body, err := json.Marshal(map[string]any{"data": []map[string]any{{"id": "t1", "messageBubbles": bubbles}}})
	if err != nil {
		t.Fatal(err)
	}
	got, err := FromChatTasks(body)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 3 || got[0].Role != roleUser || got[1].Parts[1].Name != "stock.csv" || !got[2].IsError || got[2].Parts[0].Text != "The request timed out." {
		t.Errorf("unexpected bubbles: %+v", got)
	}
}
