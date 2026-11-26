// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eval

import "testing"

func TestAggregateRunStatuses(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		statuses []string
		want     string
	}{
		{"any pending wins", []string{StatusRunning, StatusPending, StatusFailed}, StatusPending},
		{"running beats terminal", []string{StatusRunning, StatusFailed}, StatusRunning},
		{"failed beats cancelled", []string{StatusCancelled, StatusFailed}, StatusFailed},
		{"cancelled beats warnings", []string{StatusCompletedWithWarnings, StatusCancelled}, StatusCancelled},
		{"warnings beats completed", []string{StatusCompleted, StatusCompletedWithWarnings}, StatusCompletedWithWarnings},
		{"all completed", []string{StatusCompleted, StatusCompleted}, StatusCompleted},
		{"empty defaults to completed", []string{}, StatusCompleted},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := AggregateRunStatuses(c.statuses)
			if got != c.want {
				t.Fatalf("AggregateRunStatuses(%v) = %q, want %q", c.statuses, got, c.want)
			}
		})
	}
}

func TestIsTerminalStatus(t *testing.T) {
	t.Parallel()
	cases := map[string]bool{
		StatusPending:               false,
		StatusRunning:               false,
		StatusCompleted:             true,
		StatusCompletedWithWarnings: true,
		StatusFailed:                true,
		StatusCancelled:             true,
		"unknown":                   false,
	}
	for s, want := range cases {
		if got := IsTerminalStatus(s); got != want {
			t.Fatalf("IsTerminalStatus(%q) = %v, want %v", s, got, want)
		}
	}
}
