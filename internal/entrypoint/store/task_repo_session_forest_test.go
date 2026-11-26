// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func seedForestTask(t *testing.T, repo *TaskRepo, parent *string, user string, handler *string) string {
	t.Helper()
	id := idgen.New()
	if err := repo.Create(t.Context(), &Task{
		ID: id, UserID: user, StartTime: 1000, ExecutionMode: "foreground", Kind: TaskKindNormal,
		ParentTaskID: parent, HandlerName: handler, HandlerType: handler,
	}); err != nil {
		t.Fatalf("seed task: %v", err)
	}
	return id
}

func TestSessionForest_WalksEveryRootInOneRead(t *testing.T) {
	db := TestDB(t)
	repo := NewTaskRepo(db)
	agent := "agent"
	rootA := seedForestTask(t, repo, nil, "u1", &agent)
	childA := seedForestTask(t, repo, &rootA, "u2", nil)
	grandA := seedForestTask(t, repo, &childA, TaskUserUnknown, &agent)
	rootB := seedForestTask(t, repo, nil, "u1", &agent)
	unrelated := seedForestTask(t, repo, nil, "u1", &agent)
	seedForestTask(t, repo, &unrelated, "u1", &agent)

	got, err := repo.SessionForest(t.Context(), []string{rootA, rootB, childA})
	if err != nil {
		t.Fatalf("SessionForest: %v", err)
	}
	want := map[string]ForestNode{
		rootA:  {RootID: rootA, Depth: 0, UserID: "u1", HandlerName: agent, HandlerType: agent},
		childA: {RootID: rootA, Depth: 1, UserID: "u2"},
		grandA: {RootID: rootA, Depth: 2, UserID: TaskUserUnknown, HandlerName: agent, HandlerType: agent},
		rootB:  {RootID: rootB, Depth: 0, UserID: "u1", HandlerName: agent, HandlerType: agent},
	}
	if len(got) != len(want) {
		t.Fatalf("got %d nodes, want %d: %+v", len(got), len(want), got)
	}
	for _, n := range got {
		w, ok := want[n.ID]
		if !ok {
			t.Fatalf("unexpected node %s", n.ID)
		}
		if n.RootID != w.RootID || n.Depth != w.Depth || n.UserID != w.UserID ||
			n.HandlerName != w.HandlerName || n.HandlerType != w.HandlerType {
			t.Errorf("node %s = %+v, want %+v", n.ID, n, w)
		}
	}
}

func TestSessionForest_StopsAtTheDepthBound(t *testing.T) {
	db := TestDB(t)
	repo := NewTaskRepo(db)
	root := seedForestTask(t, repo, nil, "u1", nil)
	parent := root
	for range TaskTreeMaxDepth + 2 {
		parent = seedForestTask(t, repo, &parent, "u1", nil)
	}

	got, err := repo.SessionForest(t.Context(), []string{root})
	if err != nil {
		t.Fatalf("SessionForest: %v", err)
	}
	maxDepth := 0
	for _, n := range got {
		maxDepth = max(maxDepth, n.Depth)
	}
	if maxDepth != TaskTreeMaxDepth || len(got) != TaskTreeMaxDepth+1 {
		t.Fatalf("nodes=%d maxDepth=%d, want %d nodes reaching depth %d", len(got), maxDepth, TaskTreeMaxDepth+1, TaskTreeMaxDepth)
	}
}
