// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

func (cfg *ToolsetConfig) RequiresManifestForAuth() bool {
	return cfg.ConnectionType == connTypeStreamableHTTP && cfg.AuthConfig != nil && len(cfg.Manifest) == 0
}
