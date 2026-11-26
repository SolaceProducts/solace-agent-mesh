// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package samversion

import (
	"fmt"
	"os"
)

func PrintVersionAndExit(name string, requested bool) {
	if !requested {
		return
	}
	fmt.Println(name + " version " + Version)
	os.Exit(0)
}

var Version = "dev"

var STRVersion = "dev"

var AppIdentifier = "com.solace.agentmesh"

const MinClientVersion = "v2.561.0"

const APIVersion = "2026-11-27"
