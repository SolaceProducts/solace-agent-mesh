// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"cmp"
	"errors"
	"fmt"
	"maps"

	"github.com/SolaceProducts/solace-agent-mesh/internal/logging"
	"github.com/SolaceProducts/solace-agent-mesh/internal/taxonomy"
)

type ToolFailedDetail struct {
	Code       string `json:"code"`
	Message    string `json:"message"`
	StderrTail string `json:"stderr_tail,omitempty"`
	StackTrace string `json:"stack_trace,omitempty"`
	Guidance   string `json:"guidance,omitempty"`
}

type Result struct {
	Status string `json:"status"`

	Message string `json:"message"`

	Data map[string]any `json:"data,omitempty"`

	DataObjects []DataObject `json:"data_objects,omitempty"`

	ErrorCode string `json:"error_code,omitempty"`

	ValidationError string `json:"validation_error,omitempty"`

	FailedDetail *ToolFailedDetail `json:"-"`

	ErrorSite string `json:"-"`

	ErrorClass string `json:"-"`

	ApprovalRefusal ApprovalRefusal `json:"-"`

	OnDelivery func(deliver func(*Result)) `json:"-"`

	ImageBlocks []ImageBlock `json:"-"`

	LoadedSkillContentHash string `json:"-"`

	LLMData map[string]any `json:"-"`

	WithheldBytes int64 `json:"-"`

	DelegatedTask bool `json:"-"`

	TrustedPending TrustedPendingKind `json:"-"`

	ContentWindow *ContentWindow `json:"-"`
}

type TrustedPendingKind string

const (
	TrustedPendingToolApproval TrustedPendingKind = "tool_approval"
	TrustedPendingHILAsk       TrustedPendingKind = "hil_ask"
)

func WithTrustedPending(kind TrustedPendingKind) ResultOption {
	return func(r *Result) { r.TrustedPending = kind }
}

type ContentWindow struct {
	Filename  string `json:"filename"`
	Version   int    `json:"version"`
	FirstLine int    `json:"first_line"`
}

type ImageBlock struct {
	MIMEType string
	Data     []byte
}

type DataObject struct {
	Name string `json:"name"`

	Content []byte `json:"-"`

	ArtifactRef *ArtifactRef `json:"artifact_ref,omitempty"`

	MIMEType string `json:"mime_type"`

	Disposition DataDisposition `json:"disposition"`

	InlineMaxBytes int `json:"inline_max_bytes,omitempty"`

	Description string `json:"description,omitempty"`

	Preview string `json:"preview,omitempty"`

	Metadata map[string]any `json:"metadata,omitempty"`

	Tags []string `json:"tags,omitempty"`
}

type ArtifactRef struct {
	Filename  string `json:"filename"`
	Version   int    `json:"version"`
	SizeBytes int64  `json:"size_bytes"`
}

type DataDisposition string

const (
	DispositionAuto DataDisposition = "auto"

	DispositionArtifact DataDisposition = "artifact"

	DispositionInline DataDisposition = "inline"

	DispositionArtifactWithPreview DataDisposition = "artifact_with_preview"
)

const (
	StatusSuccess = "success"
	StatusError   = "error"
	StatusPartial = "partial"
	StatusPending = "pending"
	StatusFailed  = "failed"
)

const (
	ErrorCodeAuthRequired = taxonomy.ErrorCodeAuthRequired

	ErrorCodeToolFailed = taxonomy.ErrorCodeToolFailed

	ErrorCodeDelegatedTaskFailed = taxonomy.ErrorCodeDelegatedTaskFailed
)

func OK(message string, opts ...ResultOption) *Result {
	r := &Result{Status: StatusSuccess, Message: message}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

const AuthSignalUndeliverableMsg = "could not deliver the authorization prompt"

const WorkflowNodeAuthRefusalFormat = "tool %q requires interactive authorization, which is not supported from a workflow node"

const AuthSignalChannelMissingMsg = "authorization required but no signal channel available"

func AuthPromptChannelMissing() *Result {
	return ErrorFromCaller(AuthSignalChannelMissingMsg,
		WithErrorCode(DispatchErrorCode(taxonomy.DispatchClassUserInputUndeliverable)))
}

func AuthPromptUndelivered(err error) *Result {
	class := taxonomy.DispatchClassUserInputUndeliverable
	if _, ok := errors.AsType[unavailableInputError](err); ok {
		class = taxonomy.DispatchClassUserInputUnavailable
	}
	return ErrorFromCaller(fmt.Sprintf(AuthSignalUndeliverableMsg+": %v", err),
		WithErrorCode(DispatchErrorCode(class)), WithCause(err))
}

func UnavailableInput(err error) error { return unavailableInputError{err} }

type unavailableInputError struct{ error }

func (e unavailableInputError) Unwrap() error { return e.error }

func Error(message string, opts ...ResultOption) *Result {
	return errorAt(1, message, opts)
}

func ErrorFromCaller(message string, opts ...ResultOption) *Result {
	return errorAt(2, message, opts)
}

func errorAt(skip int, message string, opts []ResultOption) *Result {
	r := &Result{Status: StatusError, Message: message, ErrorSite: CallerSite(skip + 1)}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func Failed(message string, detail *ToolFailedDetail) *Result {
	return failedAt(1, message, detail)
}

func failedAt(skip int, message string, detail *ToolFailedDetail) *Result {
	return &Result{
		Status:       StatusFailed,
		Message:      message,
		ErrorCode:    ErrorCodeToolFailed,
		FailedDetail: detail,
		ErrorSite:    CallerSite(skip + 1),
	}
}

func Partial(message string, opts ...ResultOption) *Result {
	r := &Result{Status: StatusPartial, Message: message}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func Pending(message string, opts ...ResultOption) *Result {
	r := &Result{Status: StatusPending, Message: message}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

type ResultOption func(*Result)

func WithData(data map[string]any) ResultOption {
	return func(r *Result) { r.Data = data }
}

func WithDelegatedTask(task map[string]any) ResultOption {
	return func(r *Result) {
		r.Data = task
		r.DelegatedTask = true
	}
}

func WithDataObjects(objects ...DataObject) ResultOption {
	return func(r *Result) { r.DataObjects = objects }
}

func WithWithheldBytes(n int64) ResultOption {
	return func(r *Result) { r.WithheldBytes = max(n, 0) }
}

func WithheldNet(keptOut, handedOver int64) int64 {
	return max(keptOut-handedOver, 0)
}

func WithContentWindow(filename string, version, firstLine int) ResultOption {
	return func(r *Result) {
		r.ContentWindow = &ContentWindow{Filename: filename, Version: version, FirstLine: firstLine}
	}
}

func WithErrorCode(code string) ResultOption {
	return func(r *Result) { r.ErrorCode = code }
}

func AuthRequired(message string, opts ...ResultOption) *Result {
	r := &Result{Status: StatusError, Message: message, ErrorCode: ErrorCodeAuthRequired}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func ValidationErr(msg string) *Result {
	return &Result{
		Status:          StatusError,
		ValidationError: msg,
	}
}

func PublishedResultData(r *Result) any {
	if r == nil {
		return nil
	}
	if r.Status != StatusError && r.Status != StatusFailed && r.Status != StatusPending {
		if r.Data != nil {
			return r.Data
		}
		return r.Message
	}
	message, code := r.Message, r.ErrorCode
	if r.ValidationError != "" {
		message = cmp.Or(message, r.ValidationError)
		code = cmp.Or(code, DispatchErrorCode(taxonomy.DispatchClassValidation))
	}
	payload := map[string]any{"status": r.Status}
	if r.Data == nil || (message != "" && r.Status != StatusPending) {
		payload["message"] = message
	}
	if code != "" {
		payload["error_code"] = code
	}
	maps.Copy(payload, r.Data)
	return payload
}

func (r *Result) RedactCredentialsInURLs() *Result {
	if r == nil {
		return nil
	}
	r.Message = logging.RedactURLQueries(r.Message)
	r.ValidationError = logging.RedactURLQueries(r.ValidationError)
	return r
}
