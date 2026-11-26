// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package codegen

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/SolaceProducts/solace-agent-mesh/internal/secretname"
)

type FieldSchema struct {
	JSONName            string
	GoType              string `json:"-"`
	DisplayTypeOverride string `json:"-"`
	Required            bool
	MinLen              int
	MaxLen              int
	OneOf               []string
	Description         string
	Secret              bool
	Pointer             bool
}

type KindSchema struct {
	Kind        string
	PluralPath  string
	Description string
	Fields      []FieldSchema
}

func (f FieldSchema) DisplayType() string {
	if f.DisplayTypeOverride != "" {
		return f.DisplayTypeOverride
	}
	return FriendlyType(f.GoType)
}

func (f FieldSchema) MarshalJSON() ([]byte, error) {
	type alias FieldSchema
	return json.Marshal(struct {
		Type string `json:"type"`
		alias
	}{
		Type:  f.DisplayType(),
		alias: alias(f),
	})
}

func FriendlyType(raw string) string {
	t := strings.TrimSpace(raw)
	if t == "" {
		return ""
	}
	t = strings.ReplaceAll(t, "interface{}", "any")
	t = strings.TrimPrefix(t, "*")
	if elem, ok := strings.CutPrefix(t, "[]"); ok {
		return "list<" + FriendlyType(elem) + ">"
	}
	if strings.HasPrefix(t, "map[") {
		return "object"
	}
	switch t {
	case "string":
		return "string"
	case "bool":
		return "boolean"
	case "int", "int8", "int16", "int32", "int64",
		"uint", "uint8", "uint16", "uint32", "uint64", "byte", "rune":
		return "integer"
	case "float32", "float64":
		return "number"
	case "time.Duration":
		return "duration"
	}
	return "object"
}

var requestBodyDocRe = regexp.MustCompile(`^\S+ is the (?:(?:request|response) )?(?:body|payload|wire shape) for [A-Z]+ \S+\.?`)

func sanitizeSchemaDoc(doc string) string {
	doc = strings.TrimSpace(doc)
	doc = requestBodyDocRe.ReplaceAllString(doc, "")
	return strings.TrimSpace(doc)
}

func IsSecretFieldName(name string) bool {
	return secretname.IsSecretFieldName(name)
}

var minMaxRe = regexp.MustCompile(`^(min|max)=(\d+)$`)

func BuildKindSchema(kind, pluralPath string, request StructInfo, v2Copy bool) KindSchema {
	desc := sanitizeSchemaDoc(request.Doc)
	if v2Copy {
		desc = ""
	}
	if desc == "" {
		desc = fmt.Sprintf("Authoring fields for the %q resource.", kind)
	}
	out := KindSchema{
		Kind:        kind,
		PluralPath:  pluralPath,
		Description: desc,
		Fields:      make([]FieldSchema, 0, len(request.Fields)),
	}
	for _, f := range request.Fields {
		if f.DocHidden {
			continue
		}
		fs := FieldSchema{
			JSONName:    f.AuthoringKey(),
			GoType:      humanType(f.GoTypeRaw),
			Description: f.Description,
			MinLen:      -1,
			MaxLen:      -1,
			Pointer:     strings.HasPrefix(strings.TrimSpace(f.GoTypeRaw), "*"),
		}
		fs.Required = !f.OmitEmpty && !v2Copy
		applyValidate(f.Validate, &fs)
		applyValidate(f.DocLimits, &fs)
		if f.DocRequired {
			fs.Required = true
		}
		if IsSecretFieldName(f.JSONName) || IsSecretFieldName(f.GoName) {
			fs.Secret = true
		}
		out.Fields = append(out.Fields, fs)
	}
	return out
}

func applyValidate(tag string, fs *FieldSchema) {
	if tag == "" {
		return
	}
	for raw := range strings.SplitSeq(tag, ",") {
		d := strings.TrimSpace(raw)
		switch d {
		case "":
			continue
		case "required":
			fs.Required = true
			continue
		case "omitempty":
			fs.Required = false
			continue
		}
		if m := minMaxRe.FindStringSubmatch(d); m != nil {
			n, err := strconv.Atoi(m[2])
			if err != nil {
				continue
			}
			if m[1] == "min" {
				fs.MinLen = n
			} else {
				fs.MaxLen = n
			}
			continue
		}
		if rest, ok := strings.CutPrefix(d, "oneof="); ok {
			vals := strings.Fields(rest)
			if len(vals) > 0 {
				fs.OneOf = append(fs.OneOf, vals...)
			}
		}
	}
}

func humanType(t string) string {
	t = strings.TrimSpace(t)
	if t == "" {
		return ""
	}
	t = strings.ReplaceAll(t, "interface{}", "any")
	return strings.ReplaceAll(t, "apiv2.", "")
}
