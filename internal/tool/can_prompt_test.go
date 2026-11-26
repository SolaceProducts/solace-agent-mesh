// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package tool

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/SolaceProducts/solace-agent-mesh/internal/a2a"
)

func TestContext_CanPromptUser(t *testing.T) {
	waiter := func(context.Context, string, string, json.RawMessage, time.Duration) *a2a.UserInputResponseData {
		return nil
	}

	tests := []struct {
		name string
		ctx  *Context
		want bool
	}{
		{"nil context", nil, false},
		{
			"flag true wins",
			&Context{GatewayCapabilities: map[string]bool{CapInteractiveUserInput: true}},
			true,
		},
		{
			"flag false wins even with waiter",
			&Context{
				GatewayCapabilities: map[string]bool{CapInteractiveUserInput: false},
				WaitForUserInput:    waiter,
			},
			false,
		},
		{
			"absent flag + waiter falls open",
			&Context{WaitForUserInput: waiter},
			true,
		},
		{
			"absent flag + no waiter denies",
			&Context{},
			false,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.ctx.CanPromptUser(); got != tc.want {
				t.Errorf("CanPromptUser() = %v, want %v", got, tc.want)
			}
		})
	}
}
