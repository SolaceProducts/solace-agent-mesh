// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"testing"
)

const (
	genOpCreatedTime = int64(1_700_000_000_000)
	genOpDeadline    = genOpCreatedTime + 60_000
)

func genOpID(n int) string {
	return fmt.Sprintf("00000000-0000-4000-8000-%012d", n)
}

func newGenerationOperation(ctx context.Context, t *testing.T, repo *EvalGenerationOperationRepo, id, datasetID string, createdTime int64) {
	t.Helper()
	op := &EvalGenerationOperation{
		ID:            id,
		OperationType: EvalGenerationExamples,
		DatasetID:     &datasetID,
		Status:        EvalGenerationInProgress,
		CreatedBy:     "u",
		CreatedTime:   createdTime,
		Deadline:      genOpDeadline,
	}
	if err := repo.Create(ctx, op); err != nil {
		t.Fatalf("create operation %s: %v", id, err)
	}
}

func getGenerationOperation(ctx context.Context, t *testing.T, repo *EvalGenerationOperationRepo, id string) *EvalGenerationOperation {
	t.Helper()
	op, err := repo.Get(ctx, id)
	if err != nil {
		t.Fatalf("get operation %s: %v", id, err)
	}
	return op
}

func TestEvalGenerationOperationRepo_ListByDatasetTiesBreakByIDDesc(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	repo := NewEvalGenerationOperationRepo(db)
	datasetID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "DS")

	older, tiedLow, tiedMid, tiedHigh := genOpID(1), genOpID(2), genOpID(3), genOpID(4)
	for _, id := range []string{tiedMid, older, tiedHigh, tiedLow} {
		createdTime := genOpCreatedTime + 1_000
		if id == older {
			createdTime = genOpCreatedTime
		}
		if err := repo.Create(ctx, &EvalGenerationOperation{
			ID: id, OperationType: EvalGenerationDataset, DatasetID: &datasetID, Status: EvalGenerationInProgress,
			CreatedBy: "u", CreatedTime: createdTime, Deadline: genOpDeadline,
		}); err != nil {
			t.Fatalf("create %s: %v", id, err)
		}
	}

	rows, total, err := repo.ListByDataset(ctx, datasetID, 1, 10, false)
	if err != nil {
		t.Fatalf("list: %v", err)
	}
	var got []string
	for _, r := range rows {
		got = append(got, r.ID)
	}
	want := []string{tiedHigh, tiedMid, tiedLow, older}
	if !slices.Equal(got, want) || total != len(want) {
		t.Errorf("order = %v (total %d), want %v (total %d)", got, total, want, len(want))
	}

	page2, _, err := repo.ListByDataset(ctx, datasetID, 2, 2, false)
	if err != nil {
		t.Fatalf("list page 2: %v", err)
	}
	if len(page2) != 2 || page2[0].ID != tiedLow || page2[1].ID != older {
		t.Errorf("page 2 = %v, want [%s %s]", page2, tiedLow, older)
	}
}

func TestEvalGenerationOperationRepo_SucceedFailOnlyChangeInProgress(t *testing.T) {
	const completed = genOpCreatedTime + 5_000
	const laterCompleted = genOpCreatedTime + 9_000

	tests := []struct {
		name       string
		finish     func(ctx context.Context, repo *EvalGenerationOperationRepo, id, datasetID string) error
		wantStatus string
		wantMsg    *string
	}{
		{
			name:       "succeed",
			finish:     succeedBySaving,
			wantStatus: EvalGenerationSucceeded,
		},
		{
			name: "fail",
			finish: func(ctx context.Context, repo *EvalGenerationOperationRepo, id, _ string) error {
				return repo.Fail(ctx, id, "boom", "err-1", completed)
			},
			wantStatus: EvalGenerationFailed,
			wantMsg:    new("boom"),
		},
	}
	for _, first := range tests {
		for _, second := range tests {
			t.Run(first.name+" then "+second.name, func(t *testing.T) {
				db := TestDB(t)
				ctx := t.Context()
				repo := NewEvalGenerationOperationRepo(db)
				datasetID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "DS")
				id := genOpID(1)
				newGenerationOperation(ctx, t, repo, id, datasetID, genOpCreatedTime)

				if err := first.finish(ctx, repo, id, datasetID); err != nil {
					t.Fatalf("first finish: %v", err)
				}
				before := getGenerationOperation(ctx, t, repo, id)
				if before.Status != first.wantStatus || before.CompletedTime == nil {
					t.Fatalf("after first finish = %+v, want %s with a completed time", before, first.wantStatus)
				}

				if err := second.finish(ctx, repo, id, datasetID); err != nil {
					t.Fatalf("second finish returned an error, want a no-op: %v", err)
				}
				if err := succeedBySaving(ctx, repo, id, datasetID); err != nil {
					t.Fatalf("late succeed: %v", err)
				}
				if err := repo.Fail(ctx, id, "late", "late-id", laterCompleted); err != nil {
					t.Fatalf("late fail: %v", err)
				}

				after := getGenerationOperation(ctx, t, repo, id)
				if *after.CompletedTime != *before.CompletedTime || after.Status != before.Status ||
					!equalStrPtr(after.ErrorMessage, before.ErrorMessage) || !equalStrPtr(after.ErrorID, before.ErrorID) {
					t.Errorf("operation changed after a finished one was finished again:\nbefore %+v\nafter  %+v", before, after)
				}
				if !equalStrPtr(after.ErrorMessage, first.wantMsg) {
					t.Errorf("error message = %v, want %v", after.ErrorMessage, first.wantMsg)
				}
			})
		}
	}
}

func TestEvalGenerationOperationRepo_LateSucceedOrFailAfterExpiryKeepsExpiry(t *testing.T) {
	const expiryMessage = "generation timed out"
	finishers := map[string]func(ctx context.Context, repo *EvalGenerationOperationRepo, id, datasetID string) error{
		"succeed": succeedBySaving,
		"fail": func(ctx context.Context, repo *EvalGenerationOperationRepo, id, _ string) error {
			return repo.Fail(ctx, id, "late failure", "late-id", genOpDeadline+5_000)
		},
	}
	for name, finish := range finishers {
		t.Run(name, func(t *testing.T) {
			db := TestDB(t)
			ctx := t.Context()
			repo := NewEvalGenerationOperationRepo(db)
			datasetID := newTestDataset(ctx, t, NewEvalDatasetRepo(db), "DS")
			id := genOpID(1)
			newGenerationOperation(ctx, t, repo, id, datasetID, genOpCreatedTime)

			expired, err := repo.FailExpired(ctx, genOpDeadline+1, expiryMessage)
			if err != nil || !slices.Equal(expired, []string{id}) {
				t.Fatalf("FailExpired = %v, %v, want [%s]", expired, err, id)
			}
			before := getGenerationOperation(ctx, t, repo, id)

			if err := finish(ctx, repo, id, datasetID); err != nil {
				t.Fatalf("late finish: %v", err)
			}

			after := getGenerationOperation(ctx, t, repo, id)
			if after.Status != EvalGenerationFailed ||
				after.ErrorMessage == nil || *after.ErrorMessage != expiryMessage ||
				after.ErrorID == nil || *after.ErrorID != id ||
				after.CompletedTime == nil || *after.CompletedTime != genOpDeadline {
				t.Errorf("operation after late finish = %+v, want failed with the expiry message, errorId %s, completed at %d", after, id, genOpDeadline)
			}
			if after.Status != before.Status {
				t.Errorf("status changed from %s to %s", before.Status, after.Status)
			}
		})
	}
}

func succeedBySaving(ctx context.Context, repo *EvalGenerationOperationRepo, id, datasetID string) error {
	err := NewEvalExampleRepo(repo.db).SaveGeneratedExamples(ctx, id, datasetID, nil)
	if errors.Is(err, ErrEvalGenerationNotInProgress) {
		return nil
	}
	return err
}

func equalStrPtr(a, b *string) bool {
	if a == nil || b == nil {
		return a == b
	}
	return *a == *b
}
