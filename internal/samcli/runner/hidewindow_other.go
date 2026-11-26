// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package runner

import "os/exec"

func hideConsoleWindow(*exec.Cmd) {
}
