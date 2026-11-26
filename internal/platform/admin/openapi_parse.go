// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"

	"github.com/SolaceProducts/solace-agent-mesh/internal/auth"
	"github.com/SolaceProducts/solace-agent-mesh/internal/rbac"
	openapitool "github.com/SolaceProducts/solace-agent-mesh/internal/tool/openapi"
)

const (
	openAPIParseMaxOperations = 200
	openAPIParseTimeout       = 8 * time.Second
	openAPIParseAllowListNote = "A workflow tool node on a connector created from a specification URL needs the operation's operationId listed in the connector's allow_list; toolName can be a shortened form that does not match."
)

var errOpenAPIDetectorUnavailable = errors.New("OpenAPI auth detector not available")

func (d *OpenAPIAuthDetector) ParseSpec(ctx context.Context, specURL string, only []string) (*OpenAPIParseResponse, error) {
	ctx, cancel := context.WithTimeout(ctx, openAPIParseTimeout)
	defer cancel()
	raw, err := d.FetchSpec(ctx, specURL)
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 {
		return nil, &ValidationError{Message: "The specification URL returned an empty document."}
	}
	authResp, err := d.DetectAuth(ctx, "", raw)
	if err != nil {
		return nil, err
	}
	operations, err := openapitool.DescribeOperations(ctx, raw, nil, nil)
	if err != nil {
		return parseResponseWithoutOperations(authResp, operationsErrorReason(err)), nil
	}
	clashing, err := openapitool.ClashingOperationIDs(ctx, raw, only, nil)
	if err != nil {
		return parseResponseWithoutOperations(authResp, operationsErrorReason(err)), nil
	}
	resp := newOpenAPIParseResponse(authResp, filterOperations(operations, only))
	if len(clashing) > 0 {
		resp.OperationsWarning = operationsWarningIDClash
		resp.ClashingOperationIDs = clashing
	}
	return resp, nil
}

func parseResponseWithoutOperations(authResp *OpenAPIDetectAuthResponse, reason string) *OpenAPIParseResponse {
	resp := newOpenAPIParseResponse(authResp, nil)
	resp.OperationsError = reason
	return resp
}

const (
	operationsErrorExternalRefs = "the specification refers to other files, which are not followed"
	operationsErrorUnreadable   = "the specification is not valid OpenAPI for reading operations"
	operationsErrorTimeout      = "the specification took too long to read: it may be too large; ask the user to upload it on the Connectors page"
	operationsWarningIDClash    = "operationIds clash once snake-cased: the clashing operations can't back a tool; list only other operations in the allow_list"
)

var openAPIParseOperationsErrors = []string{
	operationsErrorExternalRefs,
	operationsErrorUnreadable,
	operationsErrorTimeout,
}

var openAPIParseOperationsWarnings = []string{
	operationsWarningIDClash,
}

func OpenAPIParseOperationsErrors() []string {
	return slices.Clone(openAPIParseOperationsErrors)
}

func OpenAPIParseOperationsWarnings() []string {
	return slices.Clone(openAPIParseOperationsWarnings)
}

func operationsErrorReason(err error) string {
	switch {
	case errors.Is(err, context.DeadlineExceeded):
		return operationsErrorTimeout
	case strings.Contains(err.Error(), "external reference"):
		return operationsErrorExternalRefs
	default:
		return operationsErrorUnreadable
	}
}

func filterOperations(operations []openapitool.OperationDescription, only []string) []openapitool.OperationDescription {
	if len(only) == 0 {
		return operations
	}
	matched := make([]openapitool.OperationDescription, 0, len(only))
	for _, op := range operations {
		if slices.ContainsFunc(only, func(entry string) bool { return openapitool.EntryNamesOperation(entry, op.OperationID) }) {
			matched = append(matched, op)
		}
	}
	return matched
}

func newOpenAPIParseResponse(authResp *OpenAPIDetectAuthResponse, operations []openapitool.OperationDescription) *OpenAPIParseResponse {
	shown := slices.Clone(operations)
	if len(shown) > openAPIParseMaxOperations {
		shown = shown[:openAPIParseMaxOperations]
	}
	for i := range shown {
		shown[i].Summary = truncateDescription(shown[i].Summary)
	}
	return &OpenAPIParseResponse{
		OpenAPIDetectAuthResponse: *authResp,
		Operations:                apicontract.NonNilList(shown),
		OperationsTotal:           len(operations),
		Truncated:                 len(operations) > len(shown),
		Note:                      openAPIParseAllowListNote,
	}
}

func (m *ManageablePlatform) handleOpenAPIParse(ctx context.Context, body json.RawMessage, identity *auth.Identity) (any, error) {
	if err := requireIdentityScope(identity, rbac.ScopeConnectorRead); err != nil {
		return nil, err
	}
	if m.deps.AuthDetector == nil {
		return nil, errOpenAPIDetectorUnavailable
	}
	var req OpenAPIParseRequest
	if err := json.Unmarshal(body, &req); err != nil || strings.TrimSpace(req.SpecURL) == "" {
		return nil, &ValidationError{Message: "connectors/openapi/parse needs a body with a non-empty specUrl"}
	}
	resp, err := m.deps.AuthDetector.ParseSpec(ctx, req.SpecURL, req.Operations)
	return resp, withLoggableCause(err)
}
