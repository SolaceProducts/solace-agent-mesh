// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"errors"
	"net/http"

	"github.com/SolaceProducts/solace-agent-mesh/internal/apicontract"
	platpkg "github.com/SolaceProducts/solace-agent-mesh/internal/platform"
	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

func keepUnfilledSentence(err error) error {
	ve, ok := entplatform.UnfilledDeployRefusal(err)
	if !ok {
		return err
	}
	return &apicontract.CallerError{Status: http.StatusUnprocessableEntity, Message: "The " + ve.Message, Details: ve.Details, Cause: err}
}

func failedDeployment(message, collection, id, field, reason, cause string) error {
	status, details := http.StatusBadGateway, map[string][]string(nil)
	if reason == entplatform.DeployFailureRejected {
		status = http.StatusUnprocessableEntity
		if cause != "" {
			details = map[string][]string{field: {cause}}
		}
	}
	return &apicontract.LocatedError{
		Err:      &apicontract.CallerError{Status: status, Message: message, Details: details, Cause: errors.New(cause)},
		Location: platpkg.APIV2Prefix + "/" + collection + "/" + id,
	}
}
