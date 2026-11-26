// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package store

import (
	"testing"

	"github.com/google/go-cmp/cmp"
)

var (
	nightlySyncDrive  = SystemUserToolAuthKey{Principal: "system:nightly-sync", AgentName: "support_agent", CredentialKey: "google_drive"}
	nightlySyncJira   = SystemUserToolAuthKey{Principal: "system:nightly-sync", AgentName: "support_agent", CredentialKey: "jira"}
	weeklyDigestDrive = SystemUserToolAuthKey{Principal: "system:weekly-digest", AgentName: "support_agent", CredentialKey: "google_drive"}
)

func listedRow(t *testing.T, db *DB, key SystemUserToolAuthKey) (SystemUserToolAuth, bool) {
	t.Helper()
	rows, err := ListSystemUserToolAuth(t.Context(), db, key.Principal, key.AgentName)
	if err != nil {
		t.Fatalf("ListSystemUserToolAuth(%s, %s): %v", key.Principal, key.AgentName, err)
	}
	row, ok := rows[key.CredentialKey]
	return row, ok
}

func TestSystemUserToolAuth_CompletionNeedsTheReferenceOfTheBeginAndHappensOnce(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	if err := BeginSystemUserToolAuth(ctx, db, nightlySyncDrive, "alice@example.com", 1000, "ref-1"); err != nil {
		t.Fatalf("BeginSystemUserToolAuth: %v", err)
	}
	pending := SystemUserToolAuth{
		Principal: "system:nightly-sync", AgentName: "support_agent", CredentialKey: "google_drive",
		State: SystemUserToolAuthPending, BegunBy: new("alice@example.com"), BegunTime: new(int64(1000)), BegunRef: new("ref-1"),
	}
	if got, _ := listedRow(t, db, nightlySyncDrive); cmp.Diff(pending, got) != "" {
		t.Fatalf("row after begin mismatch (-want +got):\n%s", cmp.Diff(pending, got))
	}

	completed, err := CompleteSystemUserToolAuth(ctx, db, nightlySyncDrive, "ref-of-another-sign-in", 2000)
	if err != nil || completed {
		t.Errorf("CompleteSystemUserToolAuth(another reference) = %v, %v; want false, nil", completed, err)
	}
	if got, _ := listedRow(t, db, nightlySyncDrive); cmp.Diff(pending, got) != "" {
		t.Errorf("row after a completion with another reference mismatch (-want +got):\n%s", cmp.Diff(pending, got))
	}

	completed, err = CompleteSystemUserToolAuth(ctx, db, nightlySyncDrive, "ref-1", 2000)
	if err != nil || !completed {
		t.Fatalf("CompleteSystemUserToolAuth(ref-1) = %v, %v; want true, nil", completed, err)
	}
	connected := SystemUserToolAuth{
		Principal: "system:nightly-sync", AgentName: "support_agent", CredentialKey: "google_drive",
		State: SystemUserToolAuthConnected, BegunBy: new("alice@example.com"), BegunTime: new(int64(1000)),
		AuthorizedBy: new("alice@example.com"), AuthorizedTime: new(int64(1000)), ConnectedObservedTime: new(int64(2000)),
	}
	if got, _ := listedRow(t, db, nightlySyncDrive); cmp.Diff(connected, got) != "" {
		t.Errorf("row after completion mismatch (-want +got):\n%s", cmp.Diff(connected, got))
	}

	completed, err = CompleteSystemUserToolAuth(ctx, db, nightlySyncDrive, "ref-1", 3000)
	if err != nil || completed {
		t.Errorf("CompleteSystemUserToolAuth(ref-1) a second time = %v, %v; want false, nil", completed, err)
	}
	if got, _ := listedRow(t, db, nightlySyncDrive); cmp.Diff(connected, got) != "" {
		t.Errorf("row after a second completion mismatch (-want +got):\n%s", cmp.Diff(connected, got))
	}
}

func TestSystemUserToolAuth_ASecondBeginReplacesTheFirst(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	if err := BeginSystemUserToolAuth(ctx, db, nightlySyncDrive, "alice@example.com", 1000, "ref-1"); err != nil {
		t.Fatalf("BeginSystemUserToolAuth(alice): %v", err)
	}
	if err := BeginSystemUserToolAuth(ctx, db, nightlySyncDrive, "bob@example.com", 1500, "ref-2"); err != nil {
		t.Fatalf("BeginSystemUserToolAuth(bob): %v", err)
	}

	completed, err := CompleteSystemUserToolAuth(ctx, db, nightlySyncDrive, "ref-1", 2000)
	if err != nil || completed {
		t.Errorf("CompleteSystemUserToolAuth(first begin's reference) = %v, %v; want false, nil", completed, err)
	}
	completed, err = CompleteSystemUserToolAuth(ctx, db, nightlySyncDrive, "ref-2", 2500)
	if err != nil || !completed {
		t.Fatalf("CompleteSystemUserToolAuth(second begin's reference) = %v, %v; want true, nil", completed, err)
	}
	want := SystemUserToolAuth{
		Principal: "system:nightly-sync", AgentName: "support_agent", CredentialKey: "google_drive",
		State: SystemUserToolAuthConnected, BegunBy: new("bob@example.com"), BegunTime: new(int64(1500)),
		AuthorizedBy: new("bob@example.com"), AuthorizedTime: new(int64(1500)), ConnectedObservedTime: new(int64(2500)),
	}
	if got, _ := listedRow(t, db, nightlySyncDrive); cmp.Diff(want, got) != "" {
		t.Errorf("row after the second begin completed mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}

func TestSystemUserToolAuth_DisconnectKeepsTheBeginAndStaysWithinItsCredential(t *testing.T) {
	db := TestDB(t)
	ctx := t.Context()
	for _, key := range []SystemUserToolAuthKey{nightlySyncDrive, nightlySyncJira, weeklyDigestDrive} {
		if err := BeginSystemUserToolAuth(ctx, db, key, "alice@example.com", 1000, "ref-"+key.CredentialKey+"-"+key.Principal); err != nil {
			t.Fatalf("BeginSystemUserToolAuth(%+v): %v", key, err)
		}
	}

	if err := DisconnectSystemUserToolAuth(ctx, db, nightlySyncDrive, "bob@example.com", 2000); err != nil {
		t.Fatalf("DisconnectSystemUserToolAuth: %v", err)
	}

	want := SystemUserToolAuth{
		Principal: "system:nightly-sync", AgentName: "support_agent", CredentialKey: "google_drive",
		State: SystemUserToolAuthDisconnected, BegunBy: new("alice@example.com"), BegunTime: new(int64(1000)),
		BegunRef:       new("ref-google_drive-system:nightly-sync"),
		DisconnectedBy: new("bob@example.com"), DisconnectedTime: new(int64(2000)),
	}
	if got, _ := listedRow(t, db, nightlySyncDrive); cmp.Diff(want, got) != "" {
		t.Errorf("row after a disconnect mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
	for _, other := range []SystemUserToolAuthKey{nightlySyncJira, weeklyDigestDrive} {
		got, _ := listedRow(t, db, other)
		if got.State != SystemUserToolAuthPending || got.DisconnectedBy != nil {
			t.Errorf("row %+v after another credential was disconnected = %+v, want it still pending and not disconnected", other, got)
		}
	}
}

func TestSystemUserToolAuth_ADisconnectOfACredentialWithNoRowIsRecorded(t *testing.T) {
	db := TestDB(t)

	if err := DisconnectSystemUserToolAuth(t.Context(), db, nightlySyncDrive, "bob@example.com", 2000); err != nil {
		t.Fatalf("DisconnectSystemUserToolAuth: %v", err)
	}

	want := SystemUserToolAuth{
		Principal: "system:nightly-sync", AgentName: "support_agent", CredentialKey: "google_drive",
		State: SystemUserToolAuthDisconnected, DisconnectedBy: new("bob@example.com"), DisconnectedTime: new(int64(2000)),
	}
	if got, _ := listedRow(t, db, nightlySyncDrive); cmp.Diff(want, got) != "" {
		t.Errorf("row after a disconnect with no begin mismatch (-want +got):\n%s", cmp.Diff(want, got))
	}
}
