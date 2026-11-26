// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sessioncontrol

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"testing"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/agent/extensions"
)

type fakeService struct {
	gotInfo     extensions.RequestInfo
	gotParams   CompactParams
	returnRes   *Result
	returnError error
	calls       int
}

func (f *fakeService) CompactSession(_ context.Context, info extensions.RequestInfo, params CompactParams) (*Result, error) {
	f.calls++
	f.gotInfo = info
	f.gotParams = params
	return f.returnRes, f.returnError
}

func TestHandler_ForwardsParamsAndUserID(t *testing.T) {
	svc := &fakeService{returnRes: &Result{
		SummarizedMessagesCount:       4,
		TokensFreed:                   1200,
		PostCompactionRemainingTokens: 800,
		SummarizerInputTokens:         300,
		SummarizerOutputTokens:        80,
	}}
	h := HandlerFor(svc)

	raw := json.RawMessage(`{"sessionId":"sess-xyz","targetPercent":40,"taskId":"task-1","traceId":"trace-1","channel":"ui","userUuid":"user-row","entrypointUuid":"gw-row"}`)
	res, errObj := h(t.Context(), extensions.RequestInfo{UserID: "user-1", ClientID: "gw-1"}, raw)
	if errObj != nil {
		t.Fatalf("unexpected error: %v", errObj)
	}
	if svc.calls != 1 {
		t.Fatalf("calls = %d", svc.calls)
	}
	wantInfo := extensions.RequestInfo{UserID: "user-1", ClientID: "gw-1"}
	wantParams := CompactParams{SessionID: "sess-xyz", TargetPercent: 40, TaskID: "task-1", TraceID: "trace-1",
		Origin: a2a.Origin{Channel: "ui", UserUUID: "user-row", EntrypointUUID: "gw-row"}}
	if svc.gotInfo != wantInfo || svc.gotParams != wantParams {
		t.Fatalf("service got %+v %+v, want %+v %+v", svc.gotInfo, svc.gotParams, wantInfo, wantParams)
	}
	r, ok := res.(*Result)
	if !ok {
		t.Fatalf("wrong result type: %T", res)
	}
	if r.SummarizedMessagesCount != 4 || r.PostCompactionRemainingTokens != 800 {
		t.Fatalf("result values not propagated: %#v", r)
	}
}

func TestHandler_RequiresSessionID(t *testing.T) {
	svc := &fakeService{}
	h := HandlerFor(svc)

	_, errObj := h(t.Context(), extensions.RequestInfo{}, json.RawMessage(`{}`))
	if errObj == nil || errObj.Code != -32602 {
		t.Fatalf("want invalid-params error, got %v", errObj)
	}
	if svc.calls != 0 {
		t.Fatalf("service must not be called for invalid params")
	}
}

func TestHandler_RejectsOutOfRangeTargetPercent(t *testing.T) {
	svc := &fakeService{}
	h := HandlerFor(svc)
	for _, val := range []int{-1, 101, 200} {
		raw := []byte(`{"sessionId":"s","targetPercent":` + strconv.Itoa(val) + `}`)
		_, errObj := h(t.Context(), extensions.RequestInfo{}, raw)
		if errObj == nil || errObj.Code != -32602 {
			t.Errorf("targetPercent=%d should be rejected, got %v", val, errObj)
		}
	}
	if svc.calls != 0 {
		t.Fatalf("service must not be called for invalid params")
	}
}

func TestHandler_AcceptsZeroAndBoundaryTargetPercent(t *testing.T) {
	svc := &fakeService{returnRes: &Result{}}
	h := HandlerFor(svc)
	for _, val := range []int{0, 50, 99} {
		raw := []byte(`{"sessionId":"s","targetPercent":` + strconv.Itoa(val) + `}`)
		_, errObj := h(t.Context(), extensions.RequestInfo{UserID: "u"}, raw)
		if errObj != nil {
			t.Errorf("targetPercent=%d returned %v", val, errObj)
		}
	}
}

func TestHandler_NilParamsTreatedAsEmpty(t *testing.T) {
	svc := &fakeService{}
	h := HandlerFor(svc)
	_, errObj := h(t.Context(), extensions.RequestInfo{}, nil)
	if errObj == nil || errObj.Code != -32602 {
		t.Fatalf("nil params with no sessionId must yield invalid-params, got %v", errObj)
	}
}

func TestHandler_PropagatesTypedExtensionError(t *testing.T) {
	svc := &fakeService{returnError: &extensions.Error{
		Code:    extensions.CodeNotFound,
		Message: "session not found",
		Data:    map[string]string{"sessionId": "x"},
	}}
	h := HandlerFor(svc)
	_, errObj := h(t.Context(), extensions.RequestInfo{}, json.RawMessage(`{"sessionId":"x"}`))
	if errObj == nil || errObj.Code != extensions.CodeNotFound {
		t.Fatalf("typed error not propagated: %v", errObj)
	}
}

func TestHandler_WrapsBareErrorAsInternal(t *testing.T) {
	svc := &fakeService{returnError: errors.New("boom")}
	h := HandlerFor(svc)
	_, errObj := h(t.Context(), extensions.RequestInfo{}, json.RawMessage(`{"sessionId":"x"}`))
	if errObj == nil || errObj.Code != extensions.CodeInternal {
		t.Fatalf("bare error not wrapped: %v", errObj)
	}
}

func TestHandler_RejectsMalformedJSON(t *testing.T) {
	svc := &fakeService{}
	h := HandlerFor(svc)
	_, errObj := h(t.Context(), extensions.RequestInfo{}, json.RawMessage(`{"sessionId": broken`))
	if errObj == nil || errObj.Code != -32602 {
		t.Fatalf("expected invalid-params, got %v", errObj)
	}
}
