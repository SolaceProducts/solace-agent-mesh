// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

func newIsolatedTestRunner(t *testing.T, kube *fakeKube) (*SandboxRunner, *ManifestEntry) {
	t.Helper()
	toolsets := t.TempDir()
	exe := filepath.Join(toolsets, "where", "where_am_i")
	if err := os.MkdirAll(filepath.Dir(exe), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(exe, []byte("\x7fELF native"), 0o755); err != nil {
		t.Fatal(err)
	}
	r := newTestRunner(t)
	r.cfg.Isolated.PackageURL = "http://str:8090"
	r.cfg.Isolated.ToolsetsDir = toolsets
	r.isolated = newFakeBackend(t, kube)
	return r, &ManifestEntry{ToolName: "where_am_i", Executable: exe}
}

func TestExecuteIsolated_ReturnsTheResultOfTheJob(t *testing.T) {
	kube := &fakeKube{}
	r, entry := newIsolatedTestRunner(t, kube)

	result, invokeErr := r.Execute(context.Background(), &remotetool.InvokeParams{TaskID: "task-1"}, entry, newArtifactService(), nil)
	if invokeErr != nil {
		t.Fatalf("Execute: %+v", invokeErr)
	}
	if result == nil || result.ToolResult == nil {
		t.Fatalf("result = %+v, want the tool's result", result)
	}
	if !kube.saw("POST /apis/batch/v1/namespaces/sam/jobs") {
		t.Errorf("no job was created: %v", kube.requests)
	}
}

func TestExecuteIsolated_JobFailureIsAToolFailure(t *testing.T) {
	kube := &fakeKube{jobFailed: true}
	r, entry := newIsolatedTestRunner(t, kube)

	_, invokeErr := r.Execute(context.Background(), &remotetool.InvokeParams{TaskID: "task-1"}, entry, newArtifactService(), nil)
	if invokeErr == nil || invokeErr.Code != remotetool.ErrToolExitNonzero {
		t.Fatalf("invokeErr = %+v, want %v", invokeErr, remotetool.ErrToolExitNonzero)
	}
}

func TestExecuteIsolated_RefusesWhatAJobCannotRun(t *testing.T) {
	cases := map[string]func(*remotetool.InvokeParams, *ManifestEntry, string){
		"input artifacts": func(p *remotetool.InvokeParams, _ *ManifestEntry, _ string) {
			p.ArtifactReferences = map[string]remotetool.ArtifactReference{"file": {}}
		},
		"volumes": func(p *remotetool.InvokeParams, _ *ManifestEntry, _ string) {
			p.VolumeBindings = []remotetool.VolumeBinding{{}}
		},
		"network bindings": func(p *remotetool.InvokeParams, _ *ManifestEntry, _ string) {
			p.NetworkBindings = []remotetool.NetworkBinding{{}}
		},
		"script": func(_ *remotetool.InvokeParams, e *ManifestEntry, _ string) {
			if err := os.WriteFile(e.Executable, []byte("#!/bin/sh\necho hi\n"), 0o755); err != nil {
				panic(err)
			}
		},
		"executable outside the toolsets dir": func(_ *remotetool.InvokeParams, e *ManifestEntry, dir string) {
			e.Executable = filepath.Join(dir, "elsewhere")
		},
	}
	for name, mutate := range cases {
		t.Run(name, func(t *testing.T) {
			kube := &fakeKube{}
			r, entry := newIsolatedTestRunner(t, kube)
			params := &remotetool.InvokeParams{TaskID: "task-1"}
			mutate(params, entry, t.TempDir())

			_, invokeErr := r.Execute(context.Background(), params, entry, newArtifactService(), nil)
			if invokeErr == nil || invokeErr.Code != remotetool.ErrInvalidRequest {
				t.Fatalf("invokeErr = %+v, want %v", invokeErr, remotetool.ErrInvalidRequest)
			}
			if kube.saw("POST /apis/batch/v1/namespaces/sam/jobs") {
				t.Error("a job was created for a call that cannot run in one")
			}
		})
	}
}

func TestIsolatedInvokeError_ClassifiesEachFailure(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		timedOut bool
		want     remotetool.ErrorCode
	}{
		{"timeout", &isolatedJobFailure{Reason: "DeadlineExceeded", Deadline: true, LogTail: "slow"}, true, remotetool.ErrToolTimeout},
		{"cancelled", fmt.Errorf("await: %w", context.Canceled), false, remotetool.ErrSandboxFailed},
		{"result too large", errIsolatedResultTooLarge, false, remotetool.ErrExecutionError},
		{"pod never started", &isolatedJobFailure{Reason: "the pod did not start", Startup: true}, false, remotetool.ErrSandboxFailed},
		{"tool failed", &isolatedJobFailure{Reason: "BackoffLimitExceeded", LogTail: "panic: boom"}, false, remotetool.ErrToolExitNonzero},
		{"job vanished", errIsolatedJobVanished, false, remotetool.ErrSandboxFailed},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := isolatedInvokeError("t", 30, tc.err, tc.timedOut, true, slog.Default())
			if got.Code != tc.want {
				t.Errorf("code = %v, want %v (message %q)", got.Code, tc.want, got.Message)
			}
		})
	}
}

type sweepKube struct {
	jobs     string
	listFail bool

	mu      sync.Mutex
	deleted []string
}

func (s *sweepKube) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	switch {
	case r.Method == http.MethodGet && strings.HasSuffix(r.URL.Path, "/jobs"):
		if s.listFail {
			w.WriteHeader(http.StatusForbidden)
			return
		}
		_, _ = w.Write([]byte(s.jobs))
	case r.Method == http.MethodDelete:
		s.mu.Lock()
		s.deleted = append(s.deleted, r.URL.Path[strings.LastIndex(r.URL.Path, "/")+1:])
		s.mu.Unlock()
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func newSweepBackend(t *testing.T, kube *sweepKube) *isolatedBackend {
	t.Helper()
	srv := httptest.NewServer(kube)
	t.Cleanup(srv.Close)
	return &isolatedBackend{
		cfg:    IsolatedConfig{Namespace: "sam"},
		kube:   &kubeClient{http: srv.Client(), baseURL: srv.URL},
		logger: slog.Default(),
	}
}

func TestSweepFinishedJobs_RemovesOnlyOldFinishedJobs(t *testing.T) {
	old := time.Now().Add(-2 * time.Hour).UTC().Format(time.RFC3339)
	recent := time.Now().UTC().Format(time.RFC3339)
	kube := &sweepKube{jobs: `{"items":[
		{"metadata":{"name":"old-complete"},"status":{"conditions":[{"type":"Complete","status":"True","lastTransitionTime":"` + old + `"}]}},
		{"metadata":{"name":"old-failed"},"status":{"conditions":[{"type":"Failed","status":"True","lastTransitionTime":"` + old + `"}]}},
		{"metadata":{"name":"old-succeeded"},"status":{"succeeded":1,"completionTime":"` + old + `"}},
		{"metadata":{"name":"recent"},"status":{"conditions":[{"type":"Complete","status":"True","lastTransitionTime":"` + recent + `"}]}},
		{"metadata":{"name":"running"},"status":{"active":1}}
	]}`}
	b := newSweepBackend(t, kube)

	b.sweepFinishedJobs(context.Background(), time.Hour)

	got := strings.Join(kube.deleted, ",")
	if got != "old-complete,old-failed,old-succeeded" {
		t.Errorf("deleted = %q, want the three old finished jobs", got)
	}
}

func TestSweepFinishedJobs_ListFailureDeletesNothing(t *testing.T) {
	kube := &sweepKube{listFail: true}
	b := newSweepBackend(t, kube)

	b.sweepFinishedJobs(context.Background(), time.Hour)

	if len(kube.deleted) != 0 {
		t.Errorf("deleted %v after the list failed", kube.deleted)
	}
}

func TestJobFinishedAt_RunningJobHasNoFinishTime(t *testing.T) {
	if _, ok := jobFinishedAt(&kubeJob{}); ok {
		t.Error("a job with no outcome reported a finish time")
	}
}
