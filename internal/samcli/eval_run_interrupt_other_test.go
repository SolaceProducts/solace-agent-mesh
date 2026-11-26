// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build !unix

package samcli

const canInterruptSelf = false

func interruptSelf() {}
