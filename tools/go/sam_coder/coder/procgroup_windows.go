// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build windows

package coder

import "os/exec"

func configureProcessGroup(*exec.Cmd) {}

func killProcessGroup(*exec.Cmd) {}
