// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

type markdownToolRun struct {
	svc      *artifact.ScopedService
	notified []tool.ArtifactSavedInfo
	toolCtx  *tool.Context
}

func newMarkdownToolRun(raw string) *markdownToolRun {
	run := &markdownToolRun{svc: &artifact.ScopedService{Inner: memory.New(), Scope: "namespace", Namespace: "ns"}}
	run.toolCtx = &tool.Context{
		ArtifactService: run.svc, AgentName: "a", UserID: "u", SessionID: "s",
		ControlQuerier: &jsonQuerier{raw: json.RawMessage(raw)},
		NotifyArtifactSaved: func(_ context.Context, info tool.ArtifactSavedInfo) {
			run.notified = append(run.notified, info)
		},
	}
	return run
}

func (r *markdownToolRun) load(t *testing.T, filename string) string {
	t.Helper()
	content, mime, err := r.svc.Inner.Load(context.Background(), r.svc.Resolve("a", "u", "s", filename), nil)
	if err != nil {
		t.Fatalf("load %s: %v", filename, err)
	}
	if mime != markdownMIMEType {
		t.Errorf("%s mime = %q, want %q (markdown auto-expands in chat; HTML does not)", filename, mime, markdownMIMEType)
	}
	return string(content)
}

func (r *markdownToolRun) saved(t *testing.T, filename string) bool {
	t.Helper()
	versions, err := r.svc.Inner.ListVersions(context.Background(), r.svc.Resolve("a", "u", "s", filename))
	return err == nil && len(versions) > 0
}

func assertMarkdownDelivery(t *testing.T, run *markdownToolRun, res *tool.Result, mdName, jsonName string, jsonDisposition tool.DataDisposition, wantJSON []byte) string {
	t.Helper()
	if res.Status != tool.StatusSuccess {
		t.Fatalf("status = %q (%s), want success", res.Status, res.Message)
	}
	if len(run.notified) != 1 || run.notified[0].Filename != mdName {
		t.Fatalf("artifact_saved notifications = %+v, want exactly one for %s", run.notified, mdName)
	}
	if want := fmt.Sprintf("«artifact_content:%s:%d»", mdName, run.notified[0].Version); res.Message != want {
		t.Errorf("message = %q, want %q", res.Message, want)
	}
	if tags := run.notified[0].Tags; len(tags) != 1 || tags[0] != a2a.ArtifactTagWorking {
		t.Errorf("artifact_saved tags = %v, want [__working]", tags)
	}
	meta := artifact.LoadCompanionMetadata(context.Background(), run.svc, "a", "u", "s", mdName, nil)
	if tags, _ := meta["tags"].([]any); len(tags) != 1 || tags[0] != a2a.ArtifactTagWorking {
		t.Errorf("%s stored tags = %v, want [__working]", mdName, meta["tags"])
	}
	if len(res.DataObjects) != 1 {
		t.Fatalf("data objects = %+v, want only the JSON for the LLM", res.DataObjects)
	}
	data := res.DataObjects[0]
	if data.Name != jsonName || data.Disposition != jsonDisposition || len(data.Tags) != 1 || data.Tags[0] != a2a.ArtifactTagWorking {
		t.Errorf("JSON object = %+v, want %s %s tagged __working", data, jsonDisposition, jsonName)
	}
	if string(data.Content) != string(wantJSON) {
		t.Errorf("JSON content = %s, want %s", data.Content, wantJSON)
	}
	if run.saved(t, jsonName) {
		t.Errorf("%s was saved by the tool; only the framework may persist it, under __working", jsonName)
	}
	md := run.load(t, mdName)
	if strings.Contains(md, "{{") || strings.Contains(md, "{%") {
		t.Errorf("unrendered Liquid in %s:\n%s", mdName, md)
	}
	return md
}

func TestMdSafe_CannotBreakMarkdownStructure(t *testing.T) {
	got := mdSafe("evil```name|cell\nnext`span«artifact_content:x.md» a\\|b")
	for _, bad := range []string{"`", "\n", "«", "»"} {
		if strings.Contains(got, bad) {
			t.Errorf("mdSafe output %q still contains %q", got, bad)
		}
	}
	for i := range len(got) {
		if got[i] != '|' {
			continue
		}
		backslashes := 0
		for j := i - 1; j >= 0 && got[j] == '\\'; j-- {
			backslashes++
		}
		if backslashes%2 == 0 {
			t.Errorf("mdSafe output %q has a live pipe at %d", got, i)
		}
	}
}

func TestActivityTools_FailuresAreToolErrors(t *testing.T) {
	cases := []struct {
		name, raw string
		noStore   bool
		execute   func(*markdownToolRun) (*tool.Result, error)
	}{
		{"explain/unparseable", "not json", false, explainQ},
		{"explain/save fails", sampleTrace, true, explainQ},
		{"list/unparseable", "not json", false, listDay},
		{"list/save fails", failedPage(1, 1, 20, 1, 3, `{"id":"t1","status":"failed","startDate":"2026-08-01T14:23:45Z"}`), true, listDay},
		{"summarize_feedback/control failure", "{}", false, controlFails(NewSummarizeFeedbackTool())},
		{"list_feedback/control failure", "{}", false, controlFails(NewListFeedbackTool())},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			run := newMarkdownToolRun(tc.raw)
			if tc.noStore {
				run.toolCtx.ArtifactService = nil
			}
			res, err := tc.execute(run)
			if err != nil {
				t.Fatalf("Execute returned a Go error: %v", err)
			}
			if res.Status != tool.StatusError || len(res.DataObjects) != 0 || len(run.notified) != 0 || strings.Contains(res.Message, "«") {
				t.Errorf("result = %+v, notified = %d; want a tool error with no embed, data or notification", res, len(run.notified))
			}
		})
	}
}

func controlFails(tl tool.BuiltinTool) func(*markdownToolRun) (*tool.Result, error) {
	return func(run *markdownToolRun) (*tool.Result, error) {
		run.toolCtx.ControlQuerier = &failingQuerier{err: errTest}
		return tl.Execute(context.Background(), map[string]any{"userId": "u"}, run.toolCtx)
	}
}

func explainQ(run *markdownToolRun) (*tool.Result, error) {
	return NewExplainTaskTool().Execute(context.Background(), map[string]any{"taskId": "q"}, run.toolCtx)
}

func listDay(run *markdownToolRun) (*tool.Result, error) {
	return NewListFailedTasksTool().Execute(context.Background(), map[string]any{"window": "24h"}, run.toolCtx)
}

func TestActivityTools_UntrustedTextStaysInCode(t *testing.T) {
	const hostile = "![x](https://evil.example/p.png) [a](b) <img src=x> # h"
	trace := `{"queriedTaskId":"q",
	  "root":{"id":"q","name":"` + hostile + `","type":"agent","status":"failed","statusEmoji":"❌","errorMessage":"` + hostile + `","startDate":"2026-08-01T14:23:45Z","depth":0,"isQueried":true,
	    "events":[{"createdAt":"2026-08-01T14:23:45Z","direction":"in","kind":"x` + "`" + `y","summary":"` + hostile + `"}]},
	  "tree":[{"id":"c","name":"` + hostile + `|z","type":"agent","status":"failed","statusEmoji":"❌","parentTaskId":"q","startDate":"2026-08-01T14:23:46Z","depth":1}]}`
	run := newMarkdownToolRun(trace)
	res, err := explainQ(run)
	if err != nil || res.Status != tool.StatusSuccess {
		t.Fatalf("explain: res = %+v, err = %v", res, err)
	}
	md := run.load(t, run.notified[0].Filename)
	for _, want := range []string{
		"## ❌ `" + hostile + "` — Failed",
		"`x'y` `" + hostile + "`",
		"```text\n" + hostile + "\n```",
		"| `" + hostile + "\\|z` | `c` |",
	} {
		if !strings.Contains(md, want) {
			t.Errorf("explain report missing %q:\n%s", want, md)
		}
	}

	row := `{"id":"t1","name":"` + hostile + `","status":"failed","statusEmoji":"❌","startDate":"2026-08-01T14:23:45Z","userId":"u1"}`
	run = newMarkdownToolRun(failedPage(1, 1, 20, 1, 3, row))
	if res, err = listDay(run); err != nil || res.Status != tool.StatusSuccess {
		t.Fatalf("list: res = %+v, err = %v", res, err)
	}
	if md = run.load(t, run.notified[0].Filename); !strings.Contains(md, "| `"+hostile+"` |") {
		t.Errorf("list report renders the name outside a code span:\n%s", md)
	}
}

func TestCodeSafe_KeepsPipesInsideCodeBlock(t *testing.T) {
	const want = "billing|v2''' nextartifact_content:x.md"
	if got := codeSafe("billing|v2```\nnext«artifact_content:x.md»"); got != want {
		t.Errorf("codeSafe = %q, want %q", got, want)
	}
}

func TestSaveActivityMarkdown_SaveUnavailableIsError(t *testing.T) {
	notified := false
	toolCtx := &tool.Context{NotifyArtifactSaved: func(context.Context, tool.ArtifactSavedInfo) { notified = true }}
	if _, err := saveActivityMarkdown(context.Background(), toolCtx, "x.md", "body", "d"); err == nil {
		t.Fatal("err = nil without an artifact service")
	}
	if notified {
		t.Error("artifact_saved fired for an artifact that was never saved")
	}
}
