// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !darwin && !windows

package desktop

import "runtime"

func collectPlatformFacts() platformFacts {
	return platformFacts{OS: runtime.GOOS, Arch: runtime.GOARCH}
}
