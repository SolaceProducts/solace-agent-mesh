// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package stim

import (
	"bytes"
	"fmt"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"gopkg.in/yaml.v3"
)

var adversarialStrings = map[string]string{
	"leadingSpaces":      "    indented first line\nsecond line",
	"leadingTab":         "\ttabbed first line\nsecond line",
	"blankFirstLine":     "\nafter a blank first line\nmore",
	"trailingWhitespace": "line one\nline two   ",
	"trailingNewline":    "line one\nline two\n",
	"crlf":               "line one\r\nline two\r\n",
	"looksLikeYAMLKey":   "preview: something\nmore text",
	"startsWithDash":     "- item one\n- item two",
	"startsWithPipe":     "| pipe table |\n|---|",
	"startsWithQuote":    "\"quoted\"\nnext",
	"onlyWhitespace":     "   ",
	"empty":              "",
	"tabsInterior":       "col1\tcol2\ncol3\tcol4",
	"markdownCodeFence":  "Here is the plan:\n```python\n    x = 1\n```\ndone",
	"veryLongSingleLine": strings.Repeat("word ", 400),
	"veryLongNoSpaces":   strings.Repeat("abcdefghij", 200),
	"crOnly":             "line one\rline two",
	"nestedIndentJump":   "a\n        deep\n  shallow\n",
}

func nestStimDoc(text string) map[string]any {
	return map[string]any{
		"invocation_details": map[string]any{"log_file_version": "2.0"},
		"invocation_flow": []map[string]any{{
			"id": "evt-0001",
			"payload": map[string]any{"result": map[string]any{"status": map[string]any{"message": map[string]any{
				"parts": []any{map[string]any{"kind": "text", "text": text}},
			}}}},
		}},
	}
}

func nestedText(t *testing.T, doc map[string]any) string {
	t.Helper()
	flow, ok := doc["invocation_flow"].([]any)
	if !ok || len(flow) == 0 {
		t.Fatalf("invocation_flow missing or wrong shape: %#v", doc["invocation_flow"])
	}
	event := flow[0].(map[string]any)
	result := event["payload"].(map[string]any)["result"].(map[string]any)
	parts := result["status"].(map[string]any)["message"].(map[string]any)["parts"].([]any)
	part, ok := parts[0].(map[string]any)
	if !ok {
		t.Fatalf("part 0 is not a mapping: %#v", parts[0])
	}
	raw, present := part["text"]
	if !present {
		t.Fatalf("part 0 has no text key: %#v", part)
	}
	text, ok := raw.(string)
	if !ok {
		t.Fatalf("text is %T, not a string: %#v", raw, raw)
	}
	return text
}

var blockScalarHeader = regexp.MustCompile(`(?m):\s*[|>][0-9]*[-+]?\s*$`)

func TestMarshal_RoundTripsAdversarialText(t *testing.T) {
	for name, text := range adversarialStrings {
		t.Run(name, func(t *testing.T) {
			data, err := Marshal(nestStimDoc(text))
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if loc := blockScalarHeader.FindString(string(data)); loc != "" {
				t.Errorf("emitted a block scalar (%q); indentation indicators are what broke the format", strings.TrimSpace(loc))
			}
			var back map[string]any
			if err := yaml.Unmarshal(data, &back); err != nil {
				t.Fatalf("emitted YAML does not parse back: %v\n%s", err, data)
			}
			if got := nestedText(t, back); got != text {
				t.Errorf("text content changed:\n got %q\nwant %q", got, text)
			}
		})
	}
}

func TestMarshal_NaiveMarshalIsBroken(t *testing.T) {
	const input = "    indented first line\nsecond line"
	doc := nestStimDoc(input)

	naive, err := yaml.Marshal(doc)
	if err != nil {
		t.Fatalf("naive marshal: %v", err)
	}
	naiveBroken := false
	if blockScalarHeader.MatchString(string(naive)) {
		var back map[string]any
		if unmarshalErr := yaml.Unmarshal(naive, &back); unmarshalErr != nil {
			naiveBroken = true
		} else if got := nestedTextOptional(back); got != input {
			naiveBroken = true
		}
	}
	if !naiveBroken {
		t.Log("this yaml.v3 round-trips its own block scalars; stim.Marshal is still asserted below")
	}

	ours, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]any
	if err := yaml.Unmarshal(ours, &back); err != nil {
		t.Fatalf("stim.Marshal output does not parse: %v\n%s", err, ours)
	}
	if got := nestedText(t, back); got != input {
		t.Errorf("stim.Marshal did not round-trip:\n got %q\nwant %q\noutput:\n%s", got, input, ours)
	}
}

func nestedTextOptional(doc map[string]any) string {
	flow, ok := doc["invocation_flow"].([]any)
	if !ok || len(flow) == 0 {
		return ""
	}
	event, ok := flow[0].(map[string]any)
	if !ok {
		return ""
	}
	payload, ok := event["payload"].(map[string]any)
	if !ok {
		return ""
	}
	result, ok := payload["result"].(map[string]any)
	if !ok {
		return ""
	}
	status, ok := result["status"].(map[string]any)
	if !ok {
		return ""
	}
	message, ok := status["message"].(map[string]any)
	if !ok {
		return ""
	}
	parts, ok := message["parts"].([]any)
	if !ok || len(parts) == 0 {
		return ""
	}
	part, ok := parts[0].(map[string]any)
	if !ok {
		return ""
	}
	text, _ := part["text"].(string)
	return text
}

func TestMarshal_PreservesNonStringScalars(t *testing.T) {
	doc := map[string]any{"i": 42, "i64": int64(99), "f": 3.5, "b": true, "n": nil, "plain": "clean-value"}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]any
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip: %v\n%s", err, data)
	}
	if back["i"] != 42 || back["i64"] != 99 || back["b"] != true || back["plain"] != "clean-value" {
		t.Errorf("scalars changed: %#v", back)
	}
	if f, _ := back["f"].(float64); f != 3.5 {
		t.Errorf("float = %v, want 3.5", back["f"])
	}
	if v, present := back["n"]; !present || v != nil {
		t.Errorf("nil value = %v (present=%v)", v, present)
	}
}

func TestMarshal_ReachesStringsInTypedContainers(t *testing.T) {
	risky := "  indented\nnext"
	doc := map[string]any{
		"tags":    []string{risky},
		"attrs":   map[string]string{"a": risky},
		"nested":  []map[string]string{{"b": risky}},
		"pointer": &risky,
	}
	data, err := Marshal(doc)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if loc := blockScalarHeader.FindString(string(data)); loc != "" {
		t.Fatalf("block scalar emitted inside typed container (%q)\n%s", strings.TrimSpace(loc), data)
	}
	var back struct {
		Tags    []string            `yaml:"tags"`
		Attrs   map[string]string   `yaml:"attrs"`
		Nested  []map[string]string `yaml:"nested"`
		Pointer string              `yaml:"pointer"`
	}
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip: %v\n%s", err, data)
	}
	if back.Tags[0] != risky || back.Attrs["a"] != risky || back.Nested[0]["b"] != risky || back.Pointer != risky {
		t.Errorf("typed container content changed: %#v", back)
	}
}

func TestMarshal_SanitizesDelegatedEncoding(t *testing.T) {
	type event struct {
		Text string `yaml:"text"`
	}
	risky := "  indented\nnext"
	data, err := Marshal(map[string]any{"events": []event{{Text: risky}}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if loc := blockScalarHeader.FindString(string(data)); loc != "" {
		t.Fatalf("block scalar survived delegated encoding (%q)\n%s", strings.TrimSpace(loc), data)
	}
	var back struct {
		Events []event `yaml:"events"`
	}
	if err := yaml.Unmarshal(data, &back); err != nil {
		t.Fatalf("round-trip: %v\n%s", err, data)
	}
	if back.Events[0].Text != risky {
		t.Errorf("struct field text = %q, want %q", back.Events[0].Text, risky)
	}
}

func TestNeedsQuoting(t *testing.T) {
	for _, s := range []string{"", "   ", " x", "x ", "a\nb", "a\n b", "a\n\tb", "a\tb", "a\r\nb"} {
		if !needsQuoting(s) {
			t.Errorf("want quoting for %q", s)
		}
	}
	for _, s := range []string{"clean", "no-edges", "a: b", "- x", "https://example.com/a?b=c"} {
		if needsQuoting(s) {
			t.Errorf("should not quote %q", s)
		}
	}
}

func TestNeedsQuotingCoversYAML11Tokens(t *testing.T) {
	for _, s := range []string{
		"y", "Y", "yes", "Yes", "YES", "n", "N", "no", "No", "NO",
		"on", "On", "ON", "off", "Off", "OFF",
		"1:30", "12:30:45", "-1:30", "0:00", "1:30.5",
	} {
		if !needsQuoting(s) {
			t.Errorf("want quoting for YAML-1.1 token %q: a 1.1 parser reads it as a non-string", s)
		}
	}
	for _, s := range []string{
		"nope", "yesterday", "onward", "offset", "note",
		"https://example.com/a:b", "1.2.3", "12:30:xx", "a:b", ":", "::",
	} {
		if needsQuoting(s) {
			t.Errorf("should not quote %q", s)
		}
	}
}

func TestMarshal_QuotesYAML11TokensInOutput(t *testing.T) {
	out, err := Marshal(map[string]any{"req": "yes", "dur": "1:30"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for _, want := range []string{`req: "yes"`, `dur: "1:30"`} {
		if !strings.Contains(string(out), want) {
			t.Errorf("output missing %s\ngot:\n%s", want, out)
		}
	}
}

func TestMarshal_DeterministicWithAmbiguousKeys(t *testing.T) {
	in := map[any]any{1: "int one", "1": "string one", 2: "two"}
	first, err := Marshal(in)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	for i := 0; i < 50; i++ {
		out, err := Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if string(out) != string(first) {
			t.Fatalf("output differs on run %d:\nfirst:\n%s\ngot:\n%s", i+2, first, out)
		}
	}
}

func TestMarshal_StructFieldsSurviveExactly(t *testing.T) {
	type payload struct {
		BlankFirstLine string `yaml:"blank_first_line"`
		IndentedCode   string `yaml:"indented_code"`
		TrailingSpace  string `yaml:"trailing_space"`
		Clean          string `yaml:"clean"`
		Skipped        string `yaml:"-"`
		unexported     string //nolint:unused // presence is the point: it must not be emitted
	}
	in := payload{
		BlankFirstLine: "\nafter a blank first line\nmore",
		IndentedCode:   "\n\tcode\n",
		TrailingSpace:  "trailing   ",
		Clean:          "ordinary",
		Skipped:        "must not appear",
		unexported:     "must not appear",
	}

	out, err := Marshal(map[string]any{"payload": in})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var back struct {
		Payload struct {
			BlankFirstLine string `yaml:"blank_first_line"`
			IndentedCode   string `yaml:"indented_code"`
			TrailingSpace  string `yaml:"trailing_space"`
			Clean          string `yaml:"clean"`
		} `yaml:"payload"`
	}
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("round trip: %v\n%s", err, out)
	}
	for _, c := range []struct{ name, got, want string }{
		{"blank_first_line", back.Payload.BlankFirstLine, in.BlankFirstLine},
		{"indented_code", back.Payload.IndentedCode, in.IndentedCode},
		{"trailing_space", back.Payload.TrailingSpace, in.TrailingSpace},
		{"clean", back.Payload.Clean, in.Clean},
	} {
		if c.got != c.want {
			t.Errorf("%s: got %q, want %q", c.name, c.got, c.want)
		}
	}
	if strings.Contains(string(out), "must not appear") {
		t.Errorf("a skipped or unexported field was emitted:\n%s", out)
	}
}

func TestMarshal_InvalidUTF8FallsBackToBinary(t *testing.T) {
	raw := string([]byte{0x74, 0x65, 0x78, 0x74, 0xff, 0xfe, 0x00})

	out, err := Marshal(map[string]any{"payload": raw, "other": "kept"})
	if err != nil {
		t.Fatalf("Marshal refused the document over one bad byte: %v", err)
	}
	if !strings.Contains(string(out), "other: kept") {
		t.Errorf("unrelated key lost:\n%s", out)
	}

	var back map[string]any
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("output does not parse: %v\n%s", err, out)
	}
	got, _ := back["payload"].(string)
	if got != raw {
		t.Errorf("payload did not survive: got %q, want %q", got, raw)
	}
}

func TestMarshal_QuotesTypeTokens(t *testing.T) {
	for _, tok := range []string{"=", "<<"} {
		if !needsQuoting(tok) {
			t.Errorf("%q must be quoted: a YAML-1.1 parser fails the entire document on it", tok)
		}
	}

	out, err := Marshal(map[string]any{"<<": "x", "other": "y"})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]any
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("Marshal emitted a document it cannot read back: %v\n%s", err, out)
	}
	if back["<<"] != "x" || back["other"] != "y" {
		t.Errorf("round trip lost data: %#v\n%s", back, out)
	}
}

func TestNeedsQuotingCoversUnderscoreBase60AndTimestamps(t *testing.T) {
	for _, s := range []string{"1_0:30", "1_000:30", "2001-12-14 21:59:43.10 -5", "2001-12-14"} {
		if !needsQuoting(s) {
			t.Errorf("want quoting for %q: a YAML-1.1 parser resolves it to a non-string", s)
		}
	}
	for _, s := range []string{"1_0", "not-a-date", "2001-12", "12345678", "a1:30"} {
		if needsQuoting(s) {
			t.Errorf("should not quote %q", s)
		}
	}
}

func TestNeedsQuotingCoversMultiSpaceDatesAndLeadingDotFloats(t *testing.T) {
	for _, s := range []string{
		"0000-00-00  21:59:43", "2001-13-45  21:59:43", "2001-12-14  21:59:43Z",
		"2001-12-14   21:59:43",
		".5_", ".0_", ".9_", ".671_", ".5_e+3", ".5_0", ".5",
	} {
		if !needsQuoting(s) {
			t.Errorf("want quoting for %q: a YAML-1.1 parser retypes it or refuses the document", s)
		}
	}
	for _, s := range []string{"5_", "x.5_", ".5_e3", ".e+3", "..5", "2001-12-14x21:59:43"} {
		if needsQuoting(s) {
			t.Errorf("should not quote %q", s)
		}
	}
}

func TestMarshal_FlowOnScalarStillQuotes(t *testing.T) {
	type flowed struct {
		Text string `yaml:"text,flow"`
	}
	type plain struct {
		Text string `yaml:"text"`
	}
	for _, s := range []string{"\nafter\nmore", "  deep\nsecond", "yes", "off", "1:30", "=", "<<"} {
		flowOut, err := Marshal(map[string]any{"s": flowed{Text: s}})
		if err != nil {
			t.Fatalf("Marshal(flowed %q): %v", s, err)
		}
		plainOut, err := Marshal(map[string]any{"s": plain{Text: s}})
		if err != nil {
			t.Fatalf("Marshal(plain %q): %v", s, err)
		}
		if !bytes.Equal(flowOut, plainOut) {
			t.Errorf("`,flow` changed a scalar's quoting for %q:\n flow: %s plain: %s",
				s, flowOut, plainOut)
		}
		if bytes.Contains(flowOut, []byte(": |")) {
			t.Errorf("`,flow` emitted a block scalar for %q: %s", s, flowOut)
		}
	}
}

type ptrOnlyMarshaler struct{ Body string }

func (p *ptrOnlyMarshaler) MarshalYAML() (any, error) {
	return map[string]string{"body": p.Body, "via": "ptr"}, nil
}

func TestMarshal_PointerReceiverMarshalerIsOnlyHonouredByPointer(t *testing.T) {
	t.Run("held by pointer keeps its representation", func(t *testing.T) {
		in := map[string]any{"m": &ptrOnlyMarshaler{Body: "b"}}
		ours, err := Marshal(in)
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		theirs, err := yaml.Marshal(in)
		if err != nil {
			t.Fatalf("yaml.Marshal: %v", err)
		}
		var gotOurs, gotTheirs map[string]any
		if err := yaml.Unmarshal(ours, &gotOurs); err != nil {
			t.Fatalf("our output: %v\n%s", err, ours)
		}
		if err := yaml.Unmarshal(theirs, &gotTheirs); err != nil {
			t.Fatalf("their output: %v", err)
		}
		if fmt.Sprint(gotOurs) != fmt.Sprint(gotTheirs) {
			t.Errorf("dropped the marshaler's representation\n ours: %v\ntheirs: %v", gotOurs, gotTheirs)
		}
	})

	t.Run("held by value is decomposed, not corrupted", func(t *testing.T) {
		const body = "\nafter a blank first line\nmore"
		out, err := Marshal(map[string]any{"m": ptrOnlyMarshaler{Body: body}})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var back struct {
			M struct{ Body string } `yaml:"body"`
		}
		_ = back
		var generic map[string]any
		if err := yaml.Unmarshal(out, &generic); err != nil {
			t.Fatalf("round trip: %v\n%s", err, out)
		}
		inner, ok := generic["m"].(map[string]any)
		if !ok {
			t.Fatalf("m is not a mapping: %#v\n%s", generic["m"], out)
		}
		if got, _ := inner["body"].(string); got != body {
			t.Errorf("body corrupted: got %q, want %q", got, body)
		}
	})
}

type valueMarshaler struct{ Body string }

func (v valueMarshaler) MarshalYAML() (any, error) {
	return map[string]string{"body": v.Body, "via": "value"}, nil
}

type textMarshaler struct{ Body string }

func (t textMarshaler) MarshalText() ([]byte, error) { return []byte(t.Body), nil }

func TestMarshal_SelfMarshalingValuesDoNotCorruptStrings(t *testing.T) {
	for _, body := range []string{
		"\nafter a blank first line\nmore",
		"\n\ntwo blank lines\nmore",
		"    deeply indented first line\nsecond",
		"\n",
		"plain",
	} {
		for _, c := range []struct {
			name string
			in   any
		}{
			{"pointer receiver, held by pointer", &ptrOnlyMarshaler{Body: body}},
			{"value receiver, held by value", valueMarshaler{Body: body}},
			{"value receiver, held by pointer", &valueMarshaler{Body: body}},
		} {
			t.Run(fmt.Sprintf("%s/%q", c.name, body), func(t *testing.T) {
				out, err := Marshal(map[string]any{"events": []any{map[string]any{"m": c.in}}})
				if err != nil {
					t.Fatalf("Marshal: %v", err)
				}
				var generic map[string]any
				if err := yaml.Unmarshal(out, &generic); err != nil {
					t.Fatalf("round trip: %v\n%s", err, out)
				}
				events, _ := generic["events"].([]any)
				if len(events) != 1 {
					t.Fatalf("events lost: %#v\n%s", generic, out)
				}
				ev, _ := events[0].(map[string]any)
				inner, ok := ev["m"].(map[string]any)
				if !ok {
					t.Fatalf("m is not a mapping: %#v\n%s", ev["m"], out)
				}
				if got, _ := inner["body"].(string); got != body {
					t.Errorf("body corrupted: got %q, want %q\n%s", got, body, out)
				}
			})
		}
	}

	t.Run("TextMarshaler output is quoted, not blocked", func(t *testing.T) {
		const body = "\nleading newline"
		out, err := Marshal(map[string]any{"t": textMarshaler{Body: body}})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		var back map[string]any
		if err := yaml.Unmarshal(out, &back); err != nil {
			t.Fatalf("round trip: %v\n%s", err, out)
		}
		if got, _ := back["t"].(string); got != body {
			t.Errorf("text corrupted: got %q, want %q\n%s", got, body, out)
		}
	})
}

func TestMarshal_YAMLNodeFieldsAreUsedAsIs(t *testing.T) {
	var held yaml.Node
	if err := yaml.Unmarshal([]byte("hello"), &held); err != nil {
		t.Fatalf("building the node: %v", err)
	}
	type holder struct {
		N     yaml.Node            `yaml:"n"`
		P     *yaml.Node           `yaml:"p"`
		Extra map[string]yaml.Node `yaml:",inline"`
	}
	scalar := held.Content[0]
	in := holder{N: *scalar, P: scalar, Extra: map[string]yaml.Node{"e": *scalar}}

	ours, err := Marshal(map[string]any{"h": in})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	theirs, err := yaml.Marshal(map[string]any{"h": in})
	if err != nil {
		t.Fatalf("yaml.Marshal: %v", err)
	}
	var gotOurs, gotTheirs map[string]any
	if err := yaml.Unmarshal(ours, &gotOurs); err != nil {
		t.Fatalf("our output: %v\n%s", err, ours)
	}
	if err := yaml.Unmarshal(theirs, &gotTheirs); err != nil {
		t.Fatalf("their output: %v\n%s", err, theirs)
	}
	if fmt.Sprint(gotOurs) != fmt.Sprint(gotTheirs) {
		t.Errorf("yaml.Node not treated as authoritative\n ours: %v\n%s\ntheirs: %v\n%s",
			gotOurs, ours, gotTheirs, theirs)
	}
}

func TestMarshal_DoesNotMutateCallerNodes(t *testing.T) {
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte("hello"), &doc); err != nil {
		t.Fatalf("building the node: %v", err)
	}
	scalar := doc.Content[0]
	type holder struct {
		P *yaml.Node `yaml:"p,flow"`
		V yaml.Node  `yaml:"v,flow"`
	}
	before := *scalar
	if _, err := Marshal(map[string]any{"h": holder{P: scalar, V: *scalar}}); err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if scalar.Style != before.Style {
		t.Errorf("Marshal wrote to the caller's node: Style %v -> %v", before.Style, scalar.Style)
	}
	if scalar.Tag != before.Tag || scalar.Value != before.Value || scalar.Kind != before.Kind {
		t.Errorf("Marshal wrote to the caller's node: %+v -> %+v", before, *scalar)
	}
}

func TestMarshal_SelfMarshalingTypesKeepTheirForm(t *testing.T) {
	when := time.Date(2026, 8, 6, 12, 30, 45, 0, time.UTC)
	out, err := Marshal(map[string]any{"at": when})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	var back map[string]any
	if err := yaml.Unmarshal(out, &back); err != nil {
		t.Fatalf("round trip: %v\n%s", err, out)
	}
	got, ok := back["at"].(time.Time)
	if !ok {
		t.Fatalf("time.Time did not survive as a timestamp: %T %#v\n%s", back["at"], back["at"], out)
	}
	if !got.Equal(when) {
		t.Errorf("got %v, want %v", got, when)
	}
}

func TestMarshal_StructTagBehaviours(t *testing.T) {
	type inner struct {
		Deep string `yaml:"deep"`
	}
	type outer struct {
		Named     string `yaml:"named"`
		Defaulted string
		OmitMe    string `yaml:"omitme,omitempty"`
		KeepMe    string `yaml:"keepme,omitempty"`
		Inlined   inner  `yaml:",inline"`
		Skipped   string `yaml:"-"`
	}
	out, err := Marshal(map[string]any{"s": outer{
		Named: "named-value", Defaulted: "d", KeepMe: "k",
		Inlined: inner{Deep: "i"}, Skipped: "no",
	}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	text := string(out)
	for _, want := range []string{"named: named-value", "defaulted: d", "keepme: k", "deep: i"} {
		if !strings.Contains(text, want) {
			t.Errorf("missing %q:\n%s", want, text)
		}
	}
	for _, unwanted := range []string{"omitme", "skipped"} {
		if strings.Contains(text, unwanted) {
			t.Errorf("emitted %q, which should be omitted:\n%s", unwanted, text)
		}
	}
	if strings.Contains(text, "inlined:") {
		t.Errorf("inline produced a nested key instead of hoisting:\n%s", text)
	}
}

type parityZeroer struct{ V string }

func (z parityZeroer) IsZero() bool { return z.V == "skip" }

type parityNested struct {
	A string `yaml:"a,omitempty"`
	B string `yaml:"b,omitempty"`
}

type parityStruct struct {
	EmptySlice []string          `yaml:"emptyslice,omitempty"`
	FullSlice  []string          `yaml:"fullslice,omitempty"`
	EmptyMap   map[string]string `yaml:"emptymap,omitempty"`
	EmptyStr   string            `yaml:"emptystr,omitempty"`
	ZeroInt    int               `yaml:"zeroint,omitempty"`
	ZeroBool   bool              `yaml:"zerobool,omitempty"`
	NilPtr     *parityNested     `yaml:"nilptr,omitempty"`
	ZeroArray  [0]int            `yaml:"zeroarray,omitempty"`
	SkipZeroer parityZeroer      `yaml:"skipzeroer,omitempty"`
	KeepZeroer parityZeroer      `yaml:"keepzeroer,omitempty"`
	ZeroNested parityNested      `yaml:"zeronested,omitempty"`
	Flowed     []int             `yaml:"flowed,flow"`
	Keep       string            `yaml:"keep"`
}

type parityInlineStruct struct {
	Inner parityNested `yaml:",inline"`
	Keep  string       `yaml:"keep"`
}

type parityInlinePtr struct {
	Inner *parityNested `yaml:",inline"`
	Keep  string        `yaml:"keep"`
}

type parityInlineMap struct {
	Extra map[string]string `yaml:",inline"`
	Keep  string            `yaml:"keep"`
}

type parityAlwaysEmitted struct {
	A string `yaml:"a"`
	B string `yaml:"b"`
}

type parityInlineOmitEmpty struct {
	Inner parityAlwaysEmitted `yaml:",inline,omitempty"`
	Keep  string              `yaml:"keep"`
}

type parityHidden struct {
	Shown string            `yaml:"shown"`
	M     map[string]string `yaml:"m,omitempty"`
	S     []string          `yaml:"s,omitempty"`
}

type parityEmbeddedHidden struct {
	parityHidden `yaml:",inline"`
	Keep         string `yaml:"keep"`
}

type parityEmbeddedHiddenPtr struct {
	*parityHidden `yaml:",inline"`
	Keep          string `yaml:"keep"`
}

func TestMarshal_StructTagParityWithYAMLv3(t *testing.T) {
	for _, c := range []struct {
		name string
		val  any
	}{
		{"omitempty and flow", parityStruct{
			EmptySlice: []string{},
			FullSlice:  []string{"x"},
			EmptyMap:   map[string]string{},
			SkipZeroer: parityZeroer{V: "skip"},
			KeepZeroer: parityZeroer{V: "keep"},
			Flowed:     []int{1, 2},
			Keep:       "k",
		}},
		{"inline struct", parityInlineStruct{Inner: parityNested{A: "deep"}, Keep: "k"}},
		{"inline pointer to struct", parityInlinePtr{Inner: &parityNested{A: "deep"}, Keep: "k"}},
		{"inline nil pointer", parityInlinePtr{Keep: "k"}},
		{"inline map", parityInlineMap{Extra: map[string]string{"extra": "x"}, Keep: "k"}},
		{"inline empty map", parityInlineMap{Extra: map[string]string{}, Keep: "k"}},
		{"inline,omitempty on a zero struct", parityInlineOmitEmpty{Keep: "k"}},
		{"inline,omitempty on a non-zero struct",
			parityInlineOmitEmpty{Inner: parityAlwaysEmitted{A: "deep"}, Keep: "k"}},
		{"embedded unexported type inlined",
			parityEmbeddedHidden{parityHidden{Shown: "s"}, "k"}},
		{"embedded unexported type inlined, with containers",
			parityEmbeddedHidden{parityHidden{
				Shown: "s", M: map[string]string{"mk": "mv"}, S: []string{"s1"},
			}, "k"}},
		{"embedded unexported pointer inlined",
			parityEmbeddedHiddenPtr{&parityHidden{Shown: "s"}, "k"}},
		{"embedded unexported nil pointer inlined", parityEmbeddedHiddenPtr{nil, "k"}},
	} {
		t.Run(c.name, func(t *testing.T) {
			ours, err := Marshal(map[string]any{"s": c.val})
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			theirs, err := yaml.Marshal(map[string]any{"s": c.val})
			if err != nil {
				t.Fatalf("yaml.Marshal: %v", err)
			}

			var gotOurs, gotTheirs map[string]any
			if err := yaml.Unmarshal(ours, &gotOurs); err != nil {
				t.Fatalf("our output does not parse: %v\n%s", err, ours)
			}
			if err := yaml.Unmarshal(theirs, &gotTheirs); err != nil {
				t.Fatalf("their output does not parse: %v\n%s", err, theirs)
			}
			if !reflect.DeepEqual(gotOurs, gotTheirs) {
				t.Errorf("diverges from yaml.Marshal\n ours: %#v\ntheirs: %#v\n--- our bytes:\n%s--- their bytes:\n%s",
					gotOurs, gotTheirs, ours, theirs)
			}
		})
	}
}

type collidingInlineStruct struct {
	Inner parityCollide `yaml:",inline"`
	Keep  string        `yaml:"keep"`
}

type parityCollide struct {
	Keep string `yaml:"keep"`
}

type collidingInlineMap struct {
	Extra map[string]string `yaml:",inline"`
	Keep  string            `yaml:"keep"`
}

type hiddenInlineMap map[string]string

type embeddedHiddenMap struct {
	hiddenInlineMap `yaml:",inline"`
	Keep            string `yaml:"keep"`
}

type embeddedHiddenNoInline struct {
	parityHidden
	Keep string `yaml:"keep"`
}

func TestMarshal_RefusesShapesThatWouldLoseData(t *testing.T) {
	t.Run("inlined struct key colliding with a sibling field", func(t *testing.T) {
		_, err := Marshal(map[string]any{"s": collidingInlineStruct{
			Inner: parityCollide{Keep: "inner"}, Keep: "outer",
		}})
		if err == nil {
			t.Fatal("emitted a duplicate key instead of refusing; one value is lost on reload")
		}
		if !strings.Contains(err.Error(), "duplicate key") {
			t.Errorf("error does not name the problem: %v", err)
		}
	})

	t.Run("inlined map key colliding with a sibling field", func(t *testing.T) {
		_, err := Marshal(map[string]any{"s": collidingInlineMap{
			Extra: map[string]string{"keep": "inner"}, Keep: "outer",
		}})
		if err == nil {
			t.Fatal("emitted a duplicate key instead of refusing; one value is lost on reload")
		}
	})

	t.Run("embedded unexported map type cannot be inlined", func(t *testing.T) {
		_, err := Marshal(map[string]any{"s": embeddedHiddenMap{hiddenInlineMap{"a": "1"}, "k"}})
		if err == nil {
			t.Fatal("expected an error for an unexported embedded map type")
		}
		if !strings.Contains(err.Error(), "hiddenInlineMap") {
			t.Errorf("error does not name the field: %v", err)
		}
	})

	t.Run("inline map must have string keys", func(t *testing.T) {
		type intKeyed struct {
			One   string         `yaml:"1"`
			Extra map[int]string `yaml:",inline"`
		}
		out, err := Marshal(map[string]any{"s": intKeyed{One: "str", Extra: map[int]string{1: "int"}}})
		if err == nil {
			t.Fatalf("accepted a non-string-keyed inline map:\n%s", out)
		}
		if !strings.Contains(err.Error(), "string keys") {
			t.Errorf("error does not name the problem: %v", err)
		}
	})

	t.Run("a named string key type is refused too", func(t *testing.T) {
		type keyName string
		type namedKeyed struct {
			Extra map[keyName]string `yaml:",inline"`
			Keep  string             `yaml:"keep"`
		}
		if _, err := Marshal(map[string]any{"s": namedKeyed{
			Extra: map[keyName]string{"a": "1"}, Keep: "k",
		}}); err == nil {
			t.Error("accepted a named string key type, which yaml.v3 refuses")
		}
	})

	t.Run("only one inline map per struct", func(t *testing.T) {
		type twoMaps struct {
			A map[string]string `yaml:",inline"`
			B map[string]string `yaml:",inline"`
		}
		if _, err := Marshal(map[string]any{"s": twoMaps{
			A: map[string]string{"a": "1"}, B: map[string]string{"b": "2"},
		}}); err == nil {
			t.Error("accepted two ,inline maps in one struct")
		}
	})

	t.Run("embedded unexported type without inline is skipped, not fatal", func(t *testing.T) {
		out, err := Marshal(map[string]any{"s": embeddedHiddenNoInline{parityHidden{Shown: "s"}, "k"}})
		if err != nil {
			t.Fatalf("Marshal: %v", err)
		}
		if !strings.Contains(string(out), "keep: k") {
			t.Errorf("lost the sibling field:\n%s", out)
		}
	})
}

func TestMarshal_FlowStyleIsEmittedInline(t *testing.T) {
	out, err := Marshal(map[string]any{"s": parityStruct{Flowed: []int{1, 2}, Keep: "k"}})
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}
	if !strings.Contains(string(out), "flowed: [1, 2]") {
		t.Errorf("flow style not applied:\n%s", out)
	}
}
