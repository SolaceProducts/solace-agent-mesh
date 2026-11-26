// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop

package desktop

import "errors"

const platformAlertTitle = "Solace Agent Mesh cannot start"

func EnforcePlatformSupport() error {
	refusal := platformRefusal(collectPlatformFacts())
	if refusal == "" {
		return nil
	}
	showPlatformAlert(platformAlertTitle, refusal)
	return errors.New("unsupported platform: " + refusal)
}
