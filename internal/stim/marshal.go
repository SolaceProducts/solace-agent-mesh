// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package stim

import (
	"encoding"
	"fmt"
	"reflect"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode/utf8"

	"gopkg.in/yaml.v3"
)

func Marshal(v any) ([]byte, error) {
	node, err := yamlNode(v)
	if err != nil {
		return nil, err
	}
	data, err := yaml.Marshal(node)
	if err != nil {
		return nil, fmt.Errorf("marshaling STIM YAML: %w", err)
	}
	return data, nil
}

func yamlNode(v any) (*yaml.Node, error) {
	if v == nil {
		return nullNode(), nil
	}
	if s, ok := v.(string); ok {
		return stringNode(s), nil
	}
	if node, handled, err := selfMarshaledNode(v); handled {
		return node, err
	}

	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Pointer, reflect.Interface:
		if rv.IsNil() {
			return nullNode(), nil
		}
		return yamlNode(rv.Elem().Interface())

	case reflect.String:
		return stringNode(rv.String()), nil

	case reflect.Map:
		return mappingNode(rv)

	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.Type().Elem().Kind() == reflect.Uint8 {
			return encodedNode(v)
		}
		return sequenceNode(rv)

	case reflect.Struct:
		return structNode(rv)

	default:
		return encodedNode(v)
	}
}

func nullNode() *yaml.Node {
	return &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!null", Value: "null"}
}

func selfMarshaledNode(v any) (*yaml.Node, bool, error) {
	switch value := v.(type) {
	case *yaml.Node:
		if value == nil {
			return nullNode(), true, nil
		}
		copied := *value
		return &copied, true, nil
	case yaml.Node:
		return &value, true, nil

	case time.Duration:
		return stringNode(value.String()), true, nil

	case time.Time, *time.Time:
		node, err := encodedNode(v)
		return node, true, err

	case yaml.Marshaler:
		inner, err := value.MarshalYAML()
		if err != nil {
			return nil, true, fmt.Errorf("marshaling STIM value of type %T: %w", v, err)
		}
		if inner == nil {
			return nullNode(), true, nil
		}
		node, err := yamlNode(inner)
		return node, true, err

	case encoding.TextMarshaler:
		text, err := value.MarshalText()
		if err != nil {
			return nil, true, fmt.Errorf("marshaling STIM text of type %T: %w", v, err)
		}
		return stringNode(string(text)), true, nil
	}
	return nil, false, nil
}

func isSelfMarshaling(t reflect.Type) bool {
	return t.Implements(reflect.TypeOf((*yaml.Marshaler)(nil)).Elem()) ||
		t.Implements(reflect.TypeOf((*encoding.TextMarshaler)(nil)).Elem())
}

func structNode(rv reflect.Value) (*yaml.Node, error) {
	rt := rv.Type()
	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	inlineMaps := 0
	for i := 0; i < rt.NumField(); i++ {
		f := rt.Field(i)
		if !f.IsExported() && !f.Anonymous {
			continue
		}
		name, opts, _ := strings.Cut(f.Tag.Get("yaml"), ",")
		if name == "-" && opts == "" {
			continue
		}
		if name == "" {
			name = strings.ToLower(f.Name)
		}
		fv := rv.Field(i)
		if strings.Contains(opts, "inline") {
			if isInlineMapKind(fv) {
				inlineMaps++
				if inlineMaps > 1 {
					return nil, fmt.Errorf("encoding STIM struct %s: multiple ,inline maps", rt.Name())
				}
			}
			inlined, ok, err := inlineFieldContent(fv)
			if err != nil {
				return nil, fmt.Errorf("inlining STIM field %s.%s: %w", rt.Name(), f.Name, err)
			}
			if ok {
				n.Content = append(n.Content, inlined...)
				continue
			}
		}
		if !f.IsExported() {
			continue
		}
		if strings.Contains(opts, "omitempty") && isZeroForYAML(fv) {
			continue
		}
		val, err := yamlNode(fv.Interface())
		if err != nil {
			return nil, fmt.Errorf("encoding STIM field %s.%s: %w", rt.Name(), f.Name, err)
		}
		if strings.Contains(opts, "flow") {
			val.Style |= yaml.FlowStyle
		}
		n.Content = append(n.Content, stringNode(name), val)
	}
	if err := checkDuplicateKeys(n.Content, rt); err != nil {
		return nil, err
	}
	return n, nil
}

func checkDuplicateKeys(content []*yaml.Node, rt reflect.Type) error {
	seen := make(map[string]struct{}, len(content)/2)
	for i := 0; i+1 < len(content); i += 2 {
		key := content[i].Value
		if _, dup := seen[key]; dup {
			return fmt.Errorf("encoding STIM struct %s: duplicate key %q, so one value would be lost on reload",
				rt.Name(), key)
		}
		seen[key] = struct{}{}
	}
	return nil
}

func mappingNode(rv reflect.Value) (*yaml.Node, error) {
	entries := make([]struct {
		key  *yaml.Node
		val  *yaml.Node
		sort string
		tag  string
	}, 0, rv.Len())
	iter := rv.MapRange()
	for iter.Next() {
		key, err := yamlNode(iter.Key().Interface())
		if err != nil {
			return nil, err
		}
		val, err := yamlNode(iter.Value().Interface())
		if err != nil {
			return nil, err
		}
		entries = append(entries, struct {
			key  *yaml.Node
			val  *yaml.Node
			sort string
			tag  string
		}{key, val, key.Value, key.Tag})
	}
	slices.SortFunc(entries, func(a, b struct {
		key  *yaml.Node
		val  *yaml.Node
		sort string
		tag  string
	}) int {
		if c := strings.Compare(a.sort, b.sort); c != 0 {
			return c
		}
		return strings.Compare(a.tag, b.tag)
	})

	n := &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
	for _, e := range entries {
		n.Content = append(n.Content, e.key, e.val)
	}
	return n, nil
}

func sequenceNode(rv reflect.Value) (*yaml.Node, error) {
	n := &yaml.Node{Kind: yaml.SequenceNode, Tag: "!!seq"}
	for i := range rv.Len() {
		child, err := yamlNode(rv.Index(i).Interface())
		if err != nil {
			return nil, err
		}
		n.Content = append(n.Content, child)
	}
	return n, nil
}

func stringNode(s string) *yaml.Node {
	if !utf8.ValidString(s) {
		n := &yaml.Node{Kind: yaml.ScalarNode, Value: s}
		if err := n.Encode(s); err != nil {
			return &yaml.Node{Kind: yaml.ScalarNode, Value: s}
		}
		return n
	}
	n := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: s}
	if needsQuoting(s) {
		n.Style = yaml.DoubleQuotedStyle
	}
	return n
}

func encodedNode(v any) (*yaml.Node, error) {
	n := &yaml.Node{}
	if err := n.Encode(v); err != nil {
		return nil, fmt.Errorf("encoding STIM value of type %T: %w", v, err)
	}
	quoteBlockScalarRisks(n)
	return n, nil
}

func quoteBlockScalarRisks(n *yaml.Node) {
	if n.Kind == yaml.ScalarNode && n.Tag == "!!str" && needsQuoting(n.Value) {
		n.Style = yaml.DoubleQuotedStyle
	}
	for _, child := range n.Content {
		quoteBlockScalarRisks(child)
	}
}

func needsQuoting(s string) bool {
	if s == "" || strings.TrimSpace(s) != s {
		return true
	}
	if strings.ContainsAny(s, "\n\r\t") {
		return true
	}
	return isOldBool(s) || isBase60(s) || isTypeToken(s) || isYAML11Timestamp(s) ||
		isLeadingDotFloat(s)
}

var leadingDotFloat = regexp.MustCompile(`^\.[0-9][0-9_]*([eE][-+][0-9]+)?$`)

func isLeadingDotFloat(s string) bool { return leadingDotFloat.MatchString(s) }

func isTypeToken(s string) bool {
	return s == "=" || s == "<<"
}

var yaml11Timestamp = regexp.MustCompile(
	`^\d{4}-\d{1,2}-\d{1,2}(([Tt]|[ \t]+)\d{1,2}:\d{2}:\d{2}(\.\d*)?([ \t]*(Z|[-+]\d{1,2}(:\d{2})?))?)?$`)

func isYAML11Timestamp(s string) bool { return yaml11Timestamp.MatchString(s) }

func isOldBool(s string) bool {
	switch s {
	case "y", "Y", "yes", "Yes", "YES", "n", "N", "no", "No", "NO",
		"on", "On", "ON", "off", "Off", "OFF":
		return true
	}
	return false
}

func isBase60(s string) bool {
	if !strings.Contains(s, ":") {
		return false
	}
	body := strings.TrimPrefix(strings.TrimPrefix(s, "-"), "+")
	if body == "" {
		return false
	}
	digitsSeen := false
	for _, part := range strings.Split(body, ":") {
		if part == "" {
			return false
		}
		for _, r := range part {
			if r == '.' || r == '_' {
				continue
			}
			if r < '0' || r > '9' {
				return false
			}
			digitsSeen = true
		}
	}
	return digitsSeen
}

func isInlineMapKind(fv reflect.Value) bool {
	return fv.Kind() == reflect.Map
}

func inlineFieldContent(fv reflect.Value) ([]*yaml.Node, bool, error) {
	for fv.Kind() == reflect.Pointer || fv.Kind() == reflect.Interface {
		if fv.IsNil() {
			return nil, true, nil
		}
		if isSelfMarshaling(fv.Type()) {
			return nil, false, nil
		}
		fv = fv.Elem()
	}
	switch fv.Kind() {
	case reflect.Struct:
		if isSelfMarshaling(fv.Type()) {
			return nil, false, nil
		}
		inner, err := structNode(fv)
		if err != nil {
			return nil, false, err
		}
		return inner.Content, true, nil
	case reflect.Map:
		if !fv.CanInterface() {
			return nil, false, fmt.Errorf(
				"cannot inline the unexported embedded map type %s: reflect will not read its entries", fv.Type())
		}
		if fv.Type().Key() != reflect.TypeOf("") {
			return nil, false, fmt.Errorf(
				"option ,inline needs a map with string keys, got %s", fv.Type())
		}
		inner, err := mappingNode(fv)
		if err != nil {
			return nil, false, err
		}
		return inner.Content, true, nil
	default:
		return nil, false, nil
	}
}

func isZeroForYAML(v reflect.Value) bool {
	kind := v.Kind()
	if z, ok := v.Interface().(yaml.IsZeroer); ok {
		if (kind == reflect.Pointer || kind == reflect.Interface) && v.IsNil() {
			return true
		}
		return z.IsZero()
	}
	switch kind {
	case reflect.String:
		return v.Len() == 0
	case reflect.Interface, reflect.Pointer:
		return v.IsNil()
	case reflect.Slice, reflect.Map:
		return v.Len() == 0
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return v.Int() == 0
	case reflect.Float32, reflect.Float64:
		return v.Float() == 0
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		return v.Uint() == 0
	case reflect.Bool:
		return !v.Bool()
	case reflect.Struct:
		vt := v.Type()
		for i := v.NumField() - 1; i >= 0; i-- {
			if vt.Field(i).PkgPath != "" {
				continue
			}
			if !isZeroForYAML(v.Field(i)) {
				return false
			}
		}
		return true
	default:
		return false
	}
}
