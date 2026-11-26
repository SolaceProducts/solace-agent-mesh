// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"context"
	"encoding/base64"
	"log/slog"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func agentForInlineSave(t *testing.T) (*Agent, *artifact.ScopedService) {
	t.Helper()
	agent, _, _ := newTestAgent()
	scoped := &artifact.ScopedService{Inner: artmem.New()}
	agent.svc.(*mockServices).arts = scoped
	return agent, scoped
}

func TestSaveInlineFilePartAsArtifact_BinaryBytes_SavedAndRewritten(t *testing.T) {
	t.Parallel()
	agent, scoped := agentForInlineSave(t)

	encoded := base64.StdEncoding.EncodeToString(tinyPNG)
	part := a2a.Part{
		Kind: "file",
		File: &a2a.FileContent{
			Name:     "img.png",
			MIMEType: "image/png",
			Bytes:    encoded,
		},
	}

	newPart, ok := agent.saveInlineFilePartAsArtifact(context.Background(), part, "user-1", "sess-1")
	if !ok {
		t.Fatalf("expected save to succeed, got ok=false")
	}
	if newPart.File == nil {
		t.Fatalf("newPart.File is nil")
	}
	if newPart.File.Bytes != "" {
		t.Errorf("expected Bytes cleared, got %d bytes", len(newPart.File.Bytes))
	}
	if !strings.HasPrefix(newPart.File.URI, "artifact://") {
		t.Errorf("expected artifact:// URI, got %q", newPart.File.URI)
	}

	loc := scoped.Resolve("test-agent", "user-1", "sess-1", "img.png")
	content, mime, err := scoped.Inner.Load(context.Background(), loc, nil)
	if err != nil {
		t.Fatalf("load saved artifact: %v", err)
	}
	if string(content) != string(tinyPNG) {
		t.Errorf("saved bytes don't match original (got %d bytes, want %d)", len(content), len(tinyPNG))
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
}

func TestSaveInlineFilePartAsArtifact_RejectsReservedFilename(t *testing.T) {
	t.Parallel()
	cases := []string{
		"report.metadata.json",
		"doc.converted.txt",
		artifact.BM25IndexFilename,
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			agent, scoped := agentForInlineSave(t)
			part := a2a.Part{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     name,
					MIMEType: "application/json",
					Bytes:    base64.StdEncoding.EncodeToString([]byte(`{"poisoned":true}`)),
				},
			}
			newPart, ok := agent.saveInlineFilePartAsArtifact(context.Background(), part, "user-1", "sess-1")
			if ok {
				t.Fatalf("expected save to be refused for reserved filename %q", name)
			}
			if newPart.File == nil || newPart.File.Bytes == "" {
				t.Fatalf("expected original part returned untouched")
			}
			loc := scoped.Resolve("test-agent", "user-1", "sess-1", name)
			content, _, _ := scoped.Inner.Load(context.Background(), loc, nil)
			if len(content) > 0 {
				t.Errorf("expected no artifact saved under %q, got %d bytes", name, len(content))
			}
		})
	}
}

func TestSaveInlineFilePartAsArtifact_RejectsPathTraversal(t *testing.T) {
	t.Parallel()
	cases := []string{
		"../../etc/passwd",
		"sub/dir/file.txt",
		`..\windows\evil`,
	}
	for _, name := range cases {
		t.Run(name, func(t *testing.T) {
			agent, _ := agentForInlineSave(t)
			part := a2a.Part{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     name,
					MIMEType: "application/octet-stream",
					Bytes:    base64.StdEncoding.EncodeToString([]byte("evil")),
				},
			}
			_, ok := agent.saveInlineFilePartAsArtifact(context.Background(), part, "user-1", "sess-1")
			if ok {
				t.Fatalf("expected save to be refused for path-traversal filename %q", name)
			}
		})
	}
}

func TestProcessFileParts_SmallTextInlineBytes_StillInlined(t *testing.T) {
	t.Parallel()
	agent, scoped := agentForInlineSave(t)
	agent.cfg.MaxInlineArtifactBytes = 4096

	payload := `{"hello":"world"}`
	msg := a2a.Message{
		ContextID: "sess-1",
		Parts: []a2a.Part{
			{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     "input.json",
					MIMEType: "application/json",
					Bytes:    base64.StdEncoding.EncodeToString([]byte(payload)),
				},
			},
		},
	}

	result, _, _ := agent.processFileParts(context.Background(), msg, a2a.UserProperties{UserID: "user-1"}, msg.ContextID, "hello", -1)
	if !strings.Contains(result, "hello") {
		t.Errorf("expected userText preserved, got %q", result)
	}
	if !strings.Contains(result, "world") {
		t.Errorf("expected inlined payload contents in userText, got %q", result)
	}
	loc := scoped.Resolve("test-agent", "user-1", "sess-1", "input.json")
	content, _, _ := scoped.Inner.Load(context.Background(), loc, nil)
	if len(content) > 0 {
		t.Errorf("expected no artifact saved for small text payload (should inline), got %d bytes", len(content))
	}
}

func TestProcessFileParts_BinaryInlineBytes_SavedAsArtifact(t *testing.T) {
	t.Parallel()
	agent, scoped := agentForInlineSave(t)
	agent.cfg.MaxInlineArtifactBytes = 4096

	msg := a2a.Message{
		ContextID: "sess-1",
		Parts: []a2a.Part{
			{
				Kind: "file",
				File: &a2a.FileContent{
					Name:     "photo.png",
					MIMEType: "image/png",
					Bytes:    base64.StdEncoding.EncodeToString(tinyPNG),
				},
			},
		},
	}

	_, _, _ = agent.processFileParts(context.Background(), msg, a2a.UserProperties{UserID: "user-1"}, msg.ContextID, "describe this", -1)

	loc := scoped.Resolve("test-agent", "user-1", "sess-1", "photo.png")
	content, mime, err := scoped.Inner.Load(context.Background(), loc, nil)
	if err != nil {
		t.Fatalf("expected artifact saved, got load error: %v", err)
	}
	if string(content) != string(tinyPNG) {
		t.Errorf("saved bytes differ from input (%d vs %d)", len(content), len(tinyPNG))
	}
	if mime != "image/png" {
		t.Errorf("mime = %q, want image/png", mime)
	}
}

func TestHandleArtifactBlockSave_RejectsBinaryMIMEsTheLLMCannotWrite(t *testing.T) {
	t.Parallel()
	cases := []struct {
		mime    string
		reject  bool
		content string
	}{
		{mime: "image/png", reject: true},
		{mime: "image/jpeg", reject: true},
		{mime: "application/pdf", reject: true},
		{mime: "application/zip", reject: true},
		{mime: "audio/mpeg", reject: true},
		{mime: "video/mp4", reject: true},
		{mime: "text/plain", content: "plain"},
		{mime: "text/markdown", content: "# md"},
		{mime: "text/csv", content: "a,b\n1,2"},
		{mime: "application/json", content: `{"k":"v"}`},
		{mime: "application/yaml", content: "k: v"},
		{mime: "application/xml", content: "<r/>"},
	}
	for _, tc := range cases {
		t.Run(tc.mime, func(t *testing.T) {
			agent, scoped := agentForInlineSave(t)
			sp := NewStatusPublisher(nil, StatusPublisherConfig{TaskID: "task-1", ContextID: "ctx-1", AgentName: "test-agent", ClientID: "client-1", UserID: "user-1"}, slog.Default())

			filename := "doc.bin"
			content := tc.content
			if tc.reject {
				content = "[Binary placeholder]"
			}
			rec := agent.handleArtifactBlockSave(context.Background(), sp, resolvedArtifactBlock{
				Block:    &FencedBlockEvent{FilenameSet: true},
				Filename: filename,
				MIMEType: tc.mime,
				Content:  content,
			}, &streamCallInfo{UserID: "user-1", SessionID: "sess-1"})

			loc := scoped.Resolve("test-agent", "user-1", "sess-1", filename)
			stored, _, _ := scoped.Inner.Load(context.Background(), loc, nil)

			if tc.reject {
				if rec == nil || rec.Status != "error" {
					t.Fatalf("expected error record for %q, got %+v", tc.mime, rec)
				}
				if !strings.Contains(rec.ErrorMsg, "non-text MIME type") {
					t.Errorf("ErrorMsg = %q, want substring 'non-text MIME type'", rec.ErrorMsg)
				}
				if len(stored) > 0 {
					t.Errorf("expected no artifact saved for binary MIME, got %d bytes", len(stored))
				}
				return
			}
			if rec == nil || rec.Status == "error" {
				t.Fatalf("expected success for text MIME %q, got %+v", tc.mime, rec)
			}
			if string(stored) != content {
				t.Errorf("saved content = %q, want %q", string(stored), content)
			}
		})
	}
}
