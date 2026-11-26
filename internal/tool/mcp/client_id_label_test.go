// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import "testing"

func TestClientIDLabel(t *testing.T) {
	cases := []struct {
		name string
		cfg  ToolsetConfig
		want string
	}{
		{"host", ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://mcp.example.com/a/b?k=v#f"}, "mcp.example.com"},
		{"host and port", ToolsetConfig{ConnectionType: connTypeSSE, URL: "http://127.0.0.1:8443/mcp"}, "127.0.0.1_8443"},
		{"userinfo is dropped", ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://svc:pw@mcp.example.com/mcp"}, "mcp.example.com"},
		{"no scheme", ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "mcp.example.com/mcp"}, unnamedServerLabel},
		{"colon in the first path segment", ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "mcp.example.com:8443/mcp"}, unnamedServerLabel},
		{"unparsable", ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://exa mple.com/\x7f"}, unnamedServerLabel},
		{"userinfo with a slash", ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://user:p/ss@host/"}, unnamedServerLabel},
		{"userinfo with a question mark", ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://us:p?w@host:8443/x"}, unnamedServerLabel},
		{"userinfo with a hash", ToolsetConfig{ConnectionType: connTypeStreamableHTTP, URL: "https://tok#en@host/x"}, unnamedServerLabel},
		{"stdio command base name", ToolsetConfig{ConnectionType: connTypeStdio, Command: "/opt/tools/npx"}, "npx"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := clientIDLabel(&tc.cfg); got != tc.want {
				t.Errorf("clientIDLabel = %q, want %q", got, tc.want)
			}
		})
	}
}
