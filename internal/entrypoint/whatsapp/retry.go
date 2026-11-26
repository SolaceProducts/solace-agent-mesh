// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package whatsapp

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"
)

const (
	codeAPITooManyCalls   = 4
	codeBusinessRateLimit = 80007
	codeThroughputReached = 130429
	codePairRateLimit     = 131056
	codeWindowClosed      = 131047
	headerBusinessUsage   = "X-Business-Use-Case-Usage"
	maxSendAttempts       = 4
	maxRetryWait          = 2 * time.Minute
)

const (
	codeOAuthException      = 190
	codeAPISession          = 102
	codeAPIPermissionDenied = 10
	codeCapabilityDenied    = 3
	permissionCodeMin       = 200
	permissionCodeMax       = 299
)

const codeUnknownObject = 100

const (
	subcodeTokenExpired = 463
	subcodeTokenInvalid = 467
)

var errResponseLost = errors.New("whatsapp response was lost after a 2xx")

var errUserFacingPOST = errors.New("whatsapp message POST")

var errRequestAccepted = errors.New("whatsapp message was accepted but its response was lost")

type graphErrorResponse struct {
	Error graphError `json:"error"`
}

type graphError struct {
	Message   string         `json:"message"`
	Code      int            `json:"code"`
	Subcode   int            `json:"error_subcode"`
	ErrorData graphErrorData `json:"error_data"`
	FBTraceID string         `json:"fbtrace_id"`
}

type graphErrorData struct {
	Details string `json:"details"`
}

type graphAPIError struct {
	Op          string
	HTTPStatus  int
	Attempts    int
	GraphErr    graphError
	Body        string
	UsageHeader string
}

func (e *graphAPIError) Error() string {
	detail := e.GraphErr.Message
	if detail == "" {
		detail = truncate(e.Body, 200)
	}
	if e.Attempts > 1 {
		return fmt.Sprintf("whatsapp %s failed after %d attempts (status %d, code %d): %s",
			e.Op, e.Attempts, e.HTTPStatus, e.GraphErr.Code, detail)
	}
	return fmt.Sprintf("whatsapp api status %d (code %d): %s", e.HTTPStatus, e.GraphErr.Code, detail)
}

func parseGraphError(body []byte) graphError {
	var resp graphErrorResponse
	_ = json.Unmarshal(body, &resp)
	return resp.Error
}

func safeGraphErrFields(err error) []any {
	class := string(graphFailureClassOf(err))
	if ge, ok := errors.AsType[*graphAPIError](err); ok {
		fields := []any{
			"httpStatus", ge.HTTPStatus,
			"metaErrorCode", ge.GraphErr.Code,
			"errorClass", class,
		}
		if ge.GraphErr.Subcode != 0 {
			fields = append(fields, "metaErrorSubcode", ge.GraphErr.Subcode)
		}
		return fields
	}
	return []any{"errorClass", class, "error", err}
}

func withoutRequestURL(err error) error {
	var ue *url.Error
	if errors.As(err, &ue) {
		return ue.Err
	}
	return err
}

type graphFailureClass string

const (
	classCredentials graphFailureClass = "credentials_rejected"
	classRateLimited graphFailureClass = "rate_limited"
	classTransient   graphFailureClass = "transient"
	classPermanent   graphFailureClass = "permanent"
	classUnverified  graphFailureClass = "auth_unverified"
	classTransport   graphFailureClass = "transport"
	classLocal       graphFailureClass = "local_fault"
	classCanceled    graphFailureClass = "canceled"
)

func classifyGraphFailure(httpStatus, code int) graphFailureClass {
	switch {
	case isCredentialFailure(code):
		return classCredentials
	case httpStatus == http.StatusTooManyRequests, isRateLimitCode(code):
		return classRateLimited
	case isCredentialStatus(httpStatus) && code == 0:
		return classUnverified
	case httpStatus >= http.StatusInternalServerError:
		return classTransient
	}
	return classPermanent
}

func graphFailureClassOf(err error) graphFailureClass {
	if ge, ok := errors.AsType[*graphAPIError](err); ok {
		return classifyGraphFailure(ge.HTTPStatus, ge.GraphErr.Code)
	}
	switch {
	case errors.Is(err, context.Canceled):
		return classCanceled
	case errors.Is(err, errNeverSent):
		return classLocal
	}
	return classTransport
}

func isCredentialFailure(code int) bool {
	if code >= permissionCodeMin && code <= permissionCodeMax {
		return true
	}
	switch code {
	case codeOAuthException, codeAPISession, codeAPIPermissionDenied, codeCapabilityDenied:
		return true
	}
	return false
}

func isCredentialStatus(httpStatus int) bool {
	return httpStatus == http.StatusUnauthorized || httpStatus == http.StatusForbidden
}

func isRateLimitCode(code int) bool {
	switch code {
	case codeAPITooManyCalls, codeBusinessRateLimit, codeThroughputReached, codePairRateLimit:
		return true
	}
	return false
}

type retryPolicy func(graphFailureClass) bool

func retryRateLimitedOnly(class graphFailureClass) bool {
	return class == classRateLimited
}

func retryTransient(class graphFailureClass) bool {
	return retryRateLimitedOnly(class) || class == classTransient
}

func classifySendError(err error) (code int, detail string, retryable bool, usageHeader string) {
	if errors.Is(err, errRequestAccepted) {
		return 0, err.Error(), false, ""
	}
	if errors.Is(err, errOutboundUnsendable) {
		return 0, err.Error(), false, ""
	}
	ge, ok := errors.AsType[*graphAPIError](err)
	if !ok {
		return 0, err.Error(), repeatCannotDuplicateTransportError(err), ""
	}
	detail = ge.GraphErr.ErrorData.Details
	if detail == "" {
		detail = ge.GraphErr.Message
	}
	return ge.GraphErr.Code, detail, retryRateLimitedOnly(classifyGraphFailure(ge.HTTPStatus, ge.GraphErr.Code)), ge.UsageHeader
}

func neverLeftTheWire(err error) bool {
	var opErr *net.OpError
	if errors.As(err, &opErr) && opErr.Op == "dial" {
		return true
	}
	var certErr *tls.CertificateVerificationError
	var headerErr tls.RecordHeaderError
	var alertErr tls.AlertError
	return errors.As(err, &certErr) || errors.As(err, &headerErr) || errors.As(err, &alertErr)
}

func repeatCannotDuplicateTransportError(err error) bool {
	return neverLeftTheWire(err) || !errors.Is(err, errUserFacingPOST)
}

type usageEntry struct {
	EstimatedTimeToRegainAccess int `json:"estimated_time_to_regain_access"`
}

func regainDelay(header string) (time.Duration, bool) {
	if header == "" {
		return 0, false
	}
	var usage map[string][]usageEntry
	if err := json.Unmarshal([]byte(header), &usage); err != nil {
		return 0, false
	}
	maxMinutes := 0
	for _, entries := range usage {
		for _, e := range entries {
			maxMinutes = max(maxMinutes, e.EstimatedTimeToRegainAccess)
		}
	}
	if maxMinutes <= 0 {
		return 0, false
	}
	return time.Duration(maxMinutes) * time.Minute, true
}

func retryDelay(usageHeader string, code, attempt int) time.Duration {
	if d, ok := regainDelay(usageHeader); ok {
		return min(d, maxRetryWait)
	}
	d := time.Duration(1<<(2*attempt)) * time.Second
	if code == codePairRateLimit {
		d = max(d, pairSendInterval)
	}
	return min(d, maxRetryWait)
}
