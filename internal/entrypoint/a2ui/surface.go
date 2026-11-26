// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package a2ui

import (
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

type FieldKind int

const (
	FieldText FieldKind = iota
	FieldSingleChoice
	FieldMultiChoice
)

const OtherSentinel = "__other__"

type Option struct {
	Label       string
	Value       string
	Description string
}

type Field struct {
	ID          string
	Kind        FieldKind
	Path        string
	Label       string
	Placeholder string
	Multiline   bool
	Options     []Option
}

func (f Field) HasOther() bool {
	for _, o := range f.Options {
		if o.Value == OtherSentinel {
			return true
		}
	}
	return false
}

type Button struct {
	ID      string
	Label   string
	Event   string
	Variant string
	Context map[string]any
}

func (b Button) IsCancel() bool { return b.Event == "cancel" }

type DetailKind int

const (
	DetailParagraph DetailKind = iota
	DetailValue
	DetailNote
)

type Detail struct {
	Kind  DetailKind
	Label string
	Text  string
}

func ClipNotice(droppedRunes int) string {
	return fmt.Sprintf("… (%d more characters; open the web UI to see the full value)", droppedRunes)
}

func Clip(s string, maxRunes int) string {
	total := utf8.RuneCountInString(s)
	if total <= maxRunes {
		return s
	}
	keep := max(maxRunes-utf8.RuneCountInString(ClipNotice(total)), 0)
	return string([]rune(s)[:keep]) + ClipNotice(total-keep)
}

func (s *Surface) Summary() string {
	for _, d := range s.Details {
		if d.Kind != DetailParagraph {
			continue
		}
		if s.Title == "" {
			return d.Text
		}
		return s.Title + ": " + d.Text
	}
	return s.Title
}

type Surface struct {
	SurfaceID string
	CatalogID string

	Title string

	Question string

	Details []Detail

	Fields  []Field
	Buttons []Button

	initialDataModel map[string]any
}

type rawComponent struct {
	ID          string      `json:"id"`
	Component   string      `json:"component"`
	Text        any         `json:"text"`
	Label       any         `json:"label"`
	Variant     string      `json:"variant"`
	Multiline   bool        `json:"multiline"`
	Placeholder any         `json:"placeholder"`
	Options     []rawOption `json:"options"`
	Value       *rawBinding `json:"value"`
	Action      *rawAction  `json:"action"`
	Child       string      `json:"child"`
	Children    rawChildren `json:"children"`
}

type rawChildren []string

func (c *rawChildren) UnmarshalJSON(b []byte) error {
	var list struct {
		ExplicitList []string `json:"explicitList"`
	}
	if json.Unmarshal(b, &list) == nil {
		*c = list.ExplicitList
		return nil
	}
	var ids []string
	if json.Unmarshal(b, &ids) == nil {
		*c = ids
	}
	return nil
}

type rawOption struct {
	Label       any    `json:"label"`
	Value       string `json:"value"`
	Description any    `json:"description"`
}

type rawBinding struct {
	Path string `json:"path"`
}

type rawAction struct {
	Event *rawEvent `json:"event"`
}

type rawEvent struct {
	Name    string         `json:"name"`
	Context map[string]any `json:"context"`
}

type rawSurface struct {
	SurfaceID  string         `json:"surfaceId"`
	CatalogID  string         `json:"catalogId"`
	Components []rawComponent `json:"components"`
	DataModel  map[string]any `json:"dataModel"`
}

func Parse(raw json.RawMessage) (*Surface, error) {
	var rs rawSurface
	if err := json.Unmarshal(raw, &rs); err != nil {
		return nil, fmt.Errorf("decode a2ui surface: %w", err)
	}

	s := &Surface{
		SurfaceID:        rs.SurfaceID,
		CatalogID:        rs.CatalogID,
		initialDataModel: rs.DataModel,
	}
	if s.initialDataModel == nil {
		s.initialDataModel = map[string]any{}
	}

	var lastHeading string
	values := labelledValues(rs.Components)
	for _, c := range rs.Components {
		switch c.Component {
		case "Text":
			txt := literal(c.Text)
			if value, isLabel := values.byLabel[c.ID]; isLabel {
				s.Details = append(s.Details, Detail{Kind: DetailValue, Label: txt, Text: value})
				continue
			}
			if values.isValue[c.ID] {
				continue
			}
			if c.Variant == "h3" {
				lastHeading = txt
			}
			switch {
			case txt == "":
			case s.Title == "" && (c.Variant == "body" || c.Variant == "h3"):
				s.Title = txt
			case s.Question == "" && c.Variant == "h3" && !strings.EqualFold(strings.TrimSpace(txt), strings.TrimSpace(s.Title)):
				s.Question = txt
			case c.Variant == "body":
				s.Details = append(s.Details, Detail{Kind: DetailParagraph, Text: txt})
			case c.Variant == "caption":
				s.Details = append(s.Details, Detail{Kind: DetailNote, Text: txt})
			}
		case "ChoicePicker":
			if c.Value == nil {
				continue
			}
			f := Field{
				ID:    c.ID,
				Kind:  FieldSingleChoice,
				Path:  c.Value.Path,
				Label: firstNonEmpty(literal(c.Label), lastHeading),
			}
			if c.Variant == "multipleSelection" {
				f.Kind = FieldMultiChoice
			}
			for _, o := range c.Options {
				f.Options = append(f.Options, Option{
					Label:       firstNonEmpty(literal(o.Label), o.Value),
					Value:       o.Value,
					Description: literal(o.Description),
				})
			}
			s.Fields = append(s.Fields, f)
		case "TextField":
			if c.Value == nil {
				continue
			}
			s.Fields = append(s.Fields, Field{
				ID:          c.ID,
				Kind:        FieldText,
				Path:        c.Value.Path,
				Label:       firstNonEmpty(literal(c.Label), lastHeading),
				Placeholder: literal(c.Placeholder),
				Multiline:   c.Multiline,
			})
		case "Button":
			if c.Action == nil || c.Action.Event == nil {
				continue
			}
			s.Buttons = append(s.Buttons, Button{
				ID:      c.ID,
				Label:   textByID(rs.Components, c.Child),
				Event:   c.Action.Event.Name,
				Variant: c.Variant,
				Context: c.Action.Event.Context,
			})
		}
	}

	return s, nil
}

type labelledValueTexts struct {
	byLabel map[string]string
	isValue map[string]bool
}

func labelledValues(all []rawComponent) labelledValueTexts {
	texts := make(map[string]rawComponent)
	for _, c := range all {
		if c.Component == "Text" {
			texts[c.ID] = c
		}
	}
	out := labelledValueTexts{byLabel: map[string]string{}, isValue: map[string]bool{}}
	for _, c := range all {
		if c.Component != "Column" || len(c.Children) != 2 {
			continue
		}
		label, labelOK := texts[c.Children[0]]
		value, valueOK := texts[c.Children[1]]
		if !labelOK || !valueOK || label.Variant != "caption" || value.Variant != "body" {
			continue
		}
		out.byLabel[label.ID] = literal(value.Text)
		out.isValue[value.ID] = true
	}
	return out
}

func (f Field) IsOtherTextField() bool {
	return strings.Contains(f.Path, "/other_text/")
}

func (s *Surface) BuildDataModel() map[string]any {
	return deepCopyMap(s.initialDataModel)
}

func (s *Surface) BuildResponse(btn Button, dataModel map[string]any) (status string, data map[string]any) {
	if btn.IsCancel() {
		return "cancelled", nil
	}
	return "answered", ResolveContext(btn.Context, dataModel)
}

func ResolveContext(ctx map[string]any, dataModel map[string]any) map[string]any {
	out := make(map[string]any, len(ctx))
	for k, v := range ctx {
		if p, ok := bindingPath(v); ok {
			out[k] = GetPath(dataModel, p)
			continue
		}
		out[k] = v
	}
	return out
}

func SetPath(m map[string]any, path string, value any) {
	keys := splitPath(path)
	if len(keys) == 0 {
		return
	}
	cur := m
	for _, k := range keys[:len(keys)-1] {
		next, ok := cur[k].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[k] = next
		}
		cur = next
	}
	cur[keys[len(keys)-1]] = value
}

func GetPath(m map[string]any, path string) any {
	keys := splitPath(path)
	var cur any = m
	for _, k := range keys {
		asMap, ok := cur.(map[string]any)
		if !ok {
			return nil
		}
		cur = asMap[k]
	}
	return cur
}

func ExtractRequest(event *a2a.TaskStatusUpdateEvent) (*a2a.UserInputRequestData, bool) {
	if event == nil || event.Status.State != a2a.TaskStateInputNeeded || event.Status.Message == nil {
		return nil, false
	}
	for _, p := range event.Status.Message.Parts {
		if p.Kind != "data" || p.Data == nil {
			continue
		}
		b, err := json.Marshal(p.Data)
		if err != nil {
			continue
		}
		var req a2a.UserInputRequestData
		if err := json.Unmarshal(b, &req); err != nil {
			continue
		}
		if req.Type == "user_input_request" && req.RequestID != "" {
			return &req, true
		}
	}
	return nil, false
}

func literal(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case map[string]any:
		if s, ok := t["literalString"].(string); ok {
			return s
		}
	}
	return ""
}

func bindingPath(v any) (string, bool) {
	m, ok := v.(map[string]any)
	if !ok {
		return "", false
	}
	if len(m) != 1 {
		return "", false
	}
	p, ok := m["path"].(string)
	return p, ok
}

func splitPath(path string) []string {
	path = strings.TrimPrefix(path, "/")
	if path == "" {
		return nil
	}
	return strings.Split(path, "/")
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func textByID(all []rawComponent, id string) string {
	if id == "" {
		return ""
	}
	for _, c := range all {
		if c.ID == id && c.Component == "Text" {
			return literal(c.Text)
		}
	}
	return ""
}

func deepCopyMap(m map[string]any) map[string]any {
	if m == nil {
		return map[string]any{}
	}
	b, err := json.Marshal(m)
	if err != nil {
		return map[string]any{}
	}
	var out map[string]any
	if err := json.Unmarshal(b, &out); err != nil {
		return map[string]any{}
	}
	return out
}
