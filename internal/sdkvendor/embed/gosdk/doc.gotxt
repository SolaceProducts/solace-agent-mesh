// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

// Package samtoolsdk provides a standalone SDK for developing Go tools that run
// in the Solace Agent Mesh Secure Tool Runtime (STR).
//
// Tool authors import this package to define tools with typed parameter structs,
// artifact handling, status updates, LLM callbacks, and structured results.
// Tools are compiled into standalone binaries that the STR spawns as subprocesses.
//
// Quick start:
//
//	package main
//
//	import (
//	    "context"
//	    sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
//	)
//
//	type GreetParams struct {
//	    Name string `json:"name" desc:"The person to greet"`
//	}
//
//	func greet(ctx context.Context, p GreetParams, tc *sdk.ToolContext) (*sdk.Result, error) {
//	    tc.SendStatus("Greeting " + p.Name + "...")
//	    return sdk.OK("Hello, " + p.Name), nil
//	}
//
//	func main() {
//	    sdk.Run(sdk.NewTool("greet", "Greets a person", greet))
//	}
//
// Parameter fields are required by default. To make one optional, use a pointer
// type (e.g. *string) and nil-check it in your handler. Do not use
// json:",omitempty" on a non-pointer scalar for this — ,omitempty is an output
// encoding hint, not an input-optionality signal, and the SDK emits a startup
// warning on every invocation when the two disagree. See the package README for
// details.
package samtoolsdk
