// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"errors"
	"maps"
	"net/http"
	"slices"
	"strings"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
	"github.com/SolaceProducts/solace-agent-mesh/internal/platform/apierr"
)

func rbacError(err error) error {
	if sentence, ok := entplatform.RBACConflictSentence(err); ok {
		return &CallerError{Status: http.StatusConflict, Message: sentence, Cause: err}
	}
	return err
}

func onRoleIDs(err error) error {
	e, ok := errors.AsType[*apierr.ValidationError](err)
	if !ok || e.Details["roleId"] == nil {
		return err
	}
	details := maps.Clone(e.Details)
	details["roleIds"] = append(details["roleIds"], details["roleId"]...)
	delete(details, "roleId")
	return &apierr.ValidationError{Message: e.Message, Field: e.Field, Details: details}
}

func checkOneOf(name, value string, values ...string) error {
	if value == "" || slices.Contains(values, value) {
		return nil
	}
	return QueryError(http.StatusUnprocessableEntity, name, "must be one of "+strings.Join(values, ", "))
}

func listQuery(p Page, s SortOrder) entplatform.ListQuery {
	return entplatform.ListQuery{PageNumber: p.Number, PageSize: p.Size, Sort: s.Field, Desc: s.Desc}
}
