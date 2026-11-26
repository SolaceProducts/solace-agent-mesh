// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apierr

import "fmt"

type ValidationError struct {
	Message string
	Field   string
	Details map[string][]string
}

func (e *ValidationError) Error() string { return e.Message }

type NotFoundError struct {
	Resource string
	ID       string
}

func (e *NotFoundError) Error() string {
	return fmt.Sprintf("%s not found: %s", e.Resource, e.ID)
}

type ConflictError struct {
	Message    string
	LogMessage string
}

func (e *ConflictError) Error() string {
	if e.LogMessage != "" {
		return e.LogMessage
	}
	return e.Message
}

type BusinessRuleViolationError struct {
	Code    string
	Message string
}

func (e *BusinessRuleViolationError) Error() string {
	if e.Code != "" {
		return fmt.Sprintf("%s: %s", e.Code, e.Message)
	}
	return e.Message
}

type ForbiddenError struct {
	Message string
}

func (e *ForbiddenError) Error() string { return e.Message }

type UnavailableError struct {
	Message string
	Cause   error
}

func (e *UnavailableError) Error() string { return e.Message }

func (e *UnavailableError) Unwrap() error { return e.Cause }

type ExternalServiceError struct {
	Service string
	Message string
	Cause   error
}

func (e *ExternalServiceError) Error() string {
	if e.Service != "" {
		return fmt.Sprintf("%s: %s", e.Service, e.Message)
	}
	return e.Message
}

func (e *ExternalServiceError) Unwrap() error { return e.Cause }
