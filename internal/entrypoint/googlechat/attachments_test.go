// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package googlechat

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint"
)

func TestArtifactNames_MarkersFilePartsAndSavesWithoutRepeatsOrWorkingFiles(t *testing.T) {
	task := &a2a.Task{Status: a2a.TaskStatus{Message: &a2a.Message{Parts: []a2a.Part{
		{Kind: "file", File: &a2a.FileContent{Name: "chart.png"}},
	}}}}
	saved := []entrypoint.SavedArtifact{
		{Filename: "report.csv"},
		{Filename: "scratch.json", Tags: []string{a2a.ArtifactTagWorking}},
		{Filename: "summary.md"},
	}
	got := artifactNames("See «artifact_return:report.csv:2» and «artifact_return:chart.png»", task, saved)
	want := []string{"report.csv", "chart.png", "summary.md"}
	if !slices.Equal(got, want) {
		t.Errorf("artifactNames = %v, want %v", got, want)
	}
}

func TestValidAttachmentResource(t *testing.T) {
	for name, want := range map[string]bool{
		"ClxDb250ZW50LWlk":        true,
		"":                        false,
		"x?alt=json":              false,
		"../../v1/spaces":         false,
		"/absolute":               false,
		"a#fragment":              false,
		`back\slash`:              false,
		"spaces/A/attachments/B=": true,
	} {
		if got := validAttachmentResource(name); got != want {
			t.Errorf("validAttachmentResource(%q) = %v, want %v", name, got, want)
		}
	}
}

type stalledDownloader struct{ calls atomic.Int32 }

func (d *stalledDownloader) DownloadAttachment(ctx context.Context, _ string, _ int64) ([]byte, error) {
	d.calls.Add(1)
	<-ctx.Done()
	return nil, ctx.Err()
}

func uploadedAttachments(n int) []chatAttachment {
	atts := make([]chatAttachment, n)
	for i := range atts {
		atts[i] = chatAttachment{ContentName: fmt.Sprintf("f%d.txt", i), Source: attachmentUploaded, AttachmentDataRef: &chatAttachmentDataRef{ResourceName: fmt.Sprintf("res-%d", i)}}
	}
	return atts
}

func inboundFilePartsWithin(t *testing.T, a *Adapter, atts []chatAttachment, limit time.Duration) (parts []a2a.Part, dropped []string) {
	t.Helper()
	done := make(chan struct{})
	go func() {
		defer close(done)
		parts, dropped = a.inboundFileParts(context.Background(), atts, func(context.Context, entrypoint.ArtifactUpload) (string, bool) {
			t.Error("a file that was never downloaded was stored")
			return "", false
		})
	}()
	select {
	case <-done:
		return parts, dropped
	case <-time.After(limit):
		t.Fatalf("attachment downloads still running after %v: a stalled download parks the ingress worker", limit)
		return nil, nil
	}
}

func TestInboundFileParts_EachDownloadHasADeadline(t *testing.T) {
	a := NewAdapter(nil)
	a.logger = slog.New(slog.DiscardHandler)
	downloader := &stalledDownloader{}
	a.downloader = downloader
	a.attachmentTimeout, a.attachmentsBudget = 20*time.Millisecond, time.Hour

	parts, dropped := inboundFilePartsWithin(t, a, uploadedAttachments(2), 5*time.Second)
	if len(parts) != 0 || len(dropped) != 2 || downloader.calls.Load() != 2 {
		t.Errorf("parts=%d dropped=%v calls=%d, want both stalled files dropped after their own deadline", len(parts), dropped, downloader.calls.Load())
	}
}

func TestInboundFileParts_OneMessageHasATotalBudget(t *testing.T) {
	a := NewAdapter(nil)
	a.logger = slog.New(slog.DiscardHandler)
	a.downloader = &stalledDownloader{}
	a.attachmentTimeout, a.attachmentsBudget = time.Hour, 50*time.Millisecond

	parts, dropped := inboundFilePartsWithin(t, a, uploadedAttachments(3), 5*time.Second)
	if len(parts) != 0 || len(dropped) != 3 {
		t.Errorf("parts=%d dropped=%v, want every file dropped once the budget ran out", len(parts), dropped)
	}
}

func TestArtifactNotice(t *testing.T) {
	if got := artifactNotice([]string{"a.pdf", "b.png"}, "https://sam.example.com"); got != "📎 The agent created a.pdf, b.png. <https://sam.example.com|Open them in Solace Agent Mesh>." {
		t.Errorf("notice = %q", got)
	}
}
