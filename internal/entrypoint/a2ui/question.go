// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2ui

import (
	"encoding/json"
	"fmt"
	"strings"
)

func QuestionSurface(requestID string, args map[string]any) (json.RawMessage, error) {
	var components []map[string]any
	dataModel := map[string]any{
		"answers":    map[string]any{},
		"other_text": map[string]any{},
	}

	if compName, ok := args["component_name"].(string); ok && compName != "" {
		dataModel["component_name"] = compName
	}

	answers := dataModel["answers"].(map[string]any)
	otherText := dataModel["other_text"].(map[string]any)

	var columnChildren []string

	if msg, ok := args["message"].(string); ok && msg != "" {
		components = append(components, map[string]any{
			"id":        "msg",
			"component": "Text",
			"text":      literalString(msg),
			"variant":   "body",
		})
		columnChildren = append(columnChildren, "msg")
	}

	questions := CoerceToSlice(args["questions"])
	useTabs := len(QuestionMaps(questions)) >= 2

	var tabItems []map[string]any

	for i, qRaw := range questions {
		q, ok := qRaw.(map[string]any)
		if !ok {
			continue
		}

		qKey := fmt.Sprintf("q%d", i)
		questionText, _ := q["question"].(string)
		header, _ := q["header"].(string)
		multiSelect, _ := q["multiSelect"].(bool)
		placeholder, _ := q["placeholder"].(string)

		var opts []map[string]any
		for _, opt := range RenderableOptions(q) {
			label, _ := opt["label"].(string)
			description, _ := opt["description"].(string)
			opts = append(opts, map[string]any{
				"label":       literalString(label),
				"value":       label,
				"description": literalString(description),
			})
		}

		hasOptions := len(opts) > 0

		if hasOptions {
			if multiSelect {
				answers[qKey] = []any{}
			} else {
				answers[qKey] = ""
			}
			otherText[qKey] = ""
		} else {
			answers[qKey] = ""
		}

		labelID := fmt.Sprintf("%s-label", qKey)
		pickerID := fmt.Sprintf("%s-picker", qKey)
		otherFieldID := fmt.Sprintf("%s-other", qKey)
		textFieldID := fmt.Sprintf("%s-text", qKey)

		components = append(components, map[string]any{
			"id":        labelID,
			"component": "Text",
			"text":      literalString(questionText),
			"variant":   "h3",
		})

		var questionChildren []string
		questionChildren = append(questionChildren, labelID)

		if hasOptions {
			opts = append(opts, map[string]any{
				"label": literalString("Other"),
				"value": OtherSentinel,
			})

			variant := "mutuallyExclusive"
			if multiSelect {
				variant = "multipleSelection"
			}
			components = append(components, map[string]any{
				"id":        pickerID,
				"component": "ChoicePicker",
				"label":     literalString(header),
				"variant":   variant,
				"options":   opts,
				"value":     map[string]any{"path": fmt.Sprintf("/answers/%s", qKey)},
			})

			components = append(components, map[string]any{
				"id":          otherFieldID,
				"component":   "TextField",
				"label":       literalString("Please specify"),
				"placeholder": literalString("Type your answer..."),
				"value":       map[string]any{"path": fmt.Sprintf("/other_text/%s", qKey)},
			})

			questionChildren = append(questionChildren, pickerID, otherFieldID)
		} else {
			if placeholder == "" {
				placeholder = "Type your answer..."
			}
			components = append(components, map[string]any{
				"id":          textFieldID,
				"component":   "TextField",
				"placeholder": literalString(placeholder),
				"multiline":   true,
				"value":       map[string]any{"path": fmt.Sprintf("/answers/%s", qKey)},
			})

			questionChildren = append(questionChildren, textFieldID)
		}

		if useTabs {
			panelID := fmt.Sprintf("%s-panel", qKey)
			components = append(components, map[string]any{
				"id":        panelID,
				"component": "Column",
				"children":  map[string]any{"explicitList": questionChildren},
			})

			tabTitle := header
			if tabTitle == "" {
				tabTitle = fmt.Sprintf("Q%d", len(tabItems)+1)
			}
			tabItems = append(tabItems, map[string]any{
				"title": literalString(tabTitle),
				"child": panelID,
			})
		} else {
			columnChildren = append(columnChildren, questionChildren...)
		}
	}

	if useTabs {
		components = append(components, map[string]any{
			"id":             "question-tabs",
			"component":      "Tabs",
			"x-auto-advance": true,
			"tabItems":       tabItems,
		})
		columnChildren = append(columnChildren, "question-tabs")
	}

	components = append(components,
		map[string]any{
			"id":        "submit-text",
			"component": "Text",
			"text":      literalString("Submit"),
		},
		map[string]any{
			"id":        "submit-btn",
			"component": "Button",
			"child":     "submit-text",
			"variant":   "primary",
			"action": map[string]any{
				"event": map[string]any{
					"name": "submit",
					"context": map[string]any{
						"answers":    map[string]any{"path": "/answers"},
						"other_text": map[string]any{"path": "/other_text"},
					},
					"completionText": "Submitted",
				},
			},
		},
		map[string]any{
			"id":        "cancel-text",
			"component": "Text",
			"text":      literalString("Cancel"),
		},
		map[string]any{
			"id":        "cancel-btn",
			"component": "Button",
			"child":     "cancel-text",
			"action": map[string]any{
				"event": map[string]any{
					"name":           "cancel",
					"context":        map[string]any{},
					"completionText": "Cancelled",
				},
			},
		},
		map[string]any{
			"id":        "actions",
			"component": "Row",
			"children":  map[string]any{"explicitList": []string{"submit-btn", "cancel-btn"}},
			"justify":   "end",
		},
	)
	columnChildren = append(columnChildren, "actions")

	components = append(components,
		map[string]any{
			"id":        "main-col",
			"component": "Column",
			"children":  map[string]any{"explicitList": columnChildren},
		},
		map[string]any{
			"id":        "root",
			"component": "Card",
			"child":     "main-col",
		},
	)

	surface := map[string]any{
		"surfaceId":  requestID,
		"catalogId":  "https://a2ui.org/specification/v0_9/basic_catalog.json",
		"components": components,
		"dataModel":  dataModel,
	}

	return json.Marshal(surface)
}

func QuestionAnswer(data map[string]any, index int) string {
	answersRaw, _ := data["answers"].(map[string]any)
	otherTextRaw, _ := data["other_text"].(map[string]any)
	qKey := fmt.Sprintf("q%d", index)
	answer := answersRaw[qKey]
	otherVal, _ := otherTextRaw[qKey].(string)

	switch v := answer.(type) {
	case string:
		if v == OtherSentinel && otherVal != "" {
			return otherVal
		}
		return v
	case []any:
		var selected []string
		hasOther := false
		for _, item := range v {
			s, ok := item.(string)
			if !ok {
				continue
			}
			if s == OtherSentinel {
				hasOther = true
				continue
			}
			selected = append(selected, s)
		}
		if hasOther && otherVal != "" {
			selected = append(selected, otherVal)
		}
		return strings.Join(selected, ", ")
	default:
		return fmt.Sprintf("%v", answer)
	}
}

func HasQuestionAnswer(data map[string]any, index int) bool {
	answers, _ := data["answers"].(map[string]any)
	return answers[fmt.Sprintf("q%d", index)] != nil
}

func QuestionMaps(questions []any) []map[string]any {
	qs := make([]map[string]any, 0, len(questions))
	for _, q := range questions {
		if qm, ok := q.(map[string]any); ok {
			qs = append(qs, qm)
		}
	}
	return qs
}

func CoerceToSlice(v any) []any {
	switch val := v.(type) {
	case []any:
		return val
	case string:
		var arr []any
		if json.Unmarshal([]byte(val), &arr) == nil && len(arr) > 0 {
			return arr
		}
		var obj map[string]any
		if json.Unmarshal([]byte(val), &obj) == nil && obj != nil {
			return []any{obj}
		}
		return nil
	case map[string]any:
		return []any{val}
	default:
		return nil
	}
}

func RenderableOptions(q map[string]any) []map[string]any {
	raw := CoerceToSlice(q["options"])
	out := make([]map[string]any, 0, len(raw))
	for _, o := range raw {
		om, ok := o.(map[string]any)
		if !ok {
			continue
		}
		if label, _ := om["label"].(string); strings.TrimSpace(label) == "" {
			continue
		}
		out = append(out, om)
	}
	return out
}

func literalString(s string) map[string]any {
	return map[string]any{"literalString": s}
}
