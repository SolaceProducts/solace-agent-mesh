// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package email

import (
	"fmt"
	"time"
)

type AllowlistError struct {
	Recipient      string
	Domain         string
	AllowedDomains []string
}

func (e *AllowlistError) Error() string {
	return fmt.Sprintf(
		"recipient %q rejected: domain %q not in allowlist. Allowed domains: %v",
		e.Recipient, e.Domain, e.AllowedDomains,
	)
}

type ThrottleError struct {
	Limit      int
	Window     string
	RetryAfter time.Duration
}

func (e *ThrottleError) Error() string {
	return fmt.Sprintf(
		"rate limit exceeded: %d emails per %s. Retry after %v",
		e.Limit, e.Window, e.RetryAfter.Round(time.Second),
	)
}

type SMTPError struct {
	Code    int
	Message string
}

func (e *SMTPError) Error() string {
	return fmt.Sprintf("SMTP error %d: %s", e.Code, e.Message)
}

type AttachmentError struct {
	Filename string
	Size     int64
	MaxSize  int64
	Reason   string
}

func (e *AttachmentError) Error() string {
	if e.Reason == "not_found" {
		return fmt.Sprintf("attachment %q not found", e.Filename)
	}
	if e.Reason == "total_exceeded" {
		return fmt.Sprintf(
			"total attachment size exceeds limit: %d bytes (max %d bytes)",
			e.Size, e.MaxSize,
		)
	}
	return fmt.Sprintf(
		"attachment %q exceeds size limit: %d bytes (max %d bytes)",
		e.Filename, e.Size, e.MaxSize,
	)
}

type AuthError struct {
	Message string
}

func (e *AuthError) Error() string {
	return fmt.Sprintf("authentication failed: %s", e.Message)
}

type ConnectionError struct {
	Host    string
	Port    int
	Message string
}

func (e *ConnectionError) Error() string {
	return fmt.Sprintf("connection to %s:%d failed: %s", e.Host, e.Port, e.Message)
}
