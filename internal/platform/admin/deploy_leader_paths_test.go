// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"errors"
	"log/slog"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/leader"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
)

func TestE2E_NextLeaderFinishesATakenAgentDeploy(t *testing.T) {
	t.Parallel()
	h := setupE2E(t, nil)
	requests, intents := store.NewDeployRequestRepo(h.db), store.NewDeploymentIntentRepo(h.db)
	newLeader := func() *deployLeader {
		l := newDeployLeader(requests, intents, slog.Default())
		l.poll, l.check = 20*time.Millisecond, 20*time.Millisecond
		serveDeploys(l, h.deploySvc, h.wfDeploySvc, nil)
		return l
	}
	first := newLeader()
	stopFirst := lead(t, first)
	agentID := h.createAgent("next_leader_finishes", "prompt")

	stalled := h.stallNextRollouts(1, 3*time.Second)
	type outcome struct {
		resp *AgentDeploymentResponse
		err  error
	}
	deployed := make(chan outcome, 1)
	go func() {
		resp, err := h.deploySvc.Create(h.ctx, CreateAgentDeploymentRequest{AgentID: agentID, Action: DeployActionDeploy}, "e2e-user")
		deployed <- outcome{resp, err}
	}()
	<-stalled
	stopFirst()
	if inFlight, _ := intents.List(h.ctx, store.IntentKindAgent); len(inFlight) != 1 {
		t.Fatalf("operations in flight after the leader stopped = %d, want the deploy", len(inFlight))
	}
	lead(t, newLeader())

	got := <-deployed
	if got.err != nil || got.resp == nil || got.resp.Status != DeployStatusSuccess {
		t.Fatalf("deploy = %+v, %v, want success", got.resp, got.err)
	}
	h.waitForInstance(h.agentInstanceName(agentID), 5*time.Second)
	requireOneRow(t, h, `SELECT COUNT(*) FROM agent_deployments WHERE agent_id = ?`, agentID)
}

func TestDeployLeader_ALeftOperationANewerOneReplacedIsAnswered(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	l := testDeployLeader(t, db, failingOp)
	var sent []string
	l.finishers[store.IntentKindAgent] = func(_ context.Context, payload []byte) error {
		sent = append(sent, string(payload))
		return nil
	}
	ctx := t.Context()
	object, left, newer := idgen.New(), idgen.New(), idgen.New()
	if err := l.requests.Add(ctx, left, store.IntentKindAgent, object, "test.op", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{left, newer} {
		if err := l.intents.Put(ctx, &store.DeploymentIntent{ID: id, Kind: store.IntentKindAgent, ObjectID: object, Payload: id}); err != nil {
			t.Fatal(err)
		}
		time.Sleep(2 * time.Millisecond)
	}
	req, err := l.requests.Get(ctx, left)
	if err != nil || req == nil {
		t.Fatalf("request = %+v, %v", req, err)
	}
	if done := l.runRequest(ctx, req); !done {
		t.Fatal("the replaced request was left waiting")
	}
	if len(sent) != 0 {
		t.Fatalf("sent %v, want the replaced operation dropped unsent", sent)
	}
	_, answerErr := l.readAnswer(ctx, mustGet(t, l, left))
	if !errors.As(answerErr, new(*ConflictError)) {
		t.Fatalf("answer = %v, want the replaced conflict", answerErr)
	}
	if inFlight, _ := l.intents.InFlight(ctx, left); inFlight {
		t.Fatal("the replaced operation is still in flight")
	}
}

func mustGet(t *testing.T, l *deployLeader, id string) *store.DeployRequest {
	t.Helper()
	req, err := l.requests.Get(t.Context(), id)
	if err != nil || req == nil {
		t.Fatalf("request %s = %+v, %v", id, req, err)
	}
	return req
}

func TestDeployLeader_UnknownOperationAndUnreadableAnswer(t *testing.T) {
	t.Parallel()
	l := testDeployLeader(t, testOpenDB(t), failingOp)
	ctx := t.Context()
	unknown := idgen.New()
	if err := l.requests.Add(ctx, unknown, store.IntentKindAgent, idgen.New(), "no.such.op", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if done := l.runRequest(ctx, mustGet(t, l, unknown)); !done {
		t.Fatal("the unknown operation was left waiting")
	}
	if _, err := l.readAnswer(ctx, mustGet(t, l, unknown)); err == nil {
		t.Fatal("an unknown operation was answered as a success")
	}

	unreadable := idgen.New()
	if err := l.requests.Add(ctx, unreadable, store.IntentKindAgent, idgen.New(), "test.op", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	req := mustGet(t, l, unreadable)
	if err := l.requests.Answer(ctx, req, "not json"); err != nil {
		t.Fatal(err)
	}
	if _, err := l.readAnswer(ctx, mustGet(t, l, unreadable)); err == nil {
		t.Fatal("an unreadable answer was read as a success")
	}
	if gone, _ := l.requests.Get(ctx, unreadable); gone != nil {
		t.Fatal("a read request was not deleted")
	}
}

func TestDeployLeader_ForwardRefusesWhatItCannotName(t *testing.T) {
	t.Parallel()
	l := testDeployLeader(t, testOpenDB(t), failingOp)
	if _, err := forwardOp(t.Context(), l, "test.op", "not test args", nil, func(context.Context, string) (string, error) { return "", nil }, nil); err == nil {
		t.Error("arguments of the wrong type were forwarded")
	}
	if _, err := forwardOp(t.Context(), l, "no.such.op", testOpArgs{Object: idgen.New()}, nil, func(context.Context, string) (string, error) { return "", nil }, nil); err == nil {
		t.Error("an unregistered operation was forwarded")
	}
	if n := len(waitingRequests(t, l, store.IntentKindAgent)); n != 0 {
		t.Fatalf("%d requests written, want none", n)
	}
}

func TestDeployLeader_ARequestGoneWhileWaiting(t *testing.T) {
	t.Parallel()
	l := testDeployLeader(t, testOpenDB(t), failingOp)
	ctx := t.Context()
	for _, rowExists := range []bool{true, false} {
		done := make(chan error, 1)
		go func() {
			_, err := forwardOp(ctx, l, "test.op", testOpArgs{Object: idgen.New()}, nil, func(_ context.Context, id string) (string, error) {
				if rowExists {
					return id, nil
				}
				return "", &NotFoundError{Resource: "deployment", ID: id}
			}, nil)
			done <- err
		}()
		waitUntil(t, "the request is written", func() bool { return len(waitingRequests(t, l, store.IntentKindAgent)) == 1 })
		if err := l.requests.Delete(ctx, waitingRequests(t, l, store.IntentKindAgent)[0].ID); err != nil {
			t.Fatal(err)
		}
		err := <-done
		if rowExists && err != nil {
			t.Errorf("with a deployment row got %v, want its outcome", err)
		}
		if !rowExists && !errors.Is(err, errNoLeader) {
			t.Errorf("without a deployment row got %v, want try again", err)
		}
	}
}

func TestDeployLeader_AnOperationAnotherLeaderRecordedAnswersWithTheRecordedOutcome(t *testing.T) {
	t.Parallel()
	l := testDeployLeader(t, testOpenDB(t), func(context.Context, testOpArgs, string) (string, error) {
		return "", errRecorded
	})
	lead(t, l)
	refused := errors.New("AWE refused the undeploy")
	outcome := func(status string) error {
		if status == "failed" {
			return refused
		}
		return nil
	}
	for _, tc := range []struct {
		name     string
		recorded string
		wantErr  error
	}{
		{name: "a recorded failure answers with its error", recorded: "failed", wantErr: refused},
		{name: "a recorded success answers without an error", recorded: "success", wantErr: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got, err := forwardOp(t.Context(), l, "test.op", testOpArgs{Object: idgen.New()}, nil,
				func(context.Context, string) (string, error) { return tc.recorded, nil }, outcome)
			if got != tc.recorded || !errors.Is(err, tc.wantErr) {
				t.Errorf("forwardOp answered by the other leader's record = (%q, %v), want (%q, %v)", got, err, tc.recorded, tc.wantErr)
			}
		})
	}
}

func insertAgentDeploymentTwice(t *testing.T, db *store.DB) (deploymentID string, duplicate error) {
	t.Helper()
	deployments := store.NewDeploymentRepo(db)
	row := &store.AgentDeployment{ID: idgen.New(), AgentID: seedAgentBoundTo(t, db, "two_leaders_agent"), Status: "success", Action: "deploy", CreatedBy: "leader-a"}
	if err := deployments.Create(t.Context(), row); err != nil {
		t.Fatalf("Create(first insert of %s): %v", row.ID, err)
	}
	duplicate = deployments.Create(t.Context(), row)
	if duplicate == nil {
		t.Fatalf("Create(second insert of %s) succeeded, want a unique-key error", row.ID)
	}
	return row.ID, duplicate
}

func TestDeployLeader_ADuplicateInsertIsAnsweredAsRecordedOnlyWhenItsRowExists(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	l := newDeployLeader(nil, store.NewDeploymentIntentRepo(db), slog.New(slog.DiscardHandler))
	recordedID, duplicate := insertAgentDeploymentTwice(t, db)
	for _, tc := range []struct {
		name         string
		deploymentID string
		insertErr    error
		want         error
	}{
		{name: "a duplicate of a recorded deployment is answered as recorded", deploymentID: recordedID, insertErr: duplicate, want: errRecorded},
		{name: "a duplicate with no row under its id keeps the insert error", deploymentID: idgen.New(), insertErr: duplicate, want: nil},
		{name: "another insert failure keeps the insert error", deploymentID: recordedID, insertErr: errors.New("disk I/O error"), want: nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := l.duplicateInsertOutcome(t.Context(), store.IntentKindAgent, tc.deploymentID, tc.insertErr); !errors.Is(got, tc.want) {
				t.Errorf("duplicateInsertOutcome(%s, %v) = %v, want %v", tc.deploymentID, tc.insertErr, got, tc.want)
			}
		})
	}
}

func TestDeployLeader_ADuplicateInsertWhoseRecordCannotBeReadIsAnsweredNotFinished(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	l := newDeployLeader(nil, store.NewDeploymentIntentRepo(db), slog.New(slog.DiscardHandler))
	recordedID, duplicate := insertAgentDeploymentTwice(t, db)
	if err := db.Close(); err != nil {
		t.Fatalf("close the database: %v", err)
	}
	got := l.duplicateInsertOutcome(t.Context(), store.IntentKindAgent, recordedID, duplicate)
	var unavailable *UnavailableError
	want := "the change was accepted and has not finished; check deployment " + recordedID + " before sending it again"
	if !errors.As(got, &unavailable) || unavailable.Message != want {
		t.Errorf("duplicateInsertOutcome(%s) with its record unreadable = %v, want UnavailableError %q", recordedID, got, want)
	}
}

func TestDeployLeader_SweepsStaleRequests(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	l := testDeployLeader(t, db, failingOp)
	l.sweep = 20 * time.Millisecond
	stale := idgen.New()
	if err := l.requests.Add(t.Context(), stale, store.IntentKindAgent, idgen.New(), "test.op", []byte("{}")); err != nil {
		t.Fatal(err)
	}
	if _, err := db.ExecContext(t.Context(), `UPDATE deployment_requests SET created_time = created_time - ?, done_time = created_time WHERE id = ?`,
		(staleRequestAge + time.Minute).Milliseconds(), stale); err != nil {
		t.Fatal(err)
	}
	lead(t, l)
	waitUntil(t, "the stale request is deleted", func() bool {
		req, err := l.requests.Get(t.Context(), stale)
		return err == nil && req == nil
	})
}

func TestDeployLeader_DatabaseGone(t *testing.T) {
	t.Parallel()
	db := testOpenDB(t)
	l := testDeployLeader(t, db, failingOp)
	l.sweep = 10 * time.Millisecond
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
	repo := l.requests
	req := &store.DeployRequest{ID: idgen.New()}
	checks := map[string]error{}
	checks["add"] = repo.Add(ctx, req.ID, store.IntentKindAgent, idgen.New(), "test.op", []byte("{}"))
	_, checks["get"] = repo.Get(ctx, req.ID)
	_, checks["any waiting"] = repo.AnyWaiting(ctx, time.Minute)
	_, checks["waiting"] = repo.Waiting(ctx, store.IntentKindAgent, time.Minute)
	_, checks["take"] = repo.Take(ctx, req.ID)
	checks["answer"] = repo.Answer(ctx, req, "{}")
	_, checks["withdraw"] = repo.Withdraw(ctx, req.ID)
	checks["delete"] = repo.Delete(ctx, req.ID)
	_, checks["delete older"] = repo.DeleteOlderThan(ctx, time.Minute)
	for name, err := range checks {
		if err == nil {
			t.Errorf("%s on a closed database returned no error", name)
		}
	}
	if _, err := forwardTestOp(ctx, l, testOpArgs{Object: idgen.New()}); err == nil {
		t.Error("a request was accepted with no database")
	}
	if done := l.runRequest(ctx, req); done {
		t.Error("a request was reported done with no database")
	}
	if _, err := l.giveUp(ctx, req.ID, func(context.Context) bool { return false }); err == nil {
		t.Error("giving up on a request with no database reported no error")
	}

	termCtx, cancel := context.WithCancel(ctx)
	served := make(chan error, 1)
	go func() { served <- l.serve(termCtx, leader.Term{Number: 1}) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	if err := <-served; err != nil {
		t.Fatalf("the deploy job ended with %v, want it to keep running until the term ends", err)
	}
}

func TestDeployLeader_AnswerInTheSameProcessIsReadAtOnce(t *testing.T) {
	t.Parallel()
	l := testDeployLeader(t, testOpenDB(t), failingOp)
	l.check = 5 * time.Second
	lead(t, l)
	for range 3 {
		object := idgen.New()
		start := time.Now()
		got, err := forwardTestOp(t.Context(), l, testOpArgs{Object: object})
		if err != nil || got != object {
			t.Fatalf("got (%q, %v), want the operation's answer", got, err)
		}
		if took := time.Since(start); took > time.Second {
			t.Fatalf("answered after %s, want well under the %s row read", took, l.check)
		}
	}
}
