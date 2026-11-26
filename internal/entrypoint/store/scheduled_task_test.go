// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import "testing"

func TestScheduledTask_ResolveOwnerUserID(t *testing.T) {
	strPtr := func(s string) *string { return &s }

	cases := []struct {
		name string
		task ScheduledTask
		want string
	}{
		{
			name: "user_id wins when set",
			task: ScheduledTask{UserID: strPtr("alice"), CreatedBy: "bob"},
			want: "alice",
		},
		{
			name: "falls back to created_by when user_id is nil",
			task: ScheduledTask{UserID: nil, CreatedBy: "bob"},
			want: "bob",
		},
		{
			name: "falls back to created_by when user_id points to empty string",
			task: ScheduledTask{UserID: strPtr(""), CreatedBy: "bob"},
			want: "bob",
		},
		{
			name: "no owner rather than an invented principal when nothing is set",
			task: ScheduledTask{},
			want: "",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.task.ResolveOwnerUserID(); got != tc.want {
				t.Errorf("ResolveOwnerUserID() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestScheduledTaskExecution_ArtifactUserIDFollowsTheRunNotTheTask(t *testing.T) {
	alice := "alice@example.com"
	ranAs := "system:nightly"
	task := &ScheduledTask{UserID: &alice, CreatedBy: alice}

	if got := (&ScheduledTaskExecution{}).ArtifactUserID(task); got != alice {
		t.Errorf("a run submitted as its owner: ArtifactUserID = %q, want %q", got, alice)
	}
	if got := (&ScheduledTaskExecution{RunAs: &ranAs}).ArtifactUserID(task); got != ranAs {
		t.Errorf("a run submitted as a service account: ArtifactUserID = %q, want %q", got, ranAs)
	}
	later := "system:other"
	task.RunAs = &later
	if got := (&ScheduledTaskExecution{}).ArtifactUserID(task); got != alice {
		t.Errorf("an owner's run after runAs was set later: ArtifactUserID = %q, want %q", got, alice)
	}
}
