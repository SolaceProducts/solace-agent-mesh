// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package llm

import (
	"errors"
	"regexp"
	"strings"
)

var toolChoiceUnsupportedPatterns = []string{
	"not supported",
	"unsupported",
	"does not support",
	"doesn't support",
}

var forcedChoiceRefusers = regexp.MustCompile(`claude-(?:sonnet|opus)-5[-.]5(?:$|[^0-9])`)

func ModelRefusesForcedToolChoice(model string) bool {
	return forcedChoiceRefusers.MatchString(strings.ToLower(model))
}

func (e *Error) IsToolChoiceUnsupportedError() bool {
	if !e.IsBadRequest() {
		return false
	}
	normalized := strings.NewReplacer("_", "", " ", "", "-", "").Replace(strings.ToLower(e.Message))
	if !strings.Contains(normalized, "toolchoice") {
		return false
	}
	lower := strings.ToLower(e.Message)
	for _, pat := range toolChoiceUnsupportedPatterns {
		if strings.Contains(lower, pat) {
			return true
		}
	}
	return false
}

func RefusesToolChoice(err error) bool {
	switch e := err.(type) {
	case nil:
		return false
	case *Error:
		return e.IsToolChoiceUnsupportedError()
	case interface{ Unwrap() []error }:
		for _, inner := range e.Unwrap() {
			if RefusesToolChoice(inner) {
				return true
			}
		}
		return false
	}
	return RefusesToolChoice(errors.Unwrap(err))
}
