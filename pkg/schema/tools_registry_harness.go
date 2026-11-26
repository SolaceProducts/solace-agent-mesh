// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

//go:build harness

package schema

func init() {
	ValidBuiltinGroups["lifecycle_harness_toolset"] = BuiltinGroupInfo{
		Tools: "**echo_builtin** (lifecycle-test fingerprint built-in)",
		Usage: "Internal — lifecycle test harness only.",
	}
}
