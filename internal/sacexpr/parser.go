// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package sacexpr

import "strings"

type exprSource int

const (
	sourceUnknown exprSource = iota
	sourceInputPayload
	sourceInputTopic
	sourceInputTopicLvls
	sourceInputUserProps
	sourceUserData
	sourceStatic
	sourceListItem
	sourceTemplate
)

type parsedExpr struct {
	source   exprSource
	path     string
	dataName string
}

func parseExpression(expr string) parsedExpr {
	if strings.HasPrefix(expr, "template:") {
		return parsedExpr{source: sourceTemplate, path: expr[len("template:"):]}
	}

	if strings.HasPrefix(expr, "static:") {
		return parsedExpr{source: sourceStatic, path: expr[len("static:"):]}
	}

	if strings.HasPrefix(expr, "list_item:") {
		return parsedExpr{source: sourceListItem, path: expr[len("list_item:"):]}
	}

	if strings.HasPrefix(expr, "user_data.") {
		rest := expr[len("user_data."):]
		colonIdx := strings.IndexByte(rest, ':')
		if colonIdx >= 0 {
			return parsedExpr{
				source:   sourceUserData,
				dataName: rest[:colonIdx],
				path:     rest[colonIdx+1:],
			}
		}
		return parsedExpr{source: sourceUserData, dataName: rest}
	}

	colonIdx := strings.IndexByte(expr, ':')
	if colonIdx < 0 {
		return mapSourcePrefix(expr, "")
	}

	prefix := expr[:colonIdx]
	path := expr[colonIdx+1:]
	return mapSourcePrefix(prefix, path)
}

func mapSourcePrefix(prefix, path string) parsedExpr {
	switch prefix {
	case "input.payload":
		return parsedExpr{source: sourceInputPayload, path: path}
	case "input.topic":
		return parsedExpr{source: sourceInputTopic, path: path}
	case "input.topic_levels":
		return parsedExpr{source: sourceInputTopicLvls, path: path}
	case "input.user_properties":
		return parsedExpr{source: sourceInputUserProps, path: path}
	default:
		return parsedExpr{source: sourceUnknown, path: path}
	}
}
