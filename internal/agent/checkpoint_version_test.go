// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func stampedVersion(t *testing.T, blob []byte) int {
	t.Helper()
	var head struct {
		Version int `json:"version"`
	}
	if err := json.Unmarshal(blob, &head); err != nil {
		t.Fatalf("decode the stamped version: %v", err)
	}
	return head.Version
}

func peerOnlyState() *TaskExecutionState {
	state := fullTaskState()
	state.PausedApprovals = nil
	return state
}

var checkpointVersionByKindName = map[string]int{
	"peer":          2,
	"workflow":      2,
	"hil_approval":  2,
	"auth":          2,
	"hil_ask":       3,
	"remote_tool":   3,
	"tool_approval": 3,
}

func TestEveryPendingCallKindDeclaresTheCheckpointVersionItNeeds(t *testing.T) {
	for kind := PendingCallPeer; kind <= highestPendingCallKind; kind++ {
		name := kind.String()
		t.Run(name, func(t *testing.T) {
			want, listed := checkpointVersionByKindName[name]
			if !listed {
				t.Fatalf("pending call kind %q has no entry in checkpointVersionByKindName: decide which checkpoint version an older build needs for it", name)
			}
			if _, declared := pendingCallKindMinimumVersion[kind]; !declared {
				t.Errorf("pendingCallKindMinimumVersion has no entry for %q, so a pause of that kind is stamped by default", name)
			}
			state := peerOnlyState()
			state.PendingCalls["tc-1"].Kind = kind

			blob, err := marshalTaskState(state)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			if got := stampedVersion(t, blob); got != want {
				t.Errorf("a %s pause is stamped version %d, want %d", name, got, want)
			}
		})
	}
}

func TestCheckpointVersionOfAPausedApprovalRecordAndOfAnUnknownKind(t *testing.T) {
	tests := []struct {
		name      string
		kind      PendingCallKind
		approvals map[string]pausedApprovalCall
		want      int
	}{
		{"a paused approval record beside a peer pause", PendingCallPeer, map[string]pausedApprovalCall{"tc-2": {requestID: "req-1", timeout: time.Minute}}, 3},
		{"a pause of a kind this build does not know", highestPendingCallKind + 1, nil, 3},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			state := peerOnlyState()
			state.PendingCalls["tc-1"].Kind = tt.kind
			state.PausedApprovals = tt.approvals

			if got := checkpointVersionFor(state); got != tt.want {
				t.Errorf("checkpointVersionFor(%s) = %d, want %d", tt.name, got, tt.want)
			}
		})
	}
}

func TestABuildThatKnowsOnlyTheOlderVersionAdoptsAPeerOnlyPauseAndReleasesTheOthers(t *testing.T) {
	const olderBuildVersion = 2
	for kind := PendingCallPeer; kind <= highestPendingCallKind; kind++ {
		name := kind.String()
		t.Run(name, func(t *testing.T) {
			stamped, listed := checkpointVersionByKindName[name]
			if !listed {
				t.Fatalf("pending call kind %q has no entry in checkpointVersionByKindName", name)
			}
			state := peerOnlyState()
			state.PendingCalls["tc-1"].Kind = kind
			blob, err := marshalTaskState(state)
			if err != nil {
				t.Fatalf("marshal: %v", err)
			}

			_, olderErr := unmarshalTaskStateUpTo(blob, olderBuildVersion)
			_, stampedErr := unmarshalTaskStateUpTo(blob, stamped)

			wantNewer := stamped > olderBuildVersion
			if gotNewer := errors.Is(olderErr, errCheckpointFromNewerBuild); gotNewer != wantNewer {
				t.Errorf("a version %d build refused the %s checkpoint as written by a newer build = %v (err %v), want %v", olderBuildVersion, name, gotNewer, olderErr, wantNewer)
			}
			if !wantNewer && olderErr != nil {
				t.Errorf("a version %d build failed to read a %s pause it can resume: %v", olderBuildVersion, name, olderErr)
			}
			if stampedErr != nil {
				t.Errorf("a build at the stamped version %d failed to read a %s checkpoint: %v", stamped, name, stampedErr)
			}
		})
	}
}
