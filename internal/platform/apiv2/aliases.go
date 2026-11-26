// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"context"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
)

type (
	Instant        = apicontract.Instant
	JSONObject     = apicontract.JSONObject
	Patch[T any]   = apicontract.Patch[T]
	Operation      = apicontract.Operation
	OperationError = apicontract.OperationError
	Handler        = apicontract.Handler
	FormPart       = apicontract.FormPart

	List[T any]     = apicontract.List[T]
	Page            = apicontract.Page
	SortOrder       = apicontract.SortOrder
	UploadedFile    = apicontract.UploadedFile
	RawBody         = apicontract.RawBody
	RequestOrigin   = apicontract.RequestOrigin
	IDRequest       = apicontract.IDRequest
	StrictRequest   = apicontract.StrictRequest
	Accepted        = apicontract.Accepted
	Download        = apicontract.Download
	FileResponse    = apicontract.FileResponse
	HTMLPage        = apicontract.HTMLPage
	CallerError     = apicontract.CallerError
	LocatedError    = apicontract.LocatedError
	RetryAfterError = apicontract.RetryAfterError
)

const (
	octetStream = "application/octet-stream"
	anyFileType = "*/*"

	operationPending    = apicontract.OperationPending
	operationInProgress = apicontract.OperationInProgress
	operationSucceeded  = apicontract.OperationSucceeded
	operationFailed     = apicontract.OperationFailed
	operationCancelled  = apicontract.OperationCancelled
	operationResource   = apicontract.OperationResource

	defaultPageSize      = apicontract.DefaultPageSize
	maxPageSize          = apicontract.MaxPageSize
	maxPageNumber        = apicontract.MaxPageNumber
	msgInvalidBody       = apicontract.MsgInvalidBody
	msgValidationFailed  = apicontract.MsgValidationFailed
	msgInvalidPathParam  = apicontract.MsgInvalidPathParam
	msgInvalidQueryParam = apicontract.MsgInvalidQueryParam
	msgBodyTooLarge      = apicontract.MsgBodyTooLarge
	msgBodyUnreadable    = apicontract.MsgBodyUnreadable
)

func Handle[Req, Resp any](fn func(context.Context, Req) (Resp, error), failure string) Handler {
	return apicontract.Handle(fn, failure)
}

func ListOf[S, T any](resp *apicontract.PaginatedResponse[S], to func(*S) T) List[T] {
	return apicontract.ListOf(resp, to)
}

func PageOf[T any](items []T, p Page) List[T] { return apicontract.PageOf(items, p) }

func InstantOf[T ~int64](t *T) Instant { return apicontract.InstantOf(t) }

func NullIfZero[T comparable](v T) *T { return apicontract.NullIfZero(v) }

func IsErr[T error](err error) bool { return apicontract.IsErr[T](err) }

var (
	ClassifyServiceError = apicontract.ClassifyServiceError
	IsCanonicalUUID      = apicontract.IsCanonicalUUID
	RetryAfter           = apicontract.RetryAfter
	WriteServiceError    = apicontract.WriteServiceError
	UnderValues          = apicontract.UnderValues
	CheckUUIDQuery       = apicontract.CheckUUIDQuery
	CheckOneOfQuery      = apicontract.CheckOneOfQuery
	OneOfQueryError      = apicontract.OneOfQueryError
	QueryError           = apicontract.QueryError
	CheckZipName         = apicontract.CheckZipName
	NullIfEmpty          = apicontract.NullIfEmpty
	RequiredPatch        = apicontract.RequiredPatch
)
