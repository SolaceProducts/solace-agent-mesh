// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package apiv2

import (
	"fmt"

	entplatform "github.com/SolaceProducts/solace-agent-mesh/internal/platform/admin"
)

type DeploymentFailure struct {
	Reason  string  `json:"reason" example:"runtime_error"`
	Message string  `json:"message" example:"The entrypoint runtime did not complete the change. Try again, and contact your administrator with the errorId if it fails again."`
	ErrorID *string `json:"errorId" example:"0190b2c5-3a7f-7a10-b2c4-5d6e7f809a1b"`
}

const deployFailureUnknown = "unknown"

var deployFailureMessages = map[string]string{
	entplatform.DeployFailureRejected:      "SAM cannot run this %s as it is configured on this server.",
	entplatform.DeployFailureInvalidConfig: "SAM could not build a runnable configuration from the %s's settings.",
	entplatform.DeployFailureRuntimeError:  "The %s runtime did not complete the change. Try again, and contact your administrator with the errorId if it fails again.",
	deployFailureUnknown:                   "The %s deployment failed.",
}

func deploymentFailure(kind, status, reason, errorID string) *DeploymentFailure {
	if status != entplatform.DeployStatusFailed {
		return nil
	}
	if _, known := deployFailureMessages[reason]; !known {
		reason = deployFailureUnknown
	}
	return &DeploymentFailure{
		Reason:  reason,
		Message: fmt.Sprintf(deployFailureMessages[reason], kind),
		ErrorID: NullIfZero(errorID),
	}
}
