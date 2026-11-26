// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package control

import (
	"encoding/json"
	"testing"
)

func TestEntrypointApplyAndStatusKeepTheirWireKeys(t *testing.T) {
	apply, err := json.Marshal(EntrypointApply{Name: "ep", DeploymentID: "d1", Revision: "r1", Attempt: "a1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(apply), `{"name":"ep","deployment_id":"d1","revision":"r1","attempt":"a1"}`; got != want {
		t.Errorf("EntrypointApply = %s, want %s", got, want)
	}
	status, err := json.Marshal(EntrypointStatus{Name: "ep", DeploymentID: "d1", Revision: "r1", Attempt: "a1", Error: "refused"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(status), `{"name":"ep","deployment_id":"d1","revision":"r1","attempt":"a1","error":"refused"}`; got != want {
		t.Errorf("EntrypointStatus = %s, want %s", got, want)
	}
}

func TestAnEntrypointStatusFromAnExecutorThatPredatesRevisionsAndAttemptsReadsAsEmpty(t *testing.T) {
	var status EntrypointStatus
	if err := json.Unmarshal([]byte(`{"name":"ep","deployment_id":"d1"}`), &status); err != nil {
		t.Fatal(err)
	}
	if status != (EntrypointStatus{Name: "ep", DeploymentID: "d1"}) {
		t.Errorf("status = %+v, want only the name and deployment id", status)
	}
	empty, err := json.Marshal(EntrypointApply{Name: "ep", DeploymentID: "d1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(empty), `{"name":"ep","deployment_id":"d1"}`; got != want {
		t.Errorf("EntrypointApply without a revision or attempt = %s, want %s", got, want)
	}
	bare, err := json.Marshal(EntrypointStatus{Name: "ep", DeploymentID: "d1"})
	if err != nil {
		t.Fatal(err)
	}
	if got, want := string(bare), `{"name":"ep","deployment_id":"d1"}`; got != want {
		t.Errorf("EntrypointStatus without a revision, attempt or error = %s, want %s", got, want)
	}
}
