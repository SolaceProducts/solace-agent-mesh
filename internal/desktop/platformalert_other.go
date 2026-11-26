// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build desktop && !darwin && !windows

package desktop

import (
	"fmt"
	"os"
)

func showPlatformAlert(title, message string) {
	fmt.Fprintf(os.Stderr, "%s: %s\n", title, message)
}
