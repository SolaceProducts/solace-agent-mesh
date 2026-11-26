// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !unix

package str

import "os/exec"

func applyProcGroup(cmd *exec.Cmd) {}

func killProcessGroup(pid int) error { return nil }

func reapProcessGroup(_ int) {}
