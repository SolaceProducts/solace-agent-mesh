// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !unix && !windows

package str

import "os"

func tryLockFile(*os.File) error {
	return errFileLockUnsupported
}
