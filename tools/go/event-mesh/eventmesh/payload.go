// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package eventmesh

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

func buildPayloadAndResolveParams(
	parameters []Parameter,
	args map[string]any,
	view *contextView,
) (map[string]any, map[string]any) {
	payload := map[string]any{}
	resolved := map[string]any{}

	for _, p := range parameters {
		var (
			value any
			ok    bool
		)
		switch {
		case p.ContextExpression != "":
			expr := p.ContextExpression
			if !strings.Contains(expr, ":") {
				expr = "dummy:" + expr
			}
			if view != nil {
				value, ok = view.Lookup(expr)
			}
			if !ok && p.HasDefault() {
				value, ok = p.Default, true
			}
		default:
			if v, has := args[p.Name]; has {
				value, ok = v, true
			} else if p.HasDefault() {
				value, ok = p.Default, true
			}
		}
		if !ok {
			continue
		}
		resolved[p.Name] = value
		if p.PayloadPath != "" {
			insertNested(payload, p.PayloadPath, value)
		}
	}
	return payload, resolved
}

func insertNested(root map[string]any, path string, value any) {
	parts := strings.Split(path, ".")
	cur := root
	for i, part := range parts {
		if i == len(parts)-1 {
			cur[part] = value
			return
		}
		next, ok := cur[part].(map[string]any)
		if !ok {
			next = map[string]any{}
			cur[part] = next
		}
		cur = next
	}
}

func encodePayload(payload map[string]any, format, encoding string) ([]byte, error) {
	var raw []byte
	switch format {
	case "", "json":
		b, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encoding payload as json: %w", err)
		}
		raw = b
	case "yaml":
		b, err := yaml.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("encoding payload as yaml: %w", err)
		}
		raw = b
	case "text":
		s, err := flattenToText(payload)
		if err != nil {
			return nil, err
		}
		raw = []byte(s)
	default:
		return nil, fmt.Errorf("unsupported payload_format %q", format)
	}

	switch encoding {
	case "", "utf-8":
		return raw, nil
	case "base64":
		out := make([]byte, base64.StdEncoding.EncodedLen(len(raw)))
		base64.StdEncoding.Encode(out, raw)
		return out, nil
	default:
		return nil, fmt.Errorf("unsupported payload_encoding %q", encoding)
	}
}

func flattenToText(payload map[string]any) (string, error) {
	if len(payload) != 1 {
		return "", fmt.Errorf("payload_format=text requires exactly one payload value, got %d", len(payload))
	}
	for _, v := range payload {
		switch t := v.(type) {
		case string:
			return t, nil
		case nil:
			return "", nil
		default:
			return fmt.Sprintf("%v", t), nil
		}
	}
	return "", nil
}

func decodePayload(body []byte, format, encoding string) any {
	if len(body) == 0 {
		return nil
	}
	if encoding == "base64" {
		decoded, err := base64.StdEncoding.DecodeString(string(body))
		if err == nil {
			body = decoded
		}
	}
	switch format {
	case "json", "":
		var v any
		if err := json.Unmarshal(body, &v); err == nil {
			return v
		}
	case "yaml":
		var v any
		if err := yaml.Unmarshal(body, &v); err == nil {
			return v
		}
	}
	return string(body)
}
