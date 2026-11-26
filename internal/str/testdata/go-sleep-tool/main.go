// Test-only Go STR tool that sleeps for a requested duration, observing
// context cancellation. Used by the cancel-mid-tool integration scenario
// to provide a deterministic in-flight execution window the harness can
// cancel during.
//
// Implemented as a Go STR tool (no Python venv dependency) using the
// pkg/samtoolsdk; sdk.Run handles the --schema, runner_args.json, and
// cancel-via-ctx wiring the worker uses.
package main

import (
	"context"
	"fmt"
	"time"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

type SleepParams struct {
	DurationMS int `json:"duration_ms" desc:"Sleep duration in milliseconds"`
}

func sleepTool(ctx context.Context, p SleepParams, _ *sdk.ToolContext) (*sdk.Result, error) {
	d := time.Duration(p.DurationMS) * time.Millisecond
	select {
	case <-time.After(d):
		return sdk.OK(fmt.Sprintf("slept for %d ms", p.DurationMS)), nil
	case <-ctx.Done():
		// Worker delivered a cancel — propagate so the runtime sees this as
		// a cancellation rather than as a successful tool completion.
		return nil, ctx.Err()
	}
}

func main() {
	sdk.Run(
		sdk.NewTool("sleep_tool", "Sleeps for the requested number of milliseconds (test-only).", sleepTool),
	)
}
