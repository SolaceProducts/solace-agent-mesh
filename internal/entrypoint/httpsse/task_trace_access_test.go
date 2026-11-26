// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
	platstore "github.com/SolaceProducts/solace-agent-mesh/internal/platform/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
)

const (
	tierOwner    = "tier-owner@example.com"
	tierReviewer = "tier-reviewer@example.com"
)

func TestSubjectKindsMatchStore(t *testing.T) {
	if rbac.SubjectKindAgent != store.TaskHandlerTypeAgent || rbac.SubjectKindWorkflow != store.TaskHandlerTypeWorkflow {
		t.Fatalf("rbac subject kinds %q/%q must equal store handler types %q/%q", rbac.SubjectKindAgent, rbac.SubjectKindWorkflow, store.TaskHandlerTypeAgent, store.TaskHandlerTypeWorkflow)
	}
	if string(store.SubjectTypeAgent) != rbac.SubjectKindAgent || string(store.SubjectTypeWorkflow) != rbac.SubjectKindWorkflow {
		t.Fatalf("store subject types must equal rbac subject kinds")
	}
}

type tierFixture struct {
	deps                                traceAccessDeps
	db                                  *store.DB
	root, child, grandchild, sibling    string
	rootSubject, hrSubject, procSubject string
	siblingSubject                      string
}

type tierFixtureOptions struct {
	rootKind    string
	withSibling bool
}

type tierFixtureOption func(*tierFixtureOptions)

func withRootKind(kind string) tierFixtureOption {
	return func(o *tierFixtureOptions) { o.rootKind = kind }
}

func withSibling() tierFixtureOption {
	return func(o *tierFixtureOptions) { o.withSibling = true }
}

func seedTierFixture(t *testing.T, rated bool, opts ...tierFixtureOption) tierFixture {
	t.Helper()
	cfg := tierFixtureOptions{rootKind: store.TaskHandlerTypeWorkflow}
	for _, opt := range opts {
		opt(&cfg)
	}

	th, taskRepo, _, _, _ := setupTaskHandler(t)
	ctx := context.Background()
	str := func(s string) *string { return &s }

	create := func(id string, parent *string, handler, kind string) {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: tierOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			ParentTaskID: parent, HandlerName: str(handler), HandlerType: str(kind),
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	rootID := idgen.New()
	var rootHandlerName, rootSubjectID, rootSubjectType string
	switch cfg.rootKind {
	case store.TaskHandlerTypeAgent:
		rootHandlerName, rootSubjectID, rootSubjectType = "root-bot", "root-bot", store.SubjectTypeAgent
	default:
		rootHandlerName = "workflow_01a0c9b5_94e6_743d_9896_a779500a1a6c"
		rootSubjectID, rootSubjectType = "01a0c9b5-94e6-743d-9896-a779500a1a6c", store.SubjectTypeWorkflow
	}
	create(rootID, nil, rootHandlerName, cfg.rootKind)

	f := tierFixture{root: rootID, rootSubject: rootSubjectID}
	childID, grandID := idgen.New(), idgen.New()
	create(childID, &rootID, "hr-bot", store.TaskHandlerTypeAgent)
	create(grandID, &childID, "procurement-bot", store.TaskHandlerTypeAgent)
	f.child, f.grandchild = childID, grandID
	f.hrSubject, f.procSubject = "hr-bot", "procurement-bot"
	if cfg.withSibling {
		siblingID := idgen.New()
		create(siblingID, &rootID, "finance-bot", store.TaskHandlerTypeAgent)
		f.sibling, f.siblingSubject = siblingID, "finance-bot"
	}

	fb := store.NewFeedbackRepo(th.core.Store())
	if rated {
		if _, err := fb.Submit(ctx, store.FeedbackSubmission{
			TaskID: rootID, UserID: tierOwner, SessionID: idgen.New(), Rating: "down",
			SubjectID: rootSubjectID, SubjectType: rootSubjectType, EntrypointType: store.EntrypointTypeWebUI,
		}); err != nil {
			t.Fatalf("save feedback: %v", err)
		}
	}
	f.db = th.core.Store()
	f.deps = traceAccessDeps{tasks: taskRepo, events: store.NewTaskEventRepo(f.db)}
	return f
}

func resolveFor(t *testing.T, f tierFixture, taskID, caller string, scopes ...string) traceAccess {
	t.Helper()
	return decideFor(t, f, taskID, caller, scopes...).tier
}

func decideFor(t *testing.T, f tierFixture, taskID, caller string, scopes ...string) traceDecision {
	t.Helper()
	ctx := context.Background()
	task, err := f.deps.tasks.GetByID(ctx, taskID)
	if err != nil || task == nil {
		t.Fatalf("load task %s: %v", taskID, err)
	}
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/", nil), caller, scopes...)
	got, err := resolveTraceAccess(req, f.deps, task, caller)
	if err != nil {
		t.Fatalf("resolveTraceAccess: %v", err)
	}
	return got
}

func TestResolveTraceAccess_Matrix(t *testing.T) {
	f := seedTierFixture(t, true)
	wfRead := rbac.WorkflowReadFeedbackScope(f.rootSubject)
	agTrace := rbac.AgentReadTraceScope

	tests := []struct {
		desc   string
		task   string
		caller string
		scopes []string
		want   traceAccess
	}{
		{"owner is full", f.root, tierOwner, nil, traceFull},
		{"activity wildcard is full", f.root, tierReviewer, []string{rbac.ScopeActivityRead}, traceFull},
		{"no reach at all is denied", f.root, tierReviewer, nil, traceDenied},
		{"agent feedback wildcard does not open a workflow root", f.root, tierReviewer, []string{rbac.ScopeAgentReadFeedbackAny}, traceDenied},
		{"agent trace wildcard alone is denied", f.root, tierReviewer, []string{rbac.ScopeAgentReadTraceAny}, traceDenied},
		{"workflow read on root alone is denied", f.root, tierReviewer, []string{wfRead}, traceDenied},
		{"workflow read plus one agent trace is denied", f.root, tierReviewer, []string{wfRead, agTrace(f.hrSubject)}, traceDenied},
		{"workflow read plus every agent trace is denied", f.root, tierReviewer, []string{wfRead, agTrace(f.hrSubject), agTrace(f.procSubject)}, traceDenied},
		{"workflow read plus agent trace wildcard is denied", f.root, tierReviewer, []string{wfRead, rbac.ScopeAgentReadTraceAny}, traceDenied},
		{"workflow feedback wildcard plus agent trace wildcard is denied", f.root, tierReviewer, []string{rbac.ScopeWorkflowReadFeedbackAny, rbac.ScopeAgentReadTraceAny}, traceDenied},
		{"the old feedback:*:read grants nothing", f.root, tierReviewer, []string{"feedback:*:read"}, traceDenied},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := resolveFor(t, f, tt.task, tt.caller, tt.scopes...); got != tt.want {
				t.Errorf("tier = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveTraceAccess_RootAgentIsCoveredLikeAnyOtherAgent(t *testing.T) {
	a := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent))
	agRead := rbac.AgentReadFeedbackScope
	agTrace := rbac.AgentReadTraceScope

	tests := []struct {
		desc   string
		task   string
		caller string
		scopes []string
		want   traceAccess
	}{
		{"agent read on an agent root alone is denied", a.root, tierReviewer, []string{agRead(a.rootSubject)}, traceDenied},
		{"workflow feedback wildcard does not open an agent root", a.root, tierReviewer, []string{rbac.ScopeWorkflowReadFeedbackAny}, traceDenied},
		{"feedback read is irrelevant: trace on every agent incl. root is full", a.root, tierReviewer, []string{agRead(a.rootSubject), agTrace(a.rootSubject), agTrace(a.hrSubject), agTrace(a.procSubject)}, traceFull},
		{"agent read plus trace on children but not root is denied", a.root, tierReviewer, []string{agRead(a.rootSubject), agTrace(a.hrSubject), agTrace(a.procSubject)}, traceDenied},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := resolveFor(t, a, tt.task, tt.caller, tt.scopes...); got != tt.want {
				t.Errorf("tier = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveTraceAccess_RejectsPartialGlobActivityRead(t *testing.T) {
	f := seedTierFixture(t, false)
	if got := resolveFor(t, f, f.root, tierReviewer, "activity:**:read"); got != traceDenied {
		t.Errorf("partial-glob activity scope: tier = %v, want denied", got)
	}
	if got := resolveFor(t, f, f.root, tierReviewer, rbac.ScopeActivityRead); got != traceFull {
		t.Errorf("sanity check, genuine activity wildcard: tier = %v, want full", got)
	}
}

func TestResolveTraceAccess_UnclassifiedParticipantIsRefused(t *testing.T) {
	f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent))
	ctx := context.Background()
	if err := f.deps.tasks.Create(ctx, &store.Task{
		ID: idgen.New(), UserID: tierOwner, StartTime: 2, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		ParentTaskID: &f.root,
	}); err != nil {
		t.Fatal(err)
	}
	agTrace := rbac.AgentReadTraceScope
	if got := resolveFor(t, f, f.root, tierReviewer, agTrace(f.rootSubject), agTrace(f.hrSubject), agTrace(f.procSubject)); got != traceDenied {
		t.Errorf("an unclassified participant must refuse the read, got %v", got)
	}
	if got := resolveFor(t, f, f.root, tierReviewer, rbac.ScopeAgentReadTraceAny); got != traceDenied {
		t.Errorf("the trace wildcard must not bypass an unclassified participant, got %v", got)
	}
}

func TestResolveTraceAccess_UnderivableRootSubjectDenies(t *testing.T) {
	th, taskRepo, _, _, _ := setupTaskHandler(t)
	ctx := context.Background()

	rootID := idgen.New()
	if err := taskRepo.Create(ctx, &store.Task{
		ID: rootID, UserID: tierOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}

	f := tierFixture{deps: traceAccessDeps{tasks: taskRepo, events: store.NewTaskEventRepo(th.core.Store())}, root: rootID}

	if got := resolveFor(t, f, f.root, tierOwner); got != traceFull {
		t.Errorf("owner must still get full when the root's own handler is unclassified, got %v", got)
	}
	if got := resolveFor(t, f, f.root, tierReviewer, rbac.ScopeAgentReadTraceAny); got != traceDenied {
		t.Errorf("a trace wildcard must not survive an unclassified root, got %v", got)
	}
}

func TestResolveTraceAccess_UncoveredForwardedPeerIsRefused(t *testing.T) {
	f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent))
	ctx := context.Background()

	events := store.NewTaskEventRepo(f.db)
	if err := events.Create(ctx, &store.TaskEvent{
		ID: idgen.New(), TaskID: f.root, CreatedTime: 1, Topic: "t", Direction: "in",
		Payload: `{"result":{"status":{"message":{"metadata":{"agent_name":"root-bot","forwarded_from_peer":"ghost-bot"}}}}}`,
	}); err != nil {
		t.Fatalf("create forwarded event: %v", err)
	}

	coversTree := []string{
		rbac.AgentReadTraceScope(f.rootSubject),
		rbac.AgentReadTraceScope(f.hrSubject),
		rbac.AgentReadTraceScope(f.procSubject),
	}

	if got := resolveFor(t, f, f.root, tierReviewer, coversTree...); got != traceDenied {
		t.Errorf("covering every task row but not the forwarded peer must refuse the read, got %v", got)
	}

	withPeer := append(append([]string{}, coversTree...), rbac.AgentReadTraceScope("ghost-bot"))
	if got := resolveFor(t, f, f.root, tierReviewer, withPeer...); got != traceFull {
		t.Errorf("covering the forwarded peer too must reach full, got %v", got)
	}

	if got := resolveFor(t, f, f.root, tierOwner); got != traceFull {
		t.Errorf("the owner is unaffected by peer coverage, got %v", got)
	}
	if got := resolveFor(t, f, f.root, tierReviewer, rbac.ScopeActivityRead); got != traceFull {
		t.Errorf("an activity wildcard holder is unaffected by peer coverage, got %v", got)
	}
}

func TestResolveTraceAccess_DescendantRequestStillCoversWholeTree(t *testing.T) {
	f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent), withSibling())

	trace := func(subjects ...string) []string {
		var out []string
		for _, s := range subjects {
			out = append(out, rbac.AgentReadTraceScope(s))
		}
		return out
	}

	for _, tc := range []struct {
		desc    string
		task    string
		missing string
		scopes  []string
	}{
		{"ancestor above the requested task is uncovered", f.grandchild, f.rootSubject,
			trace(f.hrSubject, f.procSubject, f.siblingSubject)},
		{"sibling branch beside the requested task is uncovered", f.child, f.siblingSubject,
			trace(f.rootSubject, f.hrSubject, f.procSubject)},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := resolveFor(t, f, tc.task, tierReviewer, tc.scopes...); got != traceDenied {
				t.Errorf("uncovered %s must refuse the read, got %v", tc.missing, got)
			}
		})
	}

	full := trace(f.rootSubject, f.hrSubject, f.procSubject, f.siblingSubject)
	for _, task := range []struct {
		desc string
		id   string
	}{{"child", f.child}, {"grandchild", f.grandchild}, {"root", f.root}} {
		t.Run("covering every agent reaches full from "+task.desc, func(t *testing.T) {
			if got := resolveFor(t, f, task.id, tierReviewer, full...); got != traceFull {
				t.Errorf("got %v, want full", got)
			}
		})
	}
}

func TestStimExportRootsNoHigherThanTheAuthorizer(t *testing.T) {
	if stimExportParentChainMaxDepth > traceParentChainMaxDepth {
		t.Errorf("STIM export depth %d exceeds the authorizer's %d: the export would root above the checked tree",
			stimExportParentChainMaxDepth, traceParentChainMaxDepth)
	}
}

func TestResolveTraceAccess_DecisionCarriesTheIDsItCovered(t *testing.T) {
	f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent), withSibling())
	ctx := context.Background()

	decisionFor := func(scopes ...string) traceDecision {
		t.Helper()
		task, err := f.deps.tasks.GetByID(ctx, f.root)
		if err != nil || task == nil {
			t.Fatalf("load root: %v", err)
		}
		req := withIdentity(httptest.NewRequest(http.MethodGet, "/", nil), tierReviewer, scopes...)
		got, err := resolveTraceAccess(req, f.deps, task, tierReviewer)
		if err != nil {
			t.Fatalf("resolveTraceAccess: %v", err)
		}
		return got
	}

	traceGrant := decisionFor(rbac.ScopeAgentReadTraceAny)
	if traceGrant.tier != traceFull {
		t.Fatalf("expected full, got %v (%s)", traceGrant.tier, traceGrant.reason)
	}
	covered := make(map[string]bool, len(traceGrant.scopedTo))
	for _, id := range traceGrant.scopedTo {
		covered[id] = true
	}
	for _, want := range []struct {
		desc string
		id   string
	}{{"root", f.root}, {"child", f.child}, {"grandchild", f.grandchild}, {"sibling", f.sibling}} {
		if !covered[want.id] {
			t.Errorf("a tree-derived decision must name the %s it checked", want.desc)
		}
	}

	late := idgen.New()
	parent := f.child
	if err := f.deps.tasks.Create(ctx, &store.Task{
		ID: late, UserID: tierOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		ParentTaskID: &parent, HandlerName: strPtr("late-bot"), HandlerType: strPtr(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create late child: %v", err)
	}
	if covered[late] {
		t.Error("a task created after the decision must not appear in the covered set")
	}

	for _, tc := range []struct {
		desc   string
		scopes []string
	}{{"activity wildcard", []string{rbac.ScopeActivityRead}}} {
		if got := decisionFor(tc.scopes...); got.tier != traceFull || len(got.scopedTo) != 0 {
			t.Errorf("%s must be full and unconfined, got tier=%v scopedTo=%d", tc.desc, got.tier, len(got.scopedTo))
		}
	}
}

func TestResolveTraceAccess_DecisionNamesItsGrant(t *testing.T) {
	f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent))
	ctx := context.Background()
	task, err := f.deps.tasks.GetByID(ctx, f.root)
	if err != nil || task == nil {
		t.Fatalf("load root: %v", err)
	}
	decisionFor := func(scopes ...string) traceDecision {
		t.Helper()
		req := withIdentity(httptest.NewRequest(http.MethodGet, "/", nil), tierReviewer, scopes...)
		got, err := resolveTraceAccess(req, f.deps, task, tierReviewer)
		if err != nil {
			t.Fatalf("resolveTraceAccess: %v", err)
		}
		return got
	}
	agRead, agTrace := rbac.AgentReadFeedbackScope, rbac.AgentReadTraceScope
	for _, tc := range []struct {
		desc   string
		scopes []string
		want   string
	}{
		{"activity wildcard", []string{rbac.ScopeActivityRead}, grantActivityRead},
		{"feedback read alone is refused and names no grant", []string{agRead(f.rootSubject)}, ""},
		{"a feedback read beside trace on every agent is still a trace full", []string{agRead(f.rootSubject), agTrace(f.hrSubject), agTrace(f.procSubject), agTrace(f.rootSubject)}, grantAgentTraceFull},
		{"trace on the root alone is refused and names no grant", []string{agTrace(f.rootSubject)}, ""},
		{"trace on every agent is a trace full", []string{agTrace(f.rootSubject), agTrace(f.hrSubject), agTrace(f.procSubject)}, grantAgentTraceFull},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := decisionFor(tc.scopes...); got.grant != tc.want {
				t.Errorf("grant = %q, want %q (tier %v)", got.grant, tc.want, got.tier)
			}
		})
	}
	if got := decisionFor(); got.grant != "" {
		t.Errorf("a refusal must carry no grant, got %q", got.grant)
	}
}

func TestResolveTraceAccess_TraceOnRootAgentOpensAnUnratedTree(t *testing.T) {
	a := seedTierFixture(t, false, withRootKind(store.TaskHandlerTypeAgent))
	agTrace := rbac.AgentReadTraceScope

	tests := []struct {
		desc   string
		scopes []string
		want   traceAccess
	}{
		{"trace on the root alone is denied", []string{agTrace(a.rootSubject)}, traceDenied},
		{"trace on the root and every child is full", []string{agTrace(a.rootSubject), agTrace(a.hrSubject), agTrace(a.procSubject)}, traceFull},
		{"trace wildcard is full", []string{rbac.ScopeAgentReadTraceAny}, traceFull},
		{"trace on the children but not the root opens nothing", []string{agTrace(a.hrSubject), agTrace(a.procSubject)}, traceDenied},
		{"feedback read without a rating opens nothing", []string{rbac.AgentReadFeedbackScope(a.rootSubject)}, traceDenied},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			if got := resolveFor(t, a, a.root, tierReviewer, tt.scopes...); got != tt.want {
				t.Errorf("tier = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResolveTraceAccess_TraceArmIsDecidedOnTheRoot(t *testing.T) {
	a := seedTierFixture(t, false, withRootKind(store.TaskHandlerTypeAgent))
	if got := resolveFor(t, a, a.child, tierReviewer, rbac.AgentReadTraceScope(a.hrSubject)); got != traceDenied {
		t.Errorf("trace on the child's agent alone: tier = %v, want denied", got)
	}
	if got := decideFor(t, a, a.child, tierReviewer, rbac.AgentReadTraceScope(a.rootSubject)); got.tier != traceDenied || got.reason != denyParticipantUncovered {
		t.Errorf("trace on the root agent, requested through the child: tier = %v reason = %q, want denied %q", got.tier, got.reason, denyParticipantUncovered)
	}
	if got := resolveFor(t, a, a.child, tierReviewer, rbac.AgentReadTraceScope(a.rootSubject), rbac.AgentReadTraceScope(a.hrSubject), rbac.AgentReadTraceScope(a.procSubject)); got != traceFull {
		t.Errorf("trace on every agent, requested through the child: tier = %v, want full", got)
	}
}

func TestResolveTraceAccess_TraceArmNeverOpensAWorkflowRoot(t *testing.T) {
	f := seedTierFixture(t, false)
	if got := resolveFor(t, f, f.root, tierReviewer, rbac.ScopeAgentReadTraceAny); got != traceDenied {
		t.Errorf("unrated workflow root with the trace wildcard: tier = %v, want denied", got)
	}
}

func TestResolveTraceAccess_ForwardedPeerNamesAreClassifiedBeforeChecking(t *testing.T) {
	const deployedUUID = "7c9e6679-7425-40de-944b-e07fc1f90ae7"
	deployedCardName := "agent_" + strings.ReplaceAll(deployedUUID, "-", "_")

	newFixture := func(t *testing.T, peerName string) tierFixture {
		t.Helper()
		f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent))
		if err := store.NewTaskEventRepo(f.db).Create(context.Background(), &store.TaskEvent{
			ID: idgen.New(), TaskID: f.root, CreatedTime: 1, Topic: "t", Direction: "in",
			Payload: `{"result":{"status":{"message":{"metadata":{"agent_name":"root-bot","forwarded_from_peer":"` + peerName + `"}}}}}`,
		}); err != nil {
			t.Fatalf("create forwarded event: %v", err)
		}
		return f
	}

	coversRows := func(f tierFixture) []string {
		return []string{
			rbac.AgentReadTraceScope(f.rootSubject),
			rbac.AgentReadTraceScope(f.hrSubject),
			rbac.AgentReadTraceScope(f.procSubject),
		}
	}

	t.Run("a deployed peer is covered by the grant on its dashed id", func(t *testing.T) {
		f := newFixture(t, deployedCardName)
		scopes := append(coversRows(f), rbac.AgentReadTraceScope(deployedUUID))
		if got := resolveFor(t, f, f.root, tierReviewer, scopes...); got != traceFull {
			t.Errorf("a card-name peer with its dashed-uuid grant must reach full, got %v", got)
		}
	})

	t.Run("a deployed peer without its grant still caps", func(t *testing.T) {
		f := newFixture(t, deployedCardName)
		if got := resolveFor(t, f, f.root, tierReviewer, coversRows(f)...); got != traceDenied {
			t.Errorf("got %v, want denied", got)
		}
	})

	t.Run("a deployed workflow peer is skipped, as workflows carry no trace scope", func(t *testing.T) {
		wfUUID := "01a0c9b5-94e6-743d-9896-a779500a1a6c"
		f := newFixture(t, "workflow_"+strings.ReplaceAll(wfUUID, "-", "_"))
		if got := resolveFor(t, f, f.root, tierReviewer, coversRows(f)...); got != traceFull {
			t.Errorf("a workflow-forwarded event must not block full, got %v", got)
		}
	})

	t.Run("an authored workflow peer is classified from its participant row", func(t *testing.T) {
		f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent))
		wfTask := idgen.New()
		parent := f.root
		if err := f.deps.tasks.Create(context.Background(), &store.Task{
			ID: wfTask, UserID: tierOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			ParentTaskID: &parent, HandlerName: ptr("payroll-flow"), HandlerType: ptr(store.TaskHandlerTypeWorkflow),
		}); err != nil {
			t.Fatalf("create workflow task: %v", err)
		}
		if err := store.NewTaskEventRepo(f.db).Create(context.Background(), &store.TaskEvent{
			ID: idgen.New(), TaskID: f.root, CreatedTime: 1, Topic: "t", Direction: "in",
			Payload: `{"result":{"status":{"message":{"metadata":{"forwarded_from_peer":"payroll-flow"}}}}}`,
		}); err != nil {
			t.Fatalf("create forwarded event: %v", err)
		}
		if got := resolveFor(t, f, f.root, tierReviewer, coversRows(f)...); got != traceFull {
			t.Errorf("an authored workflow peer must be skipped, not demanded as an agent grant, got %v", got)
		}
	})

	t.Run("a plain name matching no participant needs a grant naming it", func(t *testing.T) {
		f := newFixture(t, "ghost-bot")
		if got := resolveFor(t, f, f.root, tierReviewer, coversRows(f)...); got != traceDenied {
			t.Errorf("without a grant naming the peer, got %v, want denied", got)
		}
		withPeer := append(coversRows(f), rbac.AgentReadTraceScope("ghost-bot"))
		if got := resolveFor(t, f, f.root, tierReviewer, withPeer...); got != traceFull {
			t.Errorf("with a grant naming the peer, got %v, want full", got)
		}
	})
}

func ptr(s string) *string { return &s }

func TestResolveTraceAccess_TraceArmFailsClosedOnAnOrphan(t *testing.T) {
	th, taskRepo, _, _, _ := setupTaskHandler(t)
	ctx := context.Background()

	missingParent := idgen.New()
	orphan := idgen.New()
	if err := taskRepo.Create(ctx, &store.Task{
		ID: orphan, UserID: tierOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		ParentTaskID: &missingParent, HandlerName: ptr("orphan-bot"), HandlerType: ptr(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create orphan: %v", err)
	}
	f := tierFixture{root: orphan, deps: traceAccessDeps{
		tasks: taskRepo, events: store.NewTaskEventRepo(th.core.Store()),
	}}

	if got := resolveFor(t, f, orphan, tierReviewer, rbac.AgentReadTraceScope("orphan-bot")); got != traceDenied {
		t.Errorf("trace on the orphan's own agent: tier = %v, want denied", got)
	}
	if got := resolveFor(t, f, orphan, tierReviewer, rbac.ScopeAgentReadTraceAny); got != traceDenied {
		t.Errorf("trace wildcard on an orphan: tier = %v, want denied", got)
	}
	if got := resolveFor(t, f, orphan, tierOwner); got != traceFull {
		t.Errorf("owner of an orphan: tier = %v, want full", got)
	}
}

func TestTraceReachOpensTask_RefusalReasons(t *testing.T) {
	f := seedTierFixture(t, false, withRootKind(store.TaskHandlerTypeAgent))
	task, err := f.deps.tasks.GetByID(context.Background(), f.root)
	if err != nil || task == nil {
		t.Fatalf("load root: %v", err)
	}
	agTrace := rbac.AgentReadTraceScope

	for _, tc := range []struct {
		desc   string
		scopes []string
		want   string
	}{
		{"no grant", nil, denyNoGrant},
		{"trace on a child only", []string{agTrace(f.hrSubject)}, denySubjectNotInReach},
		{"trace on the root but not every participant", []string{agTrace(f.rootSubject)}, denyParticipantUncovered},
		{"trace on every participant opens", []string{agTrace(f.rootSubject), agTrace(f.hrSubject), agTrace(f.procSubject)}, ""},
	} {
		req := withIdentity(httptest.NewRequest(http.MethodGet, "/", nil), tierReviewer, tc.scopes...)
		got, err := traceReachOpensTask(req.Context(), f.deps, req, task)
		if err != nil {
			t.Fatalf("%s: traceReachOpensTask: %v", tc.desc, err)
		}
		if reason := got.reason; reason != tc.want {
			t.Errorf("%s: got reason %q, want %q", tc.desc, reason, tc.want)
		}
		if opened := got.tier == traceFull; opened != (tc.want == "") {
			t.Errorf("%s: opened=%v, want %v", tc.desc, opened, tc.want == "")
		}
	}
}

func TestTraceReachOpensTask_RefusesATreeTheWalkCannotFinish(t *testing.T) {
	th, taskRepo, _, _, _ := setupTaskHandler(t)
	ctx := context.Background()
	deps := traceAccessDeps{
		tasks: taskRepo, events: store.NewTaskEventRepo(th.core.Store()),
	}
	const rootBot, otherBot = "root-bot", "other-bot"
	agTrace := rbac.AgentReadTraceScope

	create := func(parent *string, handler string) string {
		t.Helper()
		id := idgen.New()
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: tierOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			ParentTaskID: parent, HandlerName: ptr(handler), HandlerType: ptr(store.TaskHandlerTypeAgent),
		}); err != nil {
			t.Fatalf("create task: %v", err)
		}
		return id
	}
	chain := func(levels int) (root, bottom string) {
		root = create(nil, rootBot)
		parent := root
		for range levels {
			parent = create(ptr(parent), rootBot)
		}
		return root, create(ptr(parent), otherBot)
	}
	decide := func(taskID string, scopes ...string) traceDecision {
		t.Helper()
		task, err := taskRepo.GetByID(ctx, taskID)
		if err != nil || task == nil {
			t.Fatalf("load task: %v", err)
		}
		req := withIdentity(httptest.NewRequest(http.MethodGet, "/", nil), tierReviewer, scopes...)
		rctx, cancel := context.WithTimeout(req.Context(), 30*time.Second)
		defer cancel()
		got, err := traceReachOpensTask(rctx, deps, req.WithContext(rctx), task)
		if err != nil {
			t.Fatalf("traceReachOpensTask: %v", err)
		}
		return got
	}

	withinRoot, _ := chain(store.TaskTreeMaxDepth - 2)
	if got := decide(withinRoot, agTrace(rootBot)); got.tier != traceDenied || got.reason != denyParticipantUncovered {
		t.Errorf("otherBot within the bound: tier = %v reason = %q, want denied %q (decided, not cut short)", got.tier, got.reason, denyParticipantUncovered)
	}

	beyondRoot, beyondBottom := chain(store.TaskTreeMaxDepth)
	if got := decide(beyondRoot, agTrace(rootBot)); got.tier != traceDenied || got.reason != denyTreeTooDeep {
		t.Errorf("otherBot below the bound: tier = %v reason = %q, want denied %q", got.tier, got.reason, denyTreeTooDeep)
	}
	if got := decide(beyondBottom, agTrace(rootBot), agTrace(otherBot)); got.tier != traceDenied || got.reason != denyTreeTooDeep {
		t.Errorf("request from below the bound: tier = %v reason = %q, want denied %q", got.tier, got.reason, denyTreeTooDeep)
	}
	req := withIdentity(httptest.NewRequest(http.MethodGet, "/", nil), tierReviewer, rbac.ScopeAgentReadTraceAny)
	task, _ := taskRepo.GetByID(ctx, beyondRoot)
	if got, err := resolveTraceAccess(req, deps, task, tierReviewer); err != nil || got.tier != traceDenied || got.reason != denyTreeTooDeep {
		t.Errorf("resolveTraceAccess on the deep tree: tier = %v reason = %q err = %v, want denied %q", got.tier, got.reason, err, denyTreeTooDeep)
	}

	first, second := idgen.New(), idgen.New()
	for _, row := range []struct{ id, parent string }{{first, second}, {second, first}} {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: row.id, UserID: tierOwner, StartTime: 1, ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			ParentTaskID: ptr(row.parent), HandlerName: ptr(rootBot), HandlerType: ptr(store.TaskHandlerTypeAgent),
		}); err != nil {
			t.Fatalf("create cycle row: %v", err)
		}
	}
	if got := decide(first, rbac.ScopeAgentReadTraceAny); got.tier != traceDenied || got.reason != denyTreeTooDeep {
		t.Errorf("cycle: tier = %v reason = %q, want denied %q", got.tier, got.reason, denyTreeTooDeep)
	}
}

func TestResolveTraceAccess_RefusalReasons(t *testing.T) {
	f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent))
	agTrace := rbac.AgentReadTraceScope
	for _, tc := range []struct {
		desc   string
		scopes []string
		want   string
	}{
		{"no scopes at all", nil, denyNoGrant},
		{"feedback read on the root is no trace grant", []string{rbac.AgentReadFeedbackScope(f.rootSubject)}, denyNoGrant},
		{"trace on some other agent", []string{agTrace("some-other-agent")}, denySubjectNotInReach},
		{"trace on the root but not every participant", []string{agTrace(f.rootSubject)}, denyParticipantUncovered},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := decideFor(t, f, f.root, tierReviewer, tc.scopes...); got.tier != traceDenied || got.reason != tc.want {
				t.Errorf("tier = %v reason = %q, want denied %q", got.tier, got.reason, tc.want)
			}
		})
	}
}

func TestResolveTraceAccess_FeedbackGrantsNeverOpenATrace(t *testing.T) {
	f := seedTierFixture(t, true, withRootKind(store.TaskHandlerTypeAgent))
	agTrace := rbac.AgentReadTraceScope
	for _, tc := range []struct {
		desc   string
		scopes []string
	}{
		{"agent feedback read on the root", []string{rbac.AgentReadFeedbackScope(f.rootSubject)}},
		{"both feedback wildcards", []string{rbac.ScopeAgentReadFeedbackAny, rbac.ScopeWorkflowReadFeedbackAny}},
		{"feedback read plus trace below the root", []string{rbac.AgentReadFeedbackScope(f.rootSubject), agTrace(f.hrSubject), agTrace(f.procSubject)}},
		{"feedback read plus trace on the root only", []string{rbac.AgentReadFeedbackScope(f.rootSubject), agTrace(f.rootSubject)}},
	} {
		t.Run(tc.desc, func(t *testing.T) {
			if got := resolveFor(t, f, f.root, tierReviewer, tc.scopes...); got != traceDenied {
				t.Errorf("tier = %v, want denied", got)
			}
		})
	}
}

func builtinSAMManagerScopes(t *testing.T) []string {
	t.Helper()
	repo := platstore.NewRBACRepo(platstore.TestDB(t))
	role, err := repo.GetRoleByName(t.Context(), "sam_manager")
	if err != nil || role == nil {
		t.Fatalf("load sam_manager: role %v, err %v", role, err)
	}
	_, scopes, _, err := repo.GetRoleByID(t.Context(), role.ID)
	if err != nil || len(scopes) == 0 {
		t.Fatalf("load sam_manager scopes: %v, err %v", scopes, err)
	}
	return scopes
}

func TestResolveTraceAccess_SAMManagerOpensNoOtherUsersTask(t *testing.T) {
	scopes := builtinSAMManagerScopes(t)
	tests := []struct {
		desc  string
		rated bool
		opts  []tierFixtureOption
	}{
		{"unrated agent root", false, []tierFixtureOption{withRootKind(store.TaskHandlerTypeAgent)}},
		{"rated agent root", true, []tierFixtureOption{withRootKind(store.TaskHandlerTypeAgent)}},
		{"rated workflow root", true, nil},
	}
	for _, tt := range tests {
		t.Run(tt.desc, func(t *testing.T) {
			f := seedTierFixture(t, tt.rated, tt.opts...)
			if got := resolveFor(t, f, f.root, tierReviewer, scopes...); got != traceDenied {
				t.Errorf("tier = %v, want denied", got)
			}
		})
	}
}
