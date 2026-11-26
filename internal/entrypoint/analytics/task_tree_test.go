// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func sp(s string) *string { return &s }

func TestTaskTree_SingleNodeRoot(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	rootID := "70000000-0000-0000-0000-000000000001"

	if err := taskRepo.Create(ctx, &store.Task{
		ID: rootID, UserID: "alice", StartTime: base,
		Status:        sp("completed"),
		ExecutionMode: "foreground",
		Kind:          store.TaskKindNormal,
		HandlerName:   sp("Orchestrator"),
		HandlerType:   sp(store.TaskHandlerTypeAgent),
		EndTime:       i64p(base + 1_000),
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: "80000000-0000-0000-0000-000000000001", TaskID: rootID,
		CreatedTime: base, Topic: "test/req", Direction: "request", Payload: `{}`,
	}); err != nil {
		t.Fatalf("create event: %v", err)
	}

	svc := NewService(db)
	tree, err := svc.TaskTree(ctx, rootID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.QueriedTaskID != rootID {
		t.Errorf("QueriedTaskID = %q, want %q", tree.QueriedTaskID, rootID)
	}
	if tree.Root.ID != rootID {
		t.Errorf("Root.ID = %q, want %q", tree.Root.ID, rootID)
	}
	if !tree.Root.IsQueried {
		t.Error("Root.IsQueried = false, want true (queried task IS the root)")
	}
	if tree.Root.Depth != 0 {
		t.Errorf("Root.Depth = %d, want 0", tree.Root.Depth)
	}
	if len(tree.Tree) != 0 {
		t.Errorf("Tree len = %d, want 0 (no descendants)", len(tree.Tree))
	}
	if tree.Root.EventsTotal != 1 {
		t.Errorf("Root.EventsTotal = %d, want 1", tree.Root.EventsTotal)
	}
	if tree.TreeEventsTotal != 1 {
		t.Errorf("TreeEventsTotal = %d, want 1", tree.TreeEventsTotal)
	}
	if tree.TreeCapped {
		t.Error("TreeCapped = true, want false (single root, nothing truncated)")
	}
}

func TestTaskTree_QueriedIsChild_WalksUpToRoot_SiblingPruneIsNotTruncation(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	rootID := "70000000-0000-0000-0000-000000000100"
	childA := "70000000-0000-0000-0000-000000000101"
	childB := "70000000-0000-0000-0000-000000000102"
	grandchild := "70000000-0000-0000-0000-000000000103"

	mustCreate := func(id string, parent *string, name string, typ string, status string) {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "alice", StartTime: base,
			Status: sp(status), ExecutionMode: "foreground",
			Kind: store.TaskKindNormal, HandlerName: sp(name),
			HandlerType: sp(typ), ParentTaskID: parent, EndTime: i64p(base + 500),
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mustCreate(rootID, nil, "ResearchOpsWorkflow", store.TaskHandlerTypeWorkflow, "completed")
	mustCreate(childA, &rootID, "DeepNewsPipeline", store.TaskHandlerTypeWorkflow, "failed")
	mustCreate(childB, &rootID, "DeepTechPipeline", store.TaskHandlerTypeWorkflow, "completed")
	mustCreate(grandchild, &childA, "ResearchPlanner", store.TaskHandlerTypeAgent, "failed")

	for i, id := range []string{rootID, childA, childB, grandchild} {
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID:          fmt.Sprintf("80000000-0000-0000-0000-%012d", i+1),
			TaskID:      id,
			CreatedTime: base + int64(i),
			Topic:       "test/req",
			Direction:   "request",
			Payload:     `{}`,
		}); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	svc := NewService(db)

	tree, err := svc.TaskTree(ctx, grandchild, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.QueriedTaskID != grandchild {
		t.Errorf("QueriedTaskID = %q, want %q", tree.QueriedTaskID, grandchild)
	}
	if tree.Root.ID != rootID {
		t.Errorf("Root.ID = %q, want %q (walk-up did not reach the ancestor)", tree.Root.ID, rootID)
	}
	if tree.Root.IsQueried {
		t.Error("Root.IsQueried = true, want false (root is not the queried node)")
	}
	if len(tree.Tree) != 2 {
		t.Fatalf("Tree len = %d, want 2 (childA + grandchild — childB pruned as sibling of ancestor)", len(tree.Tree))
	}
	for i := range tree.Tree {
		if tree.Tree[i].ID == childB {
			t.Errorf("childB leaked into tree — sibling of an ancestor must be pruned")
		}
	}
	if tree.TreeCapped {
		t.Error("TreeCapped = true, want false (lineage complete, subtree fully shown — sibling pruning is not truncation)")
	}
	queriedCount := 0
	if tree.Root.IsQueried {
		queriedCount++
	}
	var grandchildNode *TreeNodeDTO
	for i := range tree.Tree {
		if tree.Tree[i].IsQueried {
			queriedCount++
		}
		if tree.Tree[i].ID == grandchild {
			grandchildNode = &tree.Tree[i]
		}
	}
	if queriedCount != 1 {
		t.Errorf("IsQueried marked on %d nodes, want exactly 1", queriedCount)
	}
	if grandchildNode == nil {
		t.Fatal("grandchild not found in Tree slice")
	}
	if !grandchildNode.IsQueried {
		t.Error("grandchild.IsQueried = false, want true")
	}
	if grandchildNode.Depth != 2 {
		t.Errorf("grandchild.Depth = %d, want 2 (root=0, childA=1, grandchild=2)", grandchildNode.Depth)
	}
	if tree.Root.EventsTotal != 0 {
		t.Errorf("Root.EventsTotal = %d, want 0 (ancestor is a metadata-only breadcrumb)", tree.Root.EventsTotal)
	}
	for i := range tree.Tree {
		want := 0
		if tree.Tree[i].ID == grandchild {
			want = 1
		}
		if tree.Tree[i].EventsTotal != want {
			t.Errorf("Tree[%d] (%s).EventsTotal = %d, want %d", i, tree.Tree[i].ID, tree.Tree[i].EventsTotal, want)
		}
	}
	if tree.TreeEventsTotal != 1 {
		t.Errorf("TreeEventsTotal = %d, want 1 (only the queried grandchild; ancestors are metadata-only)", tree.TreeEventsTotal)
	}
}

func TestTaskTree_ScopeEnforcement(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	bobsTask := "70000000-0000-0000-0000-000000000200"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: bobsTask, UserID: "bob", StartTime: base,
		Status: sp("completed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("bob_agent"),
		HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create bob task: %v", err)
	}

	svc := NewService(db)

	if _, err := svc.TaskTree(ctx, bobsTask, "alice", false); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("alice→bob's task: err = %v, want ErrTaskNotFound", err)
	}
	if _, err := svc.TaskTree(ctx, bobsTask, "bob", false); err != nil {
		t.Errorf("bob→own task: %v, want success", err)
	}
	if _, err := svc.TaskTree(ctx, bobsTask, "admin-user", true); err != nil {
		t.Errorf("admin→bob's task: %v, want success", err)
	}
	if _, err := svc.TaskTree(ctx, "70000000-0000-0000-0000-deadbeefdead", "alice", false); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("missing task: err = %v, want ErrTaskNotFound", err)
	}
}

func TestTaskTree_PerNodeHeadTailSampling(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	rootID := "70000000-0000-0000-0000-000000000300"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: rootID, UserID: "alice", StartTime: base,
		Status: sp("failed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("BusyAgent"),
		HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	for i := 0; i < 100; i++ {
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID:          fmt.Sprintf("80000000-0000-0000-0000-%012d", i+300),
			TaskID:      rootID,
			CreatedTime: base + int64(i)*1000,
			Topic:       fmt.Sprintf("test/event-%04d", i),
			Direction:   "status",
			Payload:     statusPayload(fmt.Sprintf(`{"type":"agent_progress_update","status_text":"event-%04d"}`, i)),
		}); err != nil {
			t.Fatalf("create event %d: %v", i, err)
		}
	}

	svc := NewService(db)
	tree, err := svc.TaskTree(ctx, rootID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.Root.EventsTotal != 100 {
		t.Errorf("Root.EventsTotal = %d, want 100 (true count, not the capped fetch)", tree.Root.EventsTotal)
	}
	if !tree.Root.EventsCapped {
		t.Error("Root.EventsCapped = false, want true")
	}
	if len(tree.Root.Events) != DefaultTreeEventsPerTask {
		t.Errorf("Root.Events len = %d, want %d", len(tree.Root.Events), DefaultTreeEventsPerTask)
	}
	if tree.Root.Events[0].Summary != "event-0000" {
		t.Errorf("Events[0].Summary = %q, want event-0000", tree.Root.Events[0].Summary)
	}
	lastIdx := len(tree.Root.Events) - 1
	if tree.Root.Events[lastIdx].Summary != "event-0099" {
		t.Errorf("Events[last].Summary = %q, want event-0099 (terminal event MUST be visible)", tree.Root.Events[lastIdx].Summary)
	}
}

func TestTaskTree_EventsTotalIsTrueCountNotFetchSize(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	const totalEvents, markers = 100, 10
	taskID := "70000000-0000-0000-0000-000000001201"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "alice", StartTime: base,
		Status: sp("failed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("BusyAgent"), HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	for i := range totalEvents {
		payload := statusPayload(fmt.Sprintf(`{"type":"agent_progress_update","status_text":"e-%03d"}`, i))
		if i < markers {
			payload = statusPayload(`{"type":"llm_invocation"}`)
		}
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: fmt.Sprintf("80000000-0000-4000-b000-%012d", i), TaskID: taskID,
			CreatedTime: base + int64(i)*1000, Topic: "test/e", Direction: "status", Payload: payload,
		}); err != nil {
			t.Fatalf("create event %d: %v", i, err)
		}
	}

	tree, err := NewService(db).TaskTree(ctx, taskID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if want := totalEvents - markers; tree.Root.EventsTotal != want {
		t.Errorf("EventsTotal = %d, want %d (true count minus dropped noise; NOT the %d-row fetch)",
			tree.Root.EventsTotal, want, 2*DefaultTreeEventsPerTask)
	}
	if len(tree.Root.Events) != DefaultTreeEventsPerTask {
		t.Errorf("len(Events) = %d, want %d", len(tree.Root.Events), DefaultTreeEventsPerTask)
	}
	if !tree.Root.EventsCapped {
		t.Error("EventsCapped = false, want true")
	}
	for _, e := range tree.Root.Events {
		if e.Kind == eventKindLLMInvocation {
			t.Errorf("llm_invocation marker leaked into rendered events: %+v", e)
		}
	}
}

func TestTaskTree_DropsLLMInvocationNoise(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	taskID := "70000000-0000-0000-0000-000000000400"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "alice", StartTime: base,
		Status: sp("completed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("Agent"),
		HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	events := []*store.TaskEvent{
		{ID: "80000000-0000-0000-0000-000000000401", TaskID: taskID, CreatedTime: base + 1, Topic: "test/llm-1", Direction: "status", Payload: statusPayload(`{"type":"llm_invocation"}`)},
		{ID: "80000000-0000-0000-0000-000000000402", TaskID: taskID, CreatedTime: base + 2, Topic: "test/tool", Direction: "status", Payload: statusPayload(`{"type":"tool_invocation_start","tool_name":"x"}`)},
		{ID: "80000000-0000-0000-0000-000000000403", TaskID: taskID, CreatedTime: base + 3, Topic: "test/llm-2", Direction: "status", Payload: statusPayload(`{"type":"llm_invocation"}`)},
	}
	for _, e := range events {
		if err := eventRepo.Create(ctx, e); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	tree, err := NewService(db).TaskTree(ctx, taskID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if got := len(tree.Root.Events); got != 1 {
		t.Fatalf("Root.Events len = %d, want 1 (llm_invocation dropped)", got)
	}
	if tree.Root.Events[0].Kind != "tool_start" {
		t.Errorf("kept kind = %q, want tool_start", tree.Root.Events[0].Kind)
	}
	if tree.Root.EventsTotal != 1 {
		t.Errorf("Root.EventsTotal = %d, want 1 (markers not counted)", tree.Root.EventsTotal)
	}
	if tree.Root.EventsCapped {
		t.Error("Root.EventsCapped = true, want false (1 event, nothing dropped)")
	}
}

func TestTaskTree_EventsScopedToQueriedAndDescendants(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	root := "70000000-0000-0000-0000-000000000500"
	child := "70000000-0000-0000-0000-000000000501"
	grand := "70000000-0000-0000-0000-000000000502"

	mk := func(id, evID, parent string) {
		task := &store.Task{
			ID: id, UserID: "alice", StartTime: base, Status: sp("failed"),
			ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: sp("A"), HandlerType: sp(store.TaskHandlerTypeAgent),
		}
		if parent != "" {
			task.ParentTaskID = sp(parent)
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: evID, TaskID: id, CreatedTime: base, Direction: "status",
			Payload: statusPayload(`{"type":"agent_progress_update","status_text":"hi"}`),
		}); err != nil {
			t.Fatalf("event %s: %v", id, err)
		}
	}
	mk(root, "80000000-0000-0000-0000-000000000500", "")
	mk(child, "80000000-0000-0000-0000-000000000501", root)
	mk(grand, "80000000-0000-0000-0000-000000000502", child)

	svc := NewService(db)
	tree, err := svc.TaskTree(ctx, child, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}

	if len(tree.Root.Events) != 0 {
		t.Errorf("ancestor (root) has %d events, want 0", len(tree.Root.Events))
	}
	var queried, descendant *TreeNodeDTO
	for i := range tree.Tree {
		switch tree.Tree[i].ID {
		case child:
			queried = &tree.Tree[i]
		case grand:
			descendant = &tree.Tree[i]
		}
	}
	if queried == nil || len(queried.Events) == 0 {
		t.Error("queried node must carry its own events")
	}
	if descendant == nil || len(descendant.Events) == 0 {
		t.Error("descendant node must carry its own events")
	}
}

func TestTaskTree_TreeCappedByChildrenCount(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	root := "70000000-0000-0000-0000-000000000600"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: root, UserID: "alice", StartTime: base, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("R"), HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	for i := 0; i < MaxTreeTasks+1; i++ {
		id := fmt.Sprintf("70000000-0000-0000-0000-%012d", 700+i)
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "alice", StartTime: base + int64(i), Status: sp("completed"),
			ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: sp("C"), HandlerType: sp(store.TaskHandlerTypeAgent), ParentTaskID: sp(root),
		}); err != nil {
			t.Fatalf("create child %d: %v", i, err)
		}
	}

	svc := NewService(db)
	tree, err := svc.TaskTree(ctx, root, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if !tree.TreeCapped {
		t.Error("TreeCapped = false, want true (children fetch hit MaxTreeTasks)")
	}
	if len(tree.Tree) != MaxTreeTasks {
		t.Errorf("Tree len = %d, want %d (descendants truncated at cap)", len(tree.Tree), MaxTreeTasks)
	}
}

func TestTaskTree_HideDurationOnTimeout(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	root := "70000000-0000-0000-0000-0000000d0001"
	child := "70000000-0000-0000-0000-0000000d0002"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: root, UserID: "alice", StartTime: base, Status: sp("timeout"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("R"), HandlerType: sp(store.TaskHandlerTypeAgent),
		EndTime: i64p(base + 900_000),
	}); err != nil {
		t.Fatalf("create timeout root: %v", err)
	}
	if err := taskRepo.Create(ctx, &store.Task{
		ID: child, UserID: "alice", StartTime: base + 10, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("C"), HandlerType: sp(store.TaskHandlerTypeAgent),
		ParentTaskID: sp(root), EndTime: i64p(base + 1_010),
	}); err != nil {
		t.Fatalf("create completed child: %v", err)
	}

	tree, err := NewService(db).TaskTree(ctx, root, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if !tree.Root.HideDuration {
		t.Error("Root.HideDuration = false, want true (timeout EndTime is a sweep artifact, not runtime)")
	}
	found := false
	for i := range tree.Tree {
		if tree.Tree[i].ID == child {
			found = true
			if tree.Tree[i].HideDuration {
				t.Error("child.HideDuration = true, want false (a completed task's duration is real)")
			}
		}
	}
	if !found {
		t.Fatal("completed child missing from tree")
	}
}

func TestTaskTree_TreeEventsCappedByBudget(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	root := "70000000-0000-0000-0000-000000000800"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: root, UserID: "alice", StartTime: base, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("R"), HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	nodes := MaxTreeTotalEvents/DefaultTreeEventsPerTask + 3
	evID := 0
	for c := 0; c < nodes; c++ {
		cid := fmt.Sprintf("70000000-0000-0000-0000-%012d", 900+c)
		if err := taskRepo.Create(ctx, &store.Task{
			ID: cid, UserID: "alice", StartTime: base + int64(c), Status: sp("completed"),
			ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: sp("C"), HandlerType: sp(store.TaskHandlerTypeAgent), ParentTaskID: sp(root),
		}); err != nil {
			t.Fatalf("create child %d: %v", c, err)
		}
		for e := 0; e <= DefaultTreeEventsPerTask; e++ {
			evID++
			if err := eventRepo.Create(ctx, &store.TaskEvent{
				ID:     fmt.Sprintf("80000000-0000-0000-0000-%012d", evID),
				TaskID: cid, CreatedTime: base + int64(evID), Direction: "status",
				Payload: statusPayload(`{"type":"agent_progress_update","status_text":"x"}`),
			}); err != nil {
				t.Fatalf("create event: %v", err)
			}
		}
	}

	svc := NewService(db)
	tree, err := svc.TaskTree(ctx, root, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if !tree.TreeEventsCapped {
		t.Error("TreeEventsCapped = false, want true (aggregate exceeded MaxTreeTotalEvents)")
	}
	if tree.TreeEventsTotal <= MaxTreeTotalEvents {
		t.Errorf("TreeEventsTotal = %d, want > %d (true totals reported even when capped)", tree.TreeEventsTotal, MaxTreeTotalEvents)
	}
	rendered := len(tree.Root.Events)
	for i := range tree.Tree {
		rendered += len(tree.Tree[i].Events)
	}
	if rendered > MaxTreeTotalEvents {
		t.Errorf("rendered events = %d across all nodes, want <= %d (budget must bound materialized events, not just the reported total)", rendered, MaxTreeTotalEvents)
	}
}

func TestTaskTree_CrossUserLeafBlocked(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	bobsRoot := "70000000-0000-0000-0000-000000000600"
	alicesChild := "70000000-0000-0000-0000-000000000601"

	if err := taskRepo.Create(ctx, &store.Task{
		ID: bobsRoot, UserID: "bob", StartTime: base,
		Status: sp("completed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("BobsWorkflow"),
		HandlerType: sp(store.TaskHandlerTypeWorkflow),
	}); err != nil {
		t.Fatalf("create bob root: %v", err)
	}
	if err := taskRepo.Create(ctx, &store.Task{
		ID: alicesChild, UserID: "alice", StartTime: base,
		Status: sp("completed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("AlicesLeaf"),
		HandlerType:  sp(store.TaskHandlerTypeAgent),
		ParentTaskID: sp(bobsRoot),
	}); err != nil {
		t.Fatalf("create alice's leaf: %v", err)
	}

	svc := NewService(db)

	if _, err := svc.TaskTree(ctx, alicesChild, "alice", false); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("non-admin cross-user tree: err = %v, want ErrTaskNotFound", err)
	}

	if _, err := svc.TaskTree(ctx, alicesChild, "admin-user", true); err != nil {
		t.Errorf("admin cross-user tree: %v, want success", err)
	}

	alicesRoot := "70000000-0000-0000-0000-000000000602"
	bobsLeaf := "70000000-0000-0000-0000-000000000603"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: alicesRoot, UserID: "alice", StartTime: base,
		Status: sp("completed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("AlicesRoot"),
		HandlerType: sp(store.TaskHandlerTypeWorkflow),
	}); err != nil {
		t.Fatalf("create alice root: %v", err)
	}
	if err := taskRepo.Create(ctx, &store.Task{
		ID: bobsLeaf, UserID: "bob", StartTime: base,
		Status: sp("completed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("BobsLeaf"),
		HandlerType:  sp(store.TaskHandlerTypeAgent),
		ParentTaskID: sp(alicesRoot),
	}); err != nil {
		t.Fatalf("create bob's leaf: %v", err)
	}
	if _, err := svc.TaskTree(ctx, alicesRoot, "alice", false); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("non-admin tree with cross-user descendant: err = %v, want ErrTaskNotFound", err)
	}
}

func TestTaskTree_LineageOnly_ForFanOutRoot(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	rootID := "70000000-0000-0000-0000-000000000700"
	mustCreate := func(id string, parent *string, name string, typ string) {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "alice", StartTime: base,
			Status: sp("completed"), ExecutionMode: "foreground",
			Kind: store.TaskKindNormal, HandlerName: sp(name),
			HandlerType: sp(typ), ParentTaskID: parent, EndTime: i64p(base + 100),
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mustCreate(rootID, nil, "MyAssistant", store.TaskHandlerTypeAgent)

	const queriedIdx = 17
	var queriedParent, queriedLeaf string
	for i := 0; i < 40; i++ {
		wfID := fmt.Sprintf("70000000-0000-0000-0000-1000000007%02d", i+10)
		mustCreate(wfID, &rootID, "DeepTechPipeline", store.TaskHandlerTypeWorkflow)
		if i == queriedIdx {
			queriedParent = wfID
			queriedLeaf = fmt.Sprintf("70000000-0000-0000-0000-2000000007%02d", i+10)
			mustCreate(queriedLeaf, &wfID, "HackerNewsScraper", store.TaskHandlerTypeAgent)
		}
	}

	svc := NewService(db)
	tree, err := svc.TaskTree(ctx, queriedLeaf, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}

	if tree.Root.ID != rootID {
		t.Errorf("Root.ID = %q, want %q", tree.Root.ID, rootID)
	}
	if len(tree.Tree) != 2 {
		t.Fatalf("Tree len = %d, want 2 (queriedParent + queriedLeaf) — 39 sibling workflows must be pruned", len(tree.Tree))
	}
	for i := range tree.Tree {
		if tree.Tree[i].ID != queriedParent && tree.Tree[i].ID != queriedLeaf {
			t.Errorf("tree[%d].ID = %q, not on the queried lineage — sibling branches leaked", i, tree.Tree[i].ID)
		}
	}
	if tree.TreeCapped {
		t.Error("TreeCapped = true, want false (lineage complete; ancestor-sibling pruning is not truncation)")
	}
}

func TestTaskTree_WalkDownDepth3_FanOutWithSiblings(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	svc := NewService(db)

	const alice = "alice"
	const base = 1_700_000_000_000

	root := "70000000-0000-0000-0000-00000000aa00"
	a := "70000000-0000-0000-0000-00000000aa01"
	a1 := "70000000-0000-0000-0000-00000000aa02"
	a2 := "70000000-0000-0000-0000-00000000aa03"
	a3 := "70000000-0000-0000-0000-00000000aa04"
	q := "70000000-0000-0000-0000-00000000aa05"

	q1 := "70000000-0000-0000-0000-00000000bb01"
	q2 := "70000000-0000-0000-0000-00000000bb02"
	q3 := "70000000-0000-0000-0000-00000000bb03"
	q4 := "70000000-0000-0000-0000-00000000bb04"
	q1a := "70000000-0000-0000-0000-00000000cc01"
	q1b := "70000000-0000-0000-0000-00000000cc02"
	q2a := "70000000-0000-0000-0000-00000000cc03"
	q1a1 := "70000000-0000-0000-0000-00000000dd01"
	q1a2 := "70000000-0000-0000-0000-00000000dd02"
	q2a1 := "70000000-0000-0000-0000-00000000dd03"
	q1a1x := "70000000-0000-0000-0000-00000000ee01"

	sibOfA1 := "70000000-0000-0000-0000-00000000ff01"
	sibOfA2 := "70000000-0000-0000-0000-00000000ff02"
	sibOfA3 := "70000000-0000-0000-0000-00000000ff03"
	sibOfA1Kid := "70000000-0000-0000-0000-00000000ffa1"
	sibOfQ := "70000000-0000-0000-0000-00000000ff04"
	sibOfQKid := "70000000-0000-0000-0000-00000000ffa4"

	seed := []struct {
		id, parent string
		offset     int64
	}{
		{root, "", 0},
		{a, root, 100},
		{a1, a, 200},
		{a2, a1, 300},
		{a3, a2, 400},
		{q, a3, 500},

		{q1, q, 600},
		{q2, q, 610},
		{q3, q, 620},
		{q4, q, 630},
		{q1a, q1, 700},
		{q1b, q1, 710},
		{q2a, q2, 720},
		{q1a1, q1a, 800},
		{q1a2, q1a, 810},
		{q2a1, q2a, 820},
		{q1a1x, q1a1, 900},

		{sibOfA1, root, 150},
		{sibOfA1Kid, sibOfA1, 250},
		{sibOfA2, a, 220},
		{sibOfA3, a1, 320},
		{sibOfQ, a3, 450},
		{sibOfQKid, sibOfQ, 550},
	}
	for _, s := range seed {
		task := &store.Task{
			ID: s.id, UserID: alice, StartTime: base + s.offset,
			Status:        sp("completed"),
			ExecutionMode: "foreground",
			Kind:          store.TaskKindNormal,
			HandlerName:   sp(fmt.Sprintf("Agent-%s", s.id[len(s.id)-4:])),
			HandlerType:   sp(store.TaskHandlerTypeAgent),
			EndTime:       i64p(base + s.offset + 50),
		}
		if s.parent != "" {
			task.ParentTaskID = sp(s.parent)
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", s.id, err)
		}
	}

	tree, err := svc.TaskTree(ctx, q, alice, false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}

	if tree.QueriedTaskID != q {
		t.Errorf("QueriedTaskID = %q, want %q", tree.QueriedTaskID, q)
	}
	if tree.Root.ID != root {
		t.Fatalf("Root.ID = %q, want %q (walk-up did not reach the topmost ancestor)", tree.Root.ID, root)
	}
	if tree.Root.Depth != 0 {
		t.Errorf("Root.Depth = %d, want 0", tree.Root.Depth)
	}

	wantDepth := map[string]int{
		a: 1, a1: 2, a2: 3, a3: 4, q: 5,
		q1: 6, q2: 6, q3: 6, q4: 6,
		q1a: 7, q1b: 7, q2a: 7,
		q1a1: 8, q1a2: 8, q2a1: 8,
	}
	forbidden := map[string]string{
		q1a1x:      "level 4 below Q — must be pruned by MaxTreeWalkDownDepth",
		sibOfA1:    "sibling of A (Root's other kid)",
		sibOfA1Kid: "subtree of a sibling of A",
		sibOfA2:    "sibling of A1",
		sibOfA3:    "sibling of A2",
		sibOfQ:     "sibling of A3 (queried's aunt)",
		sibOfQKid:  "subtree of the queried's aunt",
	}

	got := make(map[string]int, len(tree.Tree))
	for i := range tree.Tree {
		id := tree.Tree[i].ID
		if reason, banned := forbidden[id]; banned {
			t.Errorf("forbidden node %q leaked into tree[] (reason: %s)", id, reason)
		}
		if _, dup := got[id]; dup {
			t.Errorf("node %q appears more than once in tree[]", id)
		}
		got[id] = tree.Tree[i].Depth
	}
	for id, want := range wantDepth {
		gotN, ok := got[id]
		if !ok {
			t.Errorf("expected node %q at depth %d missing from tree[]", id, want)
			continue
		}
		if gotN != want {
			t.Errorf("node %q depth = %d, want %d", id, gotN, want)
		}
	}

	if len(tree.Tree) != len(wantDepth) {
		t.Errorf("tree[] len = %d, want %d (expected: 4 ancestors + queried + 10 descendants ≤ level 3)",
			len(tree.Tree), len(wantDepth))
	}

	queriedMarks := 0
	if tree.Root.IsQueried {
		queriedMarks++
	}
	for i := range tree.Tree {
		if tree.Tree[i].IsQueried {
			queriedMarks++
			if tree.Tree[i].ID != q {
				t.Errorf("IsQueried marked on %q, want %q", tree.Tree[i].ID, q)
			}
		}
	}
	if queriedMarks != 1 {
		t.Errorf("IsQueried marked on %d nodes, want exactly 1", queriedMarks)
	}

	if !tree.TreeCapped {
		t.Error("TreeCapped = false, want true (subtree deeper than MaxTreeWalkDownDepth was truncated)")
	}
}

func TestTaskTree_WalkDownDepth3(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	queried := "70000000-0000-0000-0000-000000000900"
	d1 := "70000000-0000-0000-0000-000000000901"
	d2 := "70000000-0000-0000-0000-000000000902"
	d3 := "70000000-0000-0000-0000-000000000903"
	d4 := "70000000-0000-0000-0000-000000000904"

	chain := []struct {
		id, parent string
		offset     int64
	}{
		{queried, "", 0},
		{d1, queried, 100},
		{d2, d1, 200},
		{d3, d2, 300},
		{d4, d3, 400},
	}
	for _, c := range chain {
		task := &store.Task{
			ID: c.id, UserID: "alice", StartTime: base + c.offset,
			Status:        sp("completed"),
			ExecutionMode: "foreground",
			Kind:          store.TaskKindNormal,
			HandlerName:   sp("Agent" + c.id[len(c.id)-3:]),
			HandlerType:   sp(store.TaskHandlerTypeAgent),
			EndTime:       i64p(base + c.offset + 50),
		}
		if c.parent != "" {
			task.ParentTaskID = sp(c.parent)
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", c.id, err)
		}
	}

	svc := NewService(db)
	tree, err := svc.TaskTree(ctx, queried, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.QueriedTaskID != queried {
		t.Fatalf("QueriedTaskID = %q, want %q", tree.QueriedTaskID, queried)
	}
	if tree.Root.ID != queried {
		t.Fatalf("Root.ID = %q, want %q (queried IS root — no ancestors)", tree.Root.ID, queried)
	}

	got := make(map[string]int, len(tree.Tree))
	for i := range tree.Tree {
		got[tree.Tree[i].ID] = tree.Tree[i].Depth
	}
	if _, ok := got[d4]; ok {
		t.Errorf("great-great-grandchild (d4) leaked into tree — should be pruned by MaxTreeWalkDownDepth=%d", MaxTreeWalkDownDepth)
	}
	for id, wantDepth := range map[string]int{d1: 1, d2: 2, d3: 3} {
		gotDepth, ok := got[id]
		if !ok {
			t.Errorf("descendant %s at depth %d missing from tree — depth=%d walk-down did not surface it", id, wantDepth, MaxTreeWalkDownDepth)
			continue
		}
		if gotDepth != wantDepth {
			t.Errorf("descendant %s depth = %d, want %d (root=0, depth increments by 1 per hop below queried)", id, gotDepth, wantDepth)
		}
	}
	if len(tree.Tree) != 3 {
		t.Errorf("tree len = %d, want 3 (d1, d2, d3 — d4 pruned)", len(tree.Tree))
	}
}

func TestTaskTree_DeltaEventsExcluded(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	id := "71000000-0000-0000-0000-000000000001"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: id, UserID: "alice", StartTime: base, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal, EndTime: i64p(base + 1000),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if err := eventRepo.Create(ctx, &store.TaskEvent{
		ID: "81000000-0000-0000-0000-000000000001", TaskID: id,
		CreatedTime: base, Topic: "test/status", Direction: "response", Payload: `{"state":"completed"}`,
	}); err != nil {
		t.Fatalf("create structural event: %v", err)
	}
	for i := 0; i < 3; i++ {
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: fmt.Sprintf("81000000-0000-0000-0000-00000000010%d", i), TaskID: id,
			CreatedTime: base + int64(i) + 1, Topic: "test/delta", Direction: "response",
			Payload: `"chunk"`, IsDelta: true,
		}); err != nil {
			t.Fatalf("create delta event %d: %v", i, err)
		}
	}

	tree, err := NewService(db).TaskTree(ctx, id, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.Root.EventsTotal != 1 {
		t.Errorf("Root.EventsTotal = %d, want 1 (only the structural event; deltas excluded)", tree.Root.EventsTotal)
	}
}

func TestTaskTree_UnknownOwnerAncestorTolerated(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	rootID := "72000000-0000-0000-0000-000000000001"
	childID := "72000000-0000-0000-0000-000000000002"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: rootID, UserID: "unknown", StartTime: base, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal, EndTime: i64p(base + 1000),
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := taskRepo.Create(ctx, &store.Task{
		ID: childID, UserID: "alice", StartTime: base + 1, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal, ParentTaskID: sp(rootID),
		EndTime: i64p(base + 1000),
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	if _, err := NewService(db).TaskTree(ctx, childID, "alice", false); err != nil {
		t.Errorf("owner querying own task with an 'unknown'-owned ancestor must succeed; got %v", err)
	}
}

func TestTaskTree_RealOtherUserAncestorRejected(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	rootID := "73000000-0000-0000-0000-000000000001"
	childID := "73000000-0000-0000-0000-000000000002"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: rootID, UserID: "bob", StartTime: base, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal, EndTime: i64p(base + 1000),
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := taskRepo.Create(ctx, &store.Task{
		ID: childID, UserID: "alice", StartTime: base + 1, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal, ParentTaskID: sp(rootID),
		EndTime: i64p(base + 1000),
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	if _, err := NewService(db).TaskTree(ctx, childID, "alice", false); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("ancestor owned by a different real user must fail closed (ErrTaskNotFound); got %v", err)
	}
}

func TestTaskTree_NonAdminCannotAnchorOnUnknownTask(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	id := "74000000-0000-0000-0000-000000000001"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: id, UserID: "unknown", StartTime: base, Status: sp("completed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal, EndTime: i64p(base + 1000),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	if _, err := NewService(db).TaskTree(ctx, id, "alice", false); !errors.Is(err, ErrTaskNotFound) {
		t.Errorf("non-admin anchoring on an 'unknown'-owned task must fail closed; got %v", err)
	}
}

func TestTaskTree_CycleSafety(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	a := "70000000-0000-0000-0000-000000000a01"
	b := "70000000-0000-0000-0000-000000000a02"
	mk := func(id, name string, parent *string) {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "alice", StartTime: base,
			Status: sp("completed"), ExecutionMode: "foreground",
			Kind: store.TaskKindNormal, HandlerName: sp(name),
			HandlerType: sp(store.TaskHandlerTypeAgent), ParentTaskID: parent,
			EndTime: i64p(base + 500),
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk(a, "AgentA", &b)
	mk(b, "AgentB", &a)

	svc := NewService(db)

	done := make(chan struct{})
	var tree *TaskTreeResponse
	var err error
	go func() {
		defer close(done)
		tree, err = svc.TaskTree(ctx, a, "alice", false)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("TaskTree hung on cyclic parent_task_id — visited guard did not fire")
	}
	if err != nil {
		t.Fatalf("TaskTree over cyclic parent_task_id errored: %v", err)
	}
	seen := map[string]bool{tree.Root.ID: true}
	for i := range tree.Tree {
		id := tree.Tree[i].ID
		if seen[id] {
			t.Errorf("task %s appears more than once in the tree — cycle produced a duplicate node", id)
		}
		seen[id] = true
	}
}

func TestTaskTree_AncestorEventsOmitted(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	root := "70000000-0000-0000-0000-000000001001"
	child := "70000000-0000-0000-0000-000000001002"
	mk := func(id string, parent *string) {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "alice", StartTime: base,
			Status: sp("failed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: sp("N" + id[len(id)-1:]), HandlerType: sp(store.TaskHandlerTypeAgent),
			ParentTaskID: parent,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	mk(root, nil)
	mk(child, sp(root))
	for i, tid := range []string{root, child} {
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: fmt.Sprintf("80000000-0000-4000-a000-%012d", i), TaskID: tid,
			CreatedTime: base + int64(i), Topic: "test/e", Direction: "status",
			Payload: statusPayload(`{"type":"agent_progress_update","status_text":"x"}`),
		}); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	tree, err := NewService(db).TaskTree(ctx, child, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if !tree.Root.EventsOmitted {
		t.Error("Root (an ancestor) must carry EventsOmitted=true")
	}
	if len(tree.Root.Events) != 0 || tree.Root.EventsTotal != 0 {
		t.Errorf("ancestor events = %d (total %d), want 0 — ancestors are not fetched",
			len(tree.Root.Events), tree.Root.EventsTotal)
	}
	var queried *TreeNodeDTO
	for i := range tree.Tree {
		if tree.Tree[i].IsQueried {
			queried = &tree.Tree[i]
		}
	}
	if queried == nil {
		t.Fatal("queried node missing from tree")
	}
	if queried.EventsOmitted {
		t.Error("queried node must NOT be flagged EventsOmitted — its events are fetched")
	}
	if len(queried.Events) != 1 {
		t.Errorf("queried events = %d, want 1", len(queried.Events))
	}
}

func TestTaskTree_EventFreeNodeNotCapped(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	taskID := "70000000-0000-0000-0000-000000001101"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "alice", StartTime: base,
		Status: sp("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("Quiet"), HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	tree, err := NewService(db).TaskTree(ctx, taskID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.Root.EventsCapped {
		t.Error("EventsCapped = true for a task with zero events, want false")
	}
}

func TestTaskTree_NodeProjection(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	taskID := "70000000-0000-0000-0000-000000000d01"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "alice", StartTime: base, EndTime: i64p(base + 1500),
		Status: sp("failed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName:      sp("ResearchAgent"),
		HandlerType:      sp(store.TaskHandlerTypeAgent),
		ErrorMessage:     sp("AI provider 502: upstream timed out"),
		TotalInputTokens: ip(4210), TotalOutputTokens: ip(380), TotalCachedInputTokens: ip(1024),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	tree, err := NewService(db).TaskTree(ctx, taskID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	n := tree.Root
	if n.ErrorMessage != "AI provider 502: upstream timed out" {
		t.Errorf("ErrorMessage = %q, want the task's error_message verbatim", n.ErrorMessage)
	}
	if n.Name != "ResearchAgent" {
		t.Errorf("Name = %q, want %q (handler_name, slug-resolved)", n.Name, "ResearchAgent")
	}
	if n.Type != store.TaskHandlerTypeAgent {
		t.Errorf("Type = %q, want %q", n.Type, store.TaskHandlerTypeAgent)
	}
	if n.TotalInputTokens != 4210 || n.TotalOutputTokens != 380 || n.TotalCachedInputTokens != 1024 {
		t.Errorf("tokens = (in %d, out %d, cached %d), want (4210, 380, 1024)",
			n.TotalInputTokens, n.TotalOutputTokens, n.TotalCachedInputTokens)
	}
	if n.Status != "failed" || n.StatusEmoji != "❌" {
		t.Errorf("status/emoji = (%q, %q), want (failed, ❌)", n.Status, n.StatusEmoji)
	}
	if n.DurationMs == nil || *n.DurationMs != 1500 {
		t.Errorf("DurationMs = %v, want 1500", n.DurationMs)
	}
}

func TestTaskTree_UnknownHandlerTypeFallback(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	taskID := "70000000-0000-0000-0000-000000000d02"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "alice", StartTime: base,
		Status: sp("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("GhostAgent"),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	tree, err := NewService(db).TaskTree(ctx, taskID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.Root.Type != "unknown" {
		t.Errorf("Type = %q, want %q for a NULL handler_type", tree.Root.Type, "unknown")
	}
}

func TestTaskTree_EventsCapBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		events     int
		wantCapped bool
		wantLen    int
	}{
		{"exactly at cap", DefaultTreeEventsPerTask, false, DefaultTreeEventsPerTask},
		{"one over cap", DefaultTreeEventsPerTask + 1, true, DefaultTreeEventsPerTask},
	} {
		t.Run(tc.name, func(t *testing.T) {
			db := store.TestDB(t)
			ctx := t.Context()
			taskRepo := store.NewTaskRepo(db)
			eventRepo := store.NewTaskEventRepo(db)

			const base = 1_700_000_000_000
			taskID := "70000000-0000-0000-0000-000000000e01"
			if err := taskRepo.Create(ctx, &store.Task{
				ID: taskID, UserID: "alice", StartTime: base,
				Status: sp("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
				HandlerName: sp("Agent"), HandlerType: sp(store.TaskHandlerTypeAgent),
			}); err != nil {
				t.Fatalf("create task: %v", err)
			}
			for i := range tc.events {
				if err := eventRepo.Create(ctx, &store.TaskEvent{
					ID:          fmt.Sprintf("80000000-0000-4000-9000-%012d", i),
					TaskID:      taskID,
					CreatedTime: base + int64(i)*1000,
					Topic:       "test/e",
					Direction:   "status",
					Payload:     statusPayload(fmt.Sprintf(`{"type":"agent_progress_update","status_text":"e-%03d"}`, i)),
				}); err != nil {
					t.Fatalf("create event %d: %v", i, err)
				}
			}

			tree, err := NewService(db).TaskTree(ctx, taskID, "alice", false)
			if err != nil {
				t.Fatalf("TaskTree: %v", err)
			}
			if tree.Root.EventsCapped != tc.wantCapped {
				t.Errorf("%d events: EventsCapped = %v, want %v", tc.events, tree.Root.EventsCapped, tc.wantCapped)
			}
			if tree.Root.EventsTotal != tc.events {
				t.Errorf("%d events: EventsTotal = %d, want %d", tc.events, tree.Root.EventsTotal, tc.events)
			}
			if len(tree.Root.Events) != tc.wantLen {
				t.Errorf("%d events: len(Events) = %d, want %d", tc.events, len(tree.Root.Events), tc.wantLen)
			}
		})
	}
}

func TestTaskTree_TreeOrderedByDepth(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	root := "70000000-0000-0000-0000-000000000f01"
	mk := func(id string, parent *string, off int) {
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: "alice", StartTime: base + int64(off),
			Status: sp("completed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: sp("A" + id[len(id)-2:]), HandlerType: sp(store.TaskHandlerTypeAgent),
			ParentTaskID: parent,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}
	d1 := "70000000-0000-0000-0000-000000000f02"
	d2 := "70000000-0000-0000-0000-000000000f03"
	d3 := "70000000-0000-0000-0000-000000000f04"
	d1b := "70000000-0000-0000-0000-000000000f05"
	mk(root, nil, 0)
	mk(d1, sp(root), 10)
	mk(d2, sp(d1), 20)
	mk(d3, sp(d2), 30)
	mk(d1b, sp(root), 40)

	tree, err := NewService(db).TaskTree(ctx, root, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if len(tree.Tree) != 4 {
		t.Fatalf("tree has %d nodes, want 4 (d1, d1b, d2, d3)", len(tree.Tree))
	}
	for i := 1; i < len(tree.Tree); i++ {
		if tree.Tree[i].Depth < tree.Tree[i-1].Depth {
			t.Errorf("tree[] not depth-ascending: index %d depth %d follows depth %d",
				i, tree.Tree[i].Depth, tree.Tree[i-1].Depth)
		}
	}
}

func TestTaskTree_SurfacesOwnerForAudit_NeverSerialized(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	taskID := "70000000-0000-0000-0000-000000000c01"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "bob", StartTime: base,
		Status: sp("failed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("Agent"),
		HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	tree, err := NewService(db).TaskTree(ctx, taskID, "admin", true)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.Owner != "bob" {
		t.Errorf("Owner = %q, want %q (the queried task's user_id)", tree.Owner, "bob")
	}
	blob, err := json.Marshal(tree)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	if strings.Contains(string(blob), "bob") {
		t.Errorf("owner leaked onto the wire: %s", blob)
	}
}

func TestTaskTree_SelfParentNotDuplicated(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	q := "70000000-0000-0000-0000-000000000b01"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: q, UserID: "alice", StartTime: base,
		Status: sp("failed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("SelfParentAgent"),
		HandlerType: sp(store.TaskHandlerTypeAgent), ParentTaskID: sp(q),
		EndTime: i64p(base + 500),
	}); err != nil {
		t.Fatalf("create: %v", err)
	}

	tree, err := NewService(db).TaskTree(ctx, q, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	if tree.Root.ID != q {
		t.Errorf("Root.ID = %q, want %q", tree.Root.ID, q)
	}
	if len(tree.Tree) != 0 {
		t.Errorf("tree has %d nodes, want 0 — a self-parent must not appear as its own descendant", len(tree.Tree))
	}
}

func TestTaskTree_WalkUpHopsCap(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	svc := NewService(db)

	const base = 1_700_000_000_000
	const chainLen = 12
	ids := make([]string, chainLen+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("70000000-0000-0000-0000-0000000ac0%02d", i)
	}
	for i := range ids {
		task := &store.Task{
			ID: ids[i], UserID: "alice", StartTime: base + int64(i),
			Status:        sp("completed"),
			ExecutionMode: "foreground",
			Kind:          store.TaskKindNormal,
			HandlerName:   sp(fmt.Sprintf("Agent-%02d", i)),
			HandlerType:   sp(store.TaskHandlerTypeAgent),
			EndTime:       i64p(base + int64(i) + 10),
		}
		if i < chainLen {
			task.ParentTaskID = sp(ids[i+1])
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", ids[i], err)
		}
	}

	queried := ids[0]
	trueRoot := ids[chainLen]
	hop11 := ids[chainLen-1]
	wantRoot := ids[MaxTreeWalkUpHops]

	tree, err := svc.TaskTree(ctx, queried, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}

	if tree.Root.ID != wantRoot {
		t.Errorf("Root.ID = %q, want %q (hop-%d ancestor — walk-up must stop at the cap)", tree.Root.ID, wantRoot, MaxTreeWalkUpHops)
	}
	if tree.Root.ID == trueRoot {
		t.Errorf("Root.ID = trueRoot %q — walk-up exceeded MaxTreeWalkUpHops=%d", trueRoot, MaxTreeWalkUpHops)
	}

	present := map[string]int{tree.Root.ID: tree.Root.Depth}
	for i := range tree.Tree {
		present[tree.Tree[i].ID] = tree.Tree[i].Depth
	}
	if len(present) != MaxTreeWalkUpHops+1 {
		t.Errorf("tree carries %d nodes, want %d (queried + %d ancestors up to the cap)", len(present), MaxTreeWalkUpHops+1, MaxTreeWalkUpHops)
	}
	for i := 0; i <= MaxTreeWalkUpHops; i++ {
		if _, ok := present[ids[i]]; !ok {
			t.Errorf("hop-%d ancestor %q missing from tree — walk-up stopped short of the cap", i, ids[i])
		}
	}

	if _, ok := present[trueRoot]; ok {
		t.Errorf("trueRoot %q (hop %d) leaked past MaxTreeWalkUpHops=%d", trueRoot, chainLen, MaxTreeWalkUpHops)
	}
	if _, ok := present[hop11]; ok {
		t.Errorf("hop-11 ancestor %q leaked past MaxTreeWalkUpHops=%d", hop11, MaxTreeWalkUpHops)
	}

	if d := present[queried]; d != MaxTreeWalkUpHops {
		t.Errorf("queried depth = %d, want %d", d, MaxTreeWalkUpHops)
	}

	if !tree.TreeCapped {
		t.Error("TreeCapped = false, want true (walk-up hit the hop cap; Root is not the true root)")
	}
}

func TestTaskTree_StatusEmojiAndInstantDuration(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	root := "70000000-0000-0000-0000-000000000900"
	child := "70000000-0000-0000-0000-000000000901"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: root, UserID: "alice", StartTime: base, Status: sp("failed"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("R"), HandlerType: sp(store.TaskHandlerTypeAgent),
		EndTime: i64p(base),
	}); err != nil {
		t.Fatalf("create root: %v", err)
	}
	if err := taskRepo.Create(ctx, &store.Task{
		ID: child, UserID: "alice", StartTime: base + 1, Status: sp("in_progress"),
		ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("C"), HandlerType: sp(store.TaskHandlerTypeAgent),
		ParentTaskID: sp(root),
	}); err != nil {
		t.Fatalf("create child: %v", err)
	}

	tree, err := NewService(db).TaskTree(ctx, root, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}

	if tree.Root.StatusEmoji != "❌" {
		t.Errorf("Root.StatusEmoji = %q, want %q (status=failed)", tree.Root.StatusEmoji, "❌")
	}
	if tree.Root.DurationMs == nil {
		t.Fatal("Root.DurationMs = nil, want a pointer to 0 (task finished instantly)")
	}
	if *tree.Root.DurationMs != 0 {
		t.Errorf("*Root.DurationMs = %d, want 0", *tree.Root.DurationMs)
	}

	var childNode *TreeNodeDTO
	for i := range tree.Tree {
		if tree.Tree[i].ID == child {
			childNode = &tree.Tree[i]
		}
	}
	if childNode == nil {
		t.Fatal("child missing from tree")
	}
	if childNode.DurationMs != nil {
		t.Errorf("child.DurationMs = %v, want nil (task hasn't finished)", *childNode.DurationMs)
	}
	if childNode.StatusEmoji != "" {
		t.Errorf("child.StatusEmoji = %q, want %q (status=in_progress has no defined glyph)", childNode.StatusEmoji, "")
	}
}

func TestTaskTree_FitsWithinByteBudget(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	rootID := "70000000-0000-0000-0000-000000000500"
	mkTask := func(id, parent string) *store.Task {
		task := &store.Task{
			ID: id, UserID: "alice", StartTime: base,
			Status: sp("failed"), ExecutionMode: "foreground",
			Kind: store.TaskKindNormal, HandlerName: sp("Agent"),
			HandlerType: sp(store.TaskHandlerTypeAgent),
		}
		if parent != "" {
			task.ParentTaskID = sp(parent)
		}
		return task
	}
	if err := taskRepo.Create(ctx, mkTask(rootID, "")); err != nil {
		t.Fatalf("create root: %v", err)
	}

	longText := strings.Repeat("x", 300)
	evNum := 0
	for c := 0; c < 25; c++ {
		childID := fmt.Sprintf("70000000-0000-0000-0000-%012d", 600+c)
		if err := taskRepo.Create(ctx, mkTask(childID, rootID)); err != nil {
			t.Fatalf("create child %d: %v", c, err)
		}
		for i := 0; i < 40; i++ {
			evNum++
			if err := eventRepo.Create(ctx, &store.TaskEvent{
				ID:          fmt.Sprintf("80000000-0000-4000-8000-%012d", evNum),
				TaskID:      childID,
				CreatedTime: base + int64(i)*1000,
				Topic:       "test/e",
				Direction:   "status",
				Payload:     statusPayload(fmt.Sprintf(`{"type":"agent_progress_update","status_text":%q}`, longText)),
			}); err != nil {
				t.Fatalf("create event: %v", err)
			}
		}
	}

	tree, err := NewService(db).TaskTree(ctx, rootID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	b, _ := json.Marshal(tree)
	if len(b) > maxTreeBytes {
		t.Errorf("serialized tree = %d bytes, want <= %d (should be shrunk to fit)", len(b), maxTreeBytes)
	}
	if !tree.TreeEventsCapped {
		t.Error("TreeEventsCapped = false, want true (tree was shrunk to fit)")
	}
}

func TestTaskTree_ErrorMessageTruncated(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	taskID := "70000000-0000-0000-0000-000000000700"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "alice", StartTime: base,
		Status: sp("failed"), ExecutionMode: "foreground",
		Kind: store.TaskKindNormal, HandlerName: sp("Agent"),
		HandlerType:  sp(store.TaskHandlerTypeAgent),
		ErrorMessage: sp(strings.Repeat("E", 5000)),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}

	tree, err := NewService(db).TaskTree(ctx, taskID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	got := tree.Root.ErrorMessage
	if len(got) > maxErrorMessageChars+len("…") {
		t.Errorf("ErrorMessage len = %d, want <= %d", len(got), maxErrorMessageChars+len("…"))
	}
	if !strings.HasSuffix(got, "…") {
		t.Error("ErrorMessage should end with a truncation ellipsis")
	}
}

func TestTaskTree_FatErrorCascade_HitsTerminus(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)

	const base = 1_700_000_000_000
	rootID := "70000000-0000-0000-0000-000000000800"
	mk := func(id, parent string) *store.Task {
		task := &store.Task{
			ID: id, UserID: "alice", StartTime: base,
			Status: sp("failed"), ExecutionMode: "foreground",
			Kind: store.TaskKindNormal, HandlerName: sp("Agent"),
			HandlerType:  sp(store.TaskHandlerTypeAgent),
			ErrorMessage: sp(strings.Repeat("E", 1500)),
		}
		if parent != "" {
			task.ParentTaskID = sp(parent)
		}
		return task
	}
	if err := taskRepo.Create(ctx, mk(rootID, "")); err != nil {
		t.Fatalf("create root: %v", err)
	}
	for c := 0; c < 100; c++ {
		if err := taskRepo.Create(ctx, mk(fmt.Sprintf("70000000-0000-0000-0000-%012d", 900+c), rootID)); err != nil {
			t.Fatalf("create child %d: %v", c, err)
		}
	}

	tree, err := NewService(db).TaskTree(ctx, rootID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	b, _ := json.Marshal(tree)
	if len(b) > maxTreeBytes {
		t.Errorf("serialized tree = %d bytes, want <= %d (terminus should hard-clip errors)", len(b), maxTreeBytes)
	}
	if l := len(tree.Root.ErrorMessage); l > minErrorMessageChars+len("…") {
		t.Errorf("Root.ErrorMessage len = %d, want <= %d (hard-clipped at terminus)", l, minErrorMessageChars+len("…"))
	}
}

func TestTaskTree_SaturatedFetchAlwaysCapped(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)

	const base = 1_700_000_000_000
	taskID := "70000000-0000-0000-0000-000000001301"
	if err := taskRepo.Create(ctx, &store.Task{
		ID: taskID, UserID: "alice", StartTime: base,
		Status: sp("failed"), ExecutionMode: "foreground", Kind: store.TaskKindNormal,
		HandlerName: sp("Chatty"), HandlerType: sp(store.TaskHandlerTypeAgent),
	}); err != nil {
		t.Fatalf("create task: %v", err)
	}
	for i := range 70 {
		payload := statusPayload(fmt.Sprintf(`{"type":"agent_progress_update","status_text":"e-%03d"}`, i))
		if i < 20 || i >= 50 {
			payload = statusPayload(`{"type":"llm_invocation"}`)
		}
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: fmt.Sprintf("80000000-0000-4000-c000-%012d", i), TaskID: taskID,
			CreatedTime: base + int64(i)*1000, Topic: "test/e", Direction: "status", Payload: payload,
		}); err != nil {
			t.Fatalf("create event %d: %v", i, err)
		}
	}

	tree, err := NewService(db).TaskTree(ctx, taskID, "alice", false)
	if err != nil {
		t.Fatalf("TaskTree: %v", err)
	}
	t.Logf("eventsTotal=%d shown=%d capped=%v", tree.Root.EventsTotal, len(tree.Root.Events), tree.Root.EventsCapped)
	if !tree.Root.EventsCapped {
		t.Errorf("EventsCapped = false but the 60-row fetch was saturated (70 structural): %d of %d shown",
			len(tree.Root.Events), tree.Root.EventsTotal)
	}
}

func seedWithinTree(t *testing.T, db *store.DB, owners, parents map[string]string) {
	t.Helper()
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	const base = 1_700_000_000_000
	for _, id := range sortedKeys(owners) {
		task := &store.Task{
			ID: id, UserID: owners[id], StartTime: base, Status: sp("completed"),
			ExecutionMode: "foreground", Kind: store.TaskKindNormal,
			HandlerName: sp("A"), HandlerType: sp(store.TaskHandlerTypeAgent),
		}
		if p := parents[id]; p != "" {
			task.ParentTaskID = sp(p)
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: idgen.New(), TaskID: id, CreatedTime: base, Direction: "status",
			Payload: statusPayload(`{"type":"agent_progress_update","status_text":"hi"}`),
		}); err != nil {
			t.Fatalf("event %s: %v", id, err)
		}
	}
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func treeNodeIDs(tree *TaskTreeResponse) []string {
	ids := []string{tree.Root.ID}
	for i := range tree.Tree {
		ids = append(ids, tree.Tree[i].ID)
	}
	sort.Strings(ids)
	return ids
}

func TestTaskTreeWithin_ServesScopedTreeAcrossOwners(t *testing.T) {
	db := store.TestDB(t)
	root, child := idgen.New(), idgen.New()
	seedWithinTree(t, db,
		map[string]string{root: "bob", child: "carol"},
		map[string]string{child: root})

	tree, err := NewService(db).TaskTreeWithin(t.Context(), root, []string{root, child})
	if err != nil {
		t.Fatalf("TaskTreeWithin: %v", err)
	}
	want := []string{root, child}
	sort.Strings(want)
	if got := treeNodeIDs(tree); !slices.Equal(got, want) {
		t.Errorf("nodes = %v, want exactly %v", got, want)
	}
	if tree.Owner != "bob" {
		t.Errorf("Owner = %q, want bob", tree.Owner)
	}
}

func TestTaskTreeWithin_DropsChildOutsideScope(t *testing.T) {
	db := store.TestDB(t)
	root, c1, c2 := idgen.New(), idgen.New(), idgen.New()
	seedWithinTree(t, db,
		map[string]string{root: "bob", c1: "bob", c2: "carol"},
		map[string]string{c1: root, c2: root})

	tree, err := NewService(db).TaskTreeWithin(t.Context(), root, []string{root, c1})
	if err != nil {
		t.Fatalf("TaskTreeWithin: %v", err)
	}
	want := []string{root, c1}
	sort.Strings(want)
	if got := treeNodeIDs(tree); !slices.Equal(got, want) {
		t.Errorf("nodes = %v, want exactly %v (out-of-scope %s dropped)", got, want, c2)
	}
	if tree.TreeEventsTotal != 2 {
		t.Errorf("TreeEventsTotal = %d, want 2 (root + c1 only)", tree.TreeEventsTotal)
	}
}

func TestTaskTreeWithin_QueriedOutsideScopeNotFound(t *testing.T) {
	db := store.TestDB(t)
	root, other := idgen.New(), idgen.New()
	seedWithinTree(t, db, map[string]string{root: "bob", other: "bob"}, nil)

	_, err := NewService(db).TaskTreeWithin(t.Context(), root, []string{other})
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestTaskTreeWithin_AncestorOutsideScopeNotFound(t *testing.T) {
	db := store.TestDB(t)
	root, child := idgen.New(), idgen.New()
	seedWithinTree(t, db,
		map[string]string{root: "bob", child: "bob"},
		map[string]string{child: root})

	_, err := NewService(db).TaskTreeWithin(t.Context(), child, []string{child})
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestTaskTreeWithin_EmptyScopeNotFound(t *testing.T) {
	db := store.TestDB(t)
	root := idgen.New()
	seedWithinTree(t, db, map[string]string{root: "bob"}, nil)

	_, err := NewService(db).TaskTreeWithin(t.Context(), root, nil)
	if !errors.Is(err, ErrTaskNotFound) {
		t.Fatalf("err = %v, want ErrTaskNotFound", err)
	}
}

func TestTaskTreeWithin_GrandchildUnderDroppedChildDropped(t *testing.T) {
	db := store.TestDB(t)
	root, c1, g1 := idgen.New(), idgen.New(), idgen.New()
	seedWithinTree(t, db,
		map[string]string{root: "bob", c1: "bob", g1: "bob"},
		map[string]string{c1: root, g1: c1})

	tree, err := NewService(db).TaskTreeWithin(t.Context(), root, []string{root, g1})
	if err != nil {
		t.Fatalf("TaskTreeWithin: %v", err)
	}
	if ids := treeNodeIDs(tree); !slices.Equal(ids, []string{root}) {
		t.Errorf("nodes = %v, want only the root %s", ids, root)
	}
	if tree.TreeEventsTotal != 1 {
		t.Errorf("TreeEventsTotal = %d, want 1 (root only)", tree.TreeEventsTotal)
	}
}

func TestTaskTreeWithin_CapSignalSurvivesScopeFilter(t *testing.T) {
	db := store.TestDB(t)
	root := idgen.New()
	owners := map[string]string{root: "bob"}
	parents := map[string]string{}
	scope := []string{root}
	for i := 0; i < MaxTreeTasks; i++ {
		id := idgen.New()
		owners[id], parents[id] = "bob", root
		if i == 0 {
			scope = append(scope, id)
		}
	}
	seedWithinTree(t, db, owners, parents)

	tree, err := NewService(db).TaskTreeWithin(t.Context(), root, scope)
	if err != nil {
		t.Fatalf("TaskTreeWithin: %v", err)
	}
	if len(tree.Tree) >= MaxTreeTasks {
		t.Fatalf("filter kept %d nodes; test needs it below the cap", len(tree.Tree))
	}
	if !tree.TreeCapped {
		t.Error("TreeCapped = false although the fetch hit MaxTreeTasks")
	}
}
