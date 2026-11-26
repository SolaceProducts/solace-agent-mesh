// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"maps"
	"testing"
)

func TestPublishedResultData_NilResult(t *testing.T) {
	if got := PublishedResultData(nil); got != nil {
		t.Errorf("got %v, want nil", got)
	}
}

func TestPublishedResultData_SuccessKeepsTheToolsShape(t *testing.T) {
	data := map[string]any{"count": 5}
	if got, ok := PublishedResultData(OK("done", WithData(data))).(map[string]any); !ok || got["count"] != 5 || len(got) != 1 {
		t.Errorf("structured success = %#v, want the Data map unchanged", got)
	}
	if got := PublishedResultData(OK("done")); got != "done" {
		t.Errorf("message-only success = %#v, want the bare message", got)
	}
	if got, ok := PublishedResultData(OK("done", WithErrorCode("IGNORED"))).(string); !ok || got != "done" {
		t.Errorf("a success result must not grow an error_code: %#v", got)
	}
}

func TestPublishedResultData_FailureCarriesStatusAndErrorCode(t *testing.T) {
	for _, status := range []string{StatusError, StatusFailed} {
		t.Run(status, func(t *testing.T) {
			got, ok := PublishedResultData(&Result{Status: status, Message: "boom", ErrorCode: "AUTH_REQUIRED"}).(map[string]any)
			if !ok {
				t.Fatalf("got %T, want a map", got)
			}
			want := map[string]any{"status": status, "message": "boom", "error_code": "AUTH_REQUIRED"}
			if !maps.Equal(got, want) {
				t.Errorf("got %#v, want %#v", got, want)
			}
		})
	}
}

func TestPublishedResultData_APendingResultStatesItsStatus(t *testing.T) {
	data := map[string]any{"sub_task_id": "s1", "peer_name": "p"}
	got, ok := PublishedResultData(Pending(`Waiting for peer agent "p"`, WithData(data))).(map[string]any)
	if !ok {
		t.Fatalf("got %T, want a map", got)
	}
	want := map[string]any{"status": StatusPending, "sub_task_id": "s1", "peer_name": "p"}
	if !maps.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}
	if len(data) != 2 {
		t.Errorf("the tool's Data map was mutated: %#v", data)
	}

	got, _ = PublishedResultData(Pending("waiting")).(map[string]any)
	if want := map[string]any{"status": StatusPending, "message": "waiting"}; !maps.Equal(got, want) {
		t.Errorf("message-only pending = %#v, want %#v", got, want)
	}
}

func TestPublishedResultData_FailureWithoutCodeOmitsTheKey(t *testing.T) {
	got, _ := PublishedResultData(Error("boom")).(map[string]any)
	if _, present := got["error_code"]; present {
		t.Errorf("error_code present with no code set: %#v", got)
	}
}

func TestPublishedResultData_FailureWithDataAddsCodeWithoutOverridingTheTool(t *testing.T) {
	data := map[string]any{"error_type": "smtp"}
	r := Failed("boom", nil)
	r.Data = data

	got, _ := PublishedResultData(r).(map[string]any)
	if got["status"] != StatusFailed || got["error_code"] != ErrorCodeToolFailed || got["error_type"] != "smtp" || got["message"] != "boom" {
		t.Errorf("got %#v, want the tool's keys plus status, error_code and message", got)
	}
	if len(data) != 1 {
		t.Errorf("the tool's Data map was mutated: %#v", data)
	}

	own := Error("boom", WithErrorCode("DISPATCH_TIMEOUT"),
		WithData(map[string]any{"status": "rejected", "error_code": "UPSTREAM_REJECTED", "message": "upstream said no"}))
	got, _ = PublishedResultData(own).(map[string]any)
	if got["status"] != "rejected" || got["error_code"] != "UPSTREAM_REJECTED" || got["message"] != "upstream said no" {
		t.Errorf("tool-supplied keys must win on collision: %#v", got)
	}

	ownStatusOnly := Error("boom", WithErrorCode(ErrorCodeAuthRequired),
		WithData(map[string]any{"status": "rejected"}))
	got, _ = PublishedResultData(ownStatusOnly).(map[string]any)
	if got["status"] != "rejected" || got["error_code"] != ErrorCodeAuthRequired {
		t.Errorf("a tool-reported status must not suppress the error code: %#v", got)
	}
}

func TestPublishedResultData_AValidationErrorStatesItsTextAndTheValidationCode(t *testing.T) {
	got, ok := PublishedResultData(ValidationErr("missing required parameter q")).(map[string]any)
	if !ok {
		t.Fatalf("got %T, want a map", got)
	}
	want := map[string]any{"status": StatusError, "message": "missing required parameter q", "error_code": "DISPATCH_VALIDATION"}
	if !maps.Equal(got, want) {
		t.Errorf("got %#v, want %#v", got, want)
	}

	coded := ValidationErr("missing required parameter q")
	coded.Message, coded.ErrorCode = "summary", "SCHEMA_REJECTION"
	got, _ = PublishedResultData(coded).(map[string]any)
	if got["message"] != "summary" || got["error_code"] != "SCHEMA_REJECTION" {
		t.Errorf("a message or code the result already carries must win: %#v", got)
	}
}
