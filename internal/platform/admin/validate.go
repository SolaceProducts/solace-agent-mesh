// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/go-playground/validator/v10"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	"github.com/SolaceProducts/solace-agent-mesh/internal/strutil"
)

var namePattern = regexp.MustCompile(`^[^\x00-\x1F\x7F]+$`)

var gatewaySlugPattern = regexp.MustCompile(`^[a-z][a-z0-9-]*[a-z0-9]$`)

var gatewaySlugReserved = map[string]struct{}{
	"oauth":           {},
	"api":             {},
	"well-known":      {},
	"gw":              {},
	"health":          {},
	a2a.EvalGatewayID: {},
}

func isValidGatewaySlug(s string) bool {
	if !gatewaySlugPattern.MatchString(s) {
		return false
	}
	_, reserved := gatewaySlugReserved[s]
	return !reserved
}

func init() {
	for _, r := range []apicontract.Rule{
		{Tag: "safename", Check: func(fl validator.FieldLevel) bool {
			s := fl.Field().String()
			if strings.TrimSpace(s) == "" {
				return false
			}
			return namePattern.MatchString(s)
		}, Message: func(field string) string { return fmt.Sprintf("%s contains invalid characters", field) }},
		{Tag: "safetext", Check: func(fl validator.FieldLevel) bool {
			return strutil.IsSafeText(fl.Field().String())
		}, Message: func(field string) string { return fmt.Sprintf("%s contains unsafe characters", field) }},
		{Tag: "zipname", Check: func(fl validator.FieldLevel) bool {
			return strings.HasSuffix(strings.ToLower(fl.Field().String()), ".zip")
		}, Message: func(field string) string { return fmt.Sprintf("%s must be a .zip archive", field) }},
		{Tag: "gatewayslug", Check: func(fl validator.FieldLevel) bool {
			s := fl.Field().String()
			if s == "" {
				return true
			}
			return isValidGatewaySlug(s)
		}, Message: func(field string) string {
			return fmt.Sprintf("%s must be 3-63 lowercase characters, start with a letter, end with a letter or digit, contain only [a-z0-9-], and not be a reserved word (oauth, api, well-known, gw, health, evals)", field)
		}},
	} {
		apicontract.RegisterRule(r)
	}
}

func validateRequest(req any) error {
	return apicontract.ValidateRequest(req)
}

func validateAgentRequest(req any, systemPrompt string, minChars, maxChars int) error {
	err := validateRequest(req)
	verr, ok := errors.AsType[*ValidationError](err)
	if err != nil && !ok {
		return err
	}
	msg := checkInstructionLength(systemPrompt, minChars, maxChars)
	if msg == "" {
		if verr == nil {
			return nil
		}
		return verr
	}
	if verr == nil {
		return &ValidationError{Message: msg, Details: map[string][]string{"systemPrompt": {msg}}}
	}
	if verr.Details == nil {
		verr.Details = map[string][]string{}
	}
	verr.Details["systemPrompt"] = append(verr.Details["systemPrompt"], msg)
	verr.Message = strings.Join([]string{verr.Message, msg}, "; ")
	return verr
}

func checkInstructionLength(systemPrompt string, minChars, maxChars int) string {
	if systemPrompt == "" {
		return ""
	}
	got := utf8.RuneCountInString(systemPrompt)
	switch {
	case got < minChars:
		return fmt.Sprintf("systemPrompt must be %d characters or more (got %d)", minChars, got)
	case maxChars > 0 && got > maxChars:
		return fmt.Sprintf("systemPrompt must be %d characters or fewer (got %d)", maxChars, got)
	}
	return ""
}
