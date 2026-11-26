// Copyright Solace Corporation
//
// Use of this software is governed by the Business Source License (BUSL-1.1)
// included in the LICENSE file.

import type { AgentCardInfo } from "@/types";

export const getAgentTypeLabel = (type: AgentCardInfo["type"]): string => {
    return type === "external" ? "External" : "Internal";
};
