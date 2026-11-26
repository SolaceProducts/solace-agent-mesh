// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"errors"
	"fmt"
	"testing"
)

func TestDeployFailureReason(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{fmt.Errorf("deploy: %w", &ValidationError{Message: "refused"}), DeployFailureRejected},
		{&runtimeError{errors.New("instance failed to start")}, DeployFailureRuntimeError},
		{fmt.Errorf("marshal gateway config: %w", errors.New("bad yaml")), DeployFailureInvalidConfig},
	} {
		if got := deployFailureReason(tc.err); got != tc.want {
			t.Errorf("deployFailureReason(%v) = %q, want %q", tc.err, got, tc.want)
		}
	}
}
