// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package agent

import (
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/tool"
)

func vizPayload(r *tool.Result) any {
	return toolResultData(r, defaultToolOutputSaveThresholdBytes, defaultToolOutputLLMReturnMaxBytes, true)
}

func vizPayloadNoArtifactService(r *tool.Result) any {
	return toolResultData(r, defaultToolOutputSaveThresholdBytes, defaultToolOutputLLMReturnMaxBytes, false)
}

func TestToolResultData(t *testing.T) {
	t.Run("nil result", func(t *testing.T) {
		if got := vizPayload(nil); got != nil {
			t.Errorf("got %v, want nil", got)
		}
	})

	t.Run("structured data passes through", func(t *testing.T) {
		data := map[string]any{"count": 5}
		got := vizPayload(&tool.Result{Status: tool.StatusSuccess, Data: data})
		if m, ok := got.(map[string]any); !ok || m["count"] != 5 {
			t.Errorf("got %v, want the Data map", got)
		}
	})

	t.Run("success without data returns bare message", func(t *testing.T) {
		got := vizPayload(&tool.Result{Status: tool.StatusSuccess, Message: "done"})
		if got != "done" {
			t.Errorf("got %v, want bare message", got)
		}
	})

	for _, status := range []string{tool.StatusError, tool.StatusFailed} {
		t.Run("error status without data preserves status: "+status, func(t *testing.T) {
			got := vizPayload(&tool.Result{Status: status, Message: "boom"})
			m, ok := got.(map[string]any)
			if !ok {
				t.Fatalf("got %T, want a map carrying the error status", got)
			}
			if m["status"] != status {
				t.Errorf("status = %v, want %q", m["status"], status)
			}
			if m["message"] != "boom" {
				t.Errorf("message = %v, want boom", m["message"])
			}
		})
	}

	t.Run("failure carries the machine error code", func(t *testing.T) {
		got, _ := vizPayload(tool.AuthRequired("sign in first")).(map[string]any)
		if got["error_code"] != tool.ErrorCodeAuthRequired {
			t.Errorf("error_code = %v, want %s", got["error_code"], tool.ErrorCodeAuthRequired)
		}
	})

	t.Run("failure with data carries the machine error code", func(t *testing.T) {
		r := tool.Failed("remote tool crashed", nil)
		r.Data = map[string]any{"stderr": "trace"}
		got, _ := vizPayload(r).(map[string]any)
		if got["error_code"] != tool.ErrorCodeToolFailed || got["status"] != tool.StatusFailed {
			t.Errorf("got %#v, want status failed and error_code %s", got, tool.ErrorCodeToolFailed)
		}
	})
}
