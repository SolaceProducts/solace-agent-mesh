// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import (
	"testing"

	"github.com/mark3labs/mcp-go/client/transport"
)

func TestMCPDiscoveryCapIsBelowTransportMessageBound(t *testing.T) {
	if mcpDiscoveryMaxResponseBytes >= transport.DefaultMaxMessageBytes {
		t.Errorf("discovery cap %d is not below mcp-go's transport message bound %d, so the transport would refuse a reply before the discovery cap names it",
			mcpDiscoveryMaxResponseBytes, transport.DefaultMaxMessageBytes)
	}
}
