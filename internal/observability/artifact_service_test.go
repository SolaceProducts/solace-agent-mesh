// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package observability

import (
	"context"
	"errors"
	"io"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/artifact"
	artmem "github.com/SolaceProducts/solace-agent-mesh/internal/artifact/memory"
)

func assertOneHistogram(t *testing.T, rec *RecordingMetrics, wantOp string, wantErrType string) {
	t.Helper()
	hists := rec.Histograms("outbound.request.duration")
	if len(hists) != 1 {
		t.Fatalf("expected 1 histogram, got %d", len(hists))
	}
	h := hists[0]
	if h.Labels["service.peer.name"] != "artifact_service" {
		t.Errorf("service.peer.name = %q, want artifact_service", h.Labels["service.peer.name"])
	}
	if h.Labels["operation.name"] != wantOp {
		t.Errorf("operation.name = %q, want %q", h.Labels["operation.name"], wantOp)
	}
	if h.Labels["error.type"] != wantErrType {
		t.Errorf("error.type = %q, want %q", h.Labels["error.type"], wantErrType)
	}
}

func TestInstrumentedArtifactService_Save(t *testing.T) {
	ctx := t.Context()
	rec := installMetrics(t, NewRecordingMetrics())
	svc := NewInstrumentedArtifactService(artmem.New())

	_, err := svc.Save(ctx, artifact.Locator{AppName: "a", UserID: "u", SessionID: "s", Filename: "f.txt"}, []byte("hi"), "text/plain")
	if err != nil {
		t.Fatalf("Save: %v", err)
	}
	assertOneHistogram(t, rec, "save", "")
}

func TestInstrumentedArtifactService_Load(t *testing.T) {
	ctx := t.Context()
	rec := installMetrics(t, NewRecordingMetrics())
	inner := artmem.New()
	svc := NewInstrumentedArtifactService(inner)
	loc := artifact.Locator{AppName: "a", UserID: "u", SessionID: "s", Filename: "f.txt"}
	_, _ = inner.Save(ctx, loc, []byte("x"), "text/plain")

	rec.Reset()
	_, _, err := svc.Load(ctx, loc, nil)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	assertOneHistogram(t, rec, "load", "")
}

func TestInstrumentedArtifactService_Delete(t *testing.T) {
	ctx := t.Context()
	rec := installMetrics(t, NewRecordingMetrics())
	inner := artmem.New()
	svc := NewInstrumentedArtifactService(inner)
	loc := artifact.Locator{AppName: "a", UserID: "u", SessionID: "s", Filename: "f.txt"}
	_, _ = inner.Save(ctx, loc, []byte("x"), "text/plain")

	rec.Reset()
	if err := svc.Delete(ctx, loc); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	assertOneHistogram(t, rec, "delete", "")
}

func TestInstrumentedArtifactService_ListKeys(t *testing.T) {
	ctx := t.Context()
	rec := installMetrics(t, NewRecordingMetrics())
	svc := NewInstrumentedArtifactService(artmem.New())

	_, err := svc.ListKeys(ctx, artifact.KeyScope{AppName: "a", UserID: "u", SessionID: "s"})
	if err != nil {
		t.Fatalf("ListKeys: %v", err)
	}
	assertOneHistogram(t, rec, "list", "")
}

func TestInstrumentedArtifactService_ListVersions(t *testing.T) {
	ctx := t.Context()
	rec := installMetrics(t, NewRecordingMetrics())
	svc := NewInstrumentedArtifactService(artmem.New())

	_, err := svc.ListVersions(ctx, artifact.Locator{AppName: "a", UserID: "u", SessionID: "s", Filename: "missing.txt"})
	if err != nil {
		t.Fatalf("ListVersions: %v", err)
	}
	assertOneHistogram(t, rec, "list", "")
}

func TestInstrumentedArtifactService_ErrorType(t *testing.T) {
	rec := installMetrics(t, NewRecordingMetrics())
	svc := NewInstrumentedArtifactService(&failingArtifactService{err: errors.New("unauthorized")})

	_, err := svc.Save(t.Context(), artifact.Locator{AppName: "a", UserID: "u", SessionID: "s", Filename: "f"}, []byte("x"), "")
	if err == nil {
		t.Fatal("expected error from failing backend")
	}
	assertOneHistogram(t, rec, "save", "auth_error")
}

func TestInstrumentedArtifactService_NoMetricsConfigured(t *testing.T) {
	svc := NewInstrumentedArtifactService(artmem.New())
	if _, err := svc.Save(t.Context(), artifact.Locator{AppName: "a", UserID: "u", SessionID: "s", Filename: "f"}, []byte("x"), ""); err != nil {
		t.Errorf("expected no error with noop metrics, got %v", err)
	}
}

func TestInstrumentedArtifactService_InnerAccess(t *testing.T) {
	inner := artmem.New()
	svc := NewInstrumentedArtifactService(inner)
	if svc.Inner() != inner {
		t.Error("Inner() did not return the wrapped service")
	}
}

type failingArtifactService struct{ err error }

func (f *failingArtifactService) Save(context.Context, artifact.Locator, []byte, string) (int, error) {
	return 0, f.err
}
func (f *failingArtifactService) SaveVersion(context.Context, artifact.Locator, int, []byte, string) error {
	return f.err
}
func (f *failingArtifactService) Load(context.Context, artifact.Locator, *int) ([]byte, string, error) {
	return nil, "", f.err
}
func (f *failingArtifactService) Delete(context.Context, artifact.Locator) error { return f.err }
func (f *failingArtifactService) ListKeys(context.Context, artifact.KeyScope) ([]string, error) {
	return nil, f.err
}
func (f *failingArtifactService) ListVersions(context.Context, artifact.Locator) ([]int, error) {
	return nil, f.err
}
func (f *failingArtifactService) GetVersionMetadata(context.Context, artifact.Locator, *int) (*artifact.VersionMetadata, error) {
	return nil, f.err
}
func (f *failingArtifactService) SaveMetadata(context.Context, artifact.Locator, int, *artifact.VersionMetadata) error {
	return f.err
}
func (f *failingArtifactService) OpenReader(context.Context, artifact.Locator, *int) (io.ReadCloser, string, int64, error) {
	return nil, "", 0, f.err
}

var _ artifact.Service = (*failingArtifactService)(nil)
