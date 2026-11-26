// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/control"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

type testOpArgs struct {
	Kind   string
	Object string
	Fail   string
}

func (a testOpArgs) key() objectKey {
	if a.Kind == "" {
		return agentObject(a.Object)
	}
	return objectKey{a.Kind, a.Object}
}

func testDeployLeader(t *testing.T, db *store.DB, run func(context.Context, testOpArgs, string) (string, error)) *deployLeader {
	t.Helper()
	l := newDeployLeader(store.NewDeployRequestRepo(db), store.NewDeploymentIntentRepo(db), slog.New(slog.DiscardHandler))
	l.poll, l.check = 20*time.Millisecond, 10*time.Millisecond
	handleOp(l, "test.op", testOpArgs.key, run)
	return l
}

func lead(t *testing.T, l *deployLeader) (stop func()) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		_ = l.serve(ctx, leader.Term{Number: 1})
	}()
	var once sync.Once
	stop = func() {
		once.Do(func() {
			cancel()
			<-done
		})
	}
	t.Cleanup(stop)
	return stop
}

func forwardTestOp(ctx context.Context, l *deployLeader, args testOpArgs) (string, error) {
	return forwardOp(ctx, l, "test.op", args, nil, func(_ context.Context, id string) (string, error) {
		return id, nil
	}, nil)
}

func failingOp(_ context.Context, a testOpArgs, _ string) (string, error) {
	switch a.Fail {
	case "not_found":
		return "", &NotFoundError{Resource: "agent", ID: a.Object}
	case "validation":
		return "", &ValidationError{Message: "bad", Field: "agentId"}
	case "runtime":
		return a.Object, &runtimeError{errors.New("AWE refused")}
	case "panic":
		panic("broken operation")
	}
	return a.Object, nil
}

func waitUntil(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting until %s", what)
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func waitingRequests(t *testing.T, l *deployLeader, kind string) []*store.DeployRequest {
	t.Helper()
	waiting, err := l.requests.Waiting(t.Context(), kind, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	return waiting
}

type recorder struct {
	mu  sync.Mutex
	ran []string
}

func (r *recorder) add(object string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.ran = append(r.ran, object)
}

func (r *recorder) got() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.ran)
}

func TestDeployLeader_ErrorsKeepTheirType(t *testing.T) {
	t.Parallel()
	l := testDeployLeader(t, testOpenDB(t), failingOp)
	lead(t, l)
	ctx := t.Context()

	if _, err := forwardTestOp(ctx, l, testOpArgs{Object: idgen.New(), Fail: "not_found"}); !errors.As(err, new(*NotFoundError)) {
		t.Errorf("not found arrived as %T", err)
	}
	var validation *ValidationError
	if _, err := forwardTestOp(ctx, l, testOpArgs{Object: idgen.New(), Fail: "validation"}); !errors.As(err, &validation) || validation.Field != "agentId" {
		t.Errorf("validation arrived as %#v", err)
	}
	recorded := idgen.New()
	id, err := forwardTestOp(ctx, l, testOpArgs{Object: recorded, Fail: "runtime"})
	if !errors.As(err, new(*runtimeError)) || id != recorded {
		t.Errorf("a runtime failure arrived as (%q, %T), want the recorded deployment and a runtime error", id, err)
	}
	if _, err := forwardTestOp(ctx, l, testOpArgs{Object: idgen.New(), Fail: "panic"}); err == nil {
		t.Error("a panicking operation was answered as a success")
	}
	after := idgen.New()
	if got, err := forwardTestOp(ctx, l, testOpArgs{Object: after}); err != nil || got != after {
		t.Errorf("after a panic got (%q, %v), want the lane still running", got, err)
	}
	if n := len(waitingRequests(t, l, store.IntentKindAgent)); n != 0 {
		t.Errorf("%d requests left waiting, want every one answered", n)
	}
}

func TestDeployLeader_RunsOneKindInOrder(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	var ran recorder
	l := testDeployLeader(t, testOpenDB(t), func(ctx context.Context, a testOpArgs, _ string) (string, error) {
		if a.Fail == "block" {
			select {
			case <-release:
			case <-ctx.Done():
				return "", ctx.Err()
			}
		}
		ran.add(a.Object)
		return a.Object, nil
	})
	lead(t, l)
	ctx := t.Context()
	a, b, w := idgen.New(), idgen.New(), idgen.New()

	answers := make(chan string, 3)
	go func() {
		got, _ := forwardTestOp(ctx, l, testOpArgs{Object: a, Fail: "block"})
		answers <- got
	}()
	waitUntil(t, "the first request is taken", func() bool {
		waiting := waitingRequests(t, l, store.IntentKindAgent)
		return len(waiting) == 1 && waiting[0].Taken()
	})
	for i, object := range []string{a, b} {
		time.Sleep(5 * time.Millisecond)
		go func() {
			got, _ := forwardTestOp(ctx, l, testOpArgs{Object: object})
			answers <- got
		}()
		waitUntil(t, "the next request waits", func() bool { return len(waitingRequests(t, l, store.IntentKindAgent)) == i+2 })
	}
	if got, err := forwardTestOp(ctx, l, testOpArgs{Kind: store.IntentKindWorkflow, Object: w}); err != nil || got != w {
		t.Fatalf("a request of another kind got (%q, %v), want it run while the agent lane is busy", got, err)
	}
	close(release)
	for range 3 {
		<-answers
	}
	if got, want := ran.got(), []string{w, a, a, b}; !slices.Equal(got, want) {
		t.Fatalf("ran %v, want %v: the workflow first, then the agent requests oldest first", got, want)
	}
}

func TestDeployLeader_RequestWaitsForALeader(t *testing.T) {
	t.Parallel()
	l := testDeployLeader(t, testOpenDB(t), failingOp)
	answered := make(chan error, 1)
	go func() {
		_, err := forwardTestOp(t.Context(), l, testOpArgs{Object: idgen.New()})
		answered <- err
	}()
	waitUntil(t, "the request is written", func() bool { return len(waitingRequests(t, l, store.IntentKindAgent)) == 1 })
	lead(t, l)
	if err := <-answered; err != nil {
		t.Fatalf("got %v, want the request run by the leader that started", err)
	}
}

func TestDeployLeader_NoLeaderAnswersTryAgain(t *testing.T) {
	t.Parallel()
	var runs atomic.Int32
	l := testDeployLeader(t, testOpenDB(t), func(context.Context, testOpArgs, string) (string, error) {
		runs.Add(1)
		return "", nil
	})
	l.result = 100 * time.Millisecond
	if _, err := forwardTestOp(t.Context(), l, testOpArgs{Object: idgen.New()}); !errors.Is(err, errNoLeader) {
		t.Fatalf("got %v, want the no-leader answer", err)
	}
	if n := len(waitingRequests(t, l, store.IntentKindAgent)); n != 0 {
		t.Fatalf("%d requests left, want the withdrawn one gone", n)
	}
	lead(t, l)
	time.Sleep(100 * time.Millisecond)
	if runs.Load() != 0 {
		t.Fatal("a withdrawn request ran")
	}
}

func TestDeployLeader_TakenRequestIsNotTryAgain(t *testing.T) {
	t.Parallel()
	release := make(chan struct{})
	finished := make(chan struct{})
	l := testDeployLeader(t, testOpenDB(t), func(ctx context.Context, a testOpArgs, _ string) (string, error) {
		select {
		case <-release:
		case <-ctx.Done():
			return "", ctx.Err()
		}
		close(finished)
		return a.Object, nil
	})
	l.result = 200 * time.Millisecond
	lead(t, l)
	_, err := forwardOp(t.Context(), l, "test.op", testOpArgs{Object: idgen.New()}, nil, func(_ context.Context, id string) (string, error) {
		return "", &NotFoundError{Resource: "deployment", ID: id}
	}, nil)
	if errors.Is(err, errNoLeader) || !errors.As(err, new(*UnavailableError)) {
		t.Fatalf("got %v, want the not-finished answer", err)
	}
	close(release)
	<-finished
	waitUntil(t, "the taken request is answered", func() bool { return len(waitingRequests(t, l, store.IntentKindAgent)) == 0 })
}

func TestDeployLeader_TermEndLeavesTheRequestToTheNextLeader(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	started := make(chan struct{}, 1)
	first := testDeployLeader(t, db, func(ctx context.Context, _ testOpArgs, _ string) (string, error) {
		started <- struct{}{}
		<-ctx.Done()
		return "", fmt.Errorf("send to AWE: %w", ctx.Err())
	})
	stopFirst := lead(t, first)
	next := testDeployLeader(t, db, func(context.Context, testOpArgs, string) (string, error) {
		return "finished by the next leader", nil
	})
	go func() {
		<-started
		stopFirst()
		lead(t, next)
	}()
	got, err := forwardTestOp(t.Context(), first, testOpArgs{Object: idgen.New()})
	if err != nil || got != "finished by the next leader" {
		t.Fatalf("got (%q, %v), want the next leader's answer", got, err)
	}
}

func TestDeployLeader_ALeftRequestHoldsItsObject(t *testing.T) {
	t.Parallel()
	var otherLeaderRuns atomic.Bool
	otherLeaderRuns.Store(true)
	var ran recorder
	l := testDeployLeader(t, testOpenDB(t), func(_ context.Context, a testOpArgs, _ string) (string, error) {
		if a.Fail == "other_leader" && otherLeaderRuns.Load() {
			return "", errOtherLeader
		}
		ran.add(a.Object)
		return a.Object, nil
	})
	ctx := t.Context()
	a, b := idgen.New(), idgen.New()
	answers := make(chan string, 3)
	for i, args := range []testOpArgs{{Object: a, Fail: "other_leader"}, {Object: a}, {Object: b}} {
		time.Sleep(5 * time.Millisecond)
		go func() {
			got, _ := forwardTestOp(ctx, l, args)
			answers <- got
		}()
		waitUntil(t, "the request is written", func() bool { return len(waitingRequests(t, l, store.IntentKindAgent)) == i+1 })
	}
	lead(t, l)
	if got := <-answers; got != b {
		t.Fatalf("first answer %q, want %s, the only request not held", got, b)
	}
	time.Sleep(100 * time.Millisecond)
	if got := ran.got(); !slices.Equal(got, []string{b}) {
		t.Fatalf("ran %v while the other leader runs a's first request, want only b", got)
	}
	otherLeaderRuns.Store(false)
	<-answers
	<-answers
	if got, want := ran.got(), []string{b, a, a}; !slices.Equal(got, want) {
		t.Fatalf("ran %v, want %v: a's requests in order once the first is no longer held", got, want)
	}
}

func TestDeployLeader_RecordedRequestIsNotRunAgain(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	var runs atomic.Int32
	l := testDeployLeader(t, h.db, func(context.Context, testOpArgs, string) (string, error) {
		runs.Add(1)
		return "", nil
	})
	agentID := h.createAgent("recorded_request", "prompt")
	first := h.deploy(agentID)
	if err := l.requests.Add(t.Context(), first.ID, store.IntentKindAgent, agentID, "test.op", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	lead(t, l)
	waitUntil(t, "the request is answered", func() bool {
		req, _ := l.requests.Get(t.Context(), first.ID)
		return req != nil && req.Done()
	})
	req, _ := l.requests.Get(t.Context(), first.ID)
	var answer requestAnswer
	if err := json.Unmarshal([]byte(*req.Answer), &answer); err != nil || answer.DeploymentID != first.ID || answer.Error != nil {
		t.Fatalf("answer = %+v, %v, want the recorded deployment", answer, err)
	}
	if runs.Load() != 0 {
		t.Fatal("the recorded request ran again")
	}
}

func TestDeployRequests_WindowAndSweep(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	requests := store.NewDeployRequestRepo(db)
	ctx := t.Context()
	fresh, late, stale := idgen.New(), idgen.New(), idgen.New()
	for _, id := range []string{fresh, late, stale} {
		if err := requests.Add(ctx, id, store.IntentKindAgent, idgen.New(), "test.op", []byte("{}")); err != nil {
			t.Fatal(err)
		}
	}
	for id, age := range map[string]time.Duration{late: requestStartWindow + time.Minute, stale: staleRequestAge + time.Minute} {
		if _, err := db.ExecContext(ctx, `UPDATE deployment_requests SET created_time = created_time - ? WHERE id = ?`, age.Milliseconds(), id); err != nil {
			t.Fatal(err)
		}
	}
	waiting, err := requests.Waiting(ctx, store.IntentKindAgent, requestStartWindow)
	if err != nil || len(waiting) != 1 || waiting[0].ID != fresh {
		t.Fatalf("waiting = %+v, %v, want only the fresh request", waiting, err)
	}
	if found, anyErr := requests.AnyWaiting(ctx, requestStartWindow); anyErr != nil || !found {
		t.Fatalf("any waiting = %v, %v, want true", found, anyErr)
	}
	deleted, err := requests.DeleteOlderThan(ctx, staleRequestAge)
	if err != nil || deleted != 1 {
		t.Fatalf("deleted %d, %v, want only the stale request", deleted, err)
	}
	for id, kept := range map[string]bool{fresh: true, late: true, stale: false} {
		if req, _ := requests.Get(ctx, id); (req != nil) != kept {
			t.Errorf("request %s kept = %v, want %v", id, req != nil, kept)
		}
	}
}

func TestDeployRequests_TakeWithdrawAnswer(t *testing.T) {
	t.Parallel()
	requests := store.NewDeployRequestRepo(testOpenDB(t))
	ctx := t.Context()
	taken, free := idgen.New(), idgen.New()
	for _, id := range []string{taken, free} {
		if err := requests.Add(ctx, id, store.IntentKindAgent, idgen.New(), "test.op", []byte(`{"a":1}`)); err != nil {
			t.Fatal(err)
		}
	}
	if ok, err := requests.Take(ctx, taken); err != nil || !ok {
		t.Fatalf("take = %v, %v", ok, err)
	}
	if ok, err := requests.Withdraw(ctx, taken); err != nil || ok {
		t.Fatalf("withdraw of a taken request = %v, %v, want refused", ok, err)
	}
	if ok, err := requests.Withdraw(ctx, free); err != nil || !ok {
		t.Fatalf("withdraw of a free request = %v, %v, want done", ok, err)
	}
	req, err := requests.Get(ctx, taken)
	if err != nil || req == nil || !req.Taken() || req.Done() || req.Op != "test.op" || req.Args != `{"a":1}` {
		t.Fatalf("request = %+v, %v", req, err)
	}
	if err = requests.Answer(ctx, req, `{"deploymentId":"first"}`); err != nil {
		t.Fatal(err)
	}
	if err = requests.Answer(ctx, req, `{"deploymentId":"second"}`); err != nil {
		t.Fatal(err)
	}
	if ok, takeErr := requests.Take(ctx, taken); takeErr != nil || ok {
		t.Fatalf("take of an answered request = %v, %v, want refused", ok, takeErr)
	}
	req, err = requests.Get(ctx, taken)
	if err != nil || req == nil || !req.Done() || req.Answer == nil || *req.Answer != `{"deploymentId":"first"}` {
		t.Fatalf("request = %+v, %v, want the first answer kept", req, err)
	}
}

func TestFinishInFlight_RecordsADeployItsLeaderDidNot(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	agentID := h.createAgent("finish_in_flight", "prompt")
	first := h.deploy(agentID)
	instance := h.agentInstanceName(agentID)
	h.waitForInstance(instance, 5*time.Second)

	row, err := h.deployRepo.GetLatestByAgentID(h.ctx, agentID)
	if err != nil || row == nil || row.AgentConfigSnapshot == nil {
		t.Fatalf("deployed row: %+v, %v", row, err)
	}
	payloads, err := h.deploySvc.deployers.Lookup(AgentTypeStandard).ReplayFromSnapshot(*row.AgentConfigSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	unrecorded := *row
	unrecorded.ID, unrecorded.Action, unrecorded.Status = idgen.New(), DeployActionUpdate, ""
	intents := store.NewDeploymentIntentRepo(h.db)
	put := func(d store.AgentDeployment) {
		body, _ := json.Marshal(agentIntent{Deployment: d, Payloads: payloads})
		if putErr := intents.Put(h.ctx, &store.DeploymentIntent{ID: d.ID, Kind: store.IntentKindAgent, ObjectID: agentID, Payload: string(body)}); putErr != nil {
			t.Fatal(putErr)
		}
	}
	put(*row)
	put(unrecorded)
	if err = intents.Put(h.ctx, &store.DeploymentIntent{ID: idgen.New(), Kind: store.IntentKindAgent, ObjectID: "unreadable", Payload: "{"}); err != nil {
		t.Fatal(err)
	}
	gone := store.AgentDeployment{ID: idgen.New(), AgentID: idgen.New(), Action: DeployActionDeploy}
	goneBody, _ := json.Marshal(agentIntent{Deployment: gone})
	if err = intents.Put(h.ctx, &store.DeploymentIntent{ID: gone.ID, Kind: store.IntentKindAgent, ObjectID: gone.AgentID, Payload: string(goneBody)}); err != nil {
		t.Fatal(err)
	}

	l := newDeployLeader(store.NewDeployRequestRepo(h.db), intents, slog.Default())
	finishInFlight(h.ctx, l, h.deploySvc, nil, nil)

	recorded, err := h.deployRepo.GetByID(h.ctx, unrecorded.ID)
	if err != nil || recorded == nil || recorded.Status != DeployStatusSuccess {
		t.Fatalf("the deploy in flight was not recorded: %+v, %v", recorded, err)
	}
	if left, _ := intents.List(h.ctx, store.IntentKindAgent); len(left) != 1 || left[0].ObjectID != "unreadable" {
		t.Fatalf("operations still in flight = %+v, want only the unreadable one, kept for the next term", left)
	}
	if again, _ := h.deployRepo.GetByID(h.ctx, first.ID); again == nil || again.Status != DeployStatusSuccess {
		t.Fatal("the recorded deploy was changed")
	}
	h.waitForInstance(instance, 5*time.Second)
}

func TestFinishInFlight_SendsTheNewerOfTwoLeftOnOneAgent(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	agentID := h.createAgent("finish_newer_of_two", "prompt")
	h.deploy(agentID)
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)
	row, err := h.deployRepo.GetLatestByAgentID(h.ctx, agentID)
	if err != nil || row == nil || row.AgentConfigSnapshot == nil {
		t.Fatalf("deployed row: %+v, %v", row, err)
	}
	payloads, err := h.deploySvc.deployers.Lookup(AgentTypeStandard).ReplayFromSnapshot(*row.AgentConfigSnapshot)
	if err != nil {
		t.Fatal(err)
	}
	intents := store.NewDeploymentIntentRepo(h.db)
	left := func() store.AgentDeployment {
		d := *row
		d.ID, d.Action, d.Status = idgen.New(), DeployActionUpdate, ""
		body, _ := json.Marshal(agentIntent{Deployment: d, Payloads: payloads})
		if err := intents.Put(h.ctx, &store.DeploymentIntent{ID: d.ID, Kind: store.IntentKindAgent, ObjectID: agentID, Payload: string(body)}); err != nil {
			t.Fatal(err)
		}
		return d
	}
	older, newer := left(), left()

	finishInFlight(h.ctx, newDeployLeader(store.NewDeployRequestRepo(h.db), intents, slog.Default()), h.deploySvc, nil, nil)

	h.waitForAWEsToRun(h.agentInstanceName(agentID), newer.ID)
	if recorded, _ := h.deployRepo.GetByID(h.ctx, newer.ID); recorded == nil || recorded.Status != DeployStatusSuccess {
		t.Fatalf("the newer deploy is recorded as %+v, want success", recorded)
	}
	if recorded, _ := h.deployRepo.GetByID(h.ctx, older.ID); recorded != nil {
		t.Fatalf("the older deploy left a row: %+v", recorded)
	}
	if inFlight, _ := intents.List(h.ctx, store.IntentKindAgent); len(inFlight) != 0 {
		t.Fatalf("operations still in flight = %+v", inFlight)
	}
}

func TestFinishKind_SkipsALeftOperationANewerOneReplaced(t *testing.T) {
	t.Parallel()
	ctx := t.Context()
	intents := store.NewDeploymentIntentRepo(testOpenDB(t))
	l := newDeployLeader(nil, intents, slog.New(slog.DiscardHandler))
	put := func(id, object string) {
		if err := intents.Put(ctx, &store.DeploymentIntent{ID: id, Kind: store.IntentKindAgent, ObjectID: object, Payload: id}); err != nil {
			t.Fatal(err)
		}
	}
	other, left := idgen.New(), idgen.New()
	put(other, "b")
	put(left, "a")

	var finished []string
	l.finishers[store.IntentKindAgent] = func(ctx context.Context, payload []byte) error {
		finished = append(finished, string(payload))
		if string(payload) == other {
			newer := idgen.New()
			if err := l.begin(ctx, store.IntentKindAgent, "a", newer, nil); err != nil {
				t.Error(err)
			}
			l.end(ctx, newer)
		}
		return nil
	}
	l.finishKind(ctx, store.IntentKindAgent)
	if len(finished) != 1 || finished[0] != other {
		t.Fatalf("finished %v, want only %s: the left deploy of a was replaced", finished, other)
	}
}

func TestReconcilerDesiredAgents_InFlightOverRows(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	agentID := h.createAgent("desired_in_flight", "prompt")
	h.deploy(agentID)

	intents := store.NewDeploymentIntentRepo(h.db)
	pending := store.AgentDeployment{ID: idgen.New(), AgentID: agentID, Action: DeployActionUndeploy}
	body, _ := json.Marshal(agentIntent{Deployment: pending})
	if err := intents.Put(h.ctx, &store.DeploymentIntent{ID: pending.ID, Kind: store.IntentKindAgent, ObjectID: agentID, Payload: string(body)}); err != nil {
		t.Fatal(err)
	}
	r := newWorkflowReconciler(t, h)
	r.deploys = newDeployLeader(store.NewDeployRequestRepo(h.db), intents, slog.Default())
	listed, err := r.desiredAWEAgents(h.ctx)
	if err != nil {
		t.Fatal(err)
	}
	instance := h.agentInstanceName(agentID)
	i := slices.IndexFunc(listed, func(a control.AgentApply) bool { return a.Name == instance })
	if i < 0 || listed[i].DeploymentID != pending.ID || !listed[i].Undeploy {
		t.Fatalf("answer %+v, want %s listed as the undeploy in flight %s", listed, instance, pending.ID)
	}
}

func TestFinishInFlight_RecordsWorkflowAndEntrypointDeploys(t *testing.T) {
	t.Parallel()
	h := setupE2EWithGWE(t, nil)
	intents := store.NewDeploymentIntentRepo(h.db)
	put := func(kind, objectID, id string, payload any) {
		body, _ := json.Marshal(payload)
		if err := intents.Put(h.ctx, &store.DeploymentIntent{ID: id, Kind: kind, ObjectID: objectID, Payload: string(body)}); err != nil {
			t.Fatal(err)
		}
	}

	wfID := h.createWorkflow("finish_wf", map[string]any{
		"agent_type": "workflow",
		"namespace":  "${NAMESPACE, solace-agent-mesh}",
		"workflow": map[string]any{
			"output_mapping": map[string]any{"result": "{{start.output}}"},
			"nodes":          []any{map[string]any{"id": "start", "name": "start", "type": "agent", "agent_name": "test"}},
		},
	})
	h.deployWorkflow(wfID)
	wfRow, err := h.wfDeployRepo.GetLatestByWorkflowID(h.ctx, wfID)
	if err != nil || wfRow == nil || wfRow.WorkflowConfigSnapshot == nil {
		t.Fatalf("workflow row: %+v, %v", wfRow, err)
	}
	wfPending := store.WorkflowDeployment{ID: idgen.New(), WorkflowID: wfID, Action: DeployActionUpdate, CreatedBy: "e2e-user"}
	put(store.IntentKindWorkflow, wfID, wfPending.ID, workflowIntent{Deployment: wfPending, YAML: *wfRow.WorkflowConfigSnapshot})

	gwID := h.createGateway("finish_gw")
	h.deployGateway(gwID)
	applies, err := DeployedEntrypoints(h.ctx, h.db, h.yamlSvc, slog.Default())
	if err != nil || len(applies) != 1 {
		t.Fatalf("deployed entrypoints: %+v, %v", applies, err)
	}
	gwPending := store.GatewayDeployment{ID: idgen.New(), GatewayID: gwID, Action: DeployActionUpdate, CreatedBy: "e2e-user"}
	put(store.IntentKindEntrypoint, gwID, gwPending.ID, entrypointIntent{Deployment: gwPending,
		Payloads: []ControlPayload{{"name": applies[0].Name, "app_config": applies[0].AppConfig}}})

	l := newDeployLeader(store.NewDeployRequestRepo(h.db), intents, slog.Default())
	finishInFlight(h.ctx, l, nil, h.wfDeploySvc, h.gwDeploySvc)

	if recorded, err := h.wfDeployRepo.GetByID(h.ctx, wfPending.ID); err != nil || recorded == nil || recorded.Status != DeployStatusSuccess {
		t.Fatalf("the workflow deploy in flight was not recorded: %+v, %v", recorded, err)
	}
	if recorded, err := h.gwDeployRepo.GetByID(h.ctx, gwPending.ID); err != nil || recorded == nil || recorded.Status != DeployStatusSuccess {
		t.Fatalf("the entrypoint deploy in flight was not recorded: %+v, %v", recorded, err)
	}
	for _, kind := range []string{store.IntentKindWorkflow, store.IntentKindEntrypoint} {
		if left, _ := intents.List(h.ctx, kind); len(left) != 0 {
			t.Fatalf("%d %s operations still in flight, want none", len(left), kind)
		}
	}
}
