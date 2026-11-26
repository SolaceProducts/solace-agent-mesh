// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package analytics

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/store"
)

const sessionBase = 1_700_000_000_000

func seedSessionTurns(t *testing.T, db *store.DB, session string, n, childrenPer, eventsPer int) []*store.Task {
	t.Helper()
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	fat := strings.Repeat("x", 300)
	evNum := 0
	roots := make([]*store.Task, 0, n)
	mk := func(id string, parent *string, start int64) *store.Task {
		task := &store.Task{
			ID: id, UserID: "alice", StartTime: start, SessionID: sp(session),
			Status: sp("completed"), ExecutionMode: "foreground",
			Kind: store.TaskKindNormal, HandlerName: sp("Agent"),
			HandlerType: sp(store.TaskHandlerTypeAgent), ParentTaskID: parent,
		}
		if err := taskRepo.Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		return task
	}
	for i := 0; i < n; i++ {
		rootID := fmt.Sprintf("71000000-0000-0000-%04d-000000000000", i)
		root := mk(rootID, nil, sessionBase+int64(i)*60_000)
		roots = append(roots, root)
		for c := 0; c < childrenPer; c++ {
			childID := fmt.Sprintf("71000000-0000-0000-%04d-%012d", i, c+1)
			mk(childID, &rootID, root.StartTime+int64(c+1))
			for e := 0; e < eventsPer; e++ {
				evNum++
				if err := eventRepo.Create(ctx, &store.TaskEvent{
					ID:          fmt.Sprintf("81000000-0000-4000-8000-%012d", evNum),
					TaskID:      childID,
					CreatedTime: root.StartTime + int64(e)*1000,
					Topic:       "test/e",
					Direction:   "status",
					Payload:     statusPayload(fmt.Sprintf(`{"type":"agent_progress_update","status_text":%q}`, fat)),
				}); err != nil {
					t.Fatalf("create event: %v", err)
				}
			}
		}
	}
	return roots
}

func sessionTrace(t *testing.T, db *store.DB, fb *store.Feedback, roots []*store.Task) (*SessionTraceResponse, error) {
	t.Helper()
	in := SessionInput{Roots: roots}
	lineage, err := store.NewAnalyticsRepo(db).WalkAncestors(t.Context(), fb.TaskID, store.TaskTreeMaxDepth)
	if err != nil {
		t.Fatalf("rated lineage: %v", err)
	}
	if len(lineage) > 0 {
		in.RatedRoot = &lineage[len(lineage)-1]
	}
	var rootIDs []string
	for _, r := range roots {
		if r.ParentTaskID == nil {
			rootIDs = append(rootIDs, r.ID)
		}
	}
	if in.Forest, err = store.NewTaskRepo(db).SessionForest(t.Context(), rootIDs); err != nil {
		t.Fatalf("forest: %v", err)
	}
	return NewService(db).SessionTrace(t.Context(), fb, in)
}

func TestSessionTrace_ChronologicalTurnsEndAtRatedTask(t *testing.T) {
	db := store.TestDB(t)
	roots := seedSessionTurns(t, db, "72000000-0000-0000-0000-000000000001", 3, 0, 0)
	rated := roots[2]
	fb := &store.Feedback{ID: "fb1", TaskID: rated.ID, Rating: "down", Comment: sp("bad"), SubjectID: "Agent", SubjectType: store.SubjectTypeAgent}

	got, err := sessionTrace(t, db, fb, []*store.Task{roots[2], roots[1], roots[0]})
	if err != nil {
		t.Fatalf("SessionTrace: %v", err)
	}
	if len(got.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(got.Turns))
	}
	for i, r := range roots {
		if got.Turns[i].Root.ID != r.ID {
			t.Errorf("turn %d = %s, want %s (chronological)", i, got.Turns[i].Root.ID, r.ID)
		}
	}
	if got.RatedTaskID != rated.ID || got.FeedbackID != "fb1" || got.Rating != "down" || got.Comment != "bad" {
		t.Errorf("feedback fields = %+v", got)
	}
	if got.RetentionGap || got.EarlierTurnsOmitted {
		t.Errorf("gap=%v omitted=%v, want both false", got.RetentionGap, got.EarlierTurnsOmitted)
	}
	if got.Owner != "alice" {
		t.Errorf("Owner = %q, want alice", got.Owner)
	}
}

func TestSessionTrace_OverBudgetDegradesOldestFirst(t *testing.T) {
	db := store.TestDB(t)
	roots := seedSessionTurns(t, db, "72000000-0000-0000-0000-000000000002", MaxSessionTurns, 2, 40)
	fb := &store.Feedback{ID: "fb1", TaskID: roots[len(roots)-1].ID, Rating: "down"}

	got, err := sessionTrace(t, db, fb, roots)
	if err != nil {
		t.Fatalf("SessionTrace: %v", err)
	}
	b, _ := json.Marshal(got)
	if len(b) > maxSessionBytes {
		t.Errorf("serialized session = %d bytes, want <= %d", len(b), maxSessionBytes)
	}
	hasEvents := func(r TaskTreeResponse) bool {
		for _, n := range r.Tree {
			if len(n.Events) > 0 {
				return true
			}
		}
		return false
	}
	omitted := func(r TaskTreeResponse) bool {
		for _, n := range r.Tree {
			if n.EventsOmitted {
				return true
			}
		}
		return false
	}
	first, last := got.Turns[0], got.Turns[len(got.Turns)-1]
	if !omitted(first) || hasEvents(first) {
		t.Error("first turn should have its events omitted")
	}
	if !hasEvents(last) {
		t.Error("rated (last) turn should keep events")
	}
}

func TestSessionTrace_SkipsNonRootInput(t *testing.T) {
	db := store.TestDB(t)
	roots := seedSessionTurns(t, db, "72000000-0000-0000-0000-000000000005", 2, 1, 0)
	child, err := store.NewTaskRepo(db).GetByID(t.Context(), fmt.Sprintf("71000000-0000-0000-%04d-%012d", 0, 1))
	if err != nil || child == nil || child.ParentTaskID == nil {
		t.Fatalf("setup child: %v %+v", err, child)
	}
	fb := &store.Feedback{ID: "fb1", TaskID: roots[1].ID, Rating: "down"}

	got, err := sessionTrace(t, db, fb, []*store.Task{child, roots[1]})
	if err != nil {
		t.Fatalf("SessionTrace: %v", err)
	}
	if len(got.Turns) != 1 || got.Turns[0].Root.ID != roots[1].ID {
		t.Fatalf("turns = %d, want only the rated root (child and its parent must not appear)", len(got.Turns))
	}
}

func TestSessionTrace_IgnoresRootsAfterRatedTurn(t *testing.T) {
	db := store.TestDB(t)
	roots := seedSessionTurns(t, db, "72000000-0000-0000-0000-000000000006", 3, 0, 0)
	fb := &store.Feedback{ID: "fb1", TaskID: roots[1].ID, Rating: "down"}

	got, err := sessionTrace(t, db, fb, roots)
	if err != nil {
		t.Fatalf("SessionTrace: %v", err)
	}
	if len(got.Turns) != 2 || got.Turns[1].Root.ID != roots[1].ID {
		t.Fatalf("turns = %d, want 2 ending at the rated turn", len(got.Turns))
	}
}

func TestSessionTrace_TurnsMatchTaskTree(t *testing.T) {
	db := store.TestDB(t)
	ctx := t.Context()
	taskRepo := store.NewTaskRepo(db)
	eventRepo := store.NewTaskEventRepo(db)
	session := "72000000-0000-0000-0000-000000000007"
	n := 0
	mk := func(parent *string, start int64, user string) string {
		n++
		id := fmt.Sprintf("74000000-0000-0000-0000-%012d", n)
		if err := taskRepo.Create(ctx, &store.Task{
			ID: id, UserID: user, StartTime: start, SessionID: sp(session),
			Status: sp("completed"), ExecutionMode: "foreground",
			Kind: store.TaskKindNormal, HandlerName: sp("Agent"),
			HandlerType: sp(store.TaskHandlerTypeAgent), ParentTaskID: parent,
		}); err != nil {
			t.Fatalf("create task: %v", err)
		}
		return id
	}
	event := func(taskID string, i int, data string) {
		if err := eventRepo.Create(ctx, &store.TaskEvent{
			ID: fmt.Sprintf("84000000-0000-4000-8000-%012d", n*1000+i), TaskID: taskID,
			CreatedTime: sessionBase + int64(i), Topic: "test/e", Direction: "status",
			Payload: statusPayload(data),
		}); err != nil {
			t.Fatalf("create event: %v", err)
		}
	}

	deepRoot := mk(nil, sessionBase, "alice")
	parent := deepRoot
	for d := 1; d <= MaxTreeWalkDownDepth+2; d++ {
		parent = mk(&parent, sessionBase+int64(d), store.TaskUserUnknown)
		for i := range 3 {
			event(parent, i, `{"type":"agent_progress_update","status_text":"step"}`)
		}
		event(parent, 3, `{"type":"llm_invocation"}`)
	}
	wideRoot := mk(nil, sessionBase+60_000, "alice")
	for c := range MaxTreeTasks + 5 {
		mk(&wideRoot, sessionBase+60_000+int64(c%7), "alice")
	}
	ratedRoot := mk(nil, sessionBase+120_000, "alice")
	event(ratedRoot, 0, `{"type":"agent_progress_update","status_text":"rated"}`)

	roots, err := taskRepo.ListSessionRootsUpTo(ctx, session, ratedRoot, sessionBase+120_000, MaxSessionTurns+1)
	if err != nil {
		t.Fatalf("roots: %v", err)
	}
	fb := &store.Feedback{ID: "fb1", TaskID: ratedRoot, Rating: "down"}
	got, err := sessionTrace(t, db, fb, roots)
	if err != nil {
		t.Fatalf("SessionTrace: %v", err)
	}
	if len(got.Turns) != 3 {
		t.Fatalf("turns = %d, want 3", len(got.Turns))
	}
	svc := NewService(db)
	for i, turn := range got.Turns {
		want, err := svc.TaskTree(ctx, turn.Root.ID, "", true)
		if err != nil {
			t.Fatalf("TaskTree(%s): %v", turn.Root.ID, err)
		}
		gotJSON, _ := json.Marshal(turn)
		wantJSON, _ := json.Marshal(want)
		if string(gotJSON) != string(wantJSON) {
			t.Errorf("turn %d differs from TaskTree:\n got %s\nwant %s", i, gotJSON, wantJSON)
		}
	}
	if !got.Turns[0].TreeCapped || !got.Turns[1].TreeCapped {
		t.Error("fixture must cross both caps, or the comparison proves nothing about them")
	}
}

func TestSessionTrace_LongCommentClippedToFit(t *testing.T) {
	db := store.TestDB(t)
	roots := seedSessionTurns(t, db, "72000000-0000-0000-0000-000000000009", 3, 1, 5)
	comment := strings.Repeat("é", maxSessionBytes)
	fb := &store.Feedback{ID: "fb1", TaskID: roots[2].ID, Rating: "down", Comment: &comment}

	got, err := sessionTrace(t, db, fb, roots)
	if err != nil {
		t.Fatalf("SessionTrace: %v", err)
	}
	if b, _ := json.Marshal(got); len(b) > maxSessionBytes {
		t.Errorf("serialized session = %d bytes, want <= %d", len(b), maxSessionBytes)
	}
	if n := len(got.Turns); n == 0 || got.Turns[n-1].Root.ID != roots[2].ID {
		t.Fatalf("turns = %d, want the rated turn kept last", n)
	}
	if got.Comment == "" || len([]rune(got.Comment)) > maxSessionCommentRunes+1 {
		t.Errorf("comment = %d runes, want clipped to about %d", len([]rune(got.Comment)), maxSessionCommentRunes)
	}
}

func TestServedTurns_RatedTurnSurvivesAStartTimeTie(t *testing.T) {
	roots := make([]*store.Task, 0, MaxSessionTurns+1)
	for i := range MaxSessionTurns + 1 {
		roots = append(roots, &store.Task{ID: fmt.Sprintf("t%02d", i), StartTime: sessionBase})
	}
	for _, rated := range []*store.Task{roots[0], roots[MaxSessionTurns/2]} {
		got, omitted := ServedTurns(SessionInput{RatedRoot: rated, Roots: roots})
		if !omitted || len(got) != MaxSessionTurns || got[len(got)-1].ID != rated.ID {
			t.Errorf("rated %s: served %d (omitted=%v), last %s; want it served last", rated.ID, len(got), omitted, got[len(got)-1].ID)
		}
	}
}
