// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package harness

import (
	"net/http"
	"slices"
	"testing"
)

func TestUnknownFileRequestsAreRecordedBeforeScriptedFailures(t *testing.T) {
	m := NewMockGoogleChatServer()
	t.Cleanup(m.Close)
	m.ScriptDriveFailures(GoogleChatDriveFailShare, GoogleChatDriveFailDelete)

	if err := send(http.MethodPost, m.URL()+"/drive/v3/files/nosuchid/permissions?supportsAllDrives=true"); err != nil {
		t.Fatal(err)
	}
	if err := send(http.MethodDelete, m.URL()+"/drive/v3/files/nosuchid2?supportsAllDrives=true"); err != nil {
		t.Fatal(err)
	}
	got := m.UnknownDriveFileRequests()
	if len(got) != 2 || !slices.ContainsFunc(got, func(p string) bool { return p == "/drive/v3/files/nosuchid/permissions" }) {
		t.Errorf("unknown file requests = %q, want both recorded despite the scripted failures", got)
	}
	if left := m.UnconsumedDriveFailures(); len(left) != 0 {
		t.Errorf("unconsumed failures = %v, want the scripted ones consumed by those requests", left)
	}
}
