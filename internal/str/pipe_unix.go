// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !windows

package str

import "syscall"

func mkfifo(path string, mode uint32) error {
	return syscall.Mkfifo(path, mode)
}
