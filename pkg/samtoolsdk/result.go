// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

package samtoolsdk

import (
	"encoding/base64"
	"encoding/json"
	"unicode/utf8"
)

type Result struct {
	// Status is "success", "error", or "partial".
	Status string `json:"status"`

	// Message is a human-readable summary of the result.
	Message string `json:"message"`

	// Data is inline key-value data returned directly to the LLM.
	Data map[string]any `json:"data,omitempty"`

	// DataObjects are content items that may be stored as artifacts.
	DataObjects []DataObject `json:"data_objects,omitempty"`

	// ErrorCode is a machine-readable error code.
	ErrorCode string `json:"error_code,omitempty"`

	// Signals are side-channel events for the user interface, such as
	// citations. The framework never shows them to the LLM.
	Signals []Signal `json:"signals,omitempty"`
}

// Signal is a typed event a tool asks the framework to publish to the user
// interface alongside its result. The framework accepts only the types it
// allowlists, decodes Data strictly against that type's schema, and drops
// anything else.
type Signal struct {
	// Type names the signal, for example SignalRAGInfoUpdate.
	Type string `json:"type"`

	// Data is the signal payload, encoded as JSON.
	Data any `json:"data"`
}

// SignalRAGInfoUpdate carries the sources a search tool used, which the user
// interface renders as citations. Must match a2a.SignalRAGInfoUpdate.
const SignalRAGInfoUpdate = "rag_info_update"

type DataObject struct {
	// Name is the filename if stored as artifact.
	Name string

	// Content is the actual data.
	Content []byte

	// MIMEType identifies the content format.
	MIMEType string

	// Disposition controls how the framework handles this object.
	Disposition DataDisposition

	// InlineMaxBytes, when > 0, asks the framework to inline this object's
	// content up to the given size (capped by the framework's absolute inline
	// ceiling) instead of using the default inline cap. Lets a tool force a
	// larger must-read result inline, avoiding an immediate load_artifact.
	InlineMaxBytes int

	// Description is stored in artifact metadata.
	Description string

	// Preview is a custom preview for ArtifactWithPreview disposition.
	Preview string

	// Metadata is additional key-value pairs stored with the artifact.
	Metadata map[string]any

	// Tags categorize the artifact (e.g. TagWorking). A tool sets these to
	// classify an emitted artifact; the framework persists them into the
	// artifact's companion metadata, where the gateway/UI filter reads them.
	Tags []string
}

// TagWorking marks an artifact as an intermediate/scratch file the user
// doesn't need in their main artifact list. It stays fully accessible but is
// hidden from listings by default. Must match a2a.ArtifactTagWorking.
const TagWorking = "__working"

type DataDisposition string

const (
	// DispositionAuto lets the framework decide based on content size and type.
	DispositionAuto DataDisposition = "auto"

	// DispositionArtifact always stores as artifact, returns reference to LLM.
	DispositionArtifact DataDisposition = "artifact"

	// DispositionInline always returns content directly to the LLM.
	DispositionInline DataDisposition = "inline"

	// DispositionArtifactWithPreview stores as artifact and includes a preview.
	DispositionArtifactWithPreview DataDisposition = "artifact_with_preview"
)

const (
	StatusSuccess = "success"
	StatusError   = "error"
	StatusPartial = "partial"
	// StatusPending indicates the tool is waiting for an external response
	// (e.g. human approval). The STR worker intercepts this status and
	// publishes a UserInputRequest if HIL metadata is present.
	StatusPending = "pending"
)

const (
	// ErrorCodeAuthRequired indicates the tool needs OAuth authentication.
	// When the framework sees this error code, it triggers the OAuth flow
	// instead of returning the error to the LLM.
	ErrorCodeAuthRequired = "AUTH_REQUIRED"
)

// AuthRequired creates an error Result indicating the tool needs OAuth authentication.
// The framework intercepts this error code and triggers the OAuth flow.
// Use this when GetAuthToken() returns an empty string and the tool requires auth.
func AuthRequired(message string) *Result {
	return &Result{
		Status:    StatusError,
		Message:   message,
		ErrorCode: ErrorCodeAuthRequired,
	}
}

func OK(message string, opts ...ResultOption) *Result {
	r := &Result{Status: StatusSuccess, Message: message}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func Error(message string, opts ...ResultOption) *Result {
	r := &Result{Status: StatusError, Message: message}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

func Partial(message string, opts ...ResultOption) *Result {
	r := &Result{Status: StatusPartial, Message: message}
	for _, opt := range opts {
		opt(r)
	}
	return r
}

// Pending creates a pending Result indicating the tool is waiting for an
// external response (e.g. human-in-the-loop approval). The Data field
// should contain HIL metadata (hil_request_id, hil_source, etc.) so the
// STR worker can publish the UserInputRequest on the agent's behalf.
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

func WithDataObjects(objects ...DataObject) ResultOption {
	return func(r *Result) { r.DataObjects = objects }
}

func WithErrorCode(code string) ResultOption {
	return func(r *Result) { r.ErrorCode = code }
}

// WithSignals attaches signals for the framework to publish to the user
// interface, replacing any set by an earlier option. The LLM never sees them.
func WithSignals(signals ...Signal) ResultOption {
	return func(r *Result) { r.Signals = signals }
}

const (
	wireSchema        = "ToolResult"
	wireSchemaVersion = "1.0"
)

type wireResult struct {
	Schema        string           `json:"_schema"`
	SchemaVersion string           `json:"_schema_version"`
	Status        string           `json:"status"`
	Message       string           `json:"message"`
	Data          map[string]any   `json:"data,omitempty"`
	DataObjects   []wireDataObject `json:"data_objects,omitempty"`
	ErrorCode     *string          `json:"error_code"`
	Signals       []Signal         `json:"signals,omitempty"`
}

type wireDataObject struct {
	Name           string         `json:"name"`
	Content        *string        `json:"content"`
	IsBinary       bool           `json:"is_binary"`
	MIMEType       string         `json:"mime_type"`
	Disposition    string         `json:"disposition"`
	InlineMaxBytes int            `json:"inline_max_bytes,omitempty"`
	Description    *string        `json:"description"`
	Preview        *string        `json:"preview"`
	Metadata       map[string]any `json:"metadata"`
	Tags           []string       `json:"tags,omitempty"`
}

func marshalWire(r *Result) ([]byte, error) {
	wr := wireResult{
		Schema:        wireSchema,
		SchemaVersion: wireSchemaVersion,
		Status:        r.Status,
		Message:       r.Message,
		Data:          r.Data,
		ErrorCode:     nilIfEmpty(r.ErrorCode),
		Signals:       r.Signals,
	}

	for _, do := range r.DataObjects {
		wdo := wireDataObject{
			Name:           do.Name,
			MIMEType:       do.MIMEType,
			Disposition:    string(do.Disposition),
			InlineMaxBytes: do.InlineMaxBytes,
			Description:    nilIfEmpty(do.Description),
			Preview:        nilIfEmpty(do.Preview),
			Metadata:       do.Metadata,
			Tags:           do.Tags,
		}

		if do.Content != nil {
			if utf8.Valid(do.Content) {
				s := string(do.Content)
				wdo.Content = &s
				wdo.IsBinary = false
			} else {
				s := base64.StdEncoding.EncodeToString(do.Content)
				wdo.Content = &s
				wdo.IsBinary = true
			}
		}

		wr.DataObjects = append(wr.DataObjects, wdo)
	}

	return json.Marshal(wr)
}

func nilIfEmpty(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}
