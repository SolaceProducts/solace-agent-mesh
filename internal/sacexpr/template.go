// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const maxTemplateDepth = 10

func evaluateTemplate(ctx *Context, tmpl string, depth int) (any, error) {
	if depth > maxTemplateDepth {
		return nil, fmt.Errorf("template recursion depth exceeded (max %d)", maxTemplateDepth)
	}

	blocks := parseTemplateBlocks(tmpl)
	if len(blocks) == 0 {
		return tmpl, nil
	}

	if len(blocks) == 1 && blocks[0].isExpr && blocks[0].start == 0 && blocks[0].end == len(tmpl) {
		val, err := ctx.GetData(blocks[0].expression)
		if err != nil {
			return nil, err
		}
		return encodeValue(val, blocks[0].encoding)
	}

	var sb strings.Builder
	lastEnd := 0

	for _, blk := range blocks {
		if blk.start > lastEnd {
			sb.WriteString(tmpl[lastEnd:blk.start])
		}

		val, err := ctx.GetData(blk.expression)
		if err != nil {
			return nil, fmt.Errorf("template expression %q: %w", blk.expression, err)
		}

		encoded, err := encodeValue(val, blk.encoding)
		if err != nil {
			return nil, fmt.Errorf("template encode %q: %w", blk.encoding, err)
		}
		fmt.Fprintf(&sb, "%v", encoded)
		lastEnd = blk.end
	}

	if lastEnd < len(tmpl) {
		sb.WriteString(tmpl[lastEnd:])
	}

	return sb.String(), nil
}

type templateBlock struct {
	start      int
	end        int
	expression string
	encoding   string
	isExpr     bool
}

func parseTemplateBlocks(tmpl string) []templateBlock {
	var blocks []templateBlock
	i := 0

	for i < len(tmpl) {
		start := strings.Index(tmpl[i:], "{{")
		if start == -1 {
			break
		}
		start += i

		end := strings.Index(tmpl[start+2:], "}}")
		if end == -1 {
			break
		}
		end += start + 2

		inner := strings.TrimSpace(tmpl[start+2 : end])

		encoding, expr := parseEncodedExpression(inner)

		blocks = append(blocks, templateBlock{
			start:      start,
			end:        end + 2,
			expression: expr,
			encoding:   encoding,
			isExpr:     true,
		})

		i = end + 2
	}

	return blocks
}

func parseEncodedExpression(s string) (encoding, expression string) {
	idx := strings.Index(s, "://")
	if idx == -1 {
		return "text", s
	}

	encoding = s[:idx]
	expression = s[idx+3:]

	return encoding, expression
}

func encodeValue(val any, encoding string) (any, error) {
	switch {
	case encoding == "text" || encoding == "":
		return val, nil

	case encoding == "json":
		b, err := json.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("json encode: %w", err)
		}
		return string(b), nil

	case encoding == "yaml":
		b, err := yaml.Marshal(val)
		if err != nil {
			return nil, fmt.Errorf("yaml encode: %w", err)
		}
		return strings.TrimRight(string(b), "\n"), nil

	case encoding == "base64":
		s := fmt.Sprintf("%v", val)
		return base64.StdEncoding.EncodeToString([]byte(s)), nil

	case strings.HasPrefix(encoding, "datauri:"):
		mimeType := encoding[len("datauri:"):]
		s := fmt.Sprintf("%v", val)
		encoded := base64.StdEncoding.EncodeToString([]byte(s))
		return fmt.Sprintf("data:%s;base64,%s", mimeType, encoded), nil

	default:
		return val, nil
	}
}
