// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/idgen"
)

func TestSumTokensBySessionForUser(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)
	ctx := context.Background()

	sid := idgen.New()
	other := idgen.New()
	user := "user-a"
	now := time.Now().UnixMilli()

	mkTask := func(fn func(*Task)) *Task {
		task := &Task{ID: idgen.New(), UserID: user, SessionID: &sid, StartTime: now, ExecutionMode: "foreground"}
		fn(task)
		if err := repo.Create(ctx, task); err != nil {
			t.Fatalf("create task: %v", err)
		}
		return task
	}

	in1, out1 := 100, 40
	in2, out2 := 250, 90
	mkTask(func(task *Task) { task.TotalInputTokens = &in1; task.TotalOutputTokens = &out1 })
	mkTask(func(task *Task) { task.TotalInputTokens = &in2; task.TotalOutputTokens = &out2 })

	inOnly := 10
	mkTask(func(task *Task) { task.TotalInputTokens = &inOnly })

	mkTask(func(task *Task) {})
	parent := idgen.New()
	mkTask(func(task *Task) {
		task.ParentTaskID = &parent
		task.TotalInputTokens = &in2
		task.TotalOutputTokens = &out2
	})
	mkTask(func(task *Task) { task.UserID = "user-b"; task.TotalInputTokens = &in2 })
	mkTask(func(task *Task) { task.SessionID = &other; task.TotalInputTokens = &in2 })
	excluded := mkTask(func(task *Task) { task.TotalInputTokens = &in2; task.TotalOutputTokens = &out2 })

	gotIn, gotOut, err := repo.SumTokensBySessionForUser(ctx, sid, user, excluded.ID)
	if err != nil {
		t.Fatalf("SumTokensBySessionForUser: %v", err)
	}
	wantIn := in1 + in2 + inOnly
	wantOut := out1 + out2
	if gotIn != wantIn {
		t.Errorf("input = %d, want %d", gotIn, wantIn)
	}
	if gotOut != wantOut {
		t.Errorf("output = %d, want %d", gotOut, wantOut)
	}
}

func TestSumTokensBySessionForUser_NoRows(t *testing.T) {
	db := newTestDB(t)
	repo := NewTaskRepo(db)

	gotIn, gotOut, err := repo.SumTokensBySessionForUser(context.Background(), idgen.New(), "nobody", idgen.New())
	if err != nil {
		t.Fatalf("SumTokensBySessionForUser: %v", err)
	}
	if gotIn != 0 || gotOut != 0 {
		t.Errorf("got (%d, %d), want (0, 0)", gotIn, gotOut)
	}
}
