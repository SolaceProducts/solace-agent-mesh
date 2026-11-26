// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package str

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a/remotetool"
)

const fakePodLog = `@@SAM_STATUS {"status":"working"}
@@SAM_RESULT_BEGIN
{"result": {"ok": true}, "error": ""}
@@SAM_RESULT_END
`

type fakeKube struct {
	mu        sync.Mutex
	requests  []string
	podLists  int
	jobExists bool
	jobFailed bool
	jobActive bool

	createDrops bool

	pods string

	podsThen string

	finishAfterPodLists int

	logFails bool
}

func (f *fakeKube) nextPods() string {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.podLists++
	if f.podLists > 1 && f.podsThen != "" {
		return f.podsThen
	}
	return f.pods
}

func (f *fakeKube) stillActive() bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.jobActive && (f.finishAfterPodLists == 0 || f.podLists < f.finishAfterPodLists)
}

func (f *fakeKube) record(r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.requests = append(f.requests, r.Method+" "+r.URL.Path)
}

func (f *fakeKube) saw(entry string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, r := range f.requests {
		if r == entry {
			return true
		}
	}
	return false
}

func (f *fakeKube) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.record(r)
	path := r.URL.Path
	switch {
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/jobs"):
		if f.createDrops {
			if hj, ok := w.(http.Hijacker); ok {
				if conn, _, err := hj.Hijack(); err == nil {
					_ = conn.Close()
				}
			}
			return
		}
		if f.jobExists {
			w.WriteHeader(http.StatusConflict)
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"name":"str-iso-abc","uid":"uid-1"}}`))
	case r.Method == http.MethodGet && strings.Contains(path, "/jobs/"):
		if f.stillActive() {
			_, _ = w.Write([]byte(`{"metadata":{"uid":"uid-1"},"status":{"active":1}}`))
			return
		}
		if f.jobFailed {
			_, _ = w.Write([]byte(`{"metadata":{"uid":"uid-1"},"status":{"conditions":[{"type":"Failed","status":"True","reason":"BackoffLimitExceeded","message":"Job has reached the specified backoff limit"}]}}`))
			return
		}
		_, _ = w.Write([]byte(`{"metadata":{"uid":"uid-1"},"status":{"succeeded":1,"conditions":[{"type":"Complete","status":"True"}]}}`))
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/pods"):
		if pods := f.nextPods(); pods != "" {
			_, _ = w.Write([]byte(pods))
			return
		}
		_, _ = w.Write([]byte(`{"items":[{"metadata":{"name":"pod-1","ownerReferences":[{"uid":"uid-1"}]},"status":{"phase":"Succeeded","containerStatuses":[{"name":"tool","state":{"terminated":{"exitCode":0,"startedAt":"2030-01-01T00:00:02Z"}}}]}}]}`))
	case r.Method == http.MethodGet && strings.HasSuffix(path, "/log"):
		if f.logFails {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if !strings.Contains(r.Header.Get("Accept"), "application/json") {
			w.WriteHeader(http.StatusNotAcceptable)
			_, _ = w.Write([]byte(`{"kind":"Status","reason":"NotAcceptable","code":406}`))
			return
		}
		w.Header().Set("Content-Type", "text/plain")
		_, _ = w.Write([]byte(fakePodLog))
	case r.Method == http.MethodPost && strings.HasSuffix(path, "/secrets"):
		w.WriteHeader(http.StatusCreated)
	case r.Method == http.MethodDelete:
		w.WriteHeader(http.StatusOK)
	default:
		w.WriteHeader(http.StatusNotImplemented)
	}
}

func newFakeBackend(t *testing.T, kube *fakeKube) *isolatedBackend {
	t.Helper()
	srv := httptest.NewServer(kube)
	t.Cleanup(srv.Close)
	return &isolatedBackend{
		cfg:              IsolatedConfig{Namespace: "sam", Image: "str:1"},
		kube:             &kubeClient{http: srv.Client(), baseURL: srv.URL},
		logger:           slog.Default(),
		pollInterval:     10 * time.Millisecond,
		fastPollInterval: 10 * time.Millisecond,
	}
}

func TestIsolatedRun_CreatesWaitsAndCleansUp(t *testing.T) {
	kube := &fakeKube{}
	b := newFakeBackend(t, kube)

	outcome, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if !strings.Contains(string(outcome.Result), `"ok": true`) {
		t.Errorf("result = %q", outcome.Result)
	}
	for _, want := range []string{
		"POST /apis/batch/v1/namespaces/sam/jobs",
		"POST /api/v1/namespaces/sam/secrets",
		"DELETE /apis/batch/v1/namespaces/sam/jobs/str-iso-abc",
		"DELETE /api/v1/namespaces/sam/secrets/str-iso-abc",
	} {
		if !kube.saw(want) {
			t.Errorf("missing request %q in %v", want, kube.requests)
		}
	}
}

func TestIsolatedRun_AdoptsExistingJobOnConflict(t *testing.T) {
	kube := &fakeKube{jobExists: true}
	b := newFakeBackend(t, kube)

	outcome, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	if err != nil {
		t.Fatalf("run: %v", err)
	}
	if len(outcome.Result) == 0 {
		t.Error("adopted job returned no result")
	}
	if !kube.saw("GET /apis/batch/v1/namespaces/sam/jobs/str-iso-abc") {
		t.Errorf("existing job was not fetched: %v", kube.requests)
	}
}

func TestIsolatedRun_ReportsFailedJob(t *testing.T) {
	kube := &fakeKube{jobFailed: true}
	b := newFakeBackend(t, kube)

	_, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	var failure *isolatedJobFailure
	if !errors.As(err, &failure) || failure.Reason != "BackoffLimitExceeded" {
		t.Fatalf("err = %v", err)
	}
	if !kube.saw("DELETE /apis/batch/v1/namespaces/sam/jobs/str-iso-abc") {
		t.Error("failed job was not deleted")
	}
}

func TestIsolatedRun_CancelDeletesJob(t *testing.T) {
	kube := &fakeKube{jobActive: true}
	b := newFakeBackend(t, kube)
	ctx, cancel := context.WithTimeout(context.Background(), 200*time.Millisecond)
	defer cancel()

	_, err := b.run(ctx, testIsolatedInvocation(), nil, slog.Default())
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v", err)
	}
	for _, want := range []string{
		"DELETE /apis/batch/v1/namespaces/sam/jobs/str-iso-abc",
		"DELETE /api/v1/namespaces/sam/secrets/str-iso-abc",
	} {
		if !kube.saw(want) {
			t.Errorf("missing request %q in %v", want, kube.requests)
		}
	}
}

func TestIsolatedRun_PodThatNeverStartsFailsTheCall(t *testing.T) {
	kube := &fakeKube{
		jobActive: true,
		pods:      `{"items":[{"metadata":{"name":"pod-1","ownerReferences":[{"uid":"uid-1"}]},"status":{"phase":"Pending","conditions":[{"type":"PodScheduled","status":"False","reason":"Unschedulable","message":"0/3 nodes are available"}]}}]}`,
	}
	b := newFakeBackend(t, kube)
	b.cfg.StartTimeout = 50 * time.Millisecond

	_, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	var failure *isolatedJobFailure
	if !errors.As(err, &failure) || !failure.Startup {
		t.Fatalf("err = %v, want a startup failure", err)
	}
	if !strings.Contains(failure.Detail, "Unschedulable") || !strings.Contains(failure.Detail, "0/3 nodes") {
		t.Errorf("detail = %q, want the scheduler's reason", failure.Detail)
	}
	if isolatedTimedOut(err) {
		t.Error("a pod that never started was reported as a tool timeout")
	}
	if !kube.saw("DELETE /apis/batch/v1/namespaces/sam/jobs/str-iso-abc") {
		t.Error("the job of a pod that never started was not deleted")
	}
}

func TestIsolatedRun_ImagePullFailureFailsTheCall(t *testing.T) {
	kube := &fakeKube{
		jobActive: true,
		pods:      `{"items":[{"metadata":{"name":"pod-1","ownerReferences":[{"uid":"uid-1"}]},"status":{"phase":"Pending","containerStatuses":[{"name":"tool","state":{"waiting":{"reason":"ImagePullBackOff","message":"Back-off pulling image"}}}]}}]}`,
	}
	b := newFakeBackend(t, kube)

	_, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	var failure *isolatedJobFailure
	if !errors.As(err, &failure) || !failure.Startup {
		t.Fatalf("err = %v, want a startup failure", err)
	}
	if !strings.Contains(failure.Detail, "ImagePullBackOff") {
		t.Errorf("detail = %q, want the waiting reason", failure.Detail)
	}
}

func TestIsolatedRun_ToolPastItsTimeoutIsEnded(t *testing.T) {
	kube := &fakeKube{
		jobActive: true,
		pods:      `{"items":[{"metadata":{"name":"pod-1","ownerReferences":[{"uid":"uid-1"}]},"status":{"phase":"Running","containerStatuses":[{"name":"tool","state":{"running":{"startedAt":"2020-01-01T00:00:00Z"}}}]}}]}`,
	}
	b := newFakeBackend(t, kube)

	_, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	if !isolatedTimedOut(err) {
		t.Fatalf("err = %v, want a timeout", err)
	}
	if !kube.saw("DELETE /apis/batch/v1/namespaces/sam/jobs/str-iso-abc") {
		t.Error("the job of a tool that ran past its timeout was not deleted")
	}
}

func TestIsolatedRun_PodOfAnEarlierJobIsNotRead(t *testing.T) {
	kube := &fakeKube{
		jobActive: true,
		pods:      `{"items":[{"metadata":{"name":"pod-0","ownerReferences":[{"uid":"uid-0"}]},"status":{"phase":"Running","containerStatuses":[{"name":"tool","state":{"running":{"startedAt":"2020-01-01T00:00:00Z"}}}]}}]}`,
	}
	b := newFakeBackend(t, kube)
	b.cfg.StartTimeout = 50 * time.Millisecond

	_, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	if isolatedTimedOut(err) {
		t.Fatal("the pod of an earlier job was read as this job's and timed the call out")
	}
	var failure *isolatedJobFailure
	if !errors.As(err, &failure) || !failure.Startup {
		t.Fatalf("err = %v, want a startup failure: this job has no pod", err)
	}
	if kube.saw("GET /api/v1/namespaces/sam/pods/pod-0/log") {
		t.Error("the log of the earlier job's pod was read")
	}
}

func TestIsolatedRun_ToolThatEndsAtItsDeadlineKeepsItsResult(t *testing.T) {
	kube := &fakeKube{
		jobActive:           true,
		finishAfterPodLists: 2,
		pods:                `{"items":[{"metadata":{"name":"pod-1","ownerReferences":[{"uid":"uid-1"}]},"status":{"phase":"Running","containerStatuses":[{"name":"tool","state":{"running":{"startedAt":"2020-01-01T00:00:00Z"}}}]}}]}`,
		podsThen:            `{"items":[{"metadata":{"name":"pod-1","ownerReferences":[{"uid":"uid-1"}]},"status":{"phase":"Succeeded","containerStatuses":[{"name":"tool","state":{"terminated":{"exitCode":0,"startedAt":"2020-01-01T00:00:00Z"}}}]}}]}`,
	}
	b := newFakeBackend(t, kube)

	outcome, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	if err != nil {
		t.Fatalf("run: %v, want the result of a tool that ended in time", err)
	}
	if !strings.Contains(string(outcome.Result), `"ok": true`) {
		t.Errorf("result = %q", outcome.Result)
	}
}

func TestIsolatedRun_CreateWithoutAnAnswerStillCleansUp(t *testing.T) {
	kube := &fakeKube{createDrops: true}
	b := newFakeBackend(t, kube)

	if _, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default()); err == nil {
		t.Fatal("run succeeded although the job could not be created")
	}
	if !kube.saw("DELETE /apis/batch/v1/namespaces/sam/jobs/str-iso-abc") {
		t.Errorf("the job a failed create may have left was not deleted: %v", kube.requests)
	}
}

func TestIsolatedRun_FinishedJobWhoseLogCannotBeReadFailsTheCall(t *testing.T) {
	kube := &fakeKube{logFails: true}
	b := newFakeBackend(t, kube)

	_, err := b.run(context.Background(), testIsolatedInvocation(), nil, slog.Default())
	if !errors.Is(err, errIsolatedResultUnread) {
		t.Fatalf("err = %v, want %v", err, errIsolatedResultUnread)
	}
	got := isolatedInvokeError("t", 30, err, isolatedTimedOut(err), true, slog.Default())
	if got.Code != remotetool.ErrSandboxFailed {
		t.Errorf("code = %v, want %v", got.Code, remotetool.ErrSandboxFailed)
	}
}

func TestIsolatedInvokeError_APIDeadlineIsNotAToolTimeout(t *testing.T) {
	err := fmt.Errorf("create job: %w", context.DeadlineExceeded)
	if isolatedTimedOut(err) {
		t.Fatal("an API request deadline was classed as a tool timeout")
	}
	got := isolatedInvokeError("t", 30, err, isolatedTimedOut(err), true, slog.Default())
	if got.Code != remotetool.ErrSandboxFailed {
		t.Errorf("code = %v, want %v", got.Code, remotetool.ErrSandboxFailed)
	}
}

func TestDescribeJobFailure_PodEndedByKubernetesIsNotAToolFailure(t *testing.T) {
	var pod kubePod
	pod.Status.Phase = "Failed"
	pod.Status.Reason = "Evicted"
	pod.Status.Message = "The node was low on resource: memory."
	cond := &kubeJobCondition{Type: "Failed", Status: "True", Reason: "BackoffLimitExceeded"}

	failure := describeJobFailure(cond, &pod, newIsolatedLogParser(nil, nil, slog.Default()))
	if !failure.PodLost || !strings.Contains(failure.Detail, "Evicted") {
		t.Fatalf("failure = %+v, want a lost pod with the eviction as its detail", failure)
	}
	got := isolatedInvokeError("t", 30, failure, false, true, slog.Default())
	if got.Code != remotetool.ErrSandboxFailed {
		t.Errorf("code = %v, want %v", got.Code, remotetool.ErrSandboxFailed)
	}
}

func TestIsolatedLogParser_SkipsStatusesAlreadyRelayed(t *testing.T) {
	var relayed []string
	p := newIsolatedLogParser(func(s string) { relayed = append(relayed, s) }, nil, slog.Default())
	p.skipStatus = 1
	p.consume(strings.NewReader("@@SAM_STATUS {\"status\":\"one\"}\n@@SAM_STATUS {\"status\":\"two\"}\n"))

	if p.statusLines != 2 {
		t.Errorf("statusLines = %d, want 2", p.statusLines)
	}
	if len(relayed) != 1 || !strings.Contains(relayed[0], "two") {
		t.Errorf("relayed = %v, want only the second status", relayed)
	}
}
