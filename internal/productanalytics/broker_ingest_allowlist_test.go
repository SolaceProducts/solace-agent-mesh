// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package productanalytics

import "testing"

func TestTheServerIngestTopicAcceptsOnlyTheInventorySnapshot(t *testing.T) {
	assertAcceptsOnly(t, "server", brokerIngestEvents, EventInstallInventory)
}

func TestTheCLIIngestTopicAcceptsOnlyTheCommandRun(t *testing.T) {
	assertAcceptsOnly(t, "cli", brokerCLIIngestEvents, EventCLICommandRun)
}

func assertAcceptsOnly(t *testing.T, topic string, accept map[EventName]bool, want EventName) {
	t.Helper()
	if !accept[want] {
		t.Errorf("the %s ingest topic refuses %q, which is the event it exists to carry", topic, want)
	}
	for name := range catalog {
		if name == want {
			continue
		}
		if accept[name] {
			t.Errorf("the %s ingest topic accepts %q, want %q alone", topic, name, want)
		}
	}
}
