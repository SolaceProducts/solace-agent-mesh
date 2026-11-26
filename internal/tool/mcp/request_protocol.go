// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	mcpclient "github.com/mark3labs/mcp-go/client"
	"github.com/mark3labs/mcp-go/client/transport"
	mcpgo "github.com/mark3labs/mcp-go/mcp"
	"github.com/maximhq/bifrost/core/schemas"
)

type callInfo struct {
	clientInfo *mcpgo.Implementation
	def        *mcpgo.Tool
}

func bifrostToolDef(tool schemas.ChatTool) *mcpgo.Tool {
	if tool.Function == nil || tool.Function.Parameters == nil {
		return nil
	}
	schema, err := json.Marshal(tool.Function.Parameters)
	if err != nil {
		return nil
	}
	return &mcpgo.Tool{Name: tool.Function.Name, RawInputSchema: schema}
}

func addProtocolMeta(conn *mcpclient.Client, params *mcpgo.CallToolParams, headers http.Header, call callInfo) (http.Header, error) {
	version := conn.ProtocolVersion()
	if !mcpgo.IsModernProtocol(version) {
		return headers, nil
	}

	meta := &mcpgo.Meta{}
	meta.SetProtocolVersion(version)
	meta.SetClientCapabilities(conn.GetClientCapabilities())
	if call.clientInfo != nil {
		meta.SetClientInfo(*call.clientInfo)
	}
	params.Meta = meta

	encoded, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("encode tools/call params: %w", err)
	}
	out := headers.Clone()
	if out == nil {
		out = make(http.Header)
	}
	for name, value := range mcpgo.StandardHeaders(version, mcpgo.MethodToolsCall, encoded) {
		out.Set(name, value)
	}
	if call.def != nil {
		for name, value := range mcpgo.GenerateParamHeaders(call.def, encoded) {
			out.Set(name, value)
		}
	}
	return out, nil
}

func directClientInfo(cfg *ToolsetConfig) mcpgo.Implementation {
	return mcpgo.Implementation{Name: "sam-mcp-" + clientIDLabel(cfg), Version: "1.0.0"}
}

const discoverProbeTimeout = 5 * time.Second

func legacyVersionOffered(err error) (string, bool) {
	var unsupported mcpgo.UnsupportedProtocolVersionError
	if !errors.As(err, &unsupported) {
		return "", false
	}
	version := mcpgo.NegotiateMutuallySupportedVersion(unsupported.Supported)
	return version, version != "" && !mcpgo.IsModernProtocol(version)
}

func initializeDirect(ctx context.Context, client *mcpclient.Client, cfg *ToolsetConfig, version string) error {
	if version == "" {
		return InitializeServed(ctx, client, directClientInfo(cfg))
	}
	_, err := client.Initialize(ctx, directInitializeRequest(cfg, version))
	if version, legacyOnly := legacyVersionOffered(err); legacyOnly {
		_, err = client.Initialize(ctx, directInitializeRequest(cfg, version))
	}
	return err
}

func directInitializeRequest(cfg *ToolsetConfig, version string) mcpgo.InitializeRequest {
	return initializeRequest(directClientInfo(cfg), version)
}

func initializeRequest(info mcpgo.Implementation, version string) mcpgo.InitializeRequest {
	return mcpgo.InitializeRequest{
		Params: mcpgo.InitializeParams{
			ProtocolVersion: version,
			Capabilities:    mcpgo.ClientCapabilities{},
			ClientInfo:      info,
		},
	}
}

func InitializeServed(ctx context.Context, client *mcpclient.Client, info mcpgo.Implementation) error {
	_, err := client.Initialize(ctx, initializeRequest(info, mcpgo.LATEST_PROTOCOL_VERSION))
	if err == nil {
		return nil
	}
	version, legacyOnly := legacyVersionOffered(err)
	if !legacyOnly {
		if !ListsNoModernVersion(err) {
			return err
		}
		probeCtx, cancel := context.WithTimeout(ctx, discoverProbeTimeout)
		probed, probeErr := legacyVersionServedOn(probeCtx, client.GetTransport(), info)
		cancel()
		if probeErr != nil || probed == "" {
			return err
		}
		version = probed
		if httpConn, ok := client.GetTransport().(transport.HTTPConnection); ok {
			httpConn.SetProtocolVersion(version)
		}
	}
	_, err = client.Initialize(ctx, initializeRequest(info, version))
	return err
}

const noModernVersionListed = "server/discover result lists no modern version"

func ListsNoModernVersion(err error) bool {
	return strings.Contains(err.Error(), noModernVersionListed)
}

func legacyVersionServedOn(ctx context.Context, tr transport.Interface, info mcpgo.Implementation) (string, error) {
	version := mcpgo.LATEST_PROTOCOL_VERSION
	meta := &mcpgo.Meta{}
	meta.SetProtocolVersion(version)
	meta.SetClientCapabilities(mcpgo.ClientCapabilities{})
	meta.SetClientInfo(info)
	params := mcpgo.DiscoverParams{Meta: meta}
	encoded, err := json.Marshal(params)
	if err != nil {
		return "", fmt.Errorf("encode server/discover params: %w", err)
	}
	header := make(http.Header)
	for name, value := range mcpgo.StandardHeaders(version, mcpgo.MethodServerDiscover, encoded) {
		header.Set(name, value)
	}
	if httpConn, ok := tr.(transport.HTTPConnection); ok {
		httpConn.SetProtocolVersion(version)
	}
	resp, err := tr.SendRequest(ctx, transport.JSONRPCRequest{
		JSONRPC: mcpgo.JSONRPC_VERSION,
		ID:      mcpgo.NewRequestId(requestIDCounter.Add(1)),
		Method:  string(mcpgo.MethodServerDiscover),
		Params:  params,
		Header:  header,
	})
	if err != nil {
		return "", err
	}
	if resp.Error != nil {
		offered, legacyOnly := legacyVersionOffered(resp.Error.AsError())
		if !legacyOnly {
			return "", nil
		}
		return offered, nil
	}
	var result struct {
		SupportedVersions []string        `json:"supportedVersions"`
		Capabilities      json.RawMessage `json:"capabilities"`
	}
	if err := json.Unmarshal(resp.Result, &result); err != nil {
		return "", fmt.Errorf("decode server/discover result: %w", err)
	}
	if !isDiscoveryResult(result.SupportedVersions, result.Capabilities) ||
		mcpgo.IsModernProtocol(mcpgo.NegotiateMutuallySupportedVersion(result.SupportedVersions)) {
		return "", nil
	}
	return newestLegacyVersion(result.SupportedVersions), nil
}

func isDiscoveryResult(supportedVersions []string, capabilities json.RawMessage) bool {
	return supportedVersions != nil && len(capabilities) > 0 && string(capabilities) != "null"
}

func newestLegacyVersion(supported []string) string {
	legacy := mcpgo.LegacyProtocolVersions()
	for _, version := range legacy {
		if slices.Contains(supported, version) {
			return version
		}
	}
	return legacy[0]
}
