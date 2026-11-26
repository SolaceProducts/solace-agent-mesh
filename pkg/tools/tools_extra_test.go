// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"math"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/filesystem"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func TestSearchAndReplaceInArtifact(t *testing.T) {
	tests := []struct {
		name       string
		content    string
		args       map[string]any
		wantStatus string
		wantSubstr string
		wantCode   string
		checkFunc  func(t *testing.T, r *tool.Result)
	}{
		{
			name:       "missing filename",
			content:    "",
			args:       map[string]any{},
			wantStatus: tool.StatusError,
			wantSubstr: "filename is required",
			wantCode:   tool.DispatchErrorCode(taxonomy.DispatchClassValidation),
		},
		{
			name:       "empty filename string",
			args:       map[string]any{"filename": ""},
			wantStatus: tool.StatusError,
			wantSubstr: "filename is required",
			wantCode:   tool.DispatchErrorCode(taxonomy.DispatchClassValidation),
		},
		{
			name:       "missing search_expression and replacements",
			content:    "hello world",
			args:       map[string]any{"search_expression": ""},
			wantStatus: tool.StatusError,
			wantSubstr: "search_expression",
			wantCode:   tool.DispatchErrorCode(taxonomy.DispatchClassValidation),
		},
		{
			name:    "literal replacement success",
			content: "Hello World Hello",
			args: map[string]any{
				"search_expression":  "Hello",
				"replace_expression": "Hi",
			},
			wantStatus: tool.StatusSuccess,
			wantSubstr: "Successfully performed search and replace",
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				if len(r.DataObjects) == 0 {
					t.Fatal("expected DataObjects")
				}
				got := string(r.DataObjects[0].Content)
				if got != "Hi World Hi" {
					t.Errorf("content = %q, want %q", got, "Hi World Hi")
				}
			},
		},
		{
			name:    "no matches returns partial",
			content: "foo bar",
			args: map[string]any{
				"search_expression":  "baz",
				"replace_expression": "qux",
			},
			wantStatus: tool.StatusPartial,
			wantSubstr: "No matches found",
		},
		{
			name:    "regex replacement with global flag",
			content: "cat bat hat",
			args: map[string]any{
				"search_expression":  "[cbh]at",
				"replace_expression": "dog",
				"is_regexp":          true,
				"regexp_flags":       "g",
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				got := string(r.DataObjects[0].Content)
				if got != "dog dog dog" {
					t.Errorf("content = %q, want %q", got, "dog dog dog")
				}
			},
		},
		{
			name:    "regex replacement first match only",
			content: "cat bat hat",
			args: map[string]any{
				"search_expression":  "[cbh]at",
				"replace_expression": "dog",
				"is_regexp":          true,
				"regexp_flags":       "",
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				got := string(r.DataObjects[0].Content)
				if got != "dog bat hat" {
					t.Errorf("content = %q, want %q", got, "dog bat hat")
				}
			},
		},
		{
			name:    "regex case insensitive flag",
			content: "Hello hello HELLO",
			args: map[string]any{
				"search_expression":  "hello",
				"replace_expression": "hi",
				"is_regexp":          true,
				"regexp_flags":       "gi",
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				got := string(r.DataObjects[0].Content)
				if got != "hi hi hi" {
					t.Errorf("content = %q, want %q", got, "hi hi hi")
				}
			},
		},
		{
			name:    "invalid regex",
			content: "some text",
			args: map[string]any{
				"search_expression":  "[invalid",
				"replace_expression": "x",
				"is_regexp":          true,
			},
			wantStatus: tool.StatusError,
			wantSubstr: "invalid regular expression",
			wantCode:   taxonomy.ToolFailureInvalidQuery,
		},
		{
			name:    "capture group back-reference",
			content: "John Smith, Jane Doe",
			args: map[string]any{
				"search_expression":  `(\w+) (\w+)`,
				"replace_expression": "$2 $1",
				"is_regexp":          true,
				"regexp_flags":       "g",
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				got := string(r.DataObjects[0].Content)
				if got != "Smith John, Doe Jane" {
					t.Errorf("content = %q, want %q", got, "Smith John, Doe Jane")
				}
			},
		},
		{
			name:    "literal dollar in regex replacement",
			content: "price: 10",
			args: map[string]any{
				"search_expression":  "(10)",
				"replace_expression": "$$ and done",
				"is_regexp":          true,
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				got := string(r.DataObjects[0].Content)
				if got != "price: $ and done" {
					t.Errorf("content = %q, want %q", got, "price: $ and done")
				}
			},
		},
		{
			name:    "new_filename saves to different file",
			content: "original",
			args: map[string]any{
				"search_expression":  "original",
				"replace_expression": "modified",
				"new_filename":       "output.txt",
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				if len(r.DataObjects) == 0 {
					t.Fatal("expected DataObjects")
				}
				if r.DataObjects[0].Name != "output.txt" {
					t.Errorf("DataObject name = %q, want %q", r.DataObjects[0].Name, "output.txt")
				}
			},
		},
		{
			name:    "batch replacements",
			content: "aaa bbb ccc",
			args: map[string]any{
				"replacements": []any{
					map[string]any{
						"search":    "aaa",
						"replace":   "111",
						"is_regexp": false,
					},
					map[string]any{
						"search":    "bbb",
						"replace":   "222",
						"is_regexp": false,
					},
				},
			},
			wantStatus: tool.StatusSuccess,
			wantSubstr: "Batch replacement completed",
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				got := string(r.DataObjects[0].Content)
				if got != "111 222 ccc" {
					t.Errorf("content = %q, want %q", got, "111 222 ccc")
				}
			},
		},
		{
			name:    "batch and single conflict",
			content: "text",
			args: map[string]any{
				"search_expression": "text",
				"replacements": []any{
					map[string]any{
						"search":    "text",
						"replace":   "x",
						"is_regexp": false,
					},
				},
			},
			wantStatus: tool.StatusError,
			wantSubstr: "Cannot provide both",
			wantCode:   tool.DispatchErrorCode(taxonomy.DispatchClassValidation),
		},
		{
			name:    "batch with invalid item type",
			content: "text",
			args: map[string]any{
				"replacements": []any{
					"not-a-map",
				},
			},
			wantStatus: tool.StatusError,
			wantSubstr: "must be an object",
			wantCode:   taxonomy.ToolFailureInvalidQuery,
		},
		{
			name:    "batch replacements not an array",
			content: "text",
			args: map[string]any{
				"replacements": "not-an-array",
			},
			wantStatus: tool.StatusError,
			wantSubstr: "must be an array",
			wantCode:   tool.DispatchErrorCode(taxonomy.DispatchClassValidation),
		},
		{
			name:    "regex multiline flag",
			content: "line1\nline2\nline3",
			args: map[string]any{
				"search_expression":  "^line2$",
				"replace_expression": "LINE_TWO",
				"is_regexp":          true,
				"regexp_flags":       "m",
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				got := string(r.DataObjects[0].Content)
				if got != "line1\nLINE_TWO\nline3" {
					t.Errorf("content = %q", got)
				}
			},
		},
		{
			name:    "regex dotall flag",
			content: "start\nmiddle\nend",
			args: map[string]any{
				"search_expression":  "start.+end",
				"replace_expression": "REPLACED",
				"is_regexp":          true,
				"regexp_flags":       "s",
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				got := string(r.DataObjects[0].Content)
				if got != "REPLACED" {
					t.Errorf("content = %q, want %q", got, "REPLACED")
				}
			},
		},
		{
			name:    "new_description propagated",
			content: "old text",
			args: map[string]any{
				"search_expression":  "old",
				"replace_expression": "new",
				"new_description":    "updated content",
			},
			wantStatus: tool.StatusSuccess,
			checkFunc: func(t *testing.T, r *tool.Result) {
				t.Helper()
				if len(r.DataObjects) == 0 {
					t.Fatal("expected DataObjects")
				}
				if r.DataObjects[0].Description != "updated content" {
					t.Errorf("Description = %q", r.DataObjects[0].Description)
				}
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			st := &SearchAndReplaceInArtifactTool{}
			args := tc.args

			if tc.content != "" {
				if _, hasFN := args["filename"]; !hasFN {
					args["filename"] = &tool.Artifact{
						Content:  []byte(tc.content),
						Filename: "test.txt",
						Version:  0,
						MIMEType: "text/plain",
					}
				}
			}

			result, err := st.Execute(context.Background(), args, &tool.Context{})
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q; message = %q", result.Status, tc.wantStatus, result.Message)
			}
			if tc.wantSubstr != "" && !strings.Contains(result.Message, tc.wantSubstr) {
				t.Errorf("message %q does not contain %q", result.Message, tc.wantSubstr)
			}
			if result.ErrorCode != tc.wantCode {
				t.Errorf("ErrorCode = %q, want %q", result.ErrorCode, tc.wantCode)
			}
			if tc.checkFunc != nil {
				tc.checkFunc(t, result)
			}
		})
	}
}

func TestSearchAndReplaceMetadata(t *testing.T) {
	st := &SearchAndReplaceInArtifactTool{}
	if st.Name() != "artifact_search_and_replace_regex" {
		t.Errorf("Name() = %q", st.Name())
	}
	ap := st.ArtifactParams()
	if _, ok := ap["filename"]; !ok {
		t.Error("ArtifactParams missing 'filename'")
	}
}

func TestSearchAndReplaceBatchRegexWithoutGFlagReplacesAllMatches(t *testing.T) {
	st := &SearchAndReplaceInArtifactTool{}
	art := &tool.Artifact{
		Content:  []byte("abc abc abc"),
		Filename: "test.txt",
		Version:  0,
		MIMEType: "text/plain",
	}
	result, err := st.Execute(context.Background(), map[string]any{
		"filename": art,
		"replacements": []any{
			map[string]any{
				"search":    "abc",
				"replace":   "xyz",
				"is_regexp": true,
			},
		},
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
	}
	if got := string(result.DataObjects[0].Content); got != "xyz xyz xyz" {
		t.Errorf("content = %q, want %q", got, "xyz xyz xyz")
	}
}

func TestSearchAndReplaceLookaheadPatternNamesRE2(t *testing.T) {
	art := &tool.Artifact{Content: []byte("foobar"), Filename: "test.txt", MIMEType: "text/plain"}
	tests := []struct {
		name string
		args map[string]any
	}{
		{"single", map[string]any{"search_expression": "foo(?=bar)", "replace_expression": "x", "is_regexp": true}},
		{"batch", map[string]any{"replacements": []any{
			map[string]any{"search": "foo(?=bar)", "replace": "x", "is_regexp": true},
		}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tt.args["filename"] = art
			result, err := (&SearchAndReplaceInArtifactTool{}).Execute(context.Background(), tt.args, &tool.Context{})
			if err != nil {
				t.Fatal(err)
			}
			if result.Status != tool.StatusError || !strings.Contains(result.Message, "RE2") || !strings.Contains(result.Message, "no lookahead") {
				t.Errorf("got %s %q, want an error naming RE2 and lookahead", result.Status, result.Message)
			}
		})
	}
}

func TestAppendToArtifact_MissingArtifactIsCreatedFromChunk(t *testing.T) {
	ctx := context.Background()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	result, err := (&AppendToArtifactTool{}).Execute(ctx, map[string]any{
		"filename": "new.md", "content_chunk": "# Title\n", "mime_type": "text/markdown",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess || !strings.Contains(result.Message, "did not exist") {
		t.Fatalf("got %s %q, want success saying the artifact was created", result.Status, result.Message)
	}
	if result.Data["new_version"] != 0 {
		t.Errorf("new_version = %v, want the first version", result.Data["new_version"])
	}
	if _, ok := result.Data["appended_from_version"]; ok {
		t.Errorf("appended_from_version = %v, want it absent for a created artifact", result.Data["appended_from_version"])
	}

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "new.md")
	content, mime, err := svc.Load(ctx, loc, nil)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != "# Title\n" || mime != "text/markdown" {
		t.Errorf("saved %q as %q, want the chunk as text/markdown", content, mime)
	}

	again, err := (&AppendToArtifactTool{}).Execute(ctx, map[string]any{
		"filename": "new.md", "content_chunk": "more", "mime_type": "text/markdown",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if again.Status != tool.StatusSuccess || !strings.Contains(again.Message, "Chunk appended") {
		t.Errorf("second append got %s %q, want a normal append", again.Status, again.Message)
	}
	content, _, _ = svc.Load(ctx, loc, nil)
	if string(content) != "# Title\nmore" {
		t.Errorf("content = %q, want both chunks", content)
	}
}

func TestSearchAndReplaceStringFilename(t *testing.T) {
	st := &SearchAndReplaceInArtifactTool{}
	result, err := st.Execute(context.Background(), map[string]any{
		"filename":          "some_file.txt",
		"search_expression": "x",
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "could not be loaded") {
		t.Errorf("message = %q, expected 'could not be loaded'", result.Message)
	}
}

func TestArtifactModifyToolsNameTheCreateMechanism(t *testing.T) {
	refusalText := func(r *tool.Result) string { return r.Message + r.ValidationError }

	dispatch := func(t *testing.T, tl tool.Tool, toolCtx *tool.Context, args map[string]any) *tool.Result {
		t.Helper()
		set := tool.NewSet()
		if err := set.Register(tl); err != nil {
			t.Fatalf("Register(%s): %v", tl.Name(), err)
		}
		raw, err := json.Marshal(args)
		if err != nil {
			t.Fatalf("marshal args: %v", err)
		}
		result, err := set.Dispatch(t.Context(),
			tool.Call{Name: tl.Name(), Arguments: string(raw)}, toolCtx)
		if err != nil {
			t.Fatalf("Dispatch returned Go error: %v", err)
		}
		t.Logf("%s -> %s: %s", tl.Name(), result.Status, refusalText(result))
		return result
	}

	assertHint := func(t *testing.T, message string) {
		t.Helper()
		if !strings.Contains(message, "only modifies an artifact that already exists") {
			t.Errorf("message = %q, want it to say the tool cannot create an artifact", message)
		}
		if !strings.Contains(message, agent.BlockPrefixSaveArtifact) {
			t.Errorf("message = %q, want it to name %q as the way to create one",
				message, agent.BlockPrefixSaveArtifact)
		}
		if !strings.Contains(message, agent.BlockDelimiterClose) {
			t.Errorf("message = %q, want it to show the block's closing delimiter %q",
				message, agent.BlockDelimiterClose)
		}
	}

	assertNoHint := func(t *testing.T, message string) {
		t.Helper()
		if strings.Contains(message, agent.BlockPrefixSaveArtifact) {
			t.Errorf("message = %q, must not point at the create block", message)
		}
	}

	t.Run("search and replace on a missing artifact", func(t *testing.T) {
		result := dispatch(t, &SearchAndReplaceInArtifactTool{}, newTestToolCtx(newMockArtifactService()),
			map[string]any{
				"filename":          "missing.txt",
				"search_expression": "x",
			})
		if result.Status != tool.StatusError {
			t.Fatalf("status = %q, want error", result.Status)
		}
		if !strings.Contains(result.Message, `artifact "missing.txt" not found`) {
			t.Fatalf("message = %q, want the resolver's not-found refusal", result.Message)
		}
		assertHint(t, result.Message)
	})

	t.Run("yaml merge into a missing artifact", func(t *testing.T) {
		result := dispatch(t, &YAMLMergeArtifactTool{}, newTestToolCtx(newMockArtifactService()),
			map[string]any{
				"artifact_name": "missing.yaml",
				"yaml_fragment": "tools:\n  - name: x\n",
			})
		if result.Status != tool.StatusError {
			t.Fatalf("status = %q, want error", result.Status)
		}
		if !strings.Contains(result.Message, "artifact not found: missing.yaml") {
			t.Fatalf("message = %q, want the not-found refusal", result.Message)
		}
		assertHint(t, result.Message)
	})

	realStore := func(t *testing.T) (*filesystem.Store, *tool.Context) {
		t.Helper()
		store := filesystem.New(t.TempDir())
		return store, newTestToolCtx(store)
	}
	seedRealStore := func(t *testing.T, store *filesystem.Store, toolCtx *tool.Context, filename, content string) {
		t.Helper()
		loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", filename)
		if _, err := store.Save(t.Context(), loc, []byte(content), "text/markdown"); err != nil {
			t.Fatalf("seed %q: %v", filename, err)
		}
	}

	t.Run("an explicit version a real store lacks carries no hint", func(t *testing.T) {
		store, toolCtx := realStore(t)
		seedRealStore(t, store, toolCtx, "report.md", "hello")

		result := dispatch(t, &SearchAndReplaceInArtifactTool{}, toolCtx, map[string]any{
			"filename":          "report.md:9",
			"search_expression": "x",
		})
		if result.Status != tool.StatusError {
			t.Fatalf("status = %q, want error", result.Status)
		}
		if !strings.Contains(result.Message, `artifact "report.md" version 9 not found`) {
			t.Fatalf("message = %q, want the refusal to name the missing version", result.Message)
		}
		assertNoHint(t, result.Message)
	})

	t.Run("a filename that same real store never saw carries the hint", func(t *testing.T) {
		store, toolCtx := realStore(t)
		seedRealStore(t, store, toolCtx, "report.md", "hello")

		result := dispatch(t, &SearchAndReplaceInArtifactTool{}, toolCtx, map[string]any{
			"filename":          "never-saved.md",
			"search_expression": "x",
		})
		if result.Status != tool.StatusError {
			t.Fatalf("status = %q, want error", result.Status)
		}
		if !strings.Contains(result.Message, `artifact "never-saved.md" not found`) {
			t.Fatalf("message = %q, want the resolver's not-found refusal", result.Message)
		}
		assertHint(t, result.Message)
	})

	t.Run("a store that cannot answer carries no hint", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		saveTestArtifact(t.Context(), svc, toolCtx, "degraded.txt", "hello", "text/plain")
		svc.failLoads = map[string]bool{"degraded.txt": true}

		result := dispatch(t, &SearchAndReplaceInArtifactTool{}, toolCtx, map[string]any{
			"filename":          "degraded.txt",
			"search_expression": "x",
		})
		if result.Status != tool.StatusError {
			t.Fatalf("status = %q, want error", result.Status)
		}
		if !strings.Contains(result.Message, "failed to load") {
			t.Fatalf("message = %q, want the load-failure refusal", result.Message)
		}
		assertNoHint(t, result.Message)
	})

	t.Run("an empty filename carries no hint", func(t *testing.T) {
		result := dispatch(t, &SearchAndReplaceInArtifactTool{}, newTestToolCtx(newMockArtifactService()),
			map[string]any{
				"filename":          "",
				"search_expression": "x",
			})
		if result.Status != tool.StatusError {
			t.Fatalf("status = %q, want error", result.Status)
		}
		if !strings.Contains(result.Message, `artifact parameter "filename": empty filename`) {
			t.Fatalf("message = %q, want the resolver's empty-filename refusal", result.Message)
		}
		assertNoHint(t, result.Message)
	})

	t.Run("a mistyped filename is rejected by schema validation", func(t *testing.T) {
		result := dispatch(t, &SearchAndReplaceInArtifactTool{}, newTestToolCtx(newMockArtifactService()),
			map[string]any{
				"filename":          42,
				"search_expression": "x",
			})
		if result.Status != tool.StatusError {
			t.Fatalf("status = %q, want error", result.Status)
		}
		if strings.Contains(refusalText(result), "artifact resolution for") {
			t.Errorf("message = %q, want the schema check to reject it before the resolver runs",
				refusalText(result))
		}
		assertNoHint(t, refusalText(result))
	})

	t.Run("successful append carries no hint", func(t *testing.T) {
		svc := newMockArtifactService()
		toolCtx := newTestToolCtx(svc)
		saveTestArtifact(t.Context(), svc, toolCtx, "test.txt", "hello", "text/plain")
		result := dispatch(t, &AppendToArtifactTool{}, toolCtx, map[string]any{
			"filename":      "test.txt",
			"content_chunk": " world",
			"mime_type":     "text/plain",
		})
		if result.Status != tool.StatusSuccess {
			t.Fatalf("status = %q, want success; message = %q", result.Status, result.Message)
		}
		assertNoHint(t, result.Message)
	})
}

func TestConvertJSReplaceToGo(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{"simple capture group", "$1", "${1}"},
		{"two groups", "$1-$2", "${1}-${2}"},
		{"escaped dollar", "$$", "$"},
		{"mixed", "$$1 and $2", "$1 and ${2}"},
		{"no refs", "plain text", "plain text"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := convertJSReplaceToGo(tc.input)
			if got != tc.want {
				t.Errorf("convertJSReplaceToGo(%q) = %q, want %q", tc.input, got, tc.want)
			}
		})
	}
}

func TestAppendToArtifact(t *testing.T) {
	tests := []struct {
		name       string
		args       map[string]any
		setup      bool
		wantStatus string
		wantSubstr string
	}{
		{
			name:       "missing filename",
			args:       map[string]any{"content_chunk": "data", "mime_type": "text/plain"},
			wantStatus: tool.StatusError,
			wantSubstr: "filename is required",
		},
		{
			name:       "missing content_chunk",
			args:       map[string]any{"filename": "test.txt", "mime_type": "text/plain"},
			wantStatus: tool.StatusError,
			wantSubstr: "content_chunk is required",
		},
		{
			name:       "missing mime_type",
			args:       map[string]any{"filename": "test.txt", "content_chunk": "data"},
			wantStatus: tool.StatusError,
			wantSubstr: "mime_type is required",
		},
		{
			name:       "no artifact service",
			args:       map[string]any{"filename": "test.txt", "content_chunk": "data", "mime_type": "text/plain"},
			wantStatus: tool.StatusError,
			wantSubstr: "no artifact service",
		},
		{
			name:       "successful text append",
			args:       map[string]any{"filename": "test.txt", "content_chunk": " world", "mime_type": "text/plain"},
			setup:      true,
			wantStatus: tool.StatusSuccess,
			wantSubstr: "Chunk appended",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			at := &AppendToArtifactTool{}
			ctx := context.Background()

			if tc.wantSubstr == "no artifact service" {
				result, err := at.Execute(ctx, tc.args, &tool.Context{})
				if err != nil {
					t.Fatalf("Execute returned Go error: %v", err)
				}
				if result.Status != tc.wantStatus {
					t.Fatalf("status = %q, want %q; message = %q", result.Status, tc.wantStatus, result.Message)
				}
				if !strings.Contains(result.Message, tc.wantSubstr) {
					t.Errorf("message %q missing %q", result.Message, tc.wantSubstr)
				}
				return
			}

			svc := newMockArtifactService()
			toolCtx := newTestToolCtx(svc)

			if tc.setup {
				saveTestArtifact(ctx, svc, toolCtx, "test.txt", "hello", "text/plain")
			}

			result, err := at.Execute(ctx, tc.args, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tc.wantStatus {
				t.Fatalf("status = %q, want %q; message = %q", result.Status, tc.wantStatus, result.Message)
			}
			if tc.wantSubstr != "" && !strings.Contains(result.Message, tc.wantSubstr) {
				t.Errorf("message %q missing %q", result.Message, tc.wantSubstr)
			}

			if tc.wantStatus == tool.StatusSuccess && tc.setup {
				loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "test.txt")
				content, _, loadErr := svc.Load(ctx, loc, nil)
				if loadErr != nil {
					t.Fatalf("Load after append: %v", loadErr)
				}
				got := string(content)
				if got != "hello world" {
					t.Errorf("content after append = %q, want %q", got, "hello world")
				}
			}
		})
	}
}

func TestAppendToArtifactBase64Binary(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	ctx := context.Background()

	saveTestArtifact(ctx, svc, toolCtx, "data.bin", "initial", "application/octet-stream")

	extraBytes := []byte{0x01, 0x02, 0x03}
	b64Chunk := base64.StdEncoding.EncodeToString(extraBytes)

	at := &AppendToArtifactTool{}
	result, err := at.Execute(ctx, map[string]any{
		"filename":      "data.bin",
		"content_chunk": b64Chunk,
		"mime_type":     "application/octet-stream",
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute error: %v", err)
	}
	if result.Status != tool.StatusSuccess {
		t.Fatalf("status = %q; message = %q", result.Status, result.Message)
	}

	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "data.bin")
	content, _, _ := svc.Load(ctx, loc, nil)
	want := append([]byte("initial"), extraBytes...)
	if string(content) != string(want) {
		t.Errorf("combined content mismatch: got %v, want %v", content, want)
	}
}

func TestAppendToArtifactMetadata(t *testing.T) {
	at := &AppendToArtifactTool{}
	if at.Name() != "append_to_artifact" {
		t.Errorf("Name() = %q", at.Name())
	}
	params := at.Parameters()
	if params == nil {
		t.Fatal("Parameters() is nil")
	}
	for _, req := range params.Required {
		if _, ok := params.Properties[req]; !ok {
			t.Errorf("required param %q not in Properties", req)
		}
	}
}

func TestDecodeContentBytes(t *testing.T) {
	tests := []struct {
		name     string
		content  string
		mimeType string
		want     []byte
	}{
		{
			name:     "text mime returns raw bytes",
			content:  "hello",
			mimeType: "text/plain",
			want:     []byte("hello"),
		},
		{
			name:     "binary mime with valid base64",
			content:  base64.StdEncoding.EncodeToString([]byte{0xDE, 0xAD}),
			mimeType: "application/octet-stream",
			want:     []byte{0xDE, 0xAD},
		},
		{
			name:     "binary mime with invalid base64 falls back to text",
			content:  "not-base64!!!",
			mimeType: "application/octet-stream",
			want:     []byte("not-base64!!!"),
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := decodeContentBytes(tc.content, tc.mimeType)
			if string(got) != string(tc.want) {
				t.Errorf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestAskUserQuestionNoCallback(t *testing.T) {
	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"question":    "Pick one?",
				"header":      "Test",
				"multiSelect": false,
				"options": []any{
					map[string]any{"label": "A", "description": "option A"},
					map[string]any{"label": "B", "description": "option B"},
				},
			},
		},
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "can't show interactive prompts") {
		t.Errorf("message = %q, expected the can't-prompt refusal", result.Message)
	}
}

func TestAskUserQuestionEmptyQuestions(t *testing.T) {
	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(context.Background(), map[string]any{
		"questions": []any{},
	}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
}

func TestAskUserQuestionMissingQuestionsParam(t *testing.T) {
	ask := &AskUserQuestionTool{}
	result, err := ask.Execute(context.Background(), map[string]any{}, &tool.Context{})
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
}

func TestAskUserQuestionMetadata(t *testing.T) {
	ask := &AskUserQuestionTool{}
	if ask.Name() != "ask_user_question" {
		t.Errorf("Name() = %q", ask.Name())
	}
	if ask.Description() == "" {
		t.Error("Description() is empty")
	}
	params := ask.Parameters()
	if params == nil {
		t.Fatal("Parameters() is nil")
	}
	if _, ok := params.Properties["questions"]; !ok {
		t.Error("Parameters missing 'questions'")
	}
	if len(params.Required) == 0 {
		t.Error("Required is empty")
	}
}

func TestAskUserQuestionAllTextQuestionsRejected(t *testing.T) {
	ask := &AskUserQuestionTool{}
	called := false
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			called = true
			return &a2a.UserInputResponseData{Status: "answered"}
		},
	}
	result, err := ask.Execute(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{"question": "What should I name this?", "header": "Name"},
			map[string]any{"question": "Any other requirements?", "header": "Notes"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("status = %q, want error", result.Status)
	}
	if !strings.Contains(result.Message, "free-text") {
		t.Errorf("message = %q, expected 'free-text' guidance", result.Message)
	}
	for _, nudge := range []string{"2-4", "multiple-choice"} {
		if strings.Contains(result.Message, nudge) {
			t.Errorf("message = %q, must not suggest %q as a way to satisfy the tool", result.Message, nudge)
		}
	}
	if called {
		t.Error("WaitForUserInput was invoked; expected the guard to short-circuit before it")
	}
}

func TestAskUserQuestionConfigValuesWithPlaceholdersAllowed(t *testing.T) {
	ask := &AskUserQuestionTool{}
	called := false
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			called = true
			return &a2a.UserInputResponseData{Status: "answered"}
		},
	}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "PostgresConnector",
		"questions": []any{
			map[string]any{"question": "Host URI", "header": "Host", "placeholder": "postgres://…"},
			map[string]any{"question": "Message VPN", "header": "VPN", "placeholder": "e.g. default"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status == tool.StatusError {
		t.Fatalf("config collection was refused: %q", result.Message)
	}
	if !called {
		t.Error("WaitForUserInput was not invoked; a config set may be all free-text")
	}
}

func TestAskUserQuestionRejectsUnusableQuestionText(t *testing.T) {
	cases := []struct {
		name      string
		questions []any
		wantIn    string
	}{
		{
			name: "duplicate question text",
			questions: []any{
				map[string]any{"question": "Hostname", "header": "Primary", "placeholder": "db1.internal"},
				map[string]any{"question": "Hostname", "header": "Replica", "placeholder": "db2.internal"},
			},
			wantIn: "share the",
		},
		{
			name: "empty question text",
			questions: []any{
				map[string]any{"question": "", "header": "Host", "placeholder": "type here"},
			},
			wantIn: "non-empty",
		},
		{
			name: "options supplied but none are objects",
			questions: []any{
				map[string]any{"question": "Pick?", "header": "Pick", "options": []any{"a", "b"}},
			},
			wantIn: "must be an object",
		},
		{
			name:      "entries supplied but none is a question object",
			questions: []any{"junk"},
			wantIn:    "none is a question object",
		},
		{
			name: "whitespace-only question text",
			questions: []any{
				map[string]any{"question": "   ", "header": "H", "placeholder": "x"},
			},
			wantIn: "non-empty",
		},
		{
			name: "duplicates differing only by whitespace",
			questions: []any{
				map[string]any{"question": "Hostname", "header": "Primary", "placeholder": "db1"},
				map[string]any{"question": "Hostname ", "header": "Replica", "placeholder": "db2"},
			},
			wantIn: "share the",
		},
		{
			name:      "no questions at all",
			questions: []any{},
			wantIn:    "At least one question is required",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ask := &AskUserQuestionTool{}
			called := false
			toolCtx := &tool.Context{
				WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
					called = true
					return &a2a.UserInputResponseData{Status: "answered"}
				},
			}
			result, err := ask.Execute(t.Context(), map[string]any{"questions": tc.questions}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("accepted: %q", result.Message)
			}
			if !strings.Contains(result.Message, tc.wantIn) {
				t.Errorf("message = %q, want it to mention %q", result.Message, tc.wantIn)
			}
			if called {
				t.Error("WaitForUserInput was invoked; the guard must short-circuit")
			}
		})
	}
}

func TestAskUserQuestionStrayElementDoesNotRefuseConfigValues(t *testing.T) {
	ask := &AskUserQuestionTool{}
	called := false
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, surface json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			called = true
			var s struct {
				DataModel struct {
					Answers map[string]any `json:"answers"`
				} `json:"dataModel"`
			}
			if err := json.Unmarshal(surface, &s); err != nil {
				t.Fatalf("surface is not valid JSON: %v", err)
			}
			keys := make([]string, 0, len(s.DataModel.Answers))
			for k := range s.DataModel.Answers {
				keys = append(keys, k)
			}
			slices.Sort(keys)
			if len(keys) != 2 {
				t.Fatalf("surface declared %d answer slots, want 2: %v", len(keys), keys)
			}
			return &a2a.UserInputResponseData{
				Status: "answered",
				Data:   map[string]any{"answers": map[string]any{keys[0]: "myhost", keys[1]: "default"}},
			}
		},
	}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "PostgresConnector",
		"questions": []any{
			nil,
			map[string]any{"question": "Host URI", "header": "Host", "placeholder": "postgres://…"},
			map[string]any{"question": "Message VPN", "header": "VPN", "placeholder": "e.g. default"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status == tool.StatusError {
		t.Fatalf("a config set was refused because of one stray element: %q", result.Message)
	}
	if !called {
		t.Fatal("WaitForUserInput was not invoked; the real fields should still render")
	}

	answers, ok := result.Data["answers"].(map[string]string)
	if !ok {
		t.Fatalf("result data has no answers map: %#v", result.Data)
	}
	for question, want := range map[string]string{"Host URI": "myhost", "Message VPN": "default"} {
		if got := answers[question]; got != want {
			t.Errorf("question %q got %q, want %q: the surface key and the extract key disagree",
				question, got, want)
		}
	}
}

func TestAskUserQuestionComponentNameAloneIsNotAnExemption(t *testing.T) {
	ask := &AskUserQuestionTool{}
	called := false
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			called = true
			return &a2a.UserInputResponseData{Status: "answered"}
		},
	}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "SomeConnector",
		"questions": []any{
			map[string]any{"question": "Any other requirements?", "header": "Notes"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("an open-ended question was accepted because component_name was set: %q", result.Message)
	}
	if called {
		t.Error("WaitForUserInput was invoked; component_name must not bypass the guard")
	}
}

func TestAskUserQuestionPartialPlaceholdersRejected(t *testing.T) {
	ask := &AskUserQuestionTool{}
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			return &a2a.UserInputResponseData{Status: "answered"}
		},
	}
	result, err := ask.Execute(t.Context(), map[string]any{
		"component_name": "PostgresConnector",
		"questions": []any{
			map[string]any{"question": "Host URI", "header": "Host", "placeholder": "postgres://…"},
			map[string]any{"question": "Anything else?", "header": "Notes"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("a call with a placeholderless question was accepted: %q", result.Message)
	}
}

func TestAskUserQuestionFreeTextRefusalShapes(t *testing.T) {
	cases := []struct {
		name      string
		questions []any
	}{
		{
			name: "whitespace placeholder",
			questions: []any{
				map[string]any{"question": "Host URI", "header": "Host", "placeholder": "   "},
			},
		},
		{
			name: "non-string placeholder",
			questions: []any{
				map[string]any{"question": "Host URI", "header": "Host", "placeholder": 42},
			},
		},
		{
			name: "open-ended question padded with a non-map element",
			questions: []any{
				map[string]any{"question": "Any other requirements?", "header": "Notes"},
				42,
			},
		},
		{
			name: "options present but none renderable",
			questions: []any{
				map[string]any{"question": "What should I name this?", "header": "Name", "options": []any{"a", "b"}},
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ask := &AskUserQuestionTool{}
			called := false
			toolCtx := &tool.Context{
				WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
					called = true
					return &a2a.UserInputResponseData{Status: "answered"}
				},
			}
			result, err := ask.Execute(t.Context(), map[string]any{"questions": tc.questions}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("accepted: %q", result.Message)
			}
			if called {
				t.Error("WaitForUserInput was invoked; the guard must short-circuit")
			}
		})
	}
}

func TestAskUserQuestionSplitsUnusableOptionsRefusals(t *testing.T) {
	cases := []struct {
		name    string
		options []any
		wantIn  string
		notIn   string
	}{
		{
			name:    "bare strings",
			options: []any{"a", "b"},
			wantIn:  "none of them are objects",
			notIn:   "usable `label`",
		},
		{
			name: "objects carrying the wrong key",
			options: []any{
				map[string]any{"name": "A", "description": "first"},
				map[string]any{"name": "B", "description": "second"},
			},
			wantIn: "none of them carries a usable `label`",
			notIn:  "none of them are objects",
		},
		{
			name:    "single object whose label is whitespace",
			options: []any{map[string]any{"label": "  ", "description": "d"}},
			wantIn:  "none of them carries a usable `label`",
			notIn:   "none of them are objects",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ask := &AskUserQuestionTool{}
			called := false
			toolCtx := &tool.Context{
				WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
					called = true
					return &a2a.UserInputResponseData{Status: "answered"}
				},
			}
			result, err := ask.Execute(t.Context(), map[string]any{
				"questions": []any{
					map[string]any{"question": "Pick?", "header": "Pick", "options": tc.options},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("accepted: %q", result.Message)
			}
			if !strings.Contains(result.Message, tc.wantIn) {
				t.Errorf("message = %q, want it to mention %q", result.Message, tc.wantIn)
			}
			if strings.Contains(result.Message, tc.notIn) {
				t.Errorf("message = %q, must not mention %q — that diagnoses the other mistake",
					result.Message, tc.notIn)
			}
			if !strings.Contains(result.Message, "drop `options` and ask in prose instead") {
				t.Errorf("message = %q, want the drop-`options`-and-ask-in-prose escape hatch",
					result.Message)
			}
			if called {
				t.Error("WaitForUserInput was invoked; the guard must short-circuit")
			}
		})
	}
}

func TestAskUserQuestionUnusableOptionsNamesTheOffendingQuestion(t *testing.T) {
	const goodQuestion = "Which region hosts the database?"
	const badQuestion = "Which replication tier?"

	ask := &AskUserQuestionTool{}
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			return &a2a.UserInputResponseData{Status: "answered"}
		},
	}
	result, err := ask.Execute(t.Context(), map[string]any{
		"questions": []any{
			map[string]any{
				"question": goodQuestion,
				"header":   "Region",
				"options": []any{
					map[string]any{"label": "us-east-1", "description": "Virginia"},
					map[string]any{"label": "eu-west-1", "description": "Ireland"},
				},
			},
			map[string]any{"question": badQuestion, "header": "Tier", "options": []any{"basic", "ha"}},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status != tool.StatusError {
		t.Fatalf("accepted: %q", result.Message)
	}
	if !strings.Contains(result.Message, badQuestion) {
		t.Errorf("message = %q, want it to name the offending question %q", result.Message, badQuestion)
	}
	if strings.Contains(result.Message, goodQuestion) {
		t.Errorf("message = %q, must not name %q — that question was fine", result.Message, goodQuestion)
	}
}

func TestAskUserQuestionNullOptionsStringRendersTextInput(t *testing.T) {
	ask := &AskUserQuestionTool{}
	var surface json.RawMessage
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, s json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			surface = s
			return &a2a.UserInputResponseData{Status: "answered", Data: map[string]any{}}
		},
	}
	result, err := ask.Execute(t.Context(), map[string]any{
		"questions": []any{
			map[string]any{
				"question":    "Pick?",
				"header":      "Pick",
				"options":     "null",
				"placeholder": "type it",
			},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if result.Status == tool.StatusError {
		t.Fatalf("a text-only question was refused: %q", result.Message)
	}
	if surface == nil {
		t.Fatal("WaitForUserInput was not invoked; nothing was shown to the user")
	}

	var parsed struct {
		Components []map[string]any `json:"components"`
	}
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("surface is not valid JSON: %v", err)
	}
	var placeholders []string
	for _, cm := range parsed.Components {
		if cm["component"] != "TextField" {
			continue
		}
		if ph, ok := cm["placeholder"].(map[string]any); ok {
			s, _ := ph["literalString"].(string)
			placeholders = append(placeholders, s)
		}
	}
	if !slices.Contains(placeholders, "type it") {
		t.Errorf("TextField placeholders = %v, want the question's own %q", placeholders, "type it")
	}
}

func TestAskUserQuestionScalarOptionsAreRefusedAsMalformed(t *testing.T) {
	const badQuestion = "Approve the rollout?"

	cases := []struct {
		name    string
		options any
	}{
		{name: "bare string", options: "yes-no"},
		{name: "number", options: 5},
		{name: "boolean", options: true},
		{name: "JSON scalar as a string", options: "5"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ask := &AskUserQuestionTool{}
			called := false
			toolCtx := &tool.Context{
				WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
					called = true
					return &a2a.UserInputResponseData{Status: "answered"}
				},
			}
			result, err := ask.Execute(t.Context(), map[string]any{
				"questions": []any{
					map[string]any{"question": badQuestion, "header": "Rollout", "options": tc.options},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status != tool.StatusError {
				t.Fatalf("accepted: %q", result.Message)
			}
			if !strings.Contains(result.Message, "none of them are objects") {
				t.Errorf("message = %q, want the malformed-options diagnosis", result.Message)
			}
			if strings.Contains(result.Message, "every question in this call is free-text") {
				t.Errorf("message = %q, diagnoses an open-ended call — the options merely "+
					"arrived in the wrong shape", result.Message)
			}
			if called {
				t.Error("WaitForUserInput was invoked; the guard must short-circuit")
			}
		})
	}

	t.Run("alongside a well-formed question", func(t *testing.T) {
		const goodQuestion = "Which region hosts the database?"
		ask := &AskUserQuestionTool{}
		called := false
		toolCtx := &tool.Context{
			WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
				called = true
				return &a2a.UserInputResponseData{Status: "answered"}
			},
		}
		result, err := ask.Execute(t.Context(), map[string]any{
			"questions": []any{
				map[string]any{
					"question": goodQuestion,
					"header":   "Region",
					"options": []any{
						map[string]any{"label": "us-east-1", "description": "Virginia"},
					},
				},
				map[string]any{"question": badQuestion, "header": "Rollout", "options": "yes-no"},
			},
		}, toolCtx)
		if err != nil {
			t.Fatalf("Execute returned Go error: %v", err)
		}
		if result.Status != tool.StatusError {
			t.Fatalf("accepted: %q — the malformed question would render as a blank text box",
				result.Message)
		}
		if !strings.Contains(result.Message, badQuestion) {
			t.Errorf("message = %q, want it to name the offending question %q", result.Message, badQuestion)
		}
		if called {
			t.Error("WaitForUserInput was invoked; the guard must short-circuit")
		}
	})
}

func TestAskUserQuestionEmptyOptionsValuesStayTextOnly(t *testing.T) {
	cases := []struct {
		name    string
		options any
	}{
		{name: "empty array", options: []any{}},
		{name: "empty array as a JSON string", options: "[]"},
		{name: "JSON null as a string", options: "null"},
		{name: "explicit nil", options: nil},
		{name: "empty string", options: ""},
		{name: "whitespace-only string", options: "  \n\t "},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			ask := &AskUserQuestionTool{}
			called := false
			toolCtx := &tool.Context{
				WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
					called = true
					return &a2a.UserInputResponseData{Status: "answered", Data: map[string]any{}}
				},
			}
			result, err := ask.Execute(t.Context(), map[string]any{
				"questions": []any{
					map[string]any{
						"question":    "Host URI",
						"header":      "Host",
						"options":     tc.options,
						"placeholder": "postgres://…",
					},
				},
			}, toolCtx)
			if err != nil {
				t.Fatalf("Execute returned Go error: %v", err)
			}
			if result.Status == tool.StatusError {
				t.Fatalf("a text-only question was refused: %q", result.Message)
			}
			if !called {
				t.Error("WaitForUserInput was not invoked; nothing was shown to the user")
			}
		})
	}
}

func TestAskUserQuestionSingleMultiChoiceReachesCallback(t *testing.T) {
	ask := &AskUserQuestionTool{}
	called := false
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			called = true
			return &a2a.UserInputResponseData{Status: "answered", Data: map[string]any{}}
		},
	}
	result, err := ask.Execute(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which auth method?",
				"header":   "Auth",
				"options": []any{
					map[string]any{"label": "OAuth", "description": "OAuth 2.0"},
					map[string]any{"label": "API key", "description": "Static API key"},
				},
			},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if !called {
		t.Fatal("WaitForUserInput was not invoked; single multi-choice question should pass the guard")
	}
	if result.Status == tool.StatusError {
		t.Errorf("unexpected error status: %q", result.Message)
	}
}

func TestAskUserQuestionMixedPayloadReachesCallback(t *testing.T) {
	ask := &AskUserQuestionTool{}
	called := false
	toolCtx := &tool.Context{
		WaitForUserInput: func(_ context.Context, _, _ string, _ json.RawMessage, _ time.Duration) *a2a.UserInputResponseData {
			called = true
			return &a2a.UserInputResponseData{Status: "answered", Data: map[string]any{}}
		},
	}
	result, err := ask.Execute(context.Background(), map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which auth method?",
				"header":   "Auth",
				"options": []any{
					map[string]any{"label": "OAuth", "description": "OAuth 2.0"},
					map[string]any{"label": "API key", "description": "Static API key"},
				},
			},
			map[string]any{"question": "Any notes?", "header": "Notes"},
		},
	}, toolCtx)
	if err != nil {
		t.Fatalf("Execute returned Go error: %v", err)
	}
	if !called {
		t.Fatal("WaitForUserInput was not invoked; mixed payload should pass the guard")
	}
	if result.Status == tool.StatusError {
		t.Errorf("unexpected error status: %q", result.Message)
	}
}

func TestAllQuestionsAreTextOnly(t *testing.T) {
	tests := []struct {
		name      string
		questions []any
		want      bool
	}{
		{
			name:      "no questions",
			questions: []any{},
			want:      true,
		},
		{
			name: "single text-only",
			questions: []any{
				map[string]any{"question": "Name?"},
			},
			want: true,
		},
		{
			name: "single with empty options",
			questions: []any{
				map[string]any{"question": "Name?", "options": []any{}},
			},
			want: true,
		},
		{
			name: "single with options",
			questions: []any{
				map[string]any{
					"question": "Pick?",
					"options":  []any{map[string]any{"label": "A"}},
				},
			},
			want: false,
		},
		{
			name: "mixed text-only and with-options",
			questions: []any{
				map[string]any{"question": "Name?"},
				map[string]any{
					"question": "Pick?",
					"options":  []any{map[string]any{"label": "A"}},
				},
			},
			want: false,
		},
		{
			name: "non-map element before an options-bearing question",
			questions: []any{
				42,
				map[string]any{
					"question": "Pick?",
					"options":  []any{map[string]any{"label": "A"}},
				},
			},
			want: false,
		},
		{
			name:      "empty after filtering",
			questions: []any{"junk"},
			want:      true,
		},
		{
			name: "options present but none renderable",
			questions: []any{
				map[string]any{"question": "Name?", "options": []any{"a", "b"}},
			},
			want: true,
		},
		{
			name: "one renderable option among junk",
			questions: []any{
				map[string]any{"question": "Pick?", "options": []any{"a", map[string]any{"label": "A"}}},
			},
			want: false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := allQuestionsAreTextOnly(questionMaps(tc.questions)); got != tc.want {
				t.Errorf("allQuestionsAreTextOnly = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestCoerceToSlice(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want int
	}{
		{"nil", nil, -1},
		{"slice", []any{"a", "b"}, 2},
		{"single map", map[string]any{"q": "test"}, 1},
		{"json array string", `[{"q":"a"},{"q":"b"}]`, 2},
		{"json object string", `{"q":"test"}`, 1},
		{"invalid string", "not json", -1},
		{"number", 42, -1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := coerceToSlice(tc.in)
			if tc.want == -1 {
				if got != nil {
					t.Errorf("got %v, want nil", got)
				}
			} else if len(got) != tc.want {
				t.Errorf("len = %d, want %d", len(got), tc.want)
			}
		})
	}
}

func TestSanitizeFilename(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"normal.txt", "normal.txt"},
		{"path/to/file.txt", "file.txt"},
		{"file with spaces.txt", "file_with_spaces.txt"},
	}
	for _, tc := range tests {
		if got := sanitizeFilename(tc.input); got != tc.want {
			t.Errorf("sanitizeFilename(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestLayeredIntConfig_PerCallValues(t *testing.T) {
	tests := []struct {
		name   string
		config map[string]any
		key    string
		def    int
		want   int
	}{
		{"nil config", nil, "k", 100, 100},
		{"missing key", map[string]any{}, "k", 100, 100},
		{"float64 value", map[string]any{"k": float64(42)}, "k", 100, 42},
		{"int value", map[string]any{"k": 42}, "k", 100, 42},
		{"zero int uses default", map[string]any{"k": 0}, "k", 100, 100},
		{"zero float64 uses default", map[string]any{"k": float64(0)}, "k", 100, 100},
		{"negative float64 uses default", map[string]any{"k": float64(-5)}, "k", 100, 100},
		{"out-of-range float64 uses default", map[string]any{"k": float64(1e19)}, "k", 100, 100},
		{"float64 at 2^63 uses default", map[string]any{"k": float64(math.MaxInt)}, "k", 100, 100},
		{"fractional float64 below one uses default", map[string]any{"k": 0.5}, "k", 100, 100},
		{"non-integral float64 uses default", map[string]any{"k": 1.5}, "k", 100, 100},
		{"large integral float64 is honoured", map[string]any{"k": float64(5e9)}, "k", 100, 5_000_000_000},
		{"negative int uses default", map[string]any{"k": -3}, "k", 100, 100},
		{"string value uses default", map[string]any{"k": "nope"}, "k", 100, 100},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			toolCtx := &tool.Context{ToolConfig: tc.config}
			if got := layeredIntConfig(toolCtx, nil, tc.key, tc.def); got != tc.want {
				t.Errorf("got %d, want %d", got, tc.want)
			}
		})
	}
}

func TestRegisterAll(t *testing.T) {
	ts := tool.NewSet()
	RegisterAll(ts)
	wantNames := []string{"get_current_time", "artifact_search_and_replace_regex", "ask_user_question"}
	for _, want := range wantNames {
		if ts.GetTool(want) == nil {
			t.Errorf("RegisterAll did not register tool %q", want)
		}
	}
	if ts.GetTool("web_request_strict") != nil {
		t.Error("web_request_strict must not be registered as a first-class builtin (see pkg/tools/register.go)")
	}
}

func TestAppendToArtifact_CreateFiresArtifactSavedHookAndAppendDoesNot(t *testing.T) {
	ctx := context.Background()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	toolCtx.ToolCallID = "call-1"
	var saved []tool.ArtifactSavedInfo
	toolCtx.NotifyArtifactSaved = func(_ context.Context, info tool.ArtifactSavedInfo) {
		saved = append(saved, info)
	}

	for _, chunk := range []string{"abc", "def"} {
		result, err := (&AppendToArtifactTool{}).Execute(ctx, map[string]any{
			"filename": "new.txt", "content_chunk": chunk, "mime_type": "text/plain",
		}, toolCtx)
		if err != nil || result.Status != tool.StatusSuccess {
			t.Fatalf("append %q: %v %v", chunk, err, result)
		}
	}

	want := []tool.ArtifactSavedInfo{{Filename: "new.txt", Version: 0, MIMEType: "text/plain", SizeBytes: 3, ToolCallID: "call-1"}}
	if diff := cmp.Diff(want, saved); diff != "" {
		t.Errorf("artifact saved notifications mismatch (-want +got):\n%s", diff)
	}
}

func TestAppendToArtifact_PinnedVersionOfMissingArtifactIsNotCreated(t *testing.T) {
	ctx := context.Background()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)

	result, err := (&AppendToArtifactTool{}).Execute(ctx, map[string]any{
		"filename": "missing.txt:3", "content_chunk": "x", "mime_type": "text/plain",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusError || result.ErrorCode != taxonomy.ErrorCodeArtifactNotFound {
		t.Fatalf("got %s %q code %q, want a not-found error", result.Status, result.Message, result.ErrorCode)
	}
	loc := toolCtx.ArtifactService.Resolve("test-agent", "user-1", "session-1", "missing.txt")
	if content, _, _ := svc.Load(ctx, loc, nil); content != nil {
		t.Errorf("artifact was created with %q, want none", content)
	}
}

func TestAppendToArtifact_CreateSucceedsWhenVersionListingFails(t *testing.T) {
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	svc.failListVersions = map[string]bool{"new.txt": true}

	result, err := (&AppendToArtifactTool{}).Execute(context.Background(), map[string]any{
		"filename": "new.txt", "content_chunk": "abc", "mime_type": "text/plain",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess || !strings.Contains(result.Message, "first version") {
		t.Errorf("got %s %q, want a created artifact", result.Status, result.Message)
	}
}

func TestAppendToArtifact_EmptyExistingArtifactIsAppendedNotCreated(t *testing.T) {
	ctx := context.Background()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(ctx, svc, toolCtx, "empty.txt", "", "text/plain")

	result, err := (&AppendToArtifactTool{}).Execute(ctx, map[string]any{
		"filename": "empty.txt", "content_chunk": "abc", "mime_type": "text/plain",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	if result.Status != tool.StatusSuccess || !strings.Contains(result.Message, "Chunk appended") {
		t.Fatalf("got %s %q, want a normal append", result.Status, result.Message)
	}
	if result.Data["new_version"] != 1 || result.Data["appended_from_version"] != 0 {
		t.Errorf("data = %v, want version 1 appended from version 0", result.Data)
	}
}

func TestAppendToArtifact_CreateMessageListsExistingFilesForTheModel(t *testing.T) {
	ctx := context.Background()
	svc := newMockArtifactService()
	toolCtx := newTestToolCtx(svc)
	saveTestArtifact(ctx, svc, toolCtx, "report.csv", "a", "text/csv")
	saveTestArtifact(ctx, svc, toolCtx, "notes.md", "b", "text/markdown")

	result, err := (&AppendToArtifactTool{}).Execute(ctx, map[string]any{
		"filename": "reprot.csv", "content_chunk": "abc", "mime_type": "text/csv",
	}, toolCtx)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"did not exist", "report.csv", "notes.md", "name may be wrong"} {
		if !strings.Contains(result.Message, want) {
			t.Errorf("message %q missing %q", result.Message, want)
		}
	}
}
