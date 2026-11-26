// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import { DiscoveryIndicator } from "@/common/status";

import type { Toolset } from "@/types";

interface Props {
    toolset: Toolset;
    showReady?: boolean;
}

export const ToolsetDiscoveryIndicator: React.FC<Props> = ({ toolset, showReady = false }) => (
    <DiscoveryIndicator status={toolset.discoveryStatus} isBuiltin={toolset.type !== "custom"} showReady={showReady} testIdPrefix="toolsetStatus" inProgressLabel="Discovering Tools" />
);
