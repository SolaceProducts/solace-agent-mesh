// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"cmp"
	"html/template"
	"net/http"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/SolaceProducts/solace-agent-mesh/internal/entrypoint/a2ui"
)

type hilFormView struct {
	Title   string
	Action  string
	Details []hilFormDetail
	Fields  []hilFormField
	Buttons []hilFormButton
}

type hilFormDetail struct {
	IsValue bool
	IsNote  bool
	Label   string
	Text    string
}

type hilFormField struct {
	Key      string
	Label    string
	IsMulti  bool
	IsChoice bool
	Options  []hilFormOption
}

type hilFormOption struct {
	FieldKey    string
	Value       string
	Label       string
	Description string
}

type hilFormButton struct {
	ID      string
	Label   string
	Primary bool
}

var hilFormTemplate = template.Must(template.New("hilForm").Parse(`<!doctype html>
<html><head><meta charset="utf-8"><meta name="viewport" content="width=device-width, initial-scale=1">
<title>{{.Title}}</title>
<style>
body{font-family:system-ui,sans-serif;max-width:640px;margin:2rem auto;padding:0 1rem;color:#222}
fieldset{border:1px solid #ddd;border-radius:8px;margin:1rem 0;padding:1rem}
label{display:block;margin:.35rem 0}
textarea{width:100%;min-height:5rem}
.actions{margin-top:1.5rem;display:flex;gap:.75rem}
button{padding:.6rem 1.2rem;border-radius:6px;border:1px solid #ccc;cursor:pointer}
button.primary{background:#0b5cff;color:#fff;border-color:#0b5cff}
.detail{margin:.75rem 0;white-space:pre-wrap}
.label{font-weight:600;font-size:.9rem;color:#555}
pre{margin:.25rem 0;padding:.6rem;background:#f5f5f5;border-radius:6px;white-space:pre-wrap;word-break:break-word;max-height:24rem;overflow:auto}
.note{font-size:.85rem;color:#666}
</style></head>
<body>
<h2>{{.Title}}</h2>
{{range .Details}}
{{if .IsValue}}<div class="detail"><div class="label">{{.Label}}</div><pre>{{if .Text}}{{.Text}}{{else}}(empty){{end}}</pre></div>
{{else if .IsNote}}<p class="detail note">{{.Text}}</p>
{{else}}<p class="detail">{{.Text}}</p>
{{end}}
{{end}}
<form method="POST" action="{{.Action}}">
{{range .Fields}}
<fieldset>
{{if .Label}}<legend>{{.Label}}</legend>{{end}}
{{if .IsMulti}}
  {{range .Options}}<label><input type="checkbox" name="{{.FieldKey}}" value="{{.Value}}"> {{.Label}}{{if .Description}} — {{.Description}}{{end}}</label>{{end}}
{{else if .IsChoice}}
  {{range .Options}}<label><input type="radio" name="{{.FieldKey}}" value="{{.Value}}"> {{.Label}}{{if .Description}} — {{.Description}}{{end}}</label>{{end}}
{{else}}
  <textarea name="{{.Key}}"></textarea>
{{end}}
</fieldset>
{{end}}
<div class="actions">
{{range .Buttons}}<button type="submit" name="_btn" value="{{.ID}}"{{if .Primary}} class="primary"{{end}}>{{.Label}}</button>{{end}}
</div>
</form>
</body></html>`))

func renderHILForm(w http.ResponseWriter, s *a2ui.Surface, action string) error {
	view := hilFormView{Title: surfaceTitle(s), Action: action}
	for _, d := range s.Details {
		view.Details = append(view.Details, hilFormDetail{
			IsValue: d.Kind == a2ui.DetailValue, IsNote: d.Kind == a2ui.DetailNote, Label: d.Label, Text: d.Text,
		})
	}

	for i, f := range s.Fields {
		key := "f" + strconv.Itoa(i)
		ff := hilFormField{
			Key:      key,
			Label:    f.Label,
			IsMulti:  f.Kind == a2ui.FieldMultiChoice,
			IsChoice: f.Kind == a2ui.FieldSingleChoice,
		}
		for _, o := range f.Options {
			ff.Options = append(ff.Options, hilFormOption{
				FieldKey: key, Value: o.Value, Label: o.Label, Description: o.Description,
			})
		}
		view.Fields = append(view.Fields, ff)
	}

	for _, b := range s.Buttons {
		label := b.Label
		if label == "" {
			if b.IsCancel() {
				label = "Cancel"
			} else {
				label = "Submit"
			}
		}
		view.Buttons = append(view.Buttons, hilFormButton{ID: b.ID, Label: label, Primary: b.Variant == "primary"})
	}

	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	return hilFormTemplate.Execute(w, view)
}

func parseHILForm(r *http.Request, s *a2ui.Surface) (status string, data map[string]any, ok bool) {
	if err := r.ParseForm(); err != nil {
		return "", nil, false
	}

	clicked := r.PostForm.Get("_btn")
	if clicked == "" {
		return "", nil, false
	}
	var btn *a2ui.Button
	for i := range s.Buttons {
		if s.Buttons[i].ID == clicked {
			btn = &s.Buttons[i]
			break
		}
	}
	if btn == nil {
		return "", nil, false
	}
	if btn.IsCancel() {
		return "cancelled", nil, true
	}

	dataModel := s.BuildDataModel()
	for i, f := range s.Fields {
		key := "f" + strconv.Itoa(i)
		switch f.Kind {
		case a2ui.FieldMultiChoice:
			vals := r.PostForm[key]
			arr := make([]any, 0, len(vals))
			for _, v := range vals {
				arr = append(arr, v)
			}
			a2ui.SetPath(dataModel, f.Path, arr)
		default:
			a2ui.SetPath(dataModel, f.Path, strings.TrimSpace(r.PostForm.Get(key)))
		}
	}
	st, d := s.BuildResponse(*btn, dataModel)
	return st, d, true
}

func hilEmailBody(s *a2ui.Surface, link string) string {
	var b strings.Builder
	b.WriteString(oneLine(a2ui.Clip(surfaceTitle(s), emailMaxHILValueRunes)))
	b.WriteString("\n\n")
	if details := emailDetailText(s.Details); details != "" {
		b.WriteString(details)
		b.WriteString("\n\n")
	}
	b.WriteString("Please respond using this secure link (it expires shortly):\n")
	b.WriteString(link)
	b.WriteString("\n")
	return b.String()
}

const (
	emailMaxHILValueRunes  = 2000
	emailMaxHILDetailRunes = 12000
)

const msgHILDetailsOnForm = "The details of this request are too long for this email; open the link to see them."

func emailDetailText(details []a2ui.Detail) string {
	parts := make([]string, 0, len(details))
	total := 0
	for _, d := range details {
		var part string
		switch d.Kind {
		case a2ui.DetailValue:
			label := oneLine(a2ui.Clip(d.Label, emailMaxHILLabelRunes))
			part = indentBy(detailIndent, label+":") + "\n" + indentBy(valueIndent, a2ui.Clip(cmp.Or(d.Text, "(empty)"), emailMaxHILValueRunes))
		default:
			part = indentBy(detailIndent, a2ui.Clip(d.Text, emailMaxHILValueRunes))
		}
		total += utf8.RuneCountInString(part)
		if total > emailMaxHILDetailRunes {
			return msgHILDetailsOnForm
		}
		parts = append(parts, part)
	}
	return strings.Join(parts, "\n\n")
}

const (
	emailMaxHILLabelRunes = 200
	detailIndent          = "  "
	valueIndent           = "      "
)

func indentBy(prefix, s string) string {
	return prefix + strings.ReplaceAll(normalizeNewlines(s), "\n", "\n"+prefix)
}

func normalizeNewlines(s string) string {
	return strings.ReplaceAll(strings.ReplaceAll(s, "\r\n", "\n"), "\r", "\n")
}

func oneLine(s string) string {
	return strings.Join(strings.Fields(normalizeNewlines(s)), " ")
}

func surfaceTitle(s *a2ui.Surface) string {
	if s.Title != "" {
		return s.Title
	}
	return "Input requested"
}
