// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2a

import (
	"strings"
	"testing"
)

func TestParseSSEStream_TaskEvent(t *testing.T) {
	stream := "data: {\"result\":{\"kind\":\"task\",\"id\":\"t1\",\"status\":{\"state\":\"completed\"}}}\n\n"
	ch := parseSSEStream(strings.NewReader(stream))

	evt, ok := <-ch
	if !ok {
		t.Fatal("channel closed without event")
	}
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.Task == nil {
		t.Fatal("expected Task event")
	}
	if evt.Task.ID != "t1" {
		t.Errorf("task ID = %q, want %q", evt.Task.ID, "t1")
	}

	if _, ok := <-ch; ok {
		t.Error("expected channel to be closed after last event")
	}
}

func TestParseSSEStream_StatusUpdateEvent(t *testing.T) {
	stream := "data: {\"result\":{\"kind\":\"status-update\",\"taskId\":\"t2\",\"status\":{\"state\":\"working\"}}}\n\n"
	ch := parseSSEStream(strings.NewReader(stream))

	evt := <-ch
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.StatusUpdate == nil {
		t.Fatal("expected StatusUpdate event")
	}
	if evt.StatusUpdate.TaskID != "t2" {
		t.Errorf("taskId = %q, want %q", evt.StatusUpdate.TaskID, "t2")
	}
}

func TestParseSSEStream_ArtifactUpdateEvent(t *testing.T) {
	stream := "data: {\"result\":{\"kind\":\"artifact-update\",\"taskId\":\"t3\",\"artifact\":{\"artifactId\":\"a1\",\"name\":\"out.txt\",\"parts\":[]}}}\n\n"
	ch := parseSSEStream(strings.NewReader(stream))

	evt := <-ch
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.ArtifactUpdate == nil {
		t.Fatal("expected ArtifactUpdate event")
	}
	if evt.ArtifactUpdate.Artifact.Name != "out.txt" {
		t.Errorf("artifact name = %q, want %q", evt.ArtifactUpdate.Artifact.Name, "out.txt")
	}
}

func TestParseSSEStream_MultipleEvents(t *testing.T) {
	stream := "" +
		"data: {\"result\":{\"kind\":\"status-update\",\"taskId\":\"t1\",\"status\":{\"state\":\"working\"}}}\n\n" +
		"data: {\"result\":{\"kind\":\"task\",\"id\":\"t1\",\"status\":{\"state\":\"completed\"}}}\n\n"

	ch := parseSSEStream(strings.NewReader(stream))

	evt1 := <-ch
	if evt1.StatusUpdate == nil {
		t.Fatal("first event: expected StatusUpdate")
	}

	evt2 := <-ch
	if evt2.Task == nil {
		t.Fatal("second event: expected Task")
	}

	if _, ok := <-ch; ok {
		t.Error("expected channel closed")
	}
}

func TestParseSSEStream_MultiLineData(t *testing.T) {
	stream := "data: {\"result\":{\"kind\":\"task\",\n" +
		"data: \"id\":\"t1\",\"status\":{\"state\":\"completed\"}}}\n\n"

	ch := parseSSEStream(strings.NewReader(stream))
	evt := <-ch
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.Task == nil {
		t.Fatal("expected Task event from multi-line data")
	}
}

func TestParseSSEStream_IgnoresCommentAndEventLines(t *testing.T) {
	stream := ": this is a comment\n" +
		"event: message\n" +
		"data: {\"result\":{\"kind\":\"task\",\"id\":\"t1\",\"status\":{\"state\":\"completed\"}}}\n\n"

	ch := parseSSEStream(strings.NewReader(stream))
	evt := <-ch
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.Task == nil {
		t.Fatal("expected Task event")
	}
}

func TestParseSSEStream_DataWithoutTrailingNewline(t *testing.T) {
	stream := "data: {\"result\":{\"kind\":\"task\",\"id\":\"t1\",\"status\":{\"state\":\"completed\"}}}"

	ch := parseSSEStream(strings.NewReader(stream))
	evt := <-ch
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.Task == nil {
		t.Fatal("expected Task event from data without trailing newline")
	}
}

func TestParseSSEStream_RPCError(t *testing.T) {
	stream := "data: {\"error\":{\"code\":-32600,\"message\":\"invalid request\"}}\n\n"
	ch := parseSSEStream(strings.NewReader(stream))

	evt := <-ch
	if evt.Error == nil {
		t.Fatal("expected error event for JSON-RPC error")
	}
	if !strings.Contains(evt.Error.Error(), "invalid request") {
		t.Errorf("error = %v, want message containing 'invalid request'", evt.Error)
	}
}

func TestParseSSEStream_InvalidJSON(t *testing.T) {
	stream := "data: not-json\n\n"
	ch := parseSSEStream(strings.NewReader(stream))

	evt := <-ch
	if evt.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParseSSEStream_UnknownKind(t *testing.T) {
	stream := "data: {\"result\":{\"kind\":\"unknown-type\"}}\n\n"
	ch := parseSSEStream(strings.NewReader(stream))

	evt := <-ch
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.Task != nil || evt.StatusUpdate != nil || evt.ArtifactUpdate != nil {
		t.Error("expected empty event for unknown kind")
	}

	if _, ok := <-ch; ok {
		t.Error("expected channel to be closed")
	}
}

func TestParseSSEStream_EmptyStream(t *testing.T) {
	ch := parseSSEStream(strings.NewReader(""))
	if _, ok := <-ch; ok {
		t.Error("expected channel closed immediately for empty stream")
	}
}

func TestParseSSEData_NoResultFallsBackToRawData(t *testing.T) {
	data := `{"kind":"task","id":"t1","status":{"state":"completed"}}`
	evt, err := parseSSEData(data)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if evt.Task == nil {
		t.Fatal("expected Task event from raw data fallback")
	}
	if evt.Task.ID != "t1" {
		t.Errorf("task ID = %q, want %q", evt.Task.ID, "t1")
	}
}

func TestParseSSEData_DataWithSpaceAfterColon(t *testing.T) {
	stream := "data:  {\"result\":{\"kind\":\"task\",\"id\":\"t1\",\"status\":{\"state\":\"completed\"}}}\n\n"
	ch := parseSSEStream(strings.NewReader(stream))

	evt := <-ch
	if evt.Error != nil {
		t.Fatalf("unexpected error: %v", evt.Error)
	}
	if evt.Task == nil {
		t.Fatal("expected Task event")
	}
}
