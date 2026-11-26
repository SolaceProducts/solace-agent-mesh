// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

package platform

import "slices"

const (
	reasonNotBindable        = "not bindable by built agents"
	reasonNotAnnounced       = "not yet announced by a tool runtime"
	reasonPackageDiscovering = "package still being discovered"
	reasonPackageFailed      = "package discovery failed"
	reasonTypeDisabled       = "connector type disabled on this deployment"
	reasonNoStoredSpec       = "parameters unknown: no stored specification"
	reasonNeedsAllowList     = "not enumerable: ask the user to allow the operation or upload the spec on the Connectors page"
	reasonManifestMissing    = "no stored tool list: ask the user to re-save the connector while its server is reachable"
	reasonPinnedNoManifest   = "parameters unknown: tool pinned without a manifest"
	reasonResultUndeclared   = "result fields not declared"
	reasonUnsupportedInBuild = "describe not supported in this build"
	reasonUnsupportedSubtype = "unsupported subtype"
	reasonOperationConfigBad = "connector configuration invalid: parameters could not be derived"
	reasonOperationAmbiguous = "parameters ambiguous: the specification gives this operation several request formats; use an agent node"
	reasonToolNameClash      = "two tools of this connector register under one name: ask the user to deselect one on the Connectors page"
	reasonNotPermitted       = "not permitted"
	reasonToolNotFound       = "no such tool on this platform"
	reasonConnectorNotFound  = "no connector with this exact name"
	reasonToolListTruncated  = "more tools than listed: describe one by name with tool"
	reasonUseConnectorRef    = "a connector tool: describe it with connector and tool"
	reasonConnectorAmbiguous = "several connectors share this exact name: ask the user which one"
	reasonAgentNodeOnly      = "not offered to tool nodes: bind the connector to an agent node"
)

var describeToolReasons = []string{
	reasonNotBindable,
	reasonNotAnnounced,
	reasonPackageDiscovering,
	reasonPackageFailed,
	reasonTypeDisabled,
	reasonNoStoredSpec,
	reasonNeedsAllowList,
	reasonManifestMissing,
	reasonPinnedNoManifest,
	reasonResultUndeclared,
	reasonUnsupportedInBuild,
	reasonUnsupportedSubtype,
	reasonOperationConfigBad,
	reasonOperationAmbiguous,
	reasonToolNameClash,
	reasonNotPermitted,
	reasonToolNotFound,
	reasonConnectorNotFound,
	reasonToolListTruncated,
	reasonUseConnectorRef,
	reasonConnectorAmbiguous,
	reasonAgentNodeOnly,
}

func DescribeToolReasons() []string {
	return slices.Clone(describeToolReasons)
}
