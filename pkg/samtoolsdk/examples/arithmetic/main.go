// Copyright Solace Corporation
//
// Use of this software is governed by the Apache License, Version 2.0
// (Apache-2.0) included in the LICENSE-APACHE file.

// arithmetic demonstrates typed numeric parameters.
// The LLM receives a JSON schema with "integer" and "number" types;
// the SDK deserializes them into Go int/float64 before calling the handler.
package main

import (
	"context"
	"fmt"
	"math"

	sdk "github.com/SolaceProducts/solace-agent-mesh/pkg/samtoolsdk"
)

// CalcParams drives schema generation. Non-pointer fields are required.
type CalcParams struct {
	A         float64 `json:"a"          desc:"First operand"`
	B         float64 `json:"b"          desc:"Second operand"`
	Precision int     `json:"precision"  desc:"Decimal places in the result (0–10)"`
}

func calculate(_ context.Context, p CalcParams, tc *sdk.ToolContext) (*sdk.Result, error) {
	if p.Precision < 0 || p.Precision > 10 {
		return sdk.Error("precision must be between 0 and 10"), nil
	}

	_ = tc.SendStatus(fmt.Sprintf("computing with precision=%d…", p.Precision))

	format := fmt.Sprintf("%%.%df", p.Precision)
	sum := fmt.Sprintf(format, p.A+p.B)
	product := fmt.Sprintf(format, p.A*p.B)
	sqrtA := fmt.Sprintf(format, math.Sqrt(math.Abs(p.A)))

	return sdk.OK(
		fmt.Sprintf("a=%v b=%v", p.A, p.B),
		sdk.WithData(map[string]any{
			"sum":     sum,
			"product": product,
			"sqrt_a":  sqrtA,
		}),
	), nil
}

func main() {
	sdk.Run(sdk.NewTool(
		"calculate",
		"Performs basic arithmetic on two numbers and returns sum, product, and sqrt(|a|)",
		calculate,
		sdk.WithInstructions("Use this tool when the user asks for numeric calculations."),
	))
}
