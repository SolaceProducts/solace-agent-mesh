// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"context"
	"errors"
	"testing"
)

const sonnet55ChoiceRefusal = `tool_choice: type "tool" and "any" are not supported for this model.`

type choiceRefusingClient struct {
	rejectsTemperature bool
	failFirst, fail    error
	sent               []ChatRequest
}

func (c *choiceRefusingClient) ChatCompletion(_ context.Context, req *ChatRequest) (*ChatResponse, error) {
	c.sent = append(c.sent, *req)
	switch {
	case c.failFirst != nil:
		return nil, c.failFirst
	case c.rejectsTemperature && req.Temperature != nil:
		return nil, &Error{StatusCode: 400, Message: "`temperature` is deprecated for this model."}
	case req.ToolChoice != "":
		return nil, &Error{StatusCode: 400, Message: sonnet55ChoiceRefusal}
	case c.fail != nil:
		return nil, c.fail
	}
	return &ChatResponse{ToolCalls: []ToolCall{{ID: "c1", Name: "emit", Arguments: "{}"}}}, nil
}

func (c *choiceRefusingClient) ChatCompletionStream(context.Context, *ChatRequest) (<-chan StreamChunk, error) {
	return nil, errors.New("not implemented")
}

func forcedRequest() *ChatRequest {
	return &ChatRequest{Tools: []ToolDefinition{{Name: "emit"}}, ToolChoice: "required", Temperature: new(0.1)}
}

func (c *choiceRefusingClient) choices() []string {
	out := make([]string, len(c.sent))
	for i, r := range c.sent {
		out[i] = r.ToolChoice
	}
	return out
}

func TestARefusedForcedChoiceIsLeftToTheModel(t *testing.T) {
	client := &choiceRefusingClient{}
	resp, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), nil, nil, "m", nil)
	if err != nil {
		t.Fatalf("a model that refuses a forced choice failed the call: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("got %+v, want the reply to the unforced request", resp)
	}
	if got := client.choices(); len(got) != 2 || got[0] != "required" || got[1] != "" {
		t.Errorf("tool choices sent = %q, want the forced one and then none", got)
	}
}

func TestAKnownRefuserIsNotProbed(t *testing.T) {
	for name, choices := range map[string]*ToolChoicePolicy{"without a policy": nil, "with a policy": {}} {
		t.Run(name, func(t *testing.T) {
			client := &choiceRefusingClient{}
			for range 2 {
				if _, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), choices, nil, "anthropic/claude-sonnet-5-5", nil); err != nil {
					t.Fatal(err)
				}
			}
			if got := client.choices(); len(got) != 2 || got[0] != "" || got[1] != "" {
				t.Errorf("tool choices sent = %q, want two calls with the choice left to the model", got)
			}
		})
	}
	client := &choiceRefusingClient{}
	if _, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), nil, nil, "anthropic/claude-sonnet-5", nil); err != nil {
		t.Fatal(err)
	}
	if got := client.choices(); len(got) != 2 || got[0] != "required" {
		t.Errorf("a model that is not listed was not forced first: sent %q", got)
	}
}

func TestARefusalIsRememberedPerModel(t *testing.T) {
	client := &choiceRefusingClient{}
	var choices ToolChoicePolicy
	for range 3 {
		if _, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), &choices, nil, "m", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := client.choices(); len(got) != 4 {
		t.Errorf("tool choices sent = %q, want one refused probe and then three unforced calls", got)
	}
	client.sent = nil
	if _, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), &choices, nil, "other", nil); err != nil {
		t.Fatal(err)
	}
	if got := client.choices(); len(got) != 2 || got[0] != "required" {
		t.Errorf("another model inherited the refusal: sent %q", got)
	}
}

func TestWithoutAPolicyEveryCallProbes(t *testing.T) {
	client := &choiceRefusingClient{}
	for range 2 {
		if _, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), nil, nil, "m", nil); err != nil {
			t.Fatal(err)
		}
	}
	if got := client.choices(); len(got) != 4 {
		t.Errorf("tool choices sent = %q, want a probe and a retry per call", got)
	}
}

func TestARefusalBehindATemperatureRejectionIsFound(t *testing.T) {
	client := &choiceRefusingClient{rejectsTemperature: true}
	if _, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), nil, nil, "m", nil); err != nil {
		t.Fatalf("the refusal behind the temperature rejection was not handled: %v", err)
	}
	last := client.sent[len(client.sent)-1]
	if last.ToolChoice != "" || last.Temperature != nil {
		t.Errorf("last request carried choice %q, temperature %v; want neither", last.ToolChoice, last.Temperature)
	}
}

func TestAnotherErrorIsNotRetriedUnforced(t *testing.T) {
	unavailable := &Error{StatusCode: 503, Message: "overloaded"}
	client := &choiceRefusingClient{}
	req := &ChatRequest{Tools: []ToolDefinition{{Name: "emit"}}}
	client.fail = unavailable
	if _, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, req, nil, nil, "m", nil); !errors.Is(err, unavailable) {
		t.Fatalf("err = %v, want the 503", err)
	}
	if len(client.sent) != 1 {
		t.Errorf("sent %d requests, want 1: an unforced request has nothing to fall back from", len(client.sent))
	}
}

func TestAFailedRetryKeepsTheRefusal(t *testing.T) {
	unavailable := &Error{StatusCode: 503, Message: "overloaded"}
	client := &choiceRefusingClient{fail: unavailable}
	_, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), nil, nil, "m", nil)
	if !errors.Is(err, unavailable) || !RefusesToolChoice(err) {
		t.Errorf("err = %v, want both the retry's 503 and the refusal that caused the retry", err)
	}
}

func TestAForcedCallThatFailsOtherwiseIsNotRetried(t *testing.T) {
	unavailable := &Error{StatusCode: 503, Message: "overloaded"}
	client := &choiceRefusingClient{failFirst: unavailable}
	var choices ToolChoicePolicy
	_, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, forcedRequest(), &choices, nil, "m", nil)
	if !errors.Is(err, unavailable) || len(client.sent) != 1 || choices.Refuses("m") {
		t.Errorf("err %v, sent %d, refuses %v; want the 503 once and no verdict", err, len(client.sent), choices.Refuses("m"))
	}
}

func TestOnlyTheFirstRefusalOfAModelIsNews(t *testing.T) {
	var choices ToolChoicePolicy
	if !choices.Mark("m") || choices.Mark("m") || !choices.Mark("other") {
		t.Error("Mark should report a key only the first time it is marked")
	}
	var none *ToolChoicePolicy
	for range 2 {
		if !none.Mark("m") {
			t.Error("without a policy every refusal is news")
		}
	}
}

func TestAnUnforcedRefusalIsNotRetried(t *testing.T) {
	client := &choiceRefusingClient{fail: &Error{StatusCode: 400, Message: sonnet55ChoiceRefusal}}
	var choices ToolChoicePolicy
	req := &ChatRequest{Tools: []ToolDefinition{{Name: "emit"}}}
	if _, err := ChatCompletionWithToolChoiceFallback(t.Context(), client, req, &choices, nil, "m", nil); err == nil {
		t.Fatal("want the refusal back")
	}
	if len(client.sent) != 1 || choices.Refuses("m") {
		t.Errorf("sent %d, refuses %v; want one request and no verdict", len(client.sent), choices.Refuses("m"))
	}
}
