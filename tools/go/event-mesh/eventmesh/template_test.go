// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"strings"
	"testing"
)

func TestFillTopicTemplate_Basic(t *testing.T) {
	out, err := fillTopicTemplate("acme/weather/{city}/{unit}", map[string]any{
		"city": "Ottawa",
		"unit": "celsius",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "acme/weather/Ottawa/celsius" {
		t.Fatalf("unexpected topic: %q", out)
	}
}

func TestFillTopicTemplate_LegacyDoubleBrace(t *testing.T) {
	cases := map[string]string{
		"acme/weather/{{city}}/{unit}":    "acme/weather/Ottawa/celsius",
		"acme/weather/{{ city }}/{unit}":  "acme/weather/Ottawa/celsius",
		"acme/user/{{ text://city }}/get": "acme/user/Ottawa/get",
		"acme/{{json://city}}/{{ unit }}": "acme/Ottawa/celsius",
	}
	for tmpl, want := range cases {
		out, err := fillTopicTemplate(tmpl, map[string]any{"city": "Ottawa", "unit": "celsius"})
		if err != nil {
			t.Fatalf("%q: unexpected error: %v", tmpl, err)
		}
		if out != want {
			t.Fatalf("%q: got %q, want %q", tmpl, out, want)
		}
	}
}

func TestFillTopicTemplate_MissingParam(t *testing.T) {
	_, err := fillTopicTemplate("acme/{city}", map[string]any{})
	if err == nil {
		t.Fatal("expected missing-param error")
	}
	if !strings.Contains(err.Error(), `missing required input property "city" for topic address`) {
		t.Fatalf("unexpected error message: %q", err.Error())
	}
}

func TestFillTopicTemplate_NumericValue(t *testing.T) {
	out, err := fillTopicTemplate("seq/{id}", map[string]any{"id": 42})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "seq/42" {
		t.Fatalf("unexpected topic: %q", out)
	}
}

func TestFillTopicTemplate_EmptyTemplate(t *testing.T) {
	out, err := fillTopicTemplate("", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "" {
		t.Fatalf("expected empty output, got %q", out)
	}
}

func TestFillTopicTemplate_NoPlaceholders(t *testing.T) {
	out, err := fillTopicTemplate("static/topic/path", map[string]any{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "static/topic/path" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestFillTopicTemplate_WhitespaceTrim(t *testing.T) {
	out, err := fillTopicTemplate("a/{   foo   }/b", map[string]any{"foo": "bar"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "a/bar/b" {
		t.Fatalf("unexpected output: %q", out)
	}
}

func TestFillTopicTemplate_BoolStringifyMatchesPython(t *testing.T) {
	out, err := fillTopicTemplate("svc/{flag}", map[string]any{"flag": true})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "svc/True" {
		t.Fatalf("expected svc/True (Python str(True)), got %q", out)
	}
	out, _ = fillTopicTemplate("svc/{flag}", map[string]any{"flag": false})
	if out != "svc/False" {
		t.Fatalf("expected svc/False, got %q", out)
	}
}

func TestFillTopicTemplate_NoneForNil(t *testing.T) {
	out, err := fillTopicTemplate("v/{x}", map[string]any{"x": nil})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "v/None" {
		t.Fatalf("expected v/None, got %q", out)
	}
}

func TestFillTopicTemplate_FloatRendersAsInteger(t *testing.T) {
	out, err := fillTopicTemplate("seq/{id}", map[string]any{"id": float64(42)})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if out != "seq/42" {
		t.Fatalf("expected seq/42, got %q", out)
	}
}
