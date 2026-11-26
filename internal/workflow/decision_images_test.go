// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package workflow

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"hash/crc32"
	"image"
	"image/png"
	"io"
	"log/slog"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
	"github.com/SolaceProducts/solace-agent-mesh/internal/decision"
	"github.com/SolaceProducts/solace-agent-mesh/internal/samapp"
	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

const runSessionID = "test-ctx"

const escalateOnProviderError = "    default: fallback\n    on_provider_error: escalate\n"

func openAIImageWorkflow(images, decisionExtra string) string {
	doc := strings.Replace(routedDecisionWorkflow, inlineTypeSafeBlock,
		"decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n"+decisionExtra, 1)
	return strings.Replace(doc, "    route_on: department\n", "    images: "+images+"\n    route_on: department\n", 1)
}

func escalatingOpenAIImageWorkflow(images, decisionExtra string) string {
	return strings.Replace(openAIImageWorkflow(images, decisionExtra), "    default: fallback\n", escalateOnProviderError, 1)
}

func pngImage(t *testing.T, w, h int) []byte {
	t.Helper()
	var buf bytes.Buffer
	if err := png.Encode(&buf, image.NewRGBA(image.Rect(0, 0, w, h))); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func pngHeaderOnly(w, h uint32) []byte {
	var b bytes.Buffer
	b.Write([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a})
	ihdr := make([]byte, 13)
	binary.BigEndian.PutUint32(ihdr[0:], w)
	binary.BigEndian.PutUint32(ihdr[4:], h)
	ihdr[8] = 8
	ihdr[9] = 6
	var length [4]byte
	binary.BigEndian.PutUint32(length[:], uint32(len(ihdr)))
	crc := crc32.NewIEEE()
	crc.Write([]byte("IHDR"))
	crc.Write(ihdr)
	var c [4]byte
	binary.BigEndian.PutUint32(c[:], crc.Sum32())
	b.Write(length[:])
	b.WriteString("IHDR")
	b.Write(ihdr)
	b.Write(c[:])
	return b.Bytes()
}

func pngDataURL(data []byte) string {
	return "data:image/png;base64," + base64.StdEncoding.EncodeToString(data)
}

func withArtifacts(t *testing.T, files map[string][]byte) func(string, *stubServices) {
	return withArtifactStore(t, memory.New(), files)
}

func withArtifactStore(t *testing.T, inner artifact.Service, files map[string][]byte) func(string, *stubServices) {
	return func(_ string, svc *stubServices) {
		art := &artifact.ScopedService{Inner: inner, Scope: "namespace", Namespace: "ns"}
		for name, data := range files {
			loc := art.Resolve("wf", "", runSessionID, name)
			if _, err := art.Inner.Save(t.Context(), loc, data, "application/octet-stream"); err != nil {
				t.Fatalf("save %s: %v", name, err)
			}
		}
		svc.art = art
	}
}

type instrumentedStore struct {
	artifact.Service
	loadDelay        time.Duration
	finishAtDeadline bool
	noMetadata       bool
	loads            atomic.Int32
}

func (s *instrumentedStore) GetVersionMetadata(ctx context.Context, loc artifact.Locator, version *int) (*artifact.VersionMetadata, error) {
	if s.noMetadata {
		return nil, errors.New("this store keeps no metadata")
	}
	return s.Service.GetVersionMetadata(ctx, loc, version)
}

func (s *instrumentedStore) Load(ctx context.Context, loc artifact.Locator, version *int) ([]byte, string, error) {
	s.loads.Add(1)
	if s.finishAtDeadline {
		<-ctx.Done()
		return s.Service.Load(context.WithoutCancel(ctx), loc, version)
	}
	if s.loadDelay > 0 {
		select {
		case <-ctx.Done():
			return nil, "", ctx.Err()
		case <-time.After(s.loadDelay):
		}
	}
	return s.Service.Load(ctx, loc, version)
}

type bodyRecorder struct {
	mu     sync.Mutex
	bodies []string
}

func (r *bodyRecorder) RoundTrip(req *http.Request) (*http.Response, error) {
	raw, err := io.ReadAll(req.Body)
	if closeErr := req.Body.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return nil, err
	}
	req.Body = io.NopCloser(bytes.NewReader(raw))
	r.mu.Lock()
	r.bodies = append(r.bodies, string(raw))
	r.mu.Unlock()
	return http.DefaultTransport.RoundTrip(req)
}

func (r *bodyRecorder) recorded() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.bodies)
}

func withRecordedBodies(rec *bodyRecorder) func(string, *stubServices) {
	return func(_ string, svc *stubServices) { svc.httpClient = &http.Client{Transport: rec} }
}

func withWorkflowInput(run *decisionRun, input map[string]any) {
	input["ticket"] = "charged twice"
	run.e.state.NodeOutputs["workflow_input"] = map[string]any{"output": input}
}

func openAIImageURLs(t *testing.T, body map[string]any) []string {
	t.Helper()
	messages, ok := body["input"].([]any)
	if !ok {
		return nil
	}
	if len(messages) != 1 {
		t.Fatalf("input has %d messages, want one", len(messages))
	}
	content, _ := messages[0].(map[string]any)["content"].([]any)
	var urls []string
	for _, part := range content {
		if p, _ := part.(map[string]any); p["type"] == "input_image" {
			url, _ := p["image_url"].(string)
			urls = append(urls, url)
		}
	}
	return urls
}

func (f *fakeJev) sentImageURLs(t *testing.T) []string {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	return openAIImageURLs(t, f.lastBody)
}

func (r *decisionRun) assertNoProviderCall(t *testing.T) {
	t.Helper()
	if calls := r.jev.calls.Load(); calls != 0 {
		t.Errorf("provider called %d times, want none", calls)
	}
}

func (r *decisionRun) assertEscalatedOn(t *testing.T, kind decision.FailureKind) {
	t.Helper()
	r.assertBranches(t, "fallback", "billing", "tech")
	if out := r.output(t, "triage"); out["escalation_reason"] != escalationProviderError || out["provider_error"] != string(kind) {
		t.Errorf("triage output = %v, want a provider_error escalation of kind %q", out, kind)
	}
}

func assertSentImageEntry(t *testing.T, entry any, name string, size int, version *int) {
	t.Helper()
	sent, _ := entry.(map[string]any)
	want := []string{"bytes", "mime_type", "name"}
	if version != nil {
		want = append(want, "version")
	}
	if keys := slices.Sorted(maps.Keys(sent)); !slices.Equal(keys, want) {
		t.Errorf("output image keys = %v, want %v", keys, want)
	}
	if sent["name"] != name || sent["mime_type"] != "image/png" || sent["bytes"] != size {
		t.Errorf("output image = %v, want %s, image/png, %d bytes", sent, name, size)
	}
	if version != nil && sent["version"] != *version {
		t.Errorf("output image version = %v, want %d", sent["version"], *version)
	}
}

func TestDecisionNodeSendsItsImagesToOpenAI(t *testing.T) {
	photo := pngImage(t, 4, 4)
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, openAIImageWorkflow(`["{{workflow.input.photo}}"]`, ""), jev, true,
		withArtifacts(t, map[string][]byte{"photo.png": photo}))
	withWorkflowInput(run, map[string]any{"photo": "photo.png"})
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	if urls := jev.sentImageURLs(t); len(urls) != 1 || urls[0] != pngDataURL(photo) {
		t.Fatalf("image parts = %v, want the PNG as a data URL", urls)
	}
	out := run.output(t, "triage")
	images, _ := out["images"].([]any)
	if len(images) != 1 {
		t.Fatalf("output images = %v, want one entry", images)
	}
	assertSentImageEntry(t, images[0], "photo.png", len(photo), nil)
	recorded, err := json.Marshal(out)
	if err != nil {
		t.Fatalf("marshal output: %v", err)
	}
	if encoded := base64.StdEncoding.EncodeToString(photo); bytes.Contains(recorded, []byte(encoded)) {
		t.Error("the node output carries the image's bytes")
	}
}

func TestDecisionNodeExpandsAListOfImageReferences(t *testing.T) {
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	files := map[string][]byte{"front.png": pngImage(t, 2, 2), "back.png": pngImage(t, 3, 3), "receipt.png": pngImage(t, 5, 5)}
	run := newDecisionRun(t, openAIImageWorkflow(`["{{workflow.input.photos}}", "artifact://receipt.png:0"]`, ""), jev, true,
		withArtifacts(t, files))
	withWorkflowInput(run, map[string]any{"photos": []any{"front.png", "back.png:0"}})
	run.execute(t)

	if urls := jev.sentImageURLs(t); len(urls) != 3 {
		t.Fatalf("sent %d images, want 3", len(urls))
	}
	images, _ := run.output(t, "triage")["images"].([]any)
	if len(images) != 3 {
		t.Fatalf("output images = %v, want three entries in order", images)
	}
	pinned := 0
	assertSentImageEntry(t, images[0], "front.png", len(files["front.png"]), nil)
	assertSentImageEntry(t, images[1], "back.png", len(files["back.png"]), &pinned)
	assertSentImageEntry(t, images[2], "receipt.png", len(files["receipt.png"]), &pinned)
}

func TestDecisionNodeSkipsAnImageReferenceThatResolvesToNothing(t *testing.T) {
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, openAIImageWorkflow(`["{{workflow.input.photo}}"]`, ""), jev, true)
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	if !jev.sentOpenAIShape() {
		t.Errorf("request = %v, want the plain text input", jev.lastBody)
	}
	if _, has := run.output(t, "triage")["images"]; has {
		t.Error("output reports images for a call that sent none")
	}
}

func TestDecisionNodeImageFromANodeThatHasNotRunFailsTheNode(t *testing.T) {
	doc := escalatingOpenAIImageWorkflow(`["{{inspect.output.created_artifact}}"]`, "") +
		"  - {id: inspect, type: tool, tool_name: inspect_tool, depends_on: [triage]}\n"
	run := newDecisionRun(t, doc, &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, nil))
	run.execute(t)

	want := `images[0] reads node "inspect", which has not run and is not upstream of this node; add it to depends_on`
	if msg := run.errorMessage(); !strings.Contains(msg, want) {
		t.Fatalf("workflow error = %q, want %q", msg, want)
	}
	run.escalationNeverRan(t)
	run.assertNoProviderCall(t)
}

func (r *decisionRun) debugLogs() *lockedWriter {
	logs := &lockedWriter{w: &strings.Builder{}, mu: &sync.Mutex{}}
	r.e.logger = slog.New(slog.NewTextHandler(logs, &slog.HandlerOptions{Level: slog.LevelDebug}))
	return logs
}

func (l *lockedWriter) String() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.w.String()
}

func (l *lockedWriter) assertSays(t *testing.T, wants ...string) {
	t.Helper()
	got := l.String()
	for _, want := range wants {
		if !strings.Contains(got, want) {
			t.Errorf("the logs do not say %q:\n%s", want, got)
		}
	}
}

func TestDecisionNodeSkipsAnImageFromASkippedBranch(t *testing.T) {
	photo := pngImage(t, 4, 4)
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`["photo.png", "{{maybe.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [gate, maybe]\n    route_on: department\n", 1) +
		"  - {id: gate, type: tool, tool_name: gate_tool}\n" +
		"  - {id: maybe, type: tool, tool_name: maybe_tool, depends_on: [gate], when: \"'a' == 'b'\"}\n"
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, map[string][]byte{"photo.png": photo}))
	logs := run.debugLogs()
	run.execute(t)

	if msg := run.errorMessage(); msg != "" {
		t.Fatalf("workflow failed: %s", msg)
	}
	if urls := jev.sentImageURLs(t); len(urls) != 1 || urls[0] != pngDataURL(photo) {
		t.Errorf("sent %d images, want photo.png alone", len(urls))
	}
	if got := logs.String(); !strings.Contains(got, "mediaField=images mediaIndex=1 skippedNode=maybe") {
		t.Errorf("no DEBUG line names the skipped entry:\n%s", got)
	}
}

func TestDecisionNodeImageFromAFailedNodeFailsTheNode(t *testing.T) {
	doc := "fail_fast: false\n" + strings.Replace(escalatingOpenAIImageWorkflow(`["{{shot.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [mid, other]\n    route_on: department\n", 1) +
		"  - {id: shot, type: tool, tool_name: shot_tool}\n" +
		"  - {id: mid, type: tool, tool_name: mid_tool, depends_on: [shot]}\n" +
		"  - {id: other, type: tool, tool_name: other_tool}\n"
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, nil))
	run.tools["shot"].returnResult = tool.Error("screenshot service down")
	run.execute(t)

	want := `decision node "triage": images[0] reads node "shot", which failed`
	if msg := run.errorMessage(); !strings.Contains(msg, want) {
		t.Errorf("workflow error = %q, want %q", msg, want)
	}
	run.assertNoProviderCall(t)
	if run.tools["billing"].lastToolCtx != nil {
		t.Error("the billing branch ran on a decision made without its image")
	}
	run.escalationNeverRan(t)
}

func TestDecisionNodeImageFromABranchNoSwitchChoseFailsTheNode(t *testing.T) {
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`["{{shot.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [route, shot]\n    route_on: department\n", 1) + `
  - {id: probe, type: tool, tool_name: probe_tool}
  - id: route
    type: switch
    depends_on: [probe]
    cases: [{condition: "'{{probe.output.missing}}' == 'x'", node: shot}]
  - {id: shot, type: tool, tool_name: shot_tool, depends_on: [route]}
`
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, nil))
	run.execute(t)

	want := `images[0] reads node "shot", which was skipped because a when or switch condition could not be evaluated`
	if msg := run.errorMessage(); !strings.Contains(msg, want) {
		t.Errorf("workflow error = %q, want %q", msg, want)
	}
	run.assertNoProviderCall(t)
	run.escalationNeverRan(t)
}

func TestDecisionNodeImageFromATargetOfAnUnevaluatedSwitchCaseFails(t *testing.T) {
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`["{{shot.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [route, shot]\n    route_on: department\n", 1) + `
  - {id: probe, type: tool, tool_name: probe_tool}
  - id: route
    type: switch
    depends_on: [probe]
    cases: [{condition: "'{{probe.output.missing}}' == 'x'", node: shot}]
    default: other
  - {id: shot, type: tool, tool_name: shot_tool, depends_on: [route]}
  - {id: other, type: tool, tool_name: other_tool, depends_on: [route]}
`
	run := newDecisionRun(t, doc, &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, nil))
	run.execute(t)

	want := `images[0] reads node "shot", which was skipped because a when or switch condition could not be evaluated`
	if msg := run.errorMessage(); !strings.Contains(msg, want) {
		t.Errorf("workflow error = %q, want %q", msg, want)
	}
	run.assertNoProviderCall(t)
	if run.tools["other"].lastToolCtx == nil {
		t.Error("the switch's default branch did not run")
	}
}

func TestDecisionNodeImageFromANodeWhoseWhenCouldNotBeEvaluatedFails(t *testing.T) {
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`["{{shot.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [probe, shot]\n    route_on: department\n", 1) + `
  - {id: probe, type: tool, tool_name: probe_tool}
  - {id: shot, type: tool, tool_name: shot_tool, depends_on: [probe], when: "'{{probe.output.missing}}' == 'x'"}
`
	run := newDecisionRun(t, doc, &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, nil))
	run.execute(t)

	want := `images[0] reads node "shot", which was skipped because a when or switch condition could not be evaluated`
	if msg := run.errorMessage(); !strings.Contains(msg, want) {
		t.Errorf("workflow error = %q, want %q", msg, want)
	}
	run.assertNoProviderCall(t)
}

func TestDecisionNodeImageFromASkipWithNoRecordedCauseFails(t *testing.T) {
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`["{{shot.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [probe, shot]\n    route_on: department\n", 1) + `
  - {id: probe, type: tool, tool_name: probe_tool}
  - {id: shot, type: tool, tool_name: shot_tool}
`
	run := newDecisionRun(t, doc, &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, nil))
	run.e.state.SkippedNodes["shot"] = "condition not met"
	run.execute(t)

	want := `images[0] reads node "shot", which was skipped for a reason this run does not record or recognise`
	if msg := run.errorMessage(); !strings.Contains(msg, want) {
		t.Errorf("workflow error = %q, want %q", msg, want)
	}
	run.assertNoProviderCall(t)
}

func TestDecisionNodeImageFromAnUpstreamFailedNodeFails(t *testing.T) {
	doc := "fail_fast: false\n" + strings.Replace(escalatingOpenAIImageWorkflow(`["{{mid.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [mid, other]\n    route_on: department\n", 1) +
		"  - {id: shot, type: tool, tool_name: shot_tool}\n" +
		"  - {id: mid, type: tool, tool_name: mid_tool, depends_on: [shot]}\n" +
		"  - {id: other, type: tool, tool_name: other_tool}\n"
	run := newDecisionRun(t, doc, &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, nil))
	run.tools["shot"].returnResult = tool.Error("screenshot service down")
	run.execute(t)

	want := `images[0] reads node "mid", which did not run because a node upstream of it failed`
	if msg := run.errorMessage(); !strings.Contains(msg, want) {
		t.Errorf("workflow error = %q, want %q", msg, want)
	}
	run.assertNoProviderCall(t)
}

func TestDecisionNodeSkipsAnImageFromANodeThatCompletedWithoutOutput(t *testing.T) {
	photo := pngImage(t, 4, 4)
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`["photo.png", "{{shot.output.created_artifact}}", "{{shot.output.photo}}"]`, ""),
		"    route_on: department\n", "    depends_on: [shot]\n    route_on: department\n", 1) +
		"  - {id: shot, type: agent, agent_name: Shooter}\n"
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	brk := &capturingBroker{}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, map[string][]byte{"photo.png": photo}),
		func(_ string, svc *stubServices) { svc.brk = brk })
	run.e.execCtx.UserProps.A2AStatusTopic = "test/status"
	logs := run.debugLogs()
	run.start(t)
	req := waitForAgentRequests(t, brk, a2a.AgentRequestTopic(run.e.cfg.Namespace, "Shooter"), 1)[0]
	if err := run.e.Advance(run.e.execCtx.Ctx, &EventNodeComplete{NodeID: "shot", SubTaskID: req.ID, Output: nil}); err != nil {
		t.Fatalf("advance: %v", err)
	}
	run.wait(t)

	if msg := run.errorMessage(); msg != "" {
		t.Fatalf("workflow failed: %s", msg)
	}
	if urls := jev.sentImageURLs(t); len(urls) != 1 || urls[0] != pngDataURL(photo) {
		t.Errorf("sent %d images, want photo.png alone", len(urls))
	}
	const skipped = `msg="decision media entry reads a node that completed without output; skipped"`
	got := logs.String()
	if warns, debugs := strings.Count(got, "level=WARN "+skipped), strings.Count(got, "level=DEBUG "+skipped); warns != 1 || debugs != 1 {
		t.Errorf("WARN lines = %d, DEBUG lines = %d, want the first at WARN and the repeat at DEBUG:\n%s", warns, debugs, got)
	}
	logs.assertSays(t, "mediaField=images mediaIndex=1 emptyNode=shot")
}

func TestDecisionNodeImageFromATargetOfAnErroredSwitchCaseFails(t *testing.T) {
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`["{{shot.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [route, shot]\n    route_on: department\n", 1) + `
  - {id: probe, type: tool, tool_name: probe_tool}
  - id: route
    type: switch
    depends_on: [probe]
    cases: [{condition: "'{{workflow_input.output.ticket}}'", node: shot}]
    default: other
  - {id: shot, type: tool, tool_name: shot_tool, depends_on: [route]}
  - {id: other, type: tool, tool_name: other_tool, depends_on: [route]}
`
	run := newDecisionRun(t, doc, &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, nil))
	run.execute(t)

	want := `images[0] reads node "shot", which was skipped because a when or switch condition could not be evaluated`
	if msg := run.errorMessage(); !strings.Contains(msg, want) {
		t.Errorf("workflow error = %q, want %q", msg, want)
	}
	run.assertNoProviderCall(t)
}

func TestDecisionNodeSkipsAnImageFromABranchAnEarlierDecisionPruned(t *testing.T) {
	photo := pngImage(t, 4, 4)
	doc := openAIImageWorkflow(`["photo.png"]`, "") + `
  - id: review
    type: decision
    depends_on: [billing, tech]
    images: ["photo.png", "{{tech.output.created_artifact}}"]
    questions:
      department:
        type: choice
        instructions: "Which team owns it?"
        criteria: {billing: Payments, tech: Bugs, sales: Pricing}
      urgent: {type: boolean, instructions: "The ticket is urgent"}
`
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, map[string][]byte{"photo.png": photo}))
	run.execute(t)

	if msg := run.errorMessage(); msg != "" {
		t.Fatalf("workflow failed: %s", msg)
	}
	if out := run.output(t, "review"); out["answers"] == nil {
		t.Errorf("review output = %v, want its answers", out)
	}
	if urls := jev.sentImageURLs(t); len(urls) != 1 || urls[0] != pngDataURL(photo) {
		t.Errorf("review sent %d images, want photo.png alone", len(urls))
	}
}

func TestDecisionNodeLogsTheBlankListElementsItDrops(t *testing.T) {
	photo := pngImage(t, 4, 4)
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, openAIImageWorkflow(`["{{workflow.input.photos}}"]`, ""), jev, true,
		withArtifacts(t, map[string][]byte{"photo.png": photo}))
	withWorkflowInput(run, map[string]any{"photos": []any{"photo.png", nil, ""}})
	logs := run.debugLogs()
	run.execute(t)

	if urls := jev.sentImageURLs(t); len(urls) != 1 {
		t.Errorf("sent %d images, want photo.png alone", len(urls))
	}
	logs.assertSays(t, `msg="decision media entry dropped null or blank list elements"`, "mediaField=images mediaIndex=0 dropped=2")
}

func TestMapItemDecisionSkipsAnImageFromAPrunedBranch(t *testing.T) {
	pages := map[string][]byte{"p1.png": pngImage(t, 2, 2), "p2.png": pngImage(t, 3, 3)}
	doc := `
decision: {provider: openai, api_key: test-key, model: gpt-6-luna}
output_mapping: {pages: "{{each.output}}"}
nodes:
  - {id: gate, type: tool, tool_name: gate_tool}
  - {id: maybe, type: tool, tool_name: maybe_tool, depends_on: [gate], when: "'a' == 'b'"}
  - id: each
    type: map
    depends_on: [gate, maybe]
    items: "{{workflow_input.output.pages}}"
    node: classify
  - id: classify
    type: decision
    input: {page: "{{_map_item}}"}
    images: ["{{_map_item}}", "{{maybe.output.created_artifact}}"]
    questions:
      privileged: {type: boolean, instructions: "the page may be privileged"}
`
	jev := &fakeJev{respond: func(map[string]any) string {
		return `{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"privileged","probability":0.1}]}`
	}}
	rec := &bodyRecorder{}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, pages), withRecordedBodies(rec))
	run.e.state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{"pages": []any{"p1.png", "p2.png"}}}
	run.execute(t)

	if msg := run.errorMessage(); msg != "" {
		t.Fatalf("workflow failed: %s", msg)
	}
	sent := pagesSent(t, rec.recorded(), pages)
	slices.Sort(sent)
	if !slices.Equal(sent, []string{"p1.png", "p2.png"}) {
		t.Errorf("pages sent = %v, want each page once", sent)
	}
}

func TestDecisionNodeImageEntryEmbeddingATemplate(t *testing.T) {
	t.Run("an absent input inside the text is skipped", func(t *testing.T) {
		jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
		run := newDecisionRun(t, openAIImageWorkflow(`["artifact://{{workflow.input.photo}}"]`, ""), jev, true, withArtifacts(t, nil))
		run.execute(t)

		run.assertBranches(t, "billing", "tech", "fallback")
		if !jev.sentOpenAIShape() {
			t.Errorf("request = %v, want the plain text input", jev.lastBody)
		}
	})
	for name, photo := range map[string]any{"an empty string": "", "an empty list": []any{}} {
		t.Run(name+" inside the text is skipped", func(t *testing.T) {
			jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
			run := newDecisionRun(t, openAIImageWorkflow(`["artifact://{{workflow.input.photo}}"]`, ""), jev, true, withArtifacts(t, nil))
			withWorkflowInput(run, map[string]any{"photo": photo})
			run.execute(t)

			if msg := run.errorMessage(); msg != "" {
				t.Fatalf("workflow error = %q, want the entry skipped", msg)
			}
			run.assertBranches(t, "billing", "tech", "fallback")
			if !jev.sentOpenAIShape() {
				t.Errorf("request = %v, want the plain text input", jev.lastBody)
			}
		})
	}
	t.Run("a node that is not upstream fails the node", func(t *testing.T) {
		doc := escalatingOpenAIImageWorkflow(`["{{inspect.output.name}}:2"]`, "") +
			"  - {id: inspect, type: tool, tool_name: inspect_tool, depends_on: [triage]}\n"
		run := newDecisionRun(t, doc, &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, nil))
		run.execute(t)

		want := `images[0] reads node "inspect", which has not run and is not upstream of this node; add it to depends_on`
		if msg := run.errorMessage(); !strings.Contains(msg, want) {
			t.Errorf("workflow error = %q, want %q", msg, want)
		}
		run.assertNoProviderCall(t)
	})
}

func TestDecisionNodeLogsAnImageEntryThatResolvesToNothing(t *testing.T) {
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, openAIImageWorkflow(`["{{workflow.input.photo}}"]`, ""), jev, true)
	logs := run.debugLogs()
	run.execute(t)

	skipLine := regexp.MustCompile(`msg="decision media entry resolved to nothing; skipped" node=triage subTaskID=\S+ mediaField=images mediaIndex=0`)
	if got := logs.String(); !skipLine.MatchString(got) {
		t.Errorf("no DEBUG line names the skipped entry on the node-scoped logger:\n%s", got)
	}
}

func TestDecisionNodeImageTemplateErrorFailsTheNode(t *testing.T) {
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`["{{inspect.output.created_artifact}}"]`, ""),
		"    route_on: department\n", "    depends_on: [inspect]\n    route_on: department\n", 1) +
		"  - {id: inspect, type: tool, tool_name: inspect_tool}\n"
	run := newDecisionRun(t, doc, &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, nil))
	run.execute(t)

	if msg := run.errorMessage(); !strings.Contains(msg, `decision node "triage": images[0]`) || !strings.Contains(msg, "created_artifact") {
		t.Fatalf("workflow error = %q, want the images[0] template error naming created_artifact", msg)
	}
	run.escalationNeverRan(t)
	run.assertNoProviderCall(t)
}

func TestDecisionNodeSendsTheImageAToolNodeSavedInTheSameRun(t *testing.T) {
	for _, scope := range []string{artifact.ScopeNamespace, "agent"} {
		t.Run(scope, func(t *testing.T) {
			photo := pngImage(t, 6, 6)
			doc := strings.Replace(openAIImageWorkflow(`["{{inspect.output.created_artifact}}"]`, ""),
				"    route_on: department\n", "    depends_on: [inspect]\n    route_on: department\n", 1) +
				"  - {id: inspect, type: tool, tool_name: inspect_tool}\n"
			jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
			run := newDecisionRun(t, doc, jev, true, func(_ string, svc *stubServices) {
				svc.art = &artifact.ScopedService{Inner: memory.New(), Scope: scope, Namespace: "ns"}
			})
			run.tools["inspect"].returnResult = tool.OK("saved the photo",
				tool.WithDataObjects(tool.DataObject{Name: "inspected.png", Content: photo, MIMEType: "image/png"}))
			run.execute(t)

			if msg := run.errorMessage(); msg != "" {
				t.Fatalf("workflow failed: %s", msg)
			}
			if urls := jev.sentImageURLs(t); len(urls) != 1 || urls[0] != pngDataURL(photo) {
				t.Errorf("sent %d images, want the tool's PNG", len(urls))
			}
			images, _ := run.output(t, "triage")["images"].([]any)
			if len(images) != 1 {
				t.Fatalf("output images = %v, want the tool's one", images)
			}
			first := 0
			assertSentImageEntry(t, images[0], "inspected.png", len(photo), &first)
		})
	}
}

func TestDecisionNodeImageInputErrorsFailTheNode(t *testing.T) {
	cases := map[string]struct {
		files   map[string][]byte
		foreign map[string][]byte
		photo   string
		want    string
	}{
		"a missing artifact":       {photo: "nowhere.png", want: `image "nowhere.png" not found`},
		"a missing pinned version": {photo: "nowhere.png:3", want: `image "nowhere.png" version 3 not found`},
		"a non-image artifact":     {files: map[string][]byte{"notes.json": []byte(`{"a":1}`)}, photo: "notes.json", want: "not an image a decision accepts"},
		"a GIF":                    {files: map[string][]byte{"anim.gif": []byte("GIF89a\x01\x00\x01\x00")}, photo: "anim.gif", want: "image/gif"},
		"an oversized image that does not decode": {
			files: map[string][]byte{"corrupt.png": pngHeaderOnly(9000, 4000)},
			photo: "corrupt.png",
			want:  `image "corrupt.png" cannot be downscaled`,
		},
		"a PNG whose header cannot be read": {
			files: map[string][]byte{"truncated.png": pngHeaderOnly(100, 100)[:20]},
			photo: "truncated.png",
			want:  `artifact "truncated.png" is not a readable PNG, JPEG or WebP image`,
		},
		"another user's artifact": {
			foreign: map[string][]byte{"photo.png": pngImage(t, 4, 4)},
			photo:   "artifact://wf/someone-else/" + runSessionID + "/photo.png",
			want:    errForeignUserArtifact.Error(),
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
			store := memory.New()
			for file, data := range tc.foreign {
				loc := artifact.Locator{AppName: "ns", UserID: "someone-else", SessionID: runSessionID, Filename: file}
				if _, err := store.Save(t.Context(), loc, data, "image/png"); err != nil {
					t.Fatalf("seed %s: %v", file, err)
				}
			}
			run := newDecisionRun(t, escalatingOpenAIImageWorkflow(`["{{workflow.input.photo}}"]`, ""), jev, true,
				withArtifactStore(t, store, tc.files))
			withWorkflowInput(run, map[string]any{"photo": tc.photo})
			run.execute(t)

			if msg := run.errorMessage(); !strings.Contains(msg, tc.want) {
				t.Errorf("workflow error = %q, want it to say %q", msg, tc.want)
			}
			run.escalationNeverRan(t)
			run.assertNoProviderCall(t)
			if urls := jev.sentImageURLs(t); len(urls) != 0 {
				t.Errorf("sent %d images, want none", len(urls))
			}
		})
	}
}

func TestDecisionNodeImageCaps(t *testing.T) {
	a, b := pngImage(t, 4, 4), pngImage(t, 5, 5)
	files := map[string][]byte{"a.png": a, "b.png": b}
	both := `["{{workflow.input.a}}", "{{workflow.input.b}}"]`
	bothInput := map[string]any{"a": "a.png", "b": "b.png"}

	t.Run("more images than max_images escalates as input_too_large", func(t *testing.T) {
		run := newDecisionRun(t, escalatingOpenAIImageWorkflow(both, "  max_images: 1\n"),
			&fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, files))
		withWorkflowInput(run, bothInput)
		logs := run.debugLogs()
		run.execute(t)

		run.assertEscalatedOn(t, decision.FailureInputTooLarge)
		run.assertNoProviderCall(t)
		logs.assertSays(t, "failureKind=input_too_large phase=load", "mediaRefs=2 media=0")
	})
	t.Run("more bytes than max_image_bytes fails the node by default", func(t *testing.T) {
		budget := fmt.Sprintf("  max_image_bytes: %d\n", len(a)+len(b)-1)
		run := newDecisionRun(t, openAIImageWorkflow(both, budget),
			&fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifacts(t, files))
		withWorkflowInput(run, bothInput)
		run.execute(t)

		msg := run.errorMessage()
		if !strings.Contains(msg, string(decision.FailureInputTooLarge)+" failure while loading images") || !strings.Contains(msg, "after downscaling") {
			t.Fatalf("workflow error = %q, want an input_too_large failure over the images' total", msg)
		}
		run.escalationNeverRan(t)
		run.assertNoProviderCall(t)
	})
	t.Run("an image file over max_image_bytes escalates before it is read", func(t *testing.T) {
		store := &instrumentedStore{Service: memory.New()}
		budget := fmt.Sprintf("  max_image_bytes: %d\n", len(a)-1)
		run := newDecisionRun(t, escalatingOpenAIImageWorkflow(`["{{workflow.input.a}}"]`, budget),
			&fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifactStore(t, store, files))
		withWorkflowInput(run, bothInput)
		run.execute(t)

		run.assertEscalatedOn(t, decision.FailureInputTooLarge)
		run.assertNoProviderCall(t)
		if loads := store.loads.Load(); loads != 0 {
			t.Errorf("the store read the oversized image %d times, want none", loads)
		}
	})
	t.Run("an image file over max_image_bytes in a store without sizes is refused once read", func(t *testing.T) {
		store := &instrumentedStore{Service: memory.New(), noMetadata: true}
		budget := fmt.Sprintf("  max_image_bytes: %d\n", len(a)-1)
		run := newDecisionRun(t, openAIImageWorkflow(`["{{workflow.input.a}}"]`, budget),
			&fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifactStore(t, store, files))
		withWorkflowInput(run, bothInput)
		logs := run.debugLogs()
		run.execute(t)

		want := fmt.Sprintf(`image "a.png" is %d bytes; the limit is %d`, len(a), len(a)-1)
		if msg := run.errorMessage(); !strings.Contains(msg, want) {
			t.Fatalf("workflow error = %q, want %q", msg, want)
		}
		run.assertNoProviderCall(t)
		logs.assertSays(t, `msg="decision media metadata unavailable; size checked after load"`, "mediaKind=image file=a.png")
	})
}

func TestDecisionNodeImageTooLargeToDownscaleEscalates(t *testing.T) {
	run := newDecisionRun(t, escalatingOpenAIImageWorkflow(`["{{workflow.input.photo}}"]`, ""),
		&fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true,
		withArtifacts(t, map[string][]byte{"huge.png": pngHeaderOnly(30000, 30000)}))
	withWorkflowInput(run, map[string]any{"photo": "huge.png"})
	run.execute(t)

	run.assertEscalatedOn(t, decision.FailureInputTooLarge)
	run.assertNoProviderCall(t)
}

func TestDecisionNodeEscalatesWhenLoadingImagesOutlivesTheNodeBudget(t *testing.T) {
	store := &instrumentedStore{Service: memory.New(), loadDelay: 10 * time.Second}
	run := newDecisionRun(t, budgetedImageWorkflow(budgetTestTimeout, `["{{workflow.input.photo}}"]`),
		&fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true,
		withArtifactStore(t, store, map[string][]byte{"photo.png": pngImage(t, 4, 4)}))
	withWorkflowInput(run, map[string]any{"photo": "photo.png"})
	run.execute(t)

	run.assertEscalatedOn(t, decision.FailureTimeout)
	run.assertNoProviderCall(t)
}

const budgetTestTimeout = "1500ms"

func budgetedImageWorkflow(timeout, images string) string {
	return strings.Replace(escalatingOpenAIImageWorkflow(images, ""),
		escalateOnProviderError, escalateOnProviderError+"    timeout: "+timeout+"\n", 1)
}

func TestDecisionNodeImageLoadingAndTheProviderCallShareTheNodeBudget(t *testing.T) {
	store := &instrumentedStore{Service: memory.New(), loadDelay: 900 * time.Millisecond}
	jev := &fakeJev{delay: 900 * time.Millisecond, respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, budgetedImageWorkflow(budgetTestTimeout, `["{{workflow.input.photo}}"]`), jev, true,
		withArtifactStore(t, store, map[string][]byte{"photo.png": pngImage(t, 4, 4)}))
	withWorkflowInput(run, map[string]any{"photo": "photo.png"})
	logs := run.debugLogs()
	run.execute(t)

	run.assertEscalatedOn(t, decision.FailureTimeout)
	if got := logs.String(); !strings.Contains(got, "failureKind=timeout phase=provider") {
		t.Errorf("the escalation does not say the provider call timed out:\n%s", got)
	}
	if msg := run.errorMessage(); msg != "" {
		t.Errorf("the node timer fired: %s", msg)
	}
	if calls := jev.calls.Load(); calls != 1 {
		t.Errorf("provider called %d times, want the one call the budget cut short", calls)
	}
}

func TestLoadDecisionImagesChecksTheBudgetBeforeEachRead(t *testing.T) {
	store := &instrumentedStore{Service: memory.New()}
	run := newDecisionRun(t, openAIImageWorkflow(`["{{workflow.input.photo}}"]`, ""),
		&fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifactStore(t, store, nil))
	spent, cancel := context.WithDeadline(t.Context(), time.Now().Add(-time.Second))
	defer cancel()

	_, _, err := run.e.loadDecisionMedia(spent, run.e.logger, []mediaRef{{kind: decision.MediaImage, ref: "nowhere.png"}})
	if !errors.Is(err, context.DeadlineExceeded) || !strings.HasPrefix(err.Error(), "loading images: ") {
		t.Errorf("err = %v, want the budget's deadline, worded for images as before media input", err)
	}
	if loads := store.loads.Load(); loads != 0 {
		t.Errorf("the store was read %d times after the budget ran out, want none", loads)
	}
}

func TestDecisionNodeImageLoadingStopsWhenTheBudgetRunsOut(t *testing.T) {
	two := `["{{workflow.input.a}}", "{{workflow.input.b}}"]`
	cases := map[string]struct {
		timeout   string
		images    string
		files     map[string][]byte
		input     map[string]any
		slow      bool
		escalates bool
		want      string
	}{
		"a downscale after a slow read": {
			timeout: budgetTestTimeout, images: two, slow: true, escalates: true,
			files: map[string][]byte{"huge.png": pngHeaderOnly(30000, 30000)},
			input: map[string]any{"a": "huge.png"},
		},
		"a non-image read after the budget ran out": {
			timeout: budgetTestTimeout, images: two, slow: true,
			files: map[string][]byte{"notes.json": []byte(`{"a":1}`)},
			input: map[string]any{"a": "notes.json"},
			want:  "not an image a decision accepts",
		},
		"a missing image within the budget": {
			timeout: "30s", images: two,
			input: map[string]any{"a": "nowhere.png"},
			want:  `image "nowhere.png" not found`,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			store := &instrumentedStore{Service: memory.New(), finishAtDeadline: tc.slow}
			run := newDecisionRun(t, budgetedImageWorkflow(tc.timeout, tc.images),
				&fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}, true, withArtifactStore(t, store, tc.files))
			withWorkflowInput(run, tc.input)
			run.execute(t)

			run.assertNoProviderCall(t)
			if tc.escalates {
				run.assertEscalatedOn(t, decision.FailureTimeout)
				return
			}
			if msg := run.errorMessage(); !strings.Contains(msg, tc.want) {
				t.Errorf("workflow error = %q, want the input error %q", msg, tc.want)
			}
			run.escalationNeverRan(t)
		})
	}
}

func TestDecisionNodeDownscalesAnOversizedImage(t *testing.T) {
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	run := newDecisionRun(t, openAIImageWorkflow(`[wide.png]`, ""), jev, true,
		withArtifacts(t, map[string][]byte{"wide.png": pngImage(t, 9000, 10)}))
	run.execute(t)

	urls := jev.sentImageURLs(t)
	if len(urls) != 1 {
		t.Fatalf("sent %d images, want 1", len(urls))
	}
	data, err := base64.StdEncoding.DecodeString(strings.TrimPrefix(urls[0], "data:image/png;base64,"))
	if err != nil {
		t.Fatalf("image_url is not a PNG data URL: %v", err)
	}
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width > 8000 {
		t.Errorf("sent image %dx%d (err %v), want it downscaled to 8000px wide", cfg.Width, cfg.Height, err)
	}
}

func pagesSent(t *testing.T, bodies []string, pages map[string][]byte) []string {
	t.Helper()
	var sent []string
	for i, raw := range bodies {
		var named []string
		for name := range pages {
			if strings.Contains(raw, name) {
				named = append(named, name)
			}
		}
		if len(named) != 1 {
			t.Fatalf("request %d names pages %v, want exactly one", i, named)
		}
		if got := imageSentIn(t, i, raw, pages); got != named[0] {
			t.Errorf("request %d for %s carries the image of %q, not that page's own", i, named[0], got)
		}
		sent = append(sent, named[0])
	}
	return sent
}

func imageSentIn(t *testing.T, i int, raw string, files map[string][]byte) string {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal([]byte(raw), &body); err != nil {
		t.Fatalf("request %d is not JSON: %v", i, err)
	}
	urls := openAIImageURLs(t, body)
	if len(urls) != 1 {
		t.Errorf("request %d carries %d images, want one", i, len(urls))
		return ""
	}
	for name, data := range files {
		if urls[0] == pngDataURL(data) {
			return name
		}
	}
	return ""
}

func TestDecisionNodeRunsEachMapItemWithItsOwnImage(t *testing.T) {
	pages := map[string][]byte{"p1.png": pngImage(t, 2, 2), "p2.png": pngImage(t, 3, 3), "p3.png": pngImage(t, 4, 4)}
	for _, limit := range []int{1, 2} {
		t.Run(fmt.Sprintf("concurrency_limit %d", limit), func(t *testing.T) {
			doc := fmt.Sprintf(`
decision: {provider: openai, api_key: test-key, model: gpt-6-luna}
output_mapping: {pages: "{{each.output}}"}
nodes:
  - id: each
    type: map
    items: "{{workflow_input.output.pages}}"
    node: classify
    concurrency_limit: %d
  - id: classify
    type: decision
    input: {page: "{{_map_item}}"}
    images: ["{{_map_item}}"]
    questions:
      privileged: {type: boolean, instructions: "the page may be privileged"}
`, limit)
			jev := &fakeJev{respond: func(map[string]any) string {
				return `{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"privileged","probability":0.1}]}`
			}}
			rec := &bodyRecorder{}
			run := newDecisionRun(t, doc, jev, true, withArtifacts(t, pages), withRecordedBodies(rec))
			run.e.state.NodeOutputs["workflow_input"] = map[string]any{"output": map[string]any{"pages": []any{"p1.png", "p2.png", "p3.png"}}}
			run.execute(t)

			if msg := run.errorMessage(); msg != "" {
				t.Fatalf("workflow failed: %s", msg)
			}
			sent := pagesSent(t, rec.recorded(), pages)
			slices.Sort(sent)
			if !slices.Equal(sent, []string{"p1.png", "p2.png", "p3.png"}) {
				t.Errorf("pages sent = %v, want each page once", sent)
			}
		})
	}
}

func TestLoopBodyDecisionNodeSendsEachIterationsImage(t *testing.T) {
	doc := `
decision: {provider: openai, api_key: test-key, model: gpt-6-luna}
output_mapping: {r: "{{poll.output}}"}
nodes:
  - {id: poll, type: loop, node: check, condition: "{{check.output.answers.done.value}} == false", max_iterations: 5}
  - id: check
    type: decision
    input: {attempt: "{{_loop_iteration}}"}
    images: ["frame-{{_loop_iteration}}.png"]
    questions:
      done: {type: boolean, instructions: "The job has finished"}
`
	frames := map[string][]byte{}
	for i := range 6 {
		frames[fmt.Sprintf("frame-%d.png", i)] = pngImage(t, i+2, i+2)
	}
	jev := &fakeJev{}
	jev.respond = func(map[string]any) string {
		p := "0.1"
		if jev.calls.Load() >= 2 {
			p = "0.9"
		}
		return `{"model":"gpt-6-luna","answers":[{"type":"predicate","name":"done","probability":` + p + `}]}`
	}
	rec := &bodyRecorder{}
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, frames), withRecordedBodies(rec))
	run.execute(t)

	if msg := run.errorMessage(); msg != "" {
		t.Fatalf("workflow failed: %s", msg)
	}
	bodies := rec.recorded()
	if len(bodies) != 2 {
		t.Fatalf("calls = %d, want two iterations", len(bodies))
	}
	sent := []string{imageSentIn(t, 0, bodies[0], frames), imageSentIn(t, 1, bodies[1], frames)}
	if sent[0] == "" || sent[0] == sent[1] {
		t.Errorf("frames sent = %v, want a different frame on each iteration", sent)
	}
}

const textOnlyDecisionBlock = "decision:\n  api_key: test-key\n  model: acme/decider-4b\n"

func TestAliasedTypeSafeDecisionModelRefusesImagesAtRunTime(t *testing.T) {
	jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
	doc := strings.Replace(escalatingOpenAIImageWorkflow(`[a.png]`, ""),
		"decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", "decision:\n  model_alias: jev\n", 1)
	run := newDecisionRun(t, doc, jev, true,
		withRegistry(t, nil, "jev", func(url string) map[string]any {
			raw := registeredDecisionModel(url, "registry-key")
			raw["model"] = "acme/decider-4b"
			return raw
		}),
		withArtifacts(t, map[string][]byte{"a.png": pngImage(t, 2, 2)}))
	run.execute(t)

	msg := run.errorMessage()
	if !strings.Contains(msg, string(decision.FailureConfig)+" failure") || !strings.Contains(msg, "does not read images") {
		t.Fatalf("workflow error = %q, want a config failure saying the model does not read images", msg)
	}
	run.escalationNeverRan(t)
	run.assertNoProviderCall(t)
}

func TestAliasedOpenAIDecisionModelSendsImages(t *testing.T) {
	jev := &fakeJev{respond: openAIDepartmentAnswer("billing", 0.9)}
	doc := strings.Replace(openAIImageWorkflow(`[a.png]`, ""),
		"decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", "decision:\n  model_alias: luna\n", 1)
	run := newDecisionRun(t, doc, jev, true,
		withRegistry(t, nil, "luna", func(url string) map[string]any {
			raw := registeredOpenAIDecisionModel(url, "registry-key")
			raw[samapp.DecisionProviderKey] = decision.ProviderOpenAI
			return raw
		}),
		withArtifacts(t, map[string][]byte{"a.png": pngImage(t, 2, 2)}))
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	if urls := jev.sentImageURLs(t); len(urls) != 1 {
		t.Errorf("sent %d images, want 1", len(urls))
	}
}

func TestTypeSafeInlineBlockRefusesImagesAtLoad(t *testing.T) {
	doc := strings.Replace(openAIImageWorkflow(`[a.png]`, ""),
		"decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", textOnlyDecisionBlock, 1)
	cfg, err := parseYAMLWorkflow(t, doc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	err = checkDecisionMediaSupported(cfg.Decision, cfg.Nodes)
	if err == nil || !strings.Contains(err.Error(), `decision node "triage" sends images`) {
		t.Fatalf("err = %v, want the node named", err)
	}
	cfg.Decision.Provider = DecisionProviderOpenAI
	if openAIErr := checkDecisionMediaSupported(cfg.Decision, cfg.Nodes); openAIErr != nil {
		t.Errorf("openai: %v", openAIErr)
	}
	cfg.Decision.Provider, cfg.Decision.ModelAlias = DecisionProviderTypeSafe, "jev"
	if aliasErr := checkDecisionMediaSupported(cfg.Decision, cfg.Nodes); aliasErr != nil {
		t.Errorf("model_alias is checked at run time, got %v", aliasErr)
	}
}

func TestTypeSafeInlineBlockAcceptsImagesForAModelThatReadsThem(t *testing.T) {
	doc := strings.Replace(openAIImageWorkflow(`[a.png]`, ""),
		"decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", inlineTypeSafeBlock, 1)
	cfg, err := parseYAMLWorkflow(t, doc)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if jevErr := checkDecisionMediaSupported(cfg.Decision, cfg.Nodes); jevErr != nil {
		t.Errorf("Jev on TypeSafe's own API: %v", jevErr)
	}
	for _, model := range []string{"openai/gpt-6-luna-decisions", "typesafe/jev-1.14"} {
		cfg.Decision.Model = model
		if openRouterErr := checkDecisionMediaSupported(cfg.Decision, cfg.Nodes); openRouterErr != nil {
			t.Errorf("%s on the TypeSafe wire: %v", model, openRouterErr)
		}
	}
	cfg.Nodes[0].Model = "acme/decider-4b"
	err = checkDecisionMediaSupported(cfg.Decision, cfg.Nodes)
	if err == nil || !strings.Contains(err.Error(), `"acme/decider-4b" does not read them`) {
		t.Fatalf("err = %v, want the node's own text-only model refused", err)
	}
	cfg.Decision.Model, cfg.Nodes[0].Model = "acme/decider-4b", "openai/gpt-6-luna-decisions"
	if overrideErr := checkDecisionMediaSupported(cfg.Decision, cfg.Nodes); overrideErr != nil {
		t.Errorf("a node model that reads images overrides the block's: %v", overrideErr)
	}
}

func TestInlineJevBlockSendsImagesAsAssets(t *testing.T) {
	jev := &fakeJev{respond: departmentAnswer("billing", 0.9)}
	doc := strings.Replace(openAIImageWorkflow(`[a.png]`, ""),
		"decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", inlineTypeSafeBlock, 1)
	run := newDecisionRun(t, doc, jev, true, withArtifacts(t, map[string][]byte{"a.png": pngImage(t, 2, 2)}))
	run.execute(t)

	run.assertBranches(t, "billing", "tech", "fallback")
	jev.mu.Lock()
	body, header := jev.lastBody, jev.lastImagesHeader
	jev.mu.Unlock()
	if header != "true" {
		t.Errorf("images header = %q, want true", header)
	}
	assets, _ := body["assets"].(map[string]any)
	state, _ := body["state"].([]any)
	if len(assets) != 1 || len(state) != 2 {
		t.Fatalf("assets %v, state %v; want the input text then one image reference", assets, state)
	}
	text, _ := state[0].(string)
	ref, _ := state[1].(string)
	asset, _ := assets[ref].(map[string]any)
	if asset["type"] != "image" || asset["format"] != "png" || asset["data_base64"] == "" {
		t.Errorf("asset %q = %v, want a PNG image", ref, asset)
	}
	if !strings.Contains(text, "ticket") {
		t.Errorf("state[0] = %q, want the node's input as text", text)
	}
}

func TestWorkflowWithImagesOnATextOnlyDecisionModelDoesNotStart(t *testing.T) {
	initWith := func(t *testing.T, doc string, operator map[string]any) error {
		t.Helper()
		cfg, err := parseYAMLWorkflow(t, doc)
		if err != nil {
			t.Fatalf("parse: %v", err)
		}
		if operator != nil {
			if cfg.Decision, err = MergeOperatorDecision(cfg.Decision, operator); err != nil {
				t.Fatalf("merge the operator block: %v", err)
			}
		}
		svc := &stubServices{brk: stubBroker{}, features: decisionFeatureRegistry(t, true)}
		return NewWorkflowInstance(cfg).Init(t.Context(), nil, svc)
	}
	openAI := openAIImageWorkflow(`[a.png]`, "")
	textOnly := strings.Replace(openAI, "decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", textOnlyDecisionBlock, 1)
	inlineJev := strings.Replace(openAI, "decision:\n  provider: openai\n  api_key: test-key\n  model: gpt-6-luna\n", inlineTypeSafeBlock, 1)

	for name, doc := range map[string]string{"an OpenAI block": openAI, "a Jev block": inlineJev} {
		if err := initWith(t, doc, nil); err != nil {
			t.Fatalf("%s with images did not start: %v", name, err)
		}
	}
	for name, tc := range map[string]struct {
		doc      string
		operator map[string]any
	}{
		"an inline block naming a text-only model": {doc: textOnly},
		"an app_config block that switches to a text-only model": {doc: openAI, operator: map[string]any{
			"provider": "typesafe", "api_key": "ts-operator", "model": "acme/decider-4b",
		}},
	} {
		t.Run(name, func(t *testing.T) {
			err := initWith(t, tc.doc, tc.operator)
			if err == nil || !strings.Contains(err.Error(), `decision node "triage" sends images`) || !strings.Contains(err.Error(), "does not read them") {
				t.Fatalf("Init = %v, want the workflow refused for sending images to a text-only model", err)
			}
		})
	}
}

func TestCheckLoadableRefusesImagesOnTypeSafe(t *testing.T) {
	appConfig := map[string]any{
		"decision":       map[string]any{"api_key": "${KEY}", "model": "acme/decider-4b"},
		"output_mapping": map[string]any{"r": "{{triage.output}}"},
		"nodes": []any{map[string]any{
			"id": "triage", "type": "decision", "images": []any{"a.png"},
			"questions": map[string]any{"urgent": map[string]any{"type": "boolean", "instructions": "is it urgent"}},
		}},
	}
	if err := CheckLoadable("wf", appConfig); err == nil || !strings.Contains(err.Error(), "does not read them") {
		t.Fatalf("CheckLoadable = %v, want the images refusal for a text-only model", err)
	}
	appConfig["decision"] = map[string]any{"api_key": "${KEY}"}
	if err := CheckLoadable("wf", appConfig); err != nil {
		t.Errorf("CheckLoadable with TypeSafe's default Jev model = %v", err)
	}
	appConfig["decision"] = map[string]any{"provider": "openai", "api_key": "${KEY}"}
	if err := CheckLoadable("wf", appConfig); err != nil {
		t.Errorf("CheckLoadable with openai = %v", err)
	}
}

func TestCheckDecisionImagesRefusesMoreLiteralImagesThanMaxImages(t *testing.T) {
	node := func(images ...string) []*NodeConfig {
		return []*NodeConfig{{ID: "triage", Type: "decision", Images: images}}
	}
	six := []string{"1.png", "2.png", "3.png", "4.png", "5.png", "6.png"}
	openAI := &DecisionProviderConfig{Provider: DecisionProviderOpenAI}
	for name, tc := range map[string]struct {
		provider *DecisionProviderConfig
		nodes    []*NodeConfig
		wantErr  string
	}{
		"over the default":       {provider: openAI, nodes: node(six...), wantErr: `decision node "triage" names 6 images, but the decision block's max_images is 5`},
		"at the default":         {provider: openAI, nodes: node(six[:5]...)},
		"templates do not count": {provider: openAI, nodes: node(append(six[:5:5], "{{workflow.input.more}}")...)},
		"over a set max_images": {
			provider: &DecisionProviderConfig{Provider: DecisionProviderOpenAI, MaxImages: 2},
			nodes:    node("a.png", "b.png", "c.png"),
			wantErr:  "max_images is 2",
		},
		"a raised max_images": {provider: &DecisionProviderConfig{Provider: DecisionProviderOpenAI, MaxImages: 6}, nodes: node(six...)},
		"under a model_alias": {
			provider: &DecisionProviderConfig{Provider: DecisionProviderTypeSafe, ModelAlias: "luna", MaxImages: 1},
			nodes:    node("a.png", "b.png"),
			wantErr:  "max_images is 1",
		},
	} {
		t.Run(name, func(t *testing.T) {
			err := checkDecisionMediaSupported(tc.provider, tc.nodes)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestUnproducibleNodeReferences_ReportsImageEntries(t *testing.T) {
	tools := toolSetWith(t, webRequestResultTool())
	decisionNode := func(images ...string) []*NodeConfig {
		return []*NodeConfig{
			fetchImageNode(),
			{ID: "triage", Type: "decision", DependsOn: []string{"fetch_image"}, Images: images},
		}
	}

	problems := validateRefs(t, decisionNode("a.png", "{{fetch_imagee.output.created_artifact}}"), tools)
	if len(problems) != 1 || problems[0].kind != problemKindUnknownNode || problems[0].referrer != `node "triage" images[1]` {
		t.Fatalf("problems = %+v, want one unknown-node report against images[1]", problems)
	}
	if msg := reported(problems); !strings.Contains(msg, `no node "fetch_imagee"`) {
		t.Errorf("report %q does not name the typo", msg)
	}
	if got := validateRefs(t, decisionNode("{{fetch_image.output.created_artifact}}"), tools); len(got) > 0 {
		t.Errorf("a valid image reference was reported: %v", reported(got))
	}
}

func TestParseDecisionImages(t *testing.T) {
	cases := map[string]struct {
		raw     any
		want    []string
		wantErr string
	}{
		"absent":        {},
		"a list":        {raw: []any{"a.png", "{{x.output.created_artifact}}"}, want: []string{"a.png", "{{x.output.created_artifact}}"}},
		"not a list":    {raw: "a.png", wantErr: "must be a list"},
		"an empty item": {raw: []any{"  "}, wantErr: "images[0]"},
		"a map item":    {raw: []any{map[string]any{"a": 1}}, wantErr: "images[0]"},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := parseDecisionMediaList("triage", "images", tc.raw)
			if tc.wantErr != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantErr) {
					t.Fatalf("err = %v, want %q", err, tc.wantErr)
				}
				return
			}
			if err != nil || strings.Join(got, "|") != strings.Join(tc.want, "|") {
				t.Errorf("got %v, %v; want %v", got, err, tc.want)
			}
		})
	}
}

func TestImageRefsOf(t *testing.T) {
	if refs, _, err := artifactRefsOf([]any{"a.png", nil, "", []any{"b.png"}}); err == nil {
		t.Errorf("a nested list = %v, want an error", refs)
	}
	if _, _, err := artifactRefsOf(42); err == nil {
		t.Error("a number resolved as an image reference")
	}
	refs, dropped, err := artifactRefsOf([]any{"a.png", nil, "", "b.png:2"})
	if err != nil || strings.Join(refs, ",") != "a.png,b.png:2" || dropped != 2 {
		t.Errorf("refs = %v, dropped %d, %v; want the two references and the two blanks counted", refs, dropped, err)
	}
}

func TestParseDecisionProviderImageCaps(t *testing.T) {
	for name, tc := range map[string]struct {
		raw     map[string]any
		wantErr string
	}{
		"defaults":           {raw: map[string]any{}},
		"in range":           {raw: map[string]any{"max_images": 128, "max_image_bytes": 1 << 20}},
		"too many images":    {raw: map[string]any{"max_images": 129}, wantErr: "max_images"},
		"negative images":    {raw: map[string]any{"max_images": -1}, wantErr: "max_images"},
		"negative bytes":     {raw: map[string]any{"max_image_bytes": -1}, wantErr: "max_image_bytes"},
		"bytes at the limit": {raw: map[string]any{"max_image_bytes": decision.MaxImageBytesLimit}},
		"bytes over the limit": {
			raw:     map[string]any{"max_image_bytes": decision.MaxImageBytesLimit + 1},
			wantErr: fmt.Sprintf("max_image_bytes must be between 0 (the default, %d) and %d", decision.DefaultMaxImageBytes, decision.MaxImageBytesLimit),
		},
		"camelCase accepted": {raw: map[string]any{"maxImages": 3, "maxImageBytes": 10}},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := ParseDecisionProvider(tc.raw)
			if tc.wantErr == "" && err != nil {
				t.Fatalf("err = %v", err)
			}
			if tc.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tc.wantErr)) {
				t.Fatalf("err = %v, want %q", err, tc.wantErr)
			}
		})
	}
}

func TestOperatorBlockTunesTheImageCaps(t *testing.T) {
	body := &DecisionProviderConfig{Provider: DecisionProviderOpenAI, APIKey: "k", MaxImages: 2, MaxImageBytes: 100}
	merged, err := MergeOperatorDecision(body, map[string]any{"provider": "openai", "max_images": 4})
	if err != nil {
		t.Fatal(err)
	}
	if merged.MaxImages != 4 || merged.MaxImageBytes != 100 {
		t.Errorf("merged caps = %d images, %d bytes; want the operator's 4 and the author's 100", merged.MaxImages, merged.MaxImageBytes)
	}
}

func TestOwnedArtifactLocatorRefusesAnotherUsersArtifact(t *testing.T) {
	art := &artifact.ScopedService{Inner: memory.New()}
	_, err := ownedArtifactLocator(art, artifact.Locator{AppName: "wf", UserID: "mallory", SessionID: "s", Filename: "f.png"}, "alice")
	if !errors.Is(err, errForeignUserArtifact) {
		t.Fatalf("err = %v, want errForeignUserArtifact", err)
	}
	loc, err := ownedArtifactLocator(art, artifact.Locator{AppName: "wf", UserID: "Alice", SessionID: "s", Filename: "f.png"}, "alice")
	if err != nil || loc.Filename != "f.png" || loc.SessionID != "s" {
		t.Errorf("loc = %+v, %v; want the owner's own artifact", loc, err)
	}
	for _, folded := range []struct{ owner, caller string }{
		{"ivan@x.com", "İvan@x.com"},
		{"kate@x.com", "Kate@x.com"},
		{"sam@x.com", "ſam@x.com"},
	} {
		_, err = ownedArtifactLocator(art, artifact.Locator{AppName: "wf", UserID: folded.owner, SessionID: "s", Filename: "f.png"}, folded.caller)
		if !errors.Is(err, errForeignUserArtifact) {
			t.Errorf("caller %q reading %q's artifact: err = %v, want errForeignUserArtifact", folded.caller, folded.owner, err)
		}
	}
}
