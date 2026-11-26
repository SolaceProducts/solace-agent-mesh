// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"log/slog"
	"strconv"
	"sync"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	"github.com/SolaceProducts/solace-agent-mesh/internal/embed"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func completedSignalFromWire(t *testing.T, filename, mime string, version int, tags []string) *a2a.TaskStatusUpdateEvent {
	t.Helper()
	signal := a2a.ArtifactCreationProgressData{
		Type:     string(a2a.SignalArtifactCreationProgress),
		Filename: filename,
		Status:   "completed",
		MIMEType: &mime,
		Version:  &version,
		Tags:     tags,
	}
	wire, err := json.Marshal(a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{{Kind: "data", Data: signal}}}},
	})
	if err != nil {
		t.Fatalf("encode signal: %v", err)
	}
	var event a2a.TaskStatusUpdateEvent
	if err := json.Unmarshal(wire, &event); err != nil {
		t.Fatalf("decode signal: %v", err)
	}
	return &event
}

type workingFixture struct {
	adapter *Adapter
	store   *artifact.ScopedService
	ts      *taskState
	taskID  string
}

const (
	workingAgent, workingUser, workingSession = "DownloadAgent", "alice", "sess-working"
	downloadedFile                            = "attachment.csv"
	deliverableFile                           = "summary.png"
)

var (
	downloadedBytes  = []byte("id,name\n1,widget\n")
	deliverableBytes = []byte("\x89PNG\r\n\x1a\nsummary-chart")
)

func newWorkingFixture(t *testing.T) workingFixture {
	t.Helper()
	a, scoped := newArtifactAdapter(t)
	saveArtifact(t, scoped, workingAgent, workingUser, workingSession, downloadedFile, downloadedBytes, "text/csv")
	saveArtifact(t, scoped, workingAgent, workingUser, workingSession, deliverableFile, deliverableBytes, "image/png")

	const taskID = "task-working"
	_ = a.core.Tasks().Register(taskID, workingAgent, workingUser, entrypoint.TaskIdentity{})
	ts := &taskState{toolName: "download", done: make(chan taskResult, 1), agentName: workingAgent, userID: workingUser, sessionID: workingSession}
	a.tasks[taskID] = ts
	return workingFixture{adapter: a, store: scoped, ts: ts, taskID: taskID}
}

func (f workingFixture) signalSaved(t *testing.T, filename, mime string, tags ...string) {
	t.Helper()
	f.adapter.OnStatusUpdate(t.Context(), f.taskID, completedSignalFromWire(t, filename, mime, 0, tags), nil)
}

func (f workingFixture) finish(t *testing.T, finalText string) taskResult {
	t.Helper()
	f.adapter.OnFinalResponse(t.Context(), f.taskID, &a2a.Task{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{a2a.NewTextPart(finalText)}}},
	}, nil)
	res := <-f.ts.done
	if res.err != nil {
		t.Fatalf("unexpected error: %v", res.err)
	}
	return res
}

func countTextResources(contents []mcp.Content, text string) int {
	n := 0
	for _, c := range contents {
		if er, ok := c.(mcp.EmbeddedResource); ok {
			if txt, ok := er.Resource.(mcp.TextResourceContents); ok && txt.Text == text {
				n++
			}
		}
	}
	return n
}

func TestArtifactSignal_WorkingArtifactIsNotReturned(t *testing.T) {
	f := newWorkingFixture(t)

	f.signalSaved(t, downloadedFile, "text/csv", a2a.ArtifactTagWorking)
	f.signalSaved(t, deliverableFile, "image/png")
	res := f.finish(t, "Here is the summary.")

	if len(res.contents) != 1 {
		t.Fatalf("result has %d contents, want only the deliverable", len(res.contents))
	}
	findImageContent(t, res.contents)
}

func TestArtifactSignal_ArtifactWithOtherTagsIsReturned(t *testing.T) {
	f := newWorkingFixture(t)

	f.signalSaved(t, downloadedFile, "text/csv", a2a.ArtifactTagUserUploaded)
	res := f.finish(t, "done")

	if got := countTextResources(res.contents, string(downloadedBytes)); got != 1 {
		t.Errorf("artifact tagged only %s returned %d times, want 1", a2a.ArtifactTagUserUploaded, got)
	}
}

func TestArtifactSignal_WorkingArtifactReturnedExplicitlyInFinalText(t *testing.T) {
	f := newWorkingFixture(t)

	f.signalSaved(t, downloadedFile, "text/csv", a2a.ArtifactTagWorking)
	res := f.finish(t, "Here is the file you asked for. «artifact_return:"+downloadedFile+"»")

	if got := countTextResources(res.contents, string(downloadedBytes)); got != 1 {
		t.Errorf("explicitly returned __working artifact delivered %d times, want 1", got)
	}
}

func TestArtifactSignal_WorkingArtifactReturnedExplicitlyWhileStreaming(t *testing.T) {
	f := newWorkingFixture(t)

	f.signalSaved(t, downloadedFile, "text/csv", a2a.ArtifactTagWorking)
	returned := a2a.NewFilePart("artifact://"+workingAgent+"/"+workingUser+"/"+workingSession+"/"+downloadedFile+"?version=0", downloadedFile, "text/csv")
	returned.Metadata = map[string]any{"tags": []any{a2a.ArtifactTagWorking}}
	f.adapter.OnStatusUpdate(t.Context(), f.taskID, &a2a.TaskStatusUpdateEvent{
		Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{returned}}},
	}, nil)
	res := f.finish(t, "Here is the file you asked for.")

	if got := countTextResources(res.contents, string(downloadedBytes)); got != 1 {
		t.Errorf("explicitly returned __working artifact delivered %d times, want 1", got)
	}
}

func TestOnFinalResponse_ArtifactReturnOfDeliveredArtifactIsNotDuplicated(t *testing.T) {
	f := newWorkingFixture(t)

	f.signalSaved(t, deliverableFile, "image/png")
	res := f.finish(t, "Chart: «artifact_return:"+deliverableFile+"»")

	if len(res.contents) != 1 {
		t.Errorf("artifact signalled and then returned in the final text came back %d times, want 1", len(res.contents))
	}
}

func TestArtifactSignal_WorkingAmongOtherTagsIsNotReturned(t *testing.T) {
	f := newWorkingFixture(t)

	f.signalSaved(t, downloadedFile, "text/csv", "source:download", a2a.ArtifactTagWorking)
	res := f.finish(t, "done")

	if len(res.contents) != 0 {
		t.Errorf("artifact tagged __working after another tag returned %d contents, want none", len(res.contents))
	}
}

func TestOnFinalResponse_ArtifactReturnHonoursTheVersion(t *testing.T) {
	cases := map[string]struct {
		spec string
		want []byte
	}{
		"explicit older version": {spec: downloadedFile + ":0", want: downloadedBytes},
		"no version is latest":   {spec: downloadedFile, want: []byte("id,name\n2,gadget\n")},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newWorkingFixture(t)
			saveArtifact(t, f.store, workingAgent, workingUser, workingSession, downloadedFile, []byte("id,name\n2,gadget\n"), "text/csv")

			res := f.finish(t, "«artifact_return:"+tc.spec+"»")

			if got := countTextResources(res.contents, string(tc.want)); got != 1 {
				t.Errorf("returned %d resources holding %q, want 1", got, tc.want)
			}
		})
	}
}

func TestConvertArtifactFromStore_FailedLoadLevelDependsOnWhoNamedTheFile(t *testing.T) {
	const missingVersion = 9
	cases := map[string]struct {
		run  func(t *testing.T, f workingFixture)
		want slog.Level
	}{
		"artifact_return in the final text": {
			run: func(t *testing.T, f workingFixture) {
				f.finish(t, "Here it is. «artifact_return:"+downloadedFile+":"+strconv.Itoa(missingVersion)+"»")
			},
			want: slog.LevelWarn,
		},
		"progress signal": {
			run: func(t *testing.T, f workingFixture) {
				f.adapter.OnStatusUpdate(t.Context(), f.taskID, completedSignalFromWire(t, downloadedFile, "text/csv", missingVersion, nil), nil)
				f.finish(t, "done")
			},
			want: slog.LevelError,
		},
	}
	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			f := newWorkingFixture(t)
			logs := &recordedLogs{}
			f.adapter.logger = slog.New(logs)

			tc.run(t, f)

			if got := logs.levelsOf("mcp artifact: failed to load artifact"); len(got) != 1 || got[0] != tc.want {
				t.Errorf("failed-load records logged at %v, want one at %v", got, tc.want)
			}
			if len(f.ts.contents) != 0 {
				t.Errorf("a failed load produced %d contents, want none", len(f.ts.contents))
			}
		})
	}
}

type recordedLogs struct {
	mu      sync.Mutex
	records []slog.Record
}

func (r *recordedLogs) Enabled(context.Context, slog.Level) bool { return true }
func (r *recordedLogs) WithAttrs([]slog.Attr) slog.Handler       { return r }
func (r *recordedLogs) WithGroup(string) slog.Handler            { return r }

func (r *recordedLogs) Handle(_ context.Context, rec slog.Record) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.records = append(r.records, rec)
	return nil
}

func (r *recordedLogs) levelsOf(message string) []slog.Level {
	r.mu.Lock()
	defer r.mu.Unlock()
	var levels []slog.Level
	for _, rec := range r.records {
		if rec.Message == message {
			levels = append(levels, rec.Level)
		}
	}
	return levels
}

func TestAppendFinalTextSignals_InlineBinaryIsDeliveredOnceAndNeverEmpty(t *testing.T) {
	f := newWorkingFixture(t)
	chart := embed.InlineBinaryData{Name: "chart.png", MIMEType: "image/png", Bytes: deliverableBytes}

	f.adapter.appendFinalTextSignals(t.Context(), f.taskID, f.ts, []embed.Signal{
		{Type: embed.SignalInlineBinaryContent, Data: embed.InlineBinaryData{Name: "empty.png", MIMEType: "image/png"}},
		{Type: embed.SignalInlineBinaryContent, Data: chart},
		{Type: embed.SignalInlineBinaryContent, Data: chart},
	})

	if len(f.ts.contents) != 1 {
		t.Fatalf("contents = %d, want the chart once and nothing for the empty signal", len(f.ts.contents))
	}
	findImageContent(t, f.ts.contents)
}

func TestOnFinalResponse_ArtifactReturnOfEarlierArtifactIsDelivered(t *testing.T) {
	f := newWorkingFixture(t)

	res := f.finish(t, "Here is the chart from before. «artifact_return:"+deliverableFile+":0»")

	if len(res.contents) != 1 {
		t.Fatalf("result has %d contents, want the returned chart", len(res.contents))
	}
	findImageContent(t, res.contents)
}
