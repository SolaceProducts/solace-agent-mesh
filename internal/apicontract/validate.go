// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apicontract

import (
	"errors"
	"fmt"
	"reflect"
	"strings"
	"sync"

	"github.com/go-playground/validator/v10"

	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

type Rule struct {
	Tag     string
	Check   validator.Func
	Message func(field string) string
}

var (
	rulesMu        sync.Mutex
	rules          = map[string]Rule{}
	validatorBuilt bool
	built          = sync.OnceValue(buildValidator)
)

func RegisterRule(r Rule) {
	rulesMu.Lock()
	defer rulesMu.Unlock()
	if validatorBuilt {
		panic(fmt.Sprintf("apicontract: rule %q registered after the validator was built", r.Tag))
	}
	if _, dup := rules[r.Tag]; dup {
		panic(fmt.Sprintf("apicontract: rule %q is already registered", r.Tag))
	}
	rules[r.Tag] = r
}

func buildValidator() *validator.Validate {
	v := validator.New(validator.WithRequiredStructEnabled())
	v.RegisterTagNameFunc(func(fld reflect.StructField) string {
		name := strings.SplitN(fld.Tag.Get("json"), ",", 2)[0]
		if name == "-" {
			return ""
		}
		return name
	})
	rulesMu.Lock()
	defer rulesMu.Unlock()
	validatorBuilt = true
	for _, r := range rules {
		if err := v.RegisterValidation(r.Tag, r.Check); err != nil {
			panic(fmt.Sprintf("register validation %q: %v", r.Tag, err))
		}
	}
	return v
}

func ValidateRequest(req any) error {
	if err := built().Struct(req); err != nil {
		if validationErrs, ok := errors.AsType[validator.ValidationErrors](err); ok {
			details := make(map[string][]string, len(validationErrs))
			messages := make([]string, 0, len(validationErrs))
			for _, fe := range validationErrs {
				msg := formatFieldError(fe)
				messages = append(messages, msg)
				field := fe.Field()
				details[field] = append(details[field], msg)
			}
			return &apierr.ValidationError{Message: strings.Join(messages, "; "), Details: details}
		}
		return &apierr.ValidationError{Message: err.Error()}
	}
	return nil
}

func formatFieldError(fe validator.FieldError) string {
	field := fe.Field()
	switch fe.Tag() {
	case "required":
		return fmt.Sprintf("%s is required", field)
	case "min":
		switch fe.Kind() {
		case reflect.String:
			return fmt.Sprintf("%s must be at least %s characters", field, fe.Param())
		case reflect.Slice, reflect.Array, reflect.Map:
			return fmt.Sprintf("%s must have at least %s items", field, fe.Param())
		default:
			return fmt.Sprintf("%s must be at least %s", field, fe.Param())
		}
	case "max":
		switch fe.Kind() {
		case reflect.String:
			return fmt.Sprintf("%s must be at most %s characters", field, fe.Param())
		case reflect.Slice, reflect.Array, reflect.Map:
			return fmt.Sprintf("%s must have at most %s items", field, fe.Param())
		default:
			return fmt.Sprintf("%s must be at most %s", field, fe.Param())
		}
	case "oneof":
		return fmt.Sprintf("%s must be one of: %s", field, fe.Param())
	case "uuid":
		return fmt.Sprintf("%s must be a valid UUID", field)
	default:
		rulesMu.Lock()
		rule, ok := rules[fe.Tag()]
		rulesMu.Unlock()
		if ok && rule.Message != nil {
			return rule.Message(field)
		}
		return fmt.Sprintf("%s failed validation: %s", field, fe.Tag())
	}
}
