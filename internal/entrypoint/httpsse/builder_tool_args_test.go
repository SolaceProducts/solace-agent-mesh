// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package httpsse

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestPromptBuilderChat_TemplateUpdatesSentAsAStringAreRead(t *testing.T) {
	mock := &mockLLMClient{response: `{"message":"Drafted","templateUpdates":"{\"name\":\"Action items\",\"command\":\"actions\"}","confidence":0.9}`}
	h := setupPromptBuilderWithLLM(t, mock)

	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"meeting notes to action items"}`)))

	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.IsError || resp.TemplateUpdates == nil || resp.TemplateUpdates.Name != "Action items" || resp.TemplateUpdates.Command != "actions" {
		t.Errorf("got %+v (updates %+v), want the template the string held", resp, resp.TemplateUpdates)
	}
}

func TestDecodePromptTemplateArgs(t *testing.T) {
	for name, tc := range map[string]struct {
		args           string
		wantName       string
		wantConfidence float64
		wantReady      bool
		wantErr        bool
	}{
		"object":         {args: `{"message":"m","templateUpdates":{"name":"n"}}`, wantName: "n"},
		"encoded object": {args: `{"message":"m","templateUpdates":"{\"name\":\"n\"}"}`, wantName: "n"},
		"absent":         {args: `{"message":"m"}`},
		"null":           {args: `{"message":"m","templateUpdates":null}`},
		"encoded with the rest": {
			args:     `{"message":"m","templateUpdates":"{\"name\":\"n\"},\"confidence\":0.9,\"readyToSave\":true}"}`,
			wantName: "n", wantConfidence: 0.9, wantReady: true,
		},
		"top level wins": {
			args:     `{"message":"m","confidence":0.4,"templateUpdates":"{\"name\":\"n\"},\"confidence\":0.9}"}`,
			wantName: "n", wantConfidence: 0.4,
		},
		"unreadable tail keeps the template": {
			args:     `{"message":"m","templateUpdates":"{\"name\":\"n\"},garbage"}`,
			wantName: "n",
		},
		"encoded garbage": {args: `{"message":"m","templateUpdates":"not json"}`, wantErr: true},
		"wrong type":      {args: `{"message":"m","templateUpdates":3}`, wantErr: true},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := decodePromptTemplateArgs(tc.args)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err = %v, want error %v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if got.Message != "m" {
				t.Errorf("message = %q, want m", got.Message)
			}
			gotName := ""
			if got.TemplateUpdates != nil {
				gotName = got.TemplateUpdates.Name
			}
			if gotName != tc.wantName {
				t.Errorf("name = %q, want %q", gotName, tc.wantName)
			}
			if got.Confidence != tc.wantConfidence || got.ReadyToSave != tc.wantReady {
				t.Errorf("confidence %v, readyToSave %v; want %v, %v", got.Confidence, got.ReadyToSave, tc.wantConfidence, tc.wantReady)
			}
		})
	}
}

func TestParseAndValidateResponse_TaskUpdatesSentAsAString(t *testing.T) {
	resp := parseAndValidateResponse(`{"message":"Set up","taskUpdates":"{\"name\":\"Daily Report\"}","confidence":0.8}`, nil, nil, nil, nil)
	if resp.TaskUpdates["name"] != "Daily Report" {
		t.Errorf("taskUpdates = %v, want the task the string held", resp.TaskUpdates)
	}
	resp = parseAndValidateResponse(`{"message":"Set up","taskUpdates":"{\"name\":\"Daily Report\"},\"confidence\":0.8,\"readyToSave\":true}"}`, nil, nil, nil, nil)
	if resp.TaskUpdates["name"] != "Daily Report" || resp.Confidence != 0.8 || !resp.ReadyToSave {
		t.Errorf("got %+v, want the task and the arguments packed after it in the string", resp)
	}
	resp = parseAndValidateResponse(`{"message":"Set up","taskUpdates":"not json","confidence":0.8}`, nil, nil, nil, nil)
	if len(resp.TaskUpdates) != 0 {
		t.Errorf("taskUpdates = %v, want none from a string that holds no object", resp.TaskUpdates)
	}
}

func TestPromptBuilderChat_ErrorFlagsFromTheModelDoNotReachTheResponse(t *testing.T) {
	mock := &mockLLMClient{response: `{"message":"Drafted","templateUpdates":{"name":"Action items"},"confidence":0.9,"isError":true,"isWarning":true}`}
	h := setupPromptBuilderWithLLM(t, mock)

	rec := httptest.NewRecorder()
	h.handleChat(rec, httptest.NewRequest("POST", "/api/v1/prompts/chat", strings.NewReader(`{"message":"meeting notes to action items"}`)))

	var resp ChatBuilderResponse
	if err := json.NewDecoder(rec.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if resp.TemplateUpdates == nil || resp.TemplateUpdates.Name != "Action items" {
		t.Fatalf("got updates %+v, want the template the model drafted", resp.TemplateUpdates)
	}
	if resp.IsError || resp.IsWarning {
		t.Errorf("got isError=%v isWarning=%v, want both false for a normal reply", resp.IsError, resp.IsWarning)
	}
}
