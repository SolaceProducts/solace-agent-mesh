// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package remotetool

import "testing"

func TestInvokeTopic(t *testing.T) {
	got := InvokeTopic("ns", "process_file")
	want := "ns/a2a/v1/sam_remote_tool/invoke/process_file"
	if got != want {
		t.Errorf("InvokeTopic = %q, want %q", got, want)
	}
}

func TestResponseTopic(t *testing.T) {
	got := ResponseTopic("ns", "myagent", "corr-123")
	want := "ns/a2a/v1/sam_remote_tool/response/myagent/corr-123"
	if got != want {
		t.Errorf("ResponseTopic = %q, want %q", got, want)
	}
}

func TestStatusTopic(t *testing.T) {
	got := StatusTopic("ns", "myagent", "corr-123")
	want := "ns/a2a/v1/sam_remote_tool/status/myagent/corr-123"
	if got != want {
		t.Errorf("StatusTopic = %q, want %q", got, want)
	}
}

func TestInitTopic(t *testing.T) {
	got := InitTopic("ns", "process_file")
	want := "ns/a2a/v1/sam_remote_tool/init/process_file"
	if got != want {
		t.Errorf("InitTopic = %q, want %q", got, want)
	}
}

func TestResponseSubscription(t *testing.T) {
	got := ResponseSubscription("ns", "myagent")
	want := "ns/a2a/v1/sam_remote_tool/response/myagent/>"
	if got != want {
		t.Errorf("ResponseSubscription = %q, want %q", got, want)
	}
}

func TestStatusSubscription(t *testing.T) {
	got := StatusSubscription("ns", "myagent")
	want := "ns/a2a/v1/sam_remote_tool/status/myagent/>"
	if got != want {
		t.Errorf("StatusSubscription = %q, want %q", got, want)
	}
}

func TestCancelTopic(t *testing.T) {
	got := CancelTopic("ns", "corr-abc")
	want := "ns/a2a/v1/sam_remote_tool/cancel/corr-abc"
	if got != want {
		t.Errorf("CancelTopic = %q, want %q", got, want)
	}
}

func TestCancelSubscription(t *testing.T) {
	got := CancelSubscription("ns")
	want := "ns/a2a/v1/sam_remote_tool/cancel/>"
	if got != want {
		t.Errorf("CancelSubscription = %q, want %q", got, want)
	}
}

func TestExtractCancelCorrID(t *testing.T) {
	tests := []struct {
		topic string
		want  string
	}{
		{"ns/a2a/v1/sam_remote_tool/cancel/corr-abc", "corr-abc"},
		{"my/ns/a2a/v1/sam_remote_tool/cancel/123e4567", "123e4567"},
		{"ns/a2a/v1/sam_remote_tool/invoke/echo", ""},
		{"unrelated/topic", ""},
		{"", ""},
	}
	for _, tc := range tests {
		got := ExtractCancelCorrID(tc.topic)
		if got != tc.want {
			t.Errorf("ExtractCancelCorrID(%q) = %q, want %q", tc.topic, got, tc.want)
		}
	}
}
