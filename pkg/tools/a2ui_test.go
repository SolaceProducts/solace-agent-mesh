// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tools

import (
	"encoding/json"
	"testing"
)

func TestGenerateA2UISurface_SingleQuestion(t *testing.T) {
	args := map[string]any{
		"message": "I need your help choosing.",
		"questions": []any{
			map[string]any{
				"question":    "Which option?",
				"header":      "Choice",
				"multiSelect": false,
				"options": []any{
					map[string]any{"label": "Option A", "description": "First option"},
					map[string]any{"label": "Option B", "description": "Second option"},
				},
			},
		},
	}

	surface, err := generateA2UISurface("test-req-1", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	if parsed["surfaceId"] != "test-req-1" {
		t.Errorf("surfaceId = %v, want test-req-1", parsed["surfaceId"])
	}
	if parsed["catalogId"] != "https://a2ui.org/specification/v0_9/basic_catalog.json" {
		t.Errorf("unexpected catalogId: %v", parsed["catalogId"])
	}

	components, ok := parsed["components"].([]any)
	if !ok {
		t.Fatal("components is not an array")
	}
	if len(components) == 0 {
		t.Fatal("no components generated")
	}

	var picker map[string]any
	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "ChoicePicker" {
			picker = cm
			break
		}
	}
	if picker == nil {
		t.Fatal("no ChoicePicker component found")
	}

	if picker["variant"] != "mutuallyExclusive" {
		t.Errorf("variant = %v, want mutuallyExclusive", picker["variant"])
	}

	options, _ := picker["options"].([]any)
	if len(options) != 3 {
		t.Errorf("expected 3 options (2 + Other), got %d", len(options))
	}
	lastOpt := options[len(options)-1].(map[string]any)
	lastLabel := lastOpt["label"].(map[string]any)
	if lastLabel["literalString"] != "Other" {
		t.Errorf("last option label = %v, want Other", lastLabel["literalString"])
	}

	dataModel, _ := parsed["dataModel"].(map[string]any)
	answers, _ := dataModel["answers"].(map[string]any)
	if _, ok := answers["q0"]; !ok {
		t.Error("data model missing answers.q0")
	}
	otherText, _ := dataModel["other_text"].(map[string]any)
	if _, ok := otherText["q0"]; !ok {
		t.Error("data model missing other_text.q0")
	}

	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "Tabs" {
			t.Error("single question should not produce Tabs component")
		}
	}
}

func TestGenerateA2UISurface_MultiSelect(t *testing.T) {
	args := map[string]any{
		"questions": []any{
			map[string]any{
				"question":    "Select features:",
				"header":      "Features",
				"multiSelect": true,
				"options": []any{
					map[string]any{"label": "Auth", "description": "Authentication"},
					map[string]any{"label": "Cache", "description": "Caching"},
				},
			},
		},
	}

	surface, err := generateA2UISurface("test-req-2", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	components := parsed["components"].([]any)
	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "ChoicePicker" {
			if cm["variant"] != "multipleSelection" {
				t.Errorf("variant = %v, want multipleSelection", cm["variant"])
			}
			break
		}
	}

	dataModel := parsed["dataModel"].(map[string]any)
	answers := dataModel["answers"].(map[string]any)
	if _, ok := answers["q0"].([]any); !ok {
		t.Error("expected answers.q0 to be an array for multiSelect")
	}
}

func TestGenerateA2UISurface_MultipleQuestions(t *testing.T) {
	args := map[string]any{
		"message": "Configuration needed.",
		"questions": []any{
			map[string]any{
				"question":    "First question?",
				"header":      "Q1",
				"multiSelect": false,
				"options": []any{
					map[string]any{"label": "A", "description": "a"},
					map[string]any{"label": "B", "description": "b"},
				},
			},
			map[string]any{
				"question":    "Second question?",
				"header":      "Q2",
				"multiSelect": true,
				"options": []any{
					map[string]any{"label": "X", "description": "x"},
					map[string]any{"label": "Y", "description": "y"},
				},
			},
		},
	}

	surface, err := generateA2UISurface("test-req-3", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	components := parsed["components"].([]any)

	compByID := make(map[string]map[string]any)
	for _, c := range components {
		cm := c.(map[string]any)
		if id, ok := cm["id"].(string); ok {
			compByID[id] = cm
		}
	}

	pickerCount := 0
	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "ChoicePicker" {
			pickerCount++
		}
	}
	if pickerCount != 2 {
		t.Errorf("expected 2 ChoicePickers, got %d", pickerCount)
	}

	tabs, ok := compByID["question-tabs"]
	if !ok {
		t.Fatal("missing question-tabs component")
	}
	if tabs["component"] != "Tabs" {
		t.Errorf("question-tabs component = %v, want Tabs", tabs["component"])
	}
	if tabs["x-auto-advance"] != true {
		t.Errorf("x-auto-advance = %v, want true", tabs["x-auto-advance"])
	}

	tabItems, ok := tabs["tabItems"].([]any)
	if !ok {
		t.Fatal("tabItems is not an array")
	}
	if len(tabItems) != 2 {
		t.Fatalf("expected 2 tabItems, got %d", len(tabItems))
	}

	tab0 := tabItems[0].(map[string]any)
	tab0Title := tab0["title"].(map[string]any)
	if tab0Title["literalString"] != "Q1" {
		t.Errorf("tab 0 title = %v, want Q1", tab0Title["literalString"])
	}
	if tab0["child"] != "q0-panel" {
		t.Errorf("tab 0 child = %v, want q0-panel", tab0["child"])
	}

	tab1 := tabItems[1].(map[string]any)
	tab1Title := tab1["title"].(map[string]any)
	if tab1Title["literalString"] != "Q2" {
		t.Errorf("tab 1 title = %v, want Q2", tab1Title["literalString"])
	}
	if tab1["child"] != "q1-panel" {
		t.Errorf("tab 1 child = %v, want q1-panel", tab1["child"])
	}

	q0Panel, ok := compByID["q0-panel"]
	if !ok {
		t.Fatal("missing q0-panel component")
	}
	q0Children := q0Panel["children"].(map[string]any)["explicitList"].([]any)
	if len(q0Children) != 3 || q0Children[0] != "q0-label" || q0Children[1] != "q0-picker" || q0Children[2] != "q0-other" {
		t.Errorf("q0-panel children = %v, want [q0-label q0-picker q0-other]", q0Children)
	}

	mainCol, ok := compByID["main-col"]
	if !ok {
		t.Fatal("missing main-col component")
	}
	mainChildren := mainCol["children"].(map[string]any)["explicitList"].([]any)
	if len(mainChildren) != 3 || mainChildren[0] != "msg" || mainChildren[1] != "question-tabs" || mainChildren[2] != "actions" {
		t.Errorf("main-col children = %v, want [msg question-tabs actions]", mainChildren)
	}

	dataModel := parsed["dataModel"].(map[string]any)
	answers := dataModel["answers"].(map[string]any)
	if _, ok := answers["q0"]; !ok {
		t.Error("missing answers.q0")
	}
	if _, ok := answers["q1"]; !ok {
		t.Error("missing answers.q1")
	}
}

func TestGenerateA2UISurface_ThreeQuestions(t *testing.T) {
	args := map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Q1?", "header": "First", "multiSelect": false,
				"options": []any{map[string]any{"label": "A", "description": "a"}},
			},
			map[string]any{
				"question": "Q2?", "header": "Second", "multiSelect": false,
				"options": []any{map[string]any{"label": "B", "description": "b"}},
			},
			map[string]any{
				"question": "Q3?", "header": "", "multiSelect": true,
				"options": []any{map[string]any{"label": "C", "description": "c"}},
			},
		},
	}

	surface, err := generateA2UISurface("test-req-4", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	components := parsed["components"].([]any)

	var tabs map[string]any
	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "Tabs" {
			tabs = cm
			break
		}
	}
	if tabs == nil {
		t.Fatal("no Tabs component found for 3 questions")
	}

	tabItems := tabs["tabItems"].([]any)
	if len(tabItems) != 3 {
		t.Fatalf("expected 3 tabItems, got %d", len(tabItems))
	}

	tab2 := tabItems[2].(map[string]any)
	tab2Title := tab2["title"].(map[string]any)
	if tab2Title["literalString"] != "Q3" {
		t.Errorf("tab 2 title = %v, want Q3 (fallback)", tab2Title["literalString"])
	}

	dataModel := parsed["dataModel"].(map[string]any)
	answers := dataModel["answers"].(map[string]any)
	for _, key := range []string{"q0", "q1", "q2"} {
		if _, ok := answers[key]; !ok {
			t.Errorf("missing answers.%s", key)
		}
	}
}

func TestGenerateA2UISurface_TextOnlyQuestion(t *testing.T) {
	args := map[string]any{
		"message": "I need some details.",
		"questions": []any{
			map[string]any{
				"question":    "What is the name of your database?",
				"header":      "Database",
				"placeholder": "e.g., PostgreSQL on AWS RDS",
			},
		},
	}

	surface, err := generateA2UISurface("test-req-text", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	components := parsed["components"].([]any)

	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "ChoicePicker" {
			t.Error("text-only question should not produce a ChoicePicker")
		}
	}

	var textField map[string]any
	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "TextField" {
			textField = cm
			break
		}
	}
	if textField == nil {
		t.Fatal("no TextField component found")
	}
	if textField["multiline"] != true {
		t.Errorf("multiline = %v, want true", textField["multiline"])
	}

	placeholder := textField["placeholder"].(map[string]any)
	if placeholder["literalString"] != "e.g., PostgreSQL on AWS RDS" {
		t.Errorf("placeholder = %v, want 'e.g., PostgreSQL on AWS RDS'", placeholder["literalString"])
	}

	valuePath := textField["value"].(map[string]any)["path"]
	if valuePath != "/answers/q0" {
		t.Errorf("value path = %v, want /answers/q0", valuePath)
	}

	dataModel := parsed["dataModel"].(map[string]any)
	answers := dataModel["answers"].(map[string]any)
	if _, ok := answers["q0"]; !ok {
		t.Error("data model missing answers.q0")
	}
	otherText := dataModel["other_text"].(map[string]any)
	if _, ok := otherText["q0"]; ok {
		t.Error("text-only question should not have other_text.q0")
	}

	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "Tabs" {
			t.Error("single question should not produce Tabs component")
		}
	}
}

func TestGenerateA2UISurface_TextOnlyDefaultPlaceholder(t *testing.T) {
	args := map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Describe your use case?",
				"header":   "UseCase",
			},
		},
	}

	surface, err := generateA2UISurface("test-req-default", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	components := parsed["components"].([]any)

	var textField map[string]any
	for _, c := range components {
		cm := c.(map[string]any)
		if cm["component"] == "TextField" {
			textField = cm
			break
		}
	}
	if textField == nil {
		t.Fatal("no TextField component found")
	}

	placeholder := textField["placeholder"].(map[string]any)
	if placeholder["literalString"] != "Type your answer..." {
		t.Errorf("default placeholder = %v, want 'Type your answer...'", placeholder["literalString"])
	}
}

func TestGenerateA2UISurface_MixedQuestionsWithTabs(t *testing.T) {
	args := map[string]any{
		"questions": []any{
			map[string]any{
				"question":    "How often should it run?",
				"header":      "Schedule",
				"multiSelect": false,
				"options": []any{
					map[string]any{"label": "Real-time", "description": "Continuous"},
					map[string]any{"label": "Hourly", "description": "Every hour"},
				},
			},
			map[string]any{
				"question":    "What is the API endpoint URL?",
				"header":      "Endpoint",
				"placeholder": "e.g., https://api.example.com/v1",
			},
		},
	}

	surface, err := generateA2UISurface("test-req-mixed", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}

	components := parsed["components"].([]any)
	compByID := make(map[string]map[string]any)
	for _, c := range components {
		cm := c.(map[string]any)
		if id, ok := cm["id"].(string); ok {
			compByID[id] = cm
		}
	}

	tabs, ok := compByID["question-tabs"]
	if !ok {
		t.Fatal("missing question-tabs for mixed questions")
	}
	tabItems := tabs["tabItems"].([]any)
	if len(tabItems) != 2 {
		t.Fatalf("expected 2 tabItems, got %d", len(tabItems))
	}

	q0Panel := compByID["q0-panel"]
	q0Children := q0Panel["children"].(map[string]any)["explicitList"].([]any)
	if len(q0Children) != 3 {
		t.Errorf("q0-panel children count = %d, want 3", len(q0Children))
	}

	q1Panel := compByID["q1-panel"]
	q1Children := q1Panel["children"].(map[string]any)["explicitList"].([]any)
	if len(q1Children) != 2 {
		t.Errorf("q1-panel children count = %d, want 2", len(q1Children))
	}
	if q1Children[1] != "q1-text" {
		t.Errorf("q1-panel child[1] = %v, want q1-text", q1Children[1])
	}

	dataModel := parsed["dataModel"].(map[string]any)
	otherText := dataModel["other_text"].(map[string]any)
	if _, ok := otherText["q0"]; !ok {
		t.Error("q0 (with options) should have other_text entry")
	}
	if _, ok := otherText["q1"]; ok {
		t.Error("q1 (text-only) should NOT have other_text entry")
	}
}

func TestExtractAnswers_TextOnly(t *testing.T) {
	questions := []any{
		map[string]any{"question": "What is the database name?"},
	}
	data := map[string]any{
		"answers":    map[string]any{"q0": "my-postgres-db"},
		"other_text": map[string]any{},
	}

	result := extractAnswers(questions, data)
	if result["What is the database name?"] != "my-postgres-db" {
		t.Errorf("got %q, want my-postgres-db", result["What is the database name?"])
	}
}

func TestExtractAnswers_SingleSelect(t *testing.T) {
	questions := []any{
		map[string]any{"question": "Which volume?"},
	}
	data := map[string]any{
		"answers":    map[string]any{"q0": "vol-abc"},
		"other_text": map[string]any{"q0": ""},
	}

	result := extractAnswers(questions, data)
	if result["Which volume?"] != "vol-abc" {
		t.Errorf("got %q, want vol-abc", result["Which volume?"])
	}
}

func TestExtractAnswers_OtherOption(t *testing.T) {
	questions := []any{
		map[string]any{"question": "Which volume?"},
	}
	data := map[string]any{
		"answers":    map[string]any{"q0": "__other__"},
		"other_text": map[string]any{"q0": "my-custom-volume"},
	}

	result := extractAnswers(questions, data)
	if result["Which volume?"] != "my-custom-volume" {
		t.Errorf("got %q, want my-custom-volume", result["Which volume?"])
	}
}

func otherOptionValueInSurface(t *testing.T, surface json.RawMessage) string {
	t.Helper()
	var parsed struct {
		Components []map[string]any `json:"components"`
	}
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("surface is not valid JSON: %v", err)
	}
	for _, cm := range parsed.Components {
		if cm["component"] != "ChoicePicker" {
			continue
		}
		opts, _ := cm["options"].([]any)
		for _, o := range opts {
			om, _ := o.(map[string]any)
			label, _ := om["label"].(map[string]any)
			if s, _ := label["literalString"].(string); s != "Other" {
				continue
			}
			value, _ := om["value"].(string)
			if value == "" {
				t.Fatalf(`the "Other" option carries no value: %#v`, om)
			}
			return value
		}
	}
	t.Fatal(`no "Other" option in the generated surface`)
	return ""
}

func TestOtherSentinelRoundTripsFromSurfaceToAnswers(t *testing.T) {
	const questionText = "Which auth method?"
	const typedAnswer = "SAML"

	newQuestions := func(multiSelect bool) []any {
		return []any{
			map[string]any{
				"question":    questionText,
				"header":      "Auth",
				"multiSelect": multiSelect,
				"options": []any{
					map[string]any{"label": "OAuth", "description": "OAuth 2.0"},
					map[string]any{"label": "API key", "description": "Static API key"},
				},
			},
		}
	}

	t.Run("single select", func(t *testing.T) {
		questions := newQuestions(false)
		surface, err := generateA2UISurface("req-1", map[string]any{"questions": questions})
		if err != nil {
			t.Fatalf("generateA2UISurface: %v", err)
		}
		emitted := otherOptionValueInSurface(t, surface)
		answers := extractAnswers(questions, map[string]any{
			"answers":    map[string]any{"q0": emitted},
			"other_text": map[string]any{"q0": typedAnswer},
		})
		if got := answers[questionText]; got != typedAnswer {
			t.Errorf("answer = %q, want %q: the surface emitted %q but extractAnswers does not read it",
				got, typedAnswer, emitted)
		}
	})

	t.Run("multi select", func(t *testing.T) {
		questions := newQuestions(true)
		surface, err := generateA2UISurface("req-1", map[string]any{"questions": questions})
		if err != nil {
			t.Fatalf("generateA2UISurface: %v", err)
		}
		emitted := otherOptionValueInSurface(t, surface)
		answers := extractAnswers(questions, map[string]any{
			"answers":    map[string]any{"q0": []any{"OAuth", emitted}},
			"other_text": map[string]any{"q0": typedAnswer},
		})
		if got := answers[questionText]; got != "OAuth, "+typedAnswer {
			t.Errorf("answer = %q, want %q: the surface emitted %q but extractAnswers does not read it",
				got, "OAuth, "+typedAnswer, emitted)
		}
	})
}

func TestExtractAnswers_MultiSelect(t *testing.T) {
	questions := []any{
		map[string]any{"question": "Select features:"},
	}
	data := map[string]any{
		"answers":    map[string]any{"q0": []any{"Auth", "Cache"}},
		"other_text": map[string]any{"q0": ""},
	}

	result := extractAnswers(questions, data)
	if result["Select features:"] != "Auth, Cache" {
		t.Errorf("got %q, want 'Auth, Cache'", result["Select features:"])
	}
}

func TestExtractAnswers_MultiSelectWithOther(t *testing.T) {
	questions := []any{
		map[string]any{"question": "Select features:"},
	}
	data := map[string]any{
		"answers":    map[string]any{"q0": []any{"Auth", "__other__"}},
		"other_text": map[string]any{"q0": "Custom Feature"},
	}

	result := extractAnswers(questions, data)
	if result["Select features:"] != "Auth, Custom Feature" {
		t.Errorf("got %q, want 'Auth, Custom Feature'", result["Select features:"])
	}
}

func TestGenerateA2UISurface_PassesComponentNameThrough(t *testing.T) {
	args := map[string]any{
		"message":        "Provide your connector credentials.",
		"component_name": "salesforce_connector",
		"questions": []any{
			map[string]any{
				"question":    "API key?",
				"header":      "Key",
				"multiSelect": false,
				"options":     []any{},
			},
		},
	}
	surface, err := generateA2UISurface("req-cn", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	dm, _ := parsed["dataModel"].(map[string]any)
	if dm["component_name"] != "salesforce_connector" {
		t.Errorf("dataModel.component_name = %v, want salesforce_connector", dm["component_name"])
	}
}

func TestGenerateA2UISurface_SkipsNonMapOption(t *testing.T) {
	args := map[string]any{
		"message": "Pick one.",
		"questions": []any{
			map[string]any{
				"question":    "Choose",
				"header":      "C",
				"multiSelect": false,
				"options": []any{
					"not-a-map",
					map[string]any{"label": "Valid", "description": "desc"},
				},
			},
		},
	}
	if _, err := generateA2UISurface("req-skip", args); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestGenerateA2UISurface_SingleQuestionOmitsHeaderCaption(t *testing.T) {
	args := map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Which joke style?",
				"header":   "Style",
				"options": []any{
					map[string]any{"label": "Puns", "description": "Wordplay"},
					map[string]any{"label": "Witty", "description": "Dry humor"},
				},
			},
		},
	}

	surface, err := generateA2UISurface("req-no-header", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	components, ok := parsed["components"].([]any)
	if !ok {
		t.Fatal("components is not an array")
	}

	for _, c := range components {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cm["id"] == "q0-header" {
			t.Errorf("single-question surface still emits the q0-header caption: %v", cm)
		}
		if cm["component"] == "Text" && litText(t, cm["text"]) == "Style" {
			t.Errorf("header rendered as a Text component: %v", cm)
		}
	}

	var picker map[string]any
	for _, c := range components {
		if cm, ok := c.(map[string]any); ok && cm["id"] == "q0-picker" {
			picker = cm
			break
		}
	}
	if picker == nil {
		t.Fatal("no q0-picker component in the surface")
	}
	if got := litText(t, picker["label"]); got != "Style" {
		t.Errorf("ChoicePicker label = %q, want %q — the header lost its only home", got, "Style")
	}
}

func TestGenerateA2UISurface_SingleTextOnlyQuestionDropsHeader(t *testing.T) {
	args := map[string]any{
		"questions": []any{
			map[string]any{"question": "Host URI?", "header": "Host", "placeholder": "postgres://…"},
		},
	}

	surface, err := generateA2UISurface("req-text-only-header", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	components, _ := parsed["components"].([]any)

	for _, c := range components {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if cm["component"] == "ChoicePicker" {
			t.Errorf("a text-only question produced a ChoicePicker: %v", cm)
		}
		if lbl, present := cm["label"]; present && litText(t, lbl) == "Host" {
			t.Errorf("header rendered on a text-only card: %v", cm)
		}
	}
}

func TestGenerateA2UISurface_DropsLabellessOptionKeepingPicker(t *testing.T) {
	surface, err := generateA2UISurface("req-blank-label", map[string]any{
		"questions": []any{
			map[string]any{
				"question": "Pick?",
				"header":   "Pick",
				"options": []any{
					map[string]any{"label": "", "description": "d"},
					map[string]any{"label": "A", "description": "d"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	components, _ := parsed["components"].([]any)

	pickers := 0
	var labels []string
	for _, c := range components {
		cm, ok := c.(map[string]any)
		if !ok || cm["component"] != "ChoicePicker" {
			continue
		}
		pickers++
		opts, _ := cm["options"].([]any)
		for _, o := range opts {
			om, _ := o.(map[string]any)
			if label := litText(t, om["label"]); label != "Other" {
				labels = append(labels, label)
			}
		}
	}
	if pickers != 1 {
		t.Fatalf("ChoicePicker count = %d, want 1: dropping the blank row must not lose the picker", pickers)
	}
	if len(labels) != 1 || labels[0] != "A" {
		t.Errorf("option labels = %#v, want exactly [A]: a blank selectable row reached the user", labels)
	}
}

func TestGenerateA2UISurface_CoercesOptionShapes(t *testing.T) {
	cases := []struct {
		name    string
		options any
	}{
		{name: "single object", options: map[string]any{"label": "A", "description": "first"}},
		{name: "json string array", options: `[{"label":"A","description":"first"}]`},
		{name: "json string object", options: `{"label":"A","description":"first"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			surface, err := generateA2UISurface("req-opts", map[string]any{
				"questions": []any{
					map[string]any{"question": "Pick?", "header": "Pick", "options": tc.options},
				},
			})
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			var parsed map[string]any
			if err := json.Unmarshal(surface, &parsed); err != nil {
				t.Fatalf("invalid JSON: %v", err)
			}
			components, _ := parsed["components"].([]any)

			var labels []string
			for _, c := range components {
				cm, ok := c.(map[string]any)
				if !ok || cm["component"] != "ChoicePicker" {
					continue
				}
				opts, _ := cm["options"].([]any)
				for _, o := range opts {
					om, _ := o.(map[string]any)
					labels = append(labels, litText(t, om["label"]))
				}
			}
			if len(labels) == 0 {
				t.Fatal("no ChoicePicker drawn; the options were discarded and this became a text box")
			}
			if labels[0] != "A" {
				t.Errorf("first option label = %q, want %q", labels[0], "A")
			}
		})
	}
}

func TestGenerateA2UISurface_TabTitlesNumberByRenderedPosition(t *testing.T) {
	surface, err := generateA2UISurface("req-tabnum", map[string]any{
		"questions": []any{
			42,
			map[string]any{"question": "First?", "placeholder": "a"},
			map[string]any{"question": "Second?", "placeholder": "b"},
		},
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	components, _ := parsed["components"].([]any)

	var titles []string
	for _, c := range components {
		cm, ok := c.(map[string]any)
		if !ok || cm["component"] != "Tabs" {
			continue
		}
		items, _ := cm["tabItems"].([]any)
		for _, itRaw := range items {
			it, _ := itRaw.(map[string]any)
			titles = append(titles, litText(t, it["title"]))
		}
	}
	if len(titles) != 2 || titles[0] != "Q1" || titles[1] != "Q2" {
		t.Errorf("tab titles = %v, want [Q1 Q2]", titles)
	}
}

func TestGenerateA2UISurface_StrayElementDoesNotTabify(t *testing.T) {
	args := map[string]any{
		"questions": []any{
			42,
			map[string]any{
				"question": "Pick?",
				"header":   "Pick",
				"options":  []any{map[string]any{"label": "A", "description": "first"}},
			},
		},
	}

	surface, err := generateA2UISurface("req-stray", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	components, _ := parsed["components"].([]any)

	for _, c := range components {
		if cm, ok := c.(map[string]any); ok && cm["component"] == "Tabs" {
			t.Errorf("one real question was wrapped in Tabs because of a stray element: %v", cm)
		}
	}
}

func TestGenerateA2UISurface_MultiQuestionKeepsHeaderAsTabTitle(t *testing.T) {
	args := map[string]any{
		"questions": []any{
			map[string]any{"question": "First?", "header": "Alpha",
				"options": []any{map[string]any{"label": "y", "description": "yes"}}},
			map[string]any{"question": "Second?", "header": "Beta",
				"options": []any{map[string]any{"label": "n", "description": "no"}}},
		},
	}

	surface, err := generateA2UISurface("req-tabs", args)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(surface, &parsed); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	components, _ := parsed["components"].([]any)

	var titles []string
	for _, c := range components {
		cm, ok := c.(map[string]any)
		if !ok || cm["component"] != "Tabs" {
			continue
		}
		items, _ := cm["tabItems"].([]any)
		for _, itRaw := range items {
			it, ok := itRaw.(map[string]any)
			if !ok {
				continue
			}
			titles = append(titles, litText(t, it["title"]))
		}
	}

	if len(titles) != 2 || titles[0] != "Alpha" || titles[1] != "Beta" {
		t.Errorf("tab titles = %v, want [Alpha Beta]", titles)
	}
}

func litText(t *testing.T, v any) string {
	t.Helper()
	m, ok := v.(map[string]any)
	if !ok {
		t.Fatalf("expected a literalString envelope, got %T (%v)", v, v)
	}
	s, _ := m["literalString"].(string)
	return s
}
