// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package entrypoint

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	goruntime "runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/broker"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget"
	"github.com/SolaceProducts/solace-agent-mesh/internal/budget/budgettest"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/features"
	"github.com/SolaceProducts/solace-agent-mesh/internal/identity"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	"github.com/SolaceProducts/solace-agent-mesh/internal/runtime"
)

const (
	runAsCreator   = "alice@example.com"
	runAsPrincipal = "system:nightly"
	runAsRowID     = "0193a1c2-7d4e-7f00-8a00-0000000000aa"
)

const (
	unconfirmedTimeoutText     = "the identity this run executes as could not be confirmed before the run timed out"
	waitingNoteText            = "the identity this run executes as could not be confirmed; waiting to try again"
	resolutionFailedText       = "identity resolution failed"
	platformTooOldText         = "run-as needs a newer platform: upgrade the platform so it reports service accounts, then the task can run as one"
	unknownSystemUserText      = "run-as service account unavailable: it does not exist or holds no role"
	creatorLostGrantText       = "run-as authority lost: the task's creator is no longer a super administrator"
	unresolvableText           = "run-as service account cannot be resolved on this gateway"
	noOwnerText                = "scheduled task has no owner"
	platformUnreportedNoteText = "the platform does not report service accounts yet, so the identity this run executes as could not be confirmed; waiting for the platform to be upgraded"
)

type entitlementDirectory map[string]auth.Entitlement

func (d entitlementDirectory) Lookup(_ context.Context, userID string, _ map[string]any) (auth.Entitlement, error) {
	ent, known := d[userID]
	if !known {
		ent.SystemUserRow = auth.SystemUserRowMissing
	}
	return ent, nil
}

type directoryResolver struct {
	dir entitlementDirectory

	mu         sync.Mutex
	runAsAsked []string
}

func (r *directoryResolver) ResolveIdentity(ctx context.Context, userID string) (*auth.Subject, error) {
	id, err := auth.ForScheduledTaskOwner(ctx, userID, r.dir)
	if auth.IsScheduledOwnerRefusal(err) {
		return nil, fmt.Errorf("%w: %w", ErrIdentityUnauthorized, err)
	}
	return id, err
}

func (r *directoryResolver) recordAsked(principal string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.runAsAsked = append(r.runAsAsked, principal)
}

func (r *directoryResolver) ResolveRunAs(ctx context.Context, principal string) (*auth.Subject, error) {
	r.recordAsked(principal)
	id, err := auth.ForScheduledRunAs(ctx, principal, r.dir)
	if auth.IsScheduledRunAsRefusal(err) {
		return nil, fmt.Errorf("%w: %w", ErrIdentityUnauthorized, err)
	}
	return id, err
}

func (r *directoryResolver) askedRunAs() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return slices.Clone(r.runAsAsked)
}

var errBrokerDown = errors.New("broker down")

type failingLookupResolver struct {
	directoryResolver
	creatorErr error
	runAsErr   error
}

func (r *failingLookupResolver) ResolveIdentity(ctx context.Context, userID string) (*auth.Subject, error) {
	if r.creatorErr != nil {
		return nil, r.creatorErr
	}
	return r.directoryResolver.ResolveIdentity(ctx, userID)
}

func (r *failingLookupResolver) ResolveRunAs(ctx context.Context, principal string) (*auth.Subject, error) {
	if r.runAsErr != nil {
		return nil, r.runAsErr
	}
	return r.directoryResolver.ResolveRunAs(ctx, principal)
}

type principalFailingResolver struct {
	directoryResolver
	runAsErrs map[string]error
}

func (r *principalFailingResolver) ResolveRunAs(ctx context.Context, principal string) (*auth.Subject, error) {
	if err, failing := r.runAsErrs[principal]; failing {
		r.recordAsked(principal)
		return nil, err
	}
	return r.directoryResolver.ResolveRunAs(ctx, principal)
}

type ownerOnlyResolver struct{ dir entitlementDirectory }

func (r ownerOnlyResolver) ResolveIdentity(ctx context.Context, userID string) (*auth.Subject, error) {
	return auth.ForScheduledTaskOwner(ctx, userID, r.dir)
}

func superAdminDirectory() entitlementDirectory {
	return entitlementDirectory{
		runAsCreator:   {Scopes: []string{"*:*:*"}},
		runAsPrincipal: {Roles: []string{"nightly_reporter"}, Scopes: []string{"agent:test-agent:invoke"}, SystemUserID: runAsRowID},
	}
}

type runAsFixture struct {
	flags    *features.Registry
	svc      *ScheduledTaskService
	signer   *recordingSigner
	notifier *fakeUserNotifier
	agentQ   broker.Queue
}

func newRunAsFixture(t *testing.T, flagOn bool, resolver IdentityResolver) *runAsFixture {
	t.Helper()
	reg := features.NewRegistry()
	reg.Register(features.Definition{Key: features.KeyScheduledTaskRunAs, Default: flagOn})
	core := setupIdentityTestCore(t, runtime.WithFeatureRegistry(reg))
	signer := &recordingSigner{}
	core.SetTaskSigner(signer)
	core.SetIdentityResolver(resolver)
	notifier := &fakeUserNotifier{}
	svc := core.ScheduledTasks()
	svc.SetUserNotifier(notifier)
	return &runAsFixture{
		flags:    reg,
		svc:      svc,
		signer:   signer,
		notifier: notifier,
		agentQ:   budgettest.TapAgentRequests(t, core.Services().Broker(), "test-ns", "test-agent"),
	}
}

func (f *runAsFixture) setFlag(on bool) {
	f.flags.Register(features.Definition{Key: features.KeyScheduledTaskRunAs, Default: on})
}

func (f *runAsFixture) dueRunAsTask(t *testing.T, runAs string, edit func(*store.ScheduledTask)) *store.ScheduledTask {
	t.Helper()
	return dueTask(t, f.svc, time.Now().Add(-time.Second), func(st *store.ScheduledTask) {
		owner := runAsCreator
		st.CreatedBy, st.UserID = runAsCreator, &owner
		if runAs != "" {
			st.RunAs = &runAs
		}
		if edit != nil {
			edit(st)
		}
	})
}

func (f *runAsFixture) publishedUserID(t *testing.T) string {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	msg, err := f.svc.core.Services().Broker().Receive(ctx, f.agentQ)
	if err != nil || msg == nil {
		t.Fatalf("the agent received nothing: %v", err)
	}
	msg.Ack()
	return a2a.DecodeUserProperties(msg.UserProperties).UserID
}

func (f *runAsFixture) onlyRun(t *testing.T, taskID string) *store.ScheduledTaskExecution {
	t.Helper()
	runs := taskRuns(t, f.svc, taskID)
	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}
	return runs[0]
}

type runView struct {
	Status     string
	Error      string
	RetryCount int
	RunAs      string
}

func (f *runAsFixture) runViews(t *testing.T, taskID string) []runView {
	t.Helper()
	var views []runView
	for _, run := range taskRuns(t, f.svc, taskID) {
		view := runView{Status: run.Status, RetryCount: run.RetryCount}
		if run.ErrorMessage != nil {
			view.Error = *run.ErrorMessage
		}
		if run.RunAs != nil {
			view.RunAs = *run.RunAs
		}
		views = append(views, view)
	}
	slices.SortFunc(views, func(a, b runView) int { return a.RetryCount - b.RetryCount })
	return views
}

func (f *runAsFixture) assertUncounted(t *testing.T, taskID string) {
	t.Helper()
	stored := reloadTask(t, f.svc, taskID)
	if stored.ConsecutiveFailureCount != 0 || !stored.Enabled {
		t.Errorf("task %s = {consecutive failures %d, enabled %v}, want no failure counted and the task still enabled", taskID, stored.ConsecutiveFailureCount, stored.Enabled)
	}
}

func (f *runAsFixture) recipients(eventType string) []string {
	var users []string
	for _, c := range f.notifier.Calls() {
		if c.EventType == eventType {
			users = append(users, c.UserID)
		}
	}
	return users
}

func TestScheduledRunAs_RunsAsTheSystemUserWhileTheCreatorKeepsTheTask(t *testing.T) {
	f := newRunAsFixture(t, true, &directoryResolver{dir: superAdminDirectory()})
	task := f.dueRunAsTask(t, runAsPrincipal, nil)

	mustRunPasses(t, f.svc, time.Now())

	signed := f.signer.firstIdentity()
	if signed == nil || signed.UserID() != runAsPrincipal {
		t.Fatalf("signed as %v, want %s", signed, runAsPrincipal)
	}
	if signed.PlatformUserID() != runAsRowID {
		t.Errorf("signed platform user = %q, want the service account's row %q", signed.PlatformUserID(), runAsRowID)
	}
	if !slices.Equal(signed.Scopes(), []string{"agent:test-agent:invoke"}) {
		t.Errorf("signed scopes = %v, want the service account's own, not the creator's", signed.Scopes())
	}
	if got := f.publishedUserID(t); got != runAsPrincipal {
		t.Errorf("wire user id = %q, want %q", got, runAsPrincipal)
	}
	run := f.onlyRun(t, task.ID)
	if run.Status != "running" || run.RunAs == nil || *run.RunAs != runAsPrincipal {
		t.Errorf("run = {status %q, runAs %v}, want running and recorded as run by %s", run.Status, run.RunAs, runAsPrincipal)
	}
	if got := f.recipients("execution_started"); !slices.Equal(got, []string{runAsCreator}) {
		t.Errorf("execution_started went to %v, want only the creator", got)
	}
	if run.ArtifactUserID(task) != runAsPrincipal {
		t.Errorf("artifacts read back under %q, want %q, where the agent wrote them", run.ArtifactUserID(task), runAsPrincipal)
	}
}

func TestScheduledRunAs_TaskWithoutRunAsIsUnchanged(t *testing.T) {
	for _, flagOn := range []bool{false, true} {
		t.Run(fmt.Sprintf("flag %v", flagOn), func(t *testing.T) {
			resolver := &directoryResolver{dir: superAdminDirectory()}
			f := newRunAsFixture(t, flagOn, resolver)
			task := f.dueRunAsTask(t, "", nil)

			mustRunPasses(t, f.svc, time.Now())

			if signed := f.signer.firstIdentity(); signed == nil || signed.UserID() != runAsCreator {
				t.Errorf("signed as %v, want the creator", signed)
			}
			if got := f.publishedUserID(t); got != runAsCreator {
				t.Errorf("wire user id = %q, want the creator", got)
			}
			if run := f.onlyRun(t, task.ID); run.RunAs != nil {
				t.Errorf("run recorded runAs %q, want none", *run.RunAs)
			}
			if asked := resolver.askedRunAs(); len(asked) != 0 {
				t.Errorf("resolved service accounts %v for a task with no runAs", asked)
			}
		})
	}
}

func TestScheduledRunAs_FlagOffFailsTheRunClosedInsteadOfRunningAsTheCreator(t *testing.T) {
	resolver := &directoryResolver{dir: superAdminDirectory()}
	f := newRunAsFixture(t, false, resolver)
	f.svc.cfg.AutoPauseAfterConsecutiveFailures = 1
	task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.MaxRetries = 2 })

	mustRunPasses(t, f.svc, time.Now())

	wantRuns := []runView{{Status: "failed", Error: "run-as is turned off on this gateway: the task is set to run as a service account, so it was not run", RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantRuns, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs mismatch (-want +got):\n%s", diff)
	}
	if f.signer.callCount() != 0 {
		t.Errorf("signed %d tasks with the flag off, want none: the creator's identity must not run it", f.signer.callCount())
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
	if asked := resolver.askedRunAs(); len(asked) != 0 {
		t.Errorf("resolved service accounts %v with the flag off", asked)
	}
	f.assertUncounted(t, task.ID)
}

func TestScheduledRunAs_AMissingSystemUserFailsTheRunClosed(t *testing.T) {
	dir := superAdminDirectory()
	delete(dir, runAsPrincipal)
	f := newRunAsFixture(t, true, &directoryResolver{dir: dir})
	task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.MaxRetries = 2 })

	mustRunPasses(t, f.svc, time.Now())

	run := f.onlyRun(t, task.ID)
	if run.Status != "failed" || run.ErrorMessage == nil || *run.ErrorMessage != unknownSystemUserText {
		t.Fatalf("run = {status %q, error %v}, want failed with %q", run.Status, run.ErrorMessage, unknownSystemUserText)
	}
	if run.RunAs == nil || *run.RunAs != runAsPrincipal {
		t.Errorf("run recorded runAs %v, want the identity it would have used, %s", run.RunAs, runAsPrincipal)
	}
	if f.signer.callCount() != 0 {
		t.Errorf("signed %d tasks, want none", f.signer.callCount())
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
	if stored := reloadTask(t, f.svc, task.ID); stored.ConsecutiveFailureCount != 1 {
		t.Errorf("consecutive failures = %d, want the failure counted", stored.ConsecutiveFailureCount)
	}
}

func TestScheduledRunAs_ASystemUserWithNoRoleFailsTheRunClosed(t *testing.T) {
	dir := superAdminDirectory()
	dir[runAsPrincipal] = auth.Entitlement{SystemUserID: runAsRowID}
	f := newRunAsFixture(t, true, &directoryResolver{dir: dir})
	task := f.dueRunAsTask(t, runAsPrincipal, nil)

	mustRunPasses(t, f.svc, time.Now())

	if run := f.onlyRun(t, task.ID); run.Status != "failed" || *run.ErrorMessage != unknownSystemUserText {
		t.Fatalf("run = {status %q, error %v}, want failed with %q", run.Status, run.ErrorMessage, unknownSystemUserText)
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
}

func TestScheduledRunAs_ACreatorWhoLostTheUniversalGrantFailsTheRun(t *testing.T) {
	dir := superAdminDirectory()
	dir[runAsCreator] = auth.Entitlement{Scopes: []string{"agent:test-agent:invoke"}}
	resolver := &directoryResolver{dir: dir}
	f := newRunAsFixture(t, true, resolver)
	task := f.dueRunAsTask(t, runAsPrincipal, nil)

	mustRunPasses(t, f.svc, time.Now())

	run := f.onlyRun(t, task.ID)
	if run.Status != "failed" || *run.ErrorMessage != creatorLostGrantText {
		t.Fatalf("run = {status %q, error %v}, want failed with %q", run.Status, run.ErrorMessage, creatorLostGrantText)
	}
	if asked := resolver.askedRunAs(); len(asked) != 0 {
		t.Errorf("resolved %v after the creator's authority failed", asked)
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
}

func TestScheduledRunAs_AResolverThatCannotBuildASystemUserFailsTheRun(t *testing.T) {
	f := newRunAsFixture(t, true, ownerOnlyResolver{dir: superAdminDirectory()})
	task := f.dueRunAsTask(t, runAsPrincipal, nil)

	mustRunPasses(t, f.svc, time.Now())

	if run := f.onlyRun(t, task.ID); run.Status != "failed" || *run.ErrorMessage != unresolvableText {
		t.Fatalf("run = {status %q, error %v}, want failed with %q", run.Status, run.ErrorMessage, unresolvableText)
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
}

func TestScheduledRunAs_AnUnclassifiedLookupFailureIsRetriedWithAStableMessage(t *testing.T) {
	cases := []struct {
		name       string
		creatorErr error
		runAsErr   error
	}{
		{"the creator lookup fails", errBrokerDown, nil},
		{"the service account resolution fails", nil, errBrokerDown},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, true, &failingLookupResolver{
				directoryResolver: directoryResolver{dir: superAdminDirectory()},
				creatorErr:        c.creatorErr,
				runAsErr:          c.runAsErr,
			})
			task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.MaxRetries = 1 })

			mustRunPasses(t, f.svc, time.Now())

			want := []runView{
				{Status: "failed", Error: resolutionFailedText, RetryCount: 0, RunAs: runAsPrincipal},
				{Status: "pending", RetryCount: 1, RunAs: runAsPrincipal},
			}
			if diff := cmp.Diff(want, f.runViews(t, task.ID)); diff != "" {
				t.Errorf("runs mismatch (-want +got):\n%s", diff)
			}
			if f.signer.callCount() != 0 {
				t.Errorf("signed %d tasks, want none", f.signer.callCount())
			}
			budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
		})
	}
}

func TestScheduledRunAs_ASystemUserWhoseRowCannotBeConfirmedIsWaitedFor(t *testing.T) {
	resolver := &failingLookupResolver{
		directoryResolver: directoryResolver{dir: superAdminDirectory()},
		runAsErr:          fmt.Errorf("resolve run-as service account: %w", auth.ErrSystemUserLookupUnavailable),
	}
	f := newRunAsFixture(t, true, resolver)
	task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.MaxRetries = 1 })

	mustRunPasses(t, f.svc, time.Now())

	wantWaiting := []runView{{Status: "pending", Error: waitingNoteText, RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantWaiting, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs while the row cannot be confirmed mismatch (-want +got):\n%s", diff)
	}
	f.assertUncounted(t, task.ID)
	if f.signer.callCount() != 0 {
		t.Errorf("signed %d tasks while the row could not be confirmed, want none", f.signer.callCount())
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)

	resolver.runAsErr = nil
	mustRunPasses(t, f.svc, time.Now())

	wantRunning := []runView{{Status: "running", RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantRunning, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs once the row is confirmed mismatch (-want +got):\n%s", diff)
	}
	if got := f.publishedUserID(t); got != runAsPrincipal {
		t.Errorf("wire user id = %q, want %q", got, runAsPrincipal)
	}
}

type rolloutLookup struct {
	dir entitlementDirectory

	mu         sync.Mutex
	oldReplies int
}

func (l *rolloutLookup) Lookup(ctx context.Context, userID string, gatewayContext map[string]any) (auth.Entitlement, error) {
	ent, err := l.dir.Lookup(ctx, userID, gatewayContext)
	l.mu.Lock()
	defer l.mu.Unlock()
	if strings.HasPrefix(userID, "system:") && l.oldReplies > 0 {
		l.oldReplies--
		ent.SystemUserID, ent.SystemUserRow = "", ""
	}
	return ent, err
}

type rolloutResolver struct {
	directoryResolver
	lookup *rolloutLookup
}

func newRolloutResolver(oldReplies int) *rolloutResolver {
	dir := superAdminDirectory()
	return &rolloutResolver{directoryResolver: directoryResolver{dir: dir}, lookup: &rolloutLookup{dir: dir, oldReplies: oldReplies}}
}

func (r *rolloutResolver) ResolveRunAs(ctx context.Context, principal string) (*auth.Subject, error) {
	id, err := auth.ForScheduledRunAs(ctx, principal, r.lookup)
	if auth.IsScheduledRunAsRefusal(err) {
		return nil, fmt.Errorf("%w: %w", ErrIdentityUnauthorized, err)
	}
	return id, err
}

func TestScheduledRunAs_AnOlderPlatformWaitsThenEndsUncountedAtTheTimeout(t *testing.T) {
	f := newRunAsFixture(t, true, newRolloutResolver(1000))
	f.svc.cfg.AutoPauseAfterConsecutiveFailures = 1
	task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.MaxRetries = 2; st.TimeoutSeconds = 60 })
	now := time.Now()

	for i := range 3 {
		mustRunPasses(t, f.svc, now.Add(time.Duration(i)*time.Second))
	}

	wantWaiting := []runView{{Status: "pending", Error: platformUnreportedNoteText, RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantWaiting, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs while the platform reports no rows mismatch (-want +got):\n%s", diff)
	}
	f.assertUncounted(t, task.ID)
	if f.signer.callCount() != 0 {
		t.Errorf("signed %d tasks while the platform reported no rows, want none", f.signer.callCount())
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)

	mustRunPasses(t, f.svc, now.Add(10*time.Minute))

	wantEnded := []runView{{Status: "failed", Error: platformTooOldText, RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantEnded, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs after the timeout mismatch (-want +got):\n%s", diff)
	}
	f.assertUncounted(t, task.ID)
}

func TestScheduledRunAs_ARunSentToAnOldPodIsSentAgainAndRunsOnceANewPodAnswers(t *testing.T) {
	resolver := newRolloutResolver(1)
	f := newRunAsFixture(t, true, resolver)
	task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.MaxRetries = 2 })
	now := time.Now()

	mustRunPasses(t, f.svc, now)

	wantWaiting := []runView{{Status: "pending", Error: platformUnreportedNoteText, RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantWaiting, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs after an old pod answered mismatch (-want +got):\n%s", diff)
	}

	mustRunPasses(t, f.svc, now.Add(5*time.Second))

	wantRunning := []runView{{Status: "running", RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantRunning, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs after a new pod answered mismatch (-want +got):\n%s", diff)
	}
	if got := f.publishedUserID(t); got != runAsPrincipal {
		t.Errorf("wire user id = %q, want %q", got, runAsPrincipal)
	}
	f.assertUncounted(t, task.ID)
}

var (
	errPlatformLookupFailed = fmt.Errorf("platform authz lookup: %w", auth.ErrIdentityLookupUnavailable)
	errPlatformUnreachable  = fmt.Errorf("authz client: lookup: %w", auth.ErrIdentityPlatformUnreachable)
	errSystemUserRowUnknown = fmt.Errorf("resolve run-as service account: %w", auth.ErrSystemUserLookupUnavailable)
)

func TestScheduledRunAs_ATransientLookupFailureWaitsThenEndsUncountedAtTheTimeout(t *testing.T) {
	cases := []struct {
		name       string
		runAs      string
		creatorErr error
		runAsErr   error
		wantRunAs  string
	}{
		{"the creator lookup of a run-as task fails", runAsPrincipal, errPlatformLookupFailed, nil, runAsPrincipal},
		{"the service account lookup fails", runAsPrincipal, nil, fmt.Errorf("resolve run-as service account: %w", errPlatformLookupFailed), runAsPrincipal},
		{"the service account's row cannot be confirmed", runAsPrincipal, nil, errSystemUserRowUnknown, runAsPrincipal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, true, &failingLookupResolver{
				directoryResolver: directoryResolver{dir: superAdminDirectory()},
				creatorErr:        c.creatorErr,
				runAsErr:          c.runAsErr,
			})
			f.svc.cfg.AutoPauseAfterConsecutiveFailures = 1
			task := f.dueRunAsTask(t, c.runAs, func(st *store.ScheduledTask) { st.MaxRetries = 2; st.TimeoutSeconds = 60 })
			now := time.Now()

			for i := range 3 {
				mustRunPasses(t, f.svc, now.Add(time.Duration(i)*time.Second))
			}

			wantWaiting := []runView{{Status: "pending", Error: waitingNoteText, RunAs: c.wantRunAs}}
			if diff := cmp.Diff(wantWaiting, f.runViews(t, task.ID)); diff != "" {
				t.Errorf("runs while the identity cannot be confirmed mismatch (-want +got):\n%s", diff)
			}
			if f.signer.callCount() != 0 {
				t.Errorf("signed %d tasks while the identity could not be confirmed, want none", f.signer.callCount())
			}
			budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)

			mustRunPasses(t, f.svc, now.Add(10*time.Minute))

			wantEnded := []runView{{Status: "failed", Error: unconfirmedTimeoutText, RunAs: c.wantRunAs}}
			if diff := cmp.Diff(wantEnded, f.runViews(t, task.ID)); diff != "" {
				t.Errorf("runs after the timeout mismatch (-want +got):\n%s", diff)
			}
			f.assertUncounted(t, task.ID)
		})
	}
}

func TestScheduledTask_ATaskWithoutRunAsKeepsACountedFailureWhenItsOwnerLookupFails(t *testing.T) {
	cases := []struct {
		name       string
		flagOn     bool
		creatorErr error
		resolution identity.LookupResolution
	}{
		{"the flag is off and the platform lookup failed", false, errPlatformLookupFailed, ""},
		{"the flag is on and the platform lookup failed", true, errPlatformLookupFailed, ""},
		{"the flag is off and the lookup timed out", false, fmt.Errorf("%w: %w", auth.ErrIdentityLookupUnavailable, context.DeadlineExceeded), ""},
		{"the flag is on and the platform replied degraded", true, nil, identity.LookupDegraded},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := superAdminDirectory()
			dir[runAsCreator] = auth.Entitlement{Scopes: []string{"agent:test-agent:invoke"}, Resolution: c.resolution}
			f := newRunAsFixture(t, c.flagOn, &failingLookupResolver{directoryResolver: directoryResolver{dir: dir}, creatorErr: c.creatorErr})
			task := f.dueRunAsTask(t, "", func(st *store.ScheduledTask) { st.MaxRetries = 1 })

			mustRunPasses(t, f.svc, time.Now())

			want := []runView{
				{Status: "failed", Error: "identity resolution failed", RetryCount: 0},
				{Status: "pending", RetryCount: 1},
			}
			if diff := cmp.Diff(want, f.runViews(t, task.ID)); diff != "" {
				t.Errorf("runs mismatch (-want +got):\n%s", diff)
			}
			if stored := reloadTask(t, f.svc, task.ID); stored.ConsecutiveFailureCount != 1 {
				t.Errorf("consecutive failures = %d, want the failure counted", stored.ConsecutiveFailureCount)
			}
			budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
		})
	}
}

func TestScheduledTask_ADegradedOwnerLookupIsNotReportedAsAnUnauthorizedOwner(t *testing.T) {
	dir := superAdminDirectory()
	dir[runAsCreator] = auth.Entitlement{Scopes: []string{"agent:test-agent:invoke"}, Resolution: identity.LookupDegraded}
	f := newRunAsFixture(t, false, &directoryResolver{dir: dir})
	task := f.dueRunAsTask(t, "", nil)

	mustRunPasses(t, f.svc, time.Now())

	want := []runView{{Status: "failed", Error: "identity resolution failed"}}
	if diff := cmp.Diff(want, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs mismatch (-want +got):\n%s", diff)
	}
}

func TestScheduledRunAs_AFireWhileARunWaitsOnItsIdentityIsSkippedWithItsOwnMessage(t *testing.T) {
	cases := []struct {
		name     string
		runAsErr error
		wantNote string
	}{
		{"the service account's row cannot be confirmed", errSystemUserRowUnknown, waitingNoteText},
		{"the platform does not report service accounts", fmt.Errorf("resolve run-as service account: %w", auth.ErrSystemUserRowNotReported), platformUnreportedNoteText},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, true, &failingLookupResolver{
				directoryResolver: directoryResolver{dir: superAdminDirectory()},
				runAsErr:          c.runAsErr,
			})
			task := f.dueRunAsTask(t, runAsPrincipal, nil)
			now := time.Now()
			mustRunPasses(t, f.svc, now)
			if err := f.svc.repo.Update(t.Context(), task.ID, map[string]any{"next_run_at": now.Add(-time.Second).UnixMilli()}); err != nil {
				t.Fatalf("Update next_run_at: %v", err)
			}

			mustRunPasses(t, f.svc, now.Add(time.Second))

			want := []runView{
				{Status: "pending", Error: c.wantNote, RunAs: runAsPrincipal},
				{Status: "skipped", Error: "previous execution is still waiting to confirm the identity it runs as"},
			}
			got := f.runViews(t, task.ID)
			slices.SortFunc(got, func(a, b runView) int { return strings.Compare(a.Status, b.Status) })
			if diff := cmp.Diff(want, got); diff != "" {
				t.Errorf("runs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestScheduledRunAs_AnOutageLeavesAnOrdinaryRunBetweenTwoRunAsRunsToRunNormally(t *testing.T) {
	resolver := &principalFailingResolver{
		directoryResolver: directoryResolver{dir: twoSystemUsersDirectory()},
		runAsErrs:         map[string]error{runAsPrincipal: errPlatformUnreachable},
	}
	f := newRunAsFixture(t, true, resolver)
	f.svc.cfg.AutoPauseAfterConsecutiveFailures = 1
	first := f.dueRunAsTaskAt(t, runAsPrincipal, 3*time.Second, nil)
	plain := f.dueRunAsTaskAt(t, "", 2*time.Second, nil)
	last := f.dueRunAsTaskAt(t, otherPrincipal, time.Second, nil)

	mustRunPasses(t, f.svc, time.Now())

	wantWaiting := func(runAs string) []runView {
		return []runView{{Status: "pending", Error: waitingNoteText, RunAs: runAs}}
	}
	if diff := cmp.Diff(wantWaiting(runAsPrincipal), f.runViews(t, first.ID)); diff != "" {
		t.Errorf("run that met the outage mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]runView{{Status: "running"}}, f.runViews(t, plain.ID)); diff != "" {
		t.Errorf("ordinary run between the run-as runs mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantWaiting(otherPrincipal), f.runViews(t, last.ID)); diff != "" {
		t.Errorf("run-as run behind the outage mismatch (-want +got):\n%s", diff)
	}
	if got := f.publishedUserID(t); got != runAsCreator {
		t.Errorf("wire user id = %q, want the ordinary task's owner %q", got, runAsCreator)
	}
	for _, task := range []*store.ScheduledTask{first, plain, last} {
		f.assertUncounted(t, task.ID)
	}
}

func TestScheduledRunAs_ARunAsRunBehindAnOutageIsStillNotedWhenTheSendPassBudgetIsSpent(t *testing.T) {
	const budget = 50 * time.Millisecond
	resolver := &slowFailingRunAsResolver{
		directoryResolver: directoryResolver{dir: twoSystemUsersDirectory()},
		budget:            budget,
		err:               errPlatformUnreachable,
	}
	f := newRunAsFixture(t, true, resolver)
	f.svc.sendBudget = budget
	f.svc.cfg.AutoPauseAfterConsecutiveFailures = 1
	first := f.dueRunAsTaskAt(t, runAsPrincipal, 3*time.Second, nil)
	plain := f.dueRunAsTaskAt(t, "", 2*time.Second, nil)
	last := f.dueRunAsTaskAt(t, otherPrincipal, time.Second, nil)

	mustRunPasses(t, f.svc, time.Now())

	wantWaiting := func(runAs string) []runView {
		return []runView{{Status: "pending", Error: waitingNoteText, RunAs: runAs}}
	}
	if diff := cmp.Diff(wantWaiting(runAsPrincipal), f.runViews(t, first.ID)); diff != "" {
		t.Errorf("run that met the outage mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]runView{{Status: "pending"}}, f.runViews(t, plain.ID)); diff != "" {
		t.Errorf("ordinary run past the spent budget mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantWaiting(otherPrincipal), f.runViews(t, last.ID)); diff != "" {
		t.Errorf("run-as run past the spent budget mismatch (-want +got):\n%s", diff)
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
	for _, task := range []*store.ScheduledTask{first, plain, last} {
		f.assertUncounted(t, task.ID)
	}
}

func TestScheduledTask_AFireOfATaskWithoutRunAsIsSkippedAsStillRunningWhateverNoteAnUnsentRunCarries(t *testing.T) {
	for name, note := range map[string]string{"identity wait note": waitingNoteText, "platform wait note": platformUnreportedNoteText} {
		t.Run(name, func(t *testing.T) {
			f := newRunAsFixture(t, true, &directoryResolver{dir: superAdminDirectory()})
			task := f.dueRunAsTask(t, "", nil)
			earlier := f.svc.pendingRun(task, time.Now().Add(-time.Hour), 0, store.TriggerTypeScheduled, nil)
			if err := f.svc.repo.CreateExecution(t.Context(), earlier); err != nil {
				t.Fatalf("CreateExecution: %v", err)
			}
			if err := f.svc.repo.NoteUnsent(t.Context(), earlier.ID, &note); err != nil {
				t.Fatalf("NoteUnsent: %v", err)
			}

			if err := f.svc.firePass(t.Context(), time.Now()); err != nil {
				t.Fatalf("firePass: %v", err)
			}

			var skipped []runView
			for _, view := range f.runViews(t, task.ID) {
				if view.Status == "skipped" {
					skipped = append(skipped, view)
				}
			}
			want := []runView{{Status: "skipped", Error: "previous execution still running"}}
			if diff := cmp.Diff(want, skipped); diff != "" {
				t.Errorf("skipped runs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestScheduledRunAs_ADegradedLookupWaitsInsteadOfEndingTheRunAsALostAuthority(t *testing.T) {
	cases := []struct {
		name   string
		lookup string
	}{
		{"the creator's lookup is degraded", runAsCreator},
		{"the service account's lookup is degraded", runAsPrincipal},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := superAdminDirectory()
			confirmed := dir[c.lookup]
			dir[c.lookup] = auth.Entitlement{Scopes: []string{"agent:default:invoke"}, SystemUserID: runAsRowID, Resolution: identity.LookupDegraded}
			f := newRunAsFixture(t, true, &directoryResolver{dir: dir})
			f.svc.cfg.AutoPauseAfterConsecutiveFailures = 1
			task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.MaxRetries = 2 })

			mustRunPasses(t, f.svc, time.Now())

			wantWaiting := []runView{{Status: "pending", Error: waitingNoteText, RunAs: runAsPrincipal}}
			if diff := cmp.Diff(wantWaiting, f.runViews(t, task.ID)); diff != "" {
				t.Errorf("runs while the lookup is degraded mismatch (-want +got):\n%s", diff)
			}
			f.assertUncounted(t, task.ID)
			budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)

			dir[c.lookup] = confirmed
			mustRunPasses(t, f.svc, time.Now())

			wantRunning := []runView{{Status: "running", RunAs: runAsPrincipal}}
			if diff := cmp.Diff(wantRunning, f.runViews(t, task.ID)); diff != "" {
				t.Errorf("runs once the lookup is confirmed mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestScheduledRunAs_AConfirmedIdentityClearsTheWaitNoteSoALaterFailedPublishEndsAsACountedTimeout(t *testing.T) {
	resolver := &failingLookupResolver{
		directoryResolver: directoryResolver{dir: superAdminDirectory()},
		runAsErr:          errSystemUserRowUnknown,
	}
	f := newRunAsFixture(t, true, resolver)
	f.svc.cfg.AutoPauseAfterConsecutiveFailures = 5
	task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.TimeoutSeconds = 60 })
	now := time.Now()
	mustRunPasses(t, f.svc, now)

	resolver.runAsErr = nil
	brk := f.svc.core.Services().Broker()
	if err := brk.Disconnect(t.Context()); err != nil {
		t.Fatal(err)
	}
	mustRunPasses(t, f.svc, now.Add(5*time.Second))

	wantUnsent := []runView{{Status: "pending", RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantUnsent, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs after the identity was confirmed and the publish failed mismatch (-want +got):\n%s", diff)
	}

	mustRunPasses(t, f.svc, now.Add(10*time.Minute))

	wantEnded := []runView{{Status: "timeout", Error: "execution was not sent to the agent before its timeout", RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantEnded, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs after the timeout mismatch (-want +got):\n%s", diff)
	}
	if stored := reloadTask(t, f.svc, task.ID); stored.ConsecutiveFailureCount != 1 {
		t.Errorf("consecutive failures = %d, want the unsent run's timeout counted", stored.ConsecutiveFailureCount)
	}
}

func TestScheduledRunAs_AnIdentityWaitingRunOfADeletedOrPausedTaskEndsSkipped(t *testing.T) {
	cases := []struct {
		name   string
		update map[string]any
		after  time.Duration
		want   runView
	}{
		{"the task was deleted and the run has not reached its timeout", map[string]any{"deleted_at": time.Now().UnixMilli()}, 10 * time.Second, runView{Status: "skipped", Error: "scheduled task was deleted", RunAs: runAsPrincipal}},
		{"the task was deleted and the run is past its timeout", map[string]any{"deleted_at": time.Now().UnixMilli()}, 10 * time.Minute, runView{Status: "skipped", Error: "scheduled task was deleted", RunAs: runAsPrincipal}},
		{"the task was paused and the run has not reached its timeout", map[string]any{"enabled": false}, 10 * time.Second, runView{Status: "skipped", Error: "scheduled task is paused", RunAs: runAsPrincipal}},
		{"the task was paused and the run is past its timeout", map[string]any{"enabled": false}, 10 * time.Minute, runView{Status: "skipped", Error: "scheduled task is paused", RunAs: runAsPrincipal}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, true, &failingLookupResolver{
				directoryResolver: directoryResolver{dir: superAdminDirectory()},
				runAsErr:          errPlatformLookupFailed,
			})
			task := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.TimeoutSeconds = 60 })
			now := time.Now()
			mustRunPasses(t, f.svc, now)
			if err := f.svc.repo.Update(t.Context(), task.ID, c.update); err != nil {
				t.Fatalf("Update: %v", err)
			}

			mustRunPasses(t, f.svc, now.Add(c.after))

			if diff := cmp.Diff([]runView{c.want}, f.runViews(t, task.ID)); diff != "" {
				t.Errorf("runs mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestScheduledRunAs_ARunRefusedOrWaitingOnItsIdentityGetsNoChatSession(t *testing.T) {
	cases := []struct {
		name         string
		flagOn       bool
		runAs        string
		runAsErr     error
		wantStatuses []string
		wantSessions int
	}{
		{"run-as is turned off for a task that still names a service account", false, runAsPrincipal, nil, []string{"failed", "failed", "failed"}, 0},
		{"the service account cannot be confirmed", true, runAsPrincipal, errPlatformLookupFailed, []string{"pending", "skipped", "skipped"}, 0},
		{"a task without a service account is sent", true, "", nil, []string{"running", "skipped", "skipped"}, 1},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, c.flagOn, &failingLookupResolver{
				directoryResolver: directoryResolver{dir: superAdminDirectory()},
				runAsErr:          c.runAsErr,
			})
			task := f.dueRunAsTask(t, c.runAs, func(st *store.ScheduledTask) {
				st.ScheduleType, st.ScheduleExpression = "interval", "60s"
			})
			now := time.Now()

			for i := range 3 {
				mustRunPasses(t, f.svc, now.Add(time.Duration(i)*2*time.Minute))
			}

			var gotStatuses []string
			for _, run := range taskRuns(t, f.svc, task.ID) {
				gotStatuses = append(gotStatuses, run.Status)
			}
			slices.Sort(gotStatuses)
			if diff := cmp.Diff(c.wantStatuses, gotStatuses); diff != "" {
				t.Errorf("run statuses mismatch (-want +got):\n%s", diff)
			}
			sessions, _, err := f.svc.sessRepo.ListByUser(t.Context(), runAsCreator, false, false, false, "", nil, nil, 100, 0)
			if err != nil {
				t.Fatalf("ListByUser: %v", err)
			}
			if len(sessions) != c.wantSessions {
				t.Errorf("chat sessions = %d, want %d", len(sessions), c.wantSessions)
			}
			if got := f.recipients("session_created"); len(got) != 0 {
				t.Errorf("session_created went to %v, want none", got)
			}
		})
	}
}

func TestScheduledRunAs_ARunThatStopsAtTheDiscoveryWarmupDropsABudgetNoteItNoLongerHas(t *testing.T) {
	f := newRunAsFixture(t, true, &directoryResolver{dir: superAdminDirectory()})
	f.svc.core.startedAtNanos.Store(time.Now().UnixNano())
	task := f.dueRunAsTask(t, "", func(st *store.ScheduledTask) { st.TargetAgentName = "late-agent" })
	now := time.Now()
	if err := f.svc.firePass(t.Context(), now); err != nil {
		t.Fatalf("firePass: %v", err)
	}
	run := f.onlyRun(t, task.ID)
	staleNote := BudgetUnconfirmedText
	if err := f.svc.repo.NoteUnsent(t.Context(), run.ID, &staleNote); err != nil {
		t.Fatalf("NoteUnsent: %v", err)
	}

	if err := f.svc.sendPass(t.Context(), now); err != nil {
		t.Fatalf("sendPass: %v", err)
	}

	if diff := cmp.Diff([]runView{{Status: "pending"}}, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs mismatch (-want +got):\n%s", diff)
	}
}

func TestIdentityLookupFailure_NamesTheCauseOfAWait(t *testing.T) {
	cases := []struct {
		name string
		err  error
		want string
	}{
		{"the row could not be read", errSystemUserRowUnknown, "system_user_row_unavailable"},
		{"the platform reports no rows", fmt.Errorf("resolve: %w", auth.ErrSystemUserRowNotReported), "system_user_row_not_reported"},
		{"the lookup timed out", fmt.Errorf("%w: %w", auth.ErrIdentityLookupUnavailable, context.DeadlineExceeded), "timeout"},
		{"the lookup was canceled", fmt.Errorf("%w: %w", auth.ErrIdentityLookupUnavailable, context.Canceled), "canceled"},
		{"the platform failed the lookup", errPlatformLookupFailed, "platform_lookup_failed"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := identityLookupFailure(c.err); got != c.want {
				t.Errorf("identityLookupFailure(%v) = %q, want %q", c.err, got, c.want)
			}
		})
	}
}

type slowFailingRunAsResolver struct {
	directoryResolver
	budget time.Duration
	err    error

	started sync.Once
	until   time.Time
}

func (r *slowFailingRunAsResolver) ResolveRunAs(context.Context, string) (*auth.Subject, error) {
	r.started.Do(func() { r.until = time.Now().Add(r.budget + time.Nanosecond) })
	for time.Now().Before(r.until) {
		goruntime.Gosched()
	}
	return nil, r.err
}

func TestScheduledRunAs_EveryPendingRunAsRunWaitsWhenAnOutageOutlastsTheSendPass(t *testing.T) {
	const budget = 50 * time.Millisecond
	f := newRunAsFixture(t, true, &slowFailingRunAsResolver{
		directoryResolver: directoryResolver{dir: superAdminDirectory()},
		budget:            budget,
		err:               errPlatformUnreachable,
	})
	f.svc.sendBudget = budget
	f.svc.cfg.AutoPauseAfterConsecutiveFailures = 1
	first := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.Name, st.TimeoutSeconds = "first-report", 60 })
	second := f.dueRunAsTask(t, runAsPrincipal, func(st *store.ScheduledTask) { st.Name, st.TimeoutSeconds = "second-report", 60 })
	now := time.Now()

	mustRunPasses(t, f.svc, now)

	wantWaiting := []runView{{Status: "pending", Error: waitingNoteText, RunAs: runAsPrincipal}}
	for _, task := range []*store.ScheduledTask{first, second} {
		if diff := cmp.Diff(wantWaiting, f.runViews(t, task.ID)); diff != "" {
			t.Errorf("runs of task %s during the outage mismatch (-want +got):\n%s", task.ID, diff)
		}
	}

	mustRunPasses(t, f.svc, now.Add(10*time.Minute))

	wantEnded := []runView{{Status: "failed", Error: unconfirmedTimeoutText, RunAs: runAsPrincipal}}
	for _, task := range []*store.ScheduledTask{first, second} {
		if diff := cmp.Diff(wantEnded, f.runViews(t, task.ID)); diff != "" {
			t.Errorf("runs of task %s after the timeout mismatch (-want +got):\n%s", task.ID, diff)
		}
		f.assertUncounted(t, task.ID)
	}
}

const (
	otherPrincipal = "system:weekly"
	otherRowID     = "0193a1c2-7d4e-7f00-8a00-0000000000bb"
)

func twoSystemUsersDirectory() entitlementDirectory {
	dir := superAdminDirectory()
	dir[otherPrincipal] = auth.Entitlement{Roles: []string{"weekly_reporter"}, Scopes: []string{"agent:test-agent:invoke"}, SystemUserID: otherRowID}
	return dir
}

func (f *runAsFixture) dueRunAsTaskAt(t *testing.T, runAs string, ago time.Duration, edit func(*store.ScheduledTask)) *store.ScheduledTask {
	t.Helper()
	return f.dueRunAsTask(t, runAs, func(st *store.ScheduledTask) {
		dueMs := time.Now().Add(-ago).UnixMilli()
		st.NextRunAt = &dueMs
		st.Name = "report-" + idgen.New()
		if edit != nil {
			edit(st)
		}
	})
}

func TestScheduledRunAs_OneSystemUsersLookupFailureDoesNotHoldBackAnotherSystemUsersRun(t *testing.T) {
	cases := []struct {
		name          string
		unconfirmed   error
		degradedReply bool
	}{
		{"its row cannot be confirmed", errSystemUserRowUnknown, false},
		{"the platform answers degraded for it", nil, true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := twoSystemUsersDirectory()
			if c.degradedReply {
				dir[runAsPrincipal] = auth.Entitlement{Scopes: []string{"agent:test-agent:invoke"}, SystemUserID: runAsRowID, Resolution: identity.LookupDegraded}
			}
			resolver := &principalFailingResolver{directoryResolver: directoryResolver{dir: dir}}
			if c.unconfirmed != nil {
				resolver.runAsErrs = map[string]error{runAsPrincipal: c.unconfirmed}
			}
			f := newRunAsFixture(t, true, resolver)
			broken := f.dueRunAsTaskAt(t, runAsPrincipal, 2*time.Second, nil)
			healthy := f.dueRunAsTaskAt(t, otherPrincipal, time.Second, nil)

			mustRunPasses(t, f.svc, time.Now())

			wantBroken := []runView{{Status: "pending", Error: waitingNoteText, RunAs: runAsPrincipal}}
			if diff := cmp.Diff(wantBroken, f.runViews(t, broken.ID)); diff != "" {
				t.Errorf("run of the unconfirmed service account mismatch (-want +got):\n%s", diff)
			}
			wantHealthy := []runView{{Status: "running", RunAs: otherPrincipal}}
			if diff := cmp.Diff(wantHealthy, f.runViews(t, healthy.ID)); diff != "" {
				t.Errorf("run of the healthy service account mismatch (-want +got):\n%s", diff)
			}
			if diff := cmp.Diff([]string{runAsPrincipal, otherPrincipal}, resolver.askedRunAs()); diff != "" {
				t.Errorf("service accounts looked up mismatch (-want +got):\n%s", diff)
			}
			if got := f.publishedUserID(t); got != otherPrincipal {
				t.Errorf("wire user id = %q, want %q", got, otherPrincipal)
			}
			f.assertUncounted(t, broken.ID)
		})
	}
}

func TestScheduledRunAs_AnOldPodsAnswerForOneRunDoesNotMakeTheNextRunWait(t *testing.T) {
	f := newRunAsFixture(t, true, newRolloutResolver(1))
	first := f.dueRunAsTaskAt(t, runAsPrincipal, 2*time.Second, nil)
	second := f.dueRunAsTaskAt(t, runAsPrincipal, time.Second, nil)

	mustRunPasses(t, f.svc, time.Now())

	wantFirst := []runView{{Status: "pending", Error: platformUnreportedNoteText, RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantFirst, f.runViews(t, first.ID)); diff != "" {
		t.Errorf("run that met the old pod mismatch (-want +got):\n%s", diff)
	}
	wantSecond := []runView{{Status: "running", RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantSecond, f.runViews(t, second.ID)); diff != "" {
		t.Errorf("run after the old pod mismatch (-want +got):\n%s", diff)
	}
	if got := f.publishedUserID(t); got != runAsPrincipal {
		t.Errorf("wire user id = %q, want %q", got, runAsPrincipal)
	}
}

func TestScheduledRunAs_APlatformOutageIsWarnedOnceAndLaterRunsWaitWithoutALookup(t *testing.T) {
	resolver := &principalFailingResolver{
		directoryResolver: directoryResolver{dir: twoSystemUsersDirectory()},
		runAsErrs:         map[string]error{runAsPrincipal: errPlatformUnreachable},
	}
	f := newRunAsFixture(t, true, resolver)
	logs := newLogCapture()
	f.svc.logger = logs.logger
	finder := f.dueRunAsTaskAt(t, runAsPrincipal, 2*time.Second, nil)
	inheritor := f.dueRunAsTaskAt(t, otherPrincipal, time.Second, nil)

	mustRunPasses(t, f.svc, time.Now())

	type logged struct {
		Level, TaskID, Lookup string
		HasError              bool
	}
	var got []logged
	for _, line := range logs.lines() {
		if msg, _ := line["msg"].(string); !strings.Contains(msg, "to confirm the identity it runs as") {
			continue
		}
		lookup, _ := line["lookup"].(string)
		taskID, _ := line["taskID"].(string)
		level, _ := line["level"].(string)
		_, hasError := line["error"]
		got = append(got, logged{level, taskID, lookup, hasError})
	}
	want := []logged{
		{"WARN", finder.ID, "platform_lookup_failed", true},
		{"DEBUG", inheritor.ID, inheritedOutageLookup, false},
	}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("waiting log lines mismatch (-want +got):\n%s", diff)
	}
	wantWaiting := func(runAs string) []runView {
		return []runView{{Status: "pending", Error: waitingNoteText, RunAs: runAs}}
	}
	if diff := cmp.Diff(wantWaiting(runAsPrincipal), f.runViews(t, finder.ID)); diff != "" {
		t.Errorf("run that met the outage mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff(wantWaiting(otherPrincipal), f.runViews(t, inheritor.ID)); diff != "" {
		t.Errorf("run behind the outage mismatch (-want +got):\n%s", diff)
	}
	if diff := cmp.Diff([]string{runAsPrincipal}, resolver.askedRunAs()); diff != "" {
		t.Errorf("service accounts looked up mismatch (-want +got):\n%s", diff)
	}
}

func TestScheduledRunAs_AWaitingRunWarnsOnceAndNotesItsReason(t *testing.T) {
	f := newRunAsFixture(t, true, &failingLookupResolver{
		directoryResolver: directoryResolver{dir: superAdminDirectory()},
		runAsErr:          errSystemUserRowUnknown,
	})
	logs := newLogCapture()
	f.svc.logger = logs.logger
	task := f.dueRunAsTask(t, runAsPrincipal, nil)
	now := time.Now()

	for i := range 3 {
		mustRunPasses(t, f.svc, now.Add(time.Duration(i)*time.Second))
	}

	type logged struct{ Level, Lookup string }
	var got []logged
	for _, line := range logs.lines() {
		lookup, hasLookup := line["lookup"].(string)
		level, hasLevel := line["level"].(string)
		if hasLookup && hasLevel {
			got = append(got, logged{level, lookup})
		}
	}
	wantNoted := []runView{{Status: "pending", Error: waitingNoteText, RunAs: runAsPrincipal}}
	if diff := cmp.Diff(wantNoted, f.runViews(t, task.ID)); diff != "" {
		t.Errorf("runs mismatch (-want +got):\n%s", diff)
	}
	want := []logged{{"WARN", "system_user_row_unavailable"}, {"DEBUG", "system_user_row_unavailable"}, {"DEBUG", "system_user_row_unavailable"}}
	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("waiting log lines mismatch (-want +got):\n%s", diff)
	}
}

type answeringSigner struct {
	recordingSigner
	answer func(a2aTaskID string)
}

func (s *answeringSigner) SignTask(identity *auth.Subject, taskID string) (string, error) {
	token, err := s.recordingSigner.SignTask(identity, taskID)
	s.answer(taskID)
	return token, err
}

func TestScheduledRunAs_AnAnswerBeforeTheRunIsMarkedSubmittedStillKnowsTheSystemUser(t *testing.T) {
	f := newRunAsFixture(t, true, &directoryResolver{dir: superAdminDirectory()})
	f.svc.core.SetTaskSigner(&answeringSigner{answer: func(a2aTaskID string) {
		f.svc.handleAnswer(t.Context(), a2aTaskID, finalTask(a2a.TaskStateCompleted, "done"), nil)
	}})
	task := f.dueRunAsTask(t, runAsPrincipal, nil)

	mustRunPasses(t, f.svc, time.Now())

	run := f.onlyRun(t, task.ID)
	if diff := cmp.Diff(runView{Status: "completed", RunAs: runAsPrincipal}, f.runViews(t, task.ID)[0]); diff != "" {
		t.Errorf("run mismatch (-want +got):\n%s", diff)
	}
	if got := run.ArtifactUserID(task); got != runAsPrincipal {
		t.Errorf("artifacts of the answered run are read under %q, want %q, where the agent wrote them", got, runAsPrincipal)
	}
}

func TestScheduledRunAs_ARunKeepsTheIdentityItWasCreatedWith(t *testing.T) {
	type outcome struct {
		Status string
		Signed string
		RunAs  string
	}
	cases := []struct {
		name           string
		flagAtCreation bool
		flagAtSend     bool
		runAsAtCreate  string
		runAsAtSend    string
		want           outcome
	}{
		{"nothing changes", true, true, runAsPrincipal, runAsPrincipal, outcome{"running", runAsPrincipal, runAsPrincipal}},
		{"the flag is turned off after the run is created", true, false, runAsPrincipal, runAsPrincipal, outcome{"failed", "", runAsPrincipal}},
		{"the flag is turned on after the run is created", false, true, runAsPrincipal, runAsPrincipal, outcome{"running", runAsPrincipal, runAsPrincipal}},
		{"runAs is cleared after the run is created", true, true, runAsPrincipal, "", outcome{"running", runAsPrincipal, runAsPrincipal}},
		{"runAs is set after the run is created", true, true, "", runAsPrincipal, outcome{"running", runAsCreator, ""}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, c.flagAtCreation, &directoryResolver{dir: superAdminDirectory()})
			task := f.dueRunAsTask(t, c.runAsAtCreate, nil)
			if err := f.svc.firePass(t.Context(), time.Now()); err != nil {
				t.Fatalf("firePass: %v", err)
			}
			f.setFlag(c.flagAtSend)
			var update map[string]any
			if c.runAsAtSend == "" {
				update = map[string]any{"run_as": nil}
			} else {
				update = map[string]any{"run_as": c.runAsAtSend}
			}
			if err := f.svc.repo.Update(t.Context(), task.ID, update); err != nil {
				t.Fatalf("Update: %v", err)
			}

			if err := f.svc.sendPass(t.Context(), time.Now()); err != nil {
				t.Fatalf("sendPass: %v", err)
			}

			run := f.onlyRun(t, task.ID)
			got := outcome{Status: run.Status}
			if signed := f.signer.firstIdentity(); signed != nil {
				got.Signed = signed.UserID()
			}
			if run.RunAs != nil {
				got.RunAs = *run.RunAs
			}
			if diff := cmp.Diff(c.want, got); diff != "" {
				t.Errorf("run outcome mismatch (-want +got):\n%s", diff)
			}
		})
	}
}

func TestScheduledRunAs_ACallerWhoseScopesExcludeTheTargetIsRefusedByItsOwnScopes(t *testing.T) {
	cases := []struct {
		name         string
		creatorScope string
		systemScope  string
		runAs        string
		want         runView
	}{
		{"the service account lacks the target", "*:*:*", "agent:other-agent:invoke", runAsPrincipal,
			runView{Status: "failed", Error: "run-as service account not authorized to run the target agent", RunAs: runAsPrincipal}},
		{"the creator lacks the target", "agent:other-agent:invoke", "agent:test-agent:invoke", "",
			runView{Status: "failed", Error: "owner not authorized to run the target agent"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			dir := entitlementDirectory{
				runAsCreator:   {Scopes: []string{c.creatorScope}},
				runAsPrincipal: {Scopes: []string{c.systemScope}, SystemUserID: runAsRowID},
			}
			f := newRunAsFixture(t, true, &directoryResolver{dir: dir})
			task := f.dueRunAsTask(t, c.runAs, func(st *store.ScheduledTask) { st.MaxRetries = 2 })

			mustRunPasses(t, f.svc, time.Now())

			if diff := cmp.Diff([]runView{c.want}, f.runViews(t, task.ID)); diff != "" {
				t.Errorf("runs mismatch (-want +got):\n%s", diff)
			}
			budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
		})
	}
}

func TestScheduledRunAs_SpendBooksToTheSystemUsersRow(t *testing.T) {
	f := newRunAsFixture(t, true, &directoryResolver{dir: superAdminDirectory()})
	budgettest.BlockUsers(t, f.svc.core.BudgetGate(), time.Now().Add(time.Hour), budget.UserKey(runAsRowID, ""))
	runAsTask := f.dueRunAsTask(t, runAsPrincipal, nil)
	ownTask := f.dueRunAsTask(t, "", func(st *store.ScheduledTask) { st.Name = "creator-task" })

	mustRunPasses(t, f.svc, time.Now())

	if run := f.onlyRun(t, runAsTask.ID); run.Status != "skipped" || run.BudgetStop == nil {
		t.Errorf("run as the service account = {status %q, budgetStop %v}, want skipped by its row's spent budget", run.Status, run.BudgetStop)
	}
	if run := f.onlyRun(t, ownTask.ID); run.Status != "running" {
		t.Errorf("the creator's own run = %q, want running: the service account's budget is not the creator's", run.Status)
	}
	if refusal := f.svc.spentBudgetBlock(t.Context(), runAsTask); refusal == nil {
		t.Error("a manual run of the task passed the budget check its row's spent budget refuses")
	}
}

func TestScheduledRunAs_NotificationsReachTheCreatorWithTheRunsFiles(t *testing.T) {
	svc := newSchedulerSvcForTest(t, 0)
	brk := &notifTestBroker{}
	svc.SetNotifier(NewNotificationService(brk, testLogger()))
	creator := runAsCreator
	task, exec := seedFinishedRun(t, svc, "ui", nil, &creator, creator, "completed")
	if err := svc.repo.UpdateExecution(t.Context(), exec.ID, map[string]any{"run_as": runAsPrincipal}); err != nil {
		t.Fatal(err)
	}
	session := idgen.New()
	if err := svc.sessKeyRepo.CreateMapping(t.Context(), store.GatewayTypeScheduled, exec.ID, session); err != nil {
		t.Fatal(err)
	}
	art := svc.core.Services().Artifacts()
	if _, err := art.Inner.Save(t.Context(), art.Resolve("", runAsPrincipal, session, "report.csv"), []byte("x"), "text/csv"); err != nil {
		t.Fatal(err)
	}

	svc.dispatchNotifications(t.Context(), exec.ID, session, task, time.Now())

	if len(brk.published) != 1 {
		t.Fatalf("published %d NotifyRequests, want 1", len(brk.published))
	}
	var req a2a.NotifyRequest
	if err := json.Unmarshal(brk.published[0].payload, &req); err != nil {
		t.Fatal(err)
	}
	if req.RecipientUserID != runAsCreator {
		t.Errorf("notified %q, want the creator", req.RecipientUserID)
	}
	if len(req.Artifacts) != 1 || req.Artifacts[0].UserID != runAsPrincipal || req.Artifacts[0].Filename != "report.csv" {
		t.Errorf("artifacts = %+v, want the run's file under %s", req.Artifacts, runAsPrincipal)
	}
}

func TestScheduledTask_ARunWithNoOwnerFailsClosed(t *testing.T) {
	resolver := &stubIdentityResolver{ident: auth.NewTestSubject(auth.SubjectFields{
		UserID: "anyone", Scopes: []string{"agent:test-agent:invoke"}, Authenticated: true,
	})}
	f := newRunAsFixture(t, true, resolver)
	task := dueTask(t, f.svc, time.Now().Add(-time.Second), func(st *store.ScheduledTask) {
		st.CreatedBy, st.UserID = "", nil
	})

	mustRunPasses(t, f.svc, time.Now())

	if run := f.onlyRun(t, task.ID); run.Status != "failed" || *run.ErrorMessage != noOwnerText {
		t.Fatalf("run = {status %q, error %v}, want failed with %q", run.Status, run.ErrorMessage, noOwnerText)
	}
	if resolver.callCount() != 0 {
		t.Errorf("resolved an identity %d times for a task with no owner", resolver.callCount())
	}
	budgettest.AssertNotDelivered(t, f.svc.core.Services().Broker(), f.agentQ)
}

func TestCheckRunAsWrite(t *testing.T) {
	superAdmin := auth.NewTestSubject(auth.SubjectFields{UserID: runAsCreator, Scopes: []string{"*:*:*"}, Authenticated: true})
	ordinary := auth.NewTestSubject(auth.SubjectFields{UserID: "bob@example.com", Scopes: []string{"agent:*:invoke"}, Authenticated: true})
	cases := []struct {
		name     string
		flagOn   bool
		resolver IdentityResolver
		author   *auth.Subject
		runAs    string
		want     error
	}{
		{"a super administrator names a service account", true, &directoryResolver{dir: superAdminDirectory()}, superAdmin, runAsPrincipal, nil},
		{"the flag is off", false, &directoryResolver{dir: superAdminDirectory()}, superAdmin, runAsPrincipal, ErrRunAsDisabled},
		{"an ordinary user", true, &directoryResolver{dir: superAdminDirectory()}, ordinary, runAsPrincipal, ErrRunAsForbidden},
		{"no author", true, &directoryResolver{dir: superAdminDirectory()}, nil, runAsPrincipal, ErrRunAsForbidden},
		{"an unknown service account", true, &directoryResolver{dir: superAdminDirectory()}, superAdmin, "system:typo", ErrRunAsUnknownPrincipal},
		{"a person", true, &directoryResolver{dir: superAdminDirectory()}, superAdmin, "bob@example.com", auth.ErrInvalidScheduledRunAs},
		{"the eval service account", true, &directoryResolver{dir: superAdminDirectory()}, superAdmin, "system:eval", auth.ErrInvalidScheduledRunAs},
		{"a platform that reports no service account rows", true, &directoryResolver{dir: entitlementDirectory{runAsPrincipal: {Scopes: []string{"agent:test-agent:invoke"}}}}, superAdmin, runAsPrincipal, ErrRunAsPlatformTooOld},
		{"a resolver with no service accounts", true, ownerOnlyResolver{dir: superAdminDirectory()}, superAdmin, runAsPrincipal, ErrRunAsUnresolvable},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, c.flagOn, c.resolver)
			err := f.svc.CheckRunAsWrite(t.Context(), c.author, c.runAs)
			if !errors.Is(err, c.want) {
				t.Errorf("CheckRunAsWrite = %v, want %v", err, c.want)
			}
		})
	}
}

func TestCheckRunAsWrite_AnOldPodsAnswerIsAskedAgainBeforeSayingThePlatformIsTooOld(t *testing.T) {
	superAdmin := auth.NewTestSubject(auth.SubjectFields{UserID: runAsCreator, Scopes: []string{"*:*:*"}, Authenticated: true})
	cases := []struct {
		name       string
		oldReplies int
		want       error
	}{
		{"the first reply is from an old pod and the second from a new one", 1, nil},
		{"both replies are from old pods", 2, ErrRunAsPlatformTooOld},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			f := newRunAsFixture(t, true, newRolloutResolver(c.oldReplies))

			err := f.svc.CheckRunAsWrite(t.Context(), superAdmin, runAsPrincipal)

			if !errors.Is(err, c.want) {
				t.Errorf("CheckRunAsWrite = %v, want %v", err, c.want)
			}
		})
	}
}
