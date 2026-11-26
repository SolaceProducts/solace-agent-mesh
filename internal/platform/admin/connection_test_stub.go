// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build nosolace

package platform

import (
	"context"
	"fmt"
)

func defaultTestBrokerConnection(_ context.Context, _, _, _, _ string, _ bool) error {
	return fmt.Errorf("broker connection testing not available in this build")
}
